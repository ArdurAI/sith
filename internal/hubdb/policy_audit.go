// SPDX-License-Identifier: Apache-2.0

package hubdb

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ArdurAI/sith/internal/auditrecord"
	"github.com/ArdurAI/sith/internal/pep"
	"github.com/ArdurAI/sith/internal/tenancy"
	"github.com/ArdurAI/sith/internal/tracing"
)

const (
	policyAuditFormatVersion   int16    = 1
	approvalAuditFormatVersion int16    = 2
	approvalEvidenceHashDomain          = "sith-approval-grant-evidence/v1"
	policyDecisionEventKind             = "policy-decision"
	approvalCreatedEventKind            = "approval-created"
	approvalConsumedEventKind           = "approval-consumed"
	approvalAuditVerb          pep.Verb = "approval.grant"
)

var approvalEvidencePattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

type policyAuditEntry struct {
	sequence     int64
	format       int16
	recordedAt   time.Time
	traceID      tracing.ID
	workspaceID  tenancy.WorkspaceID
	actor        string
	role         tenancy.Role
	action       tenancy.Action
	verb         pep.Verb
	verdict      pep.Verdict
	reasonCode   string
	eventKind    string
	evidence     string
	previousHash []byte
	entryHash    []byte
}

// Record implements pep.Auditor by atomically appending one privacy-minimized PEP decision to
// the active workspace chain. The mutable head serializes same-workspace writers; history rows are
// append-only at the application-role privilege boundary.
func (database *AppDB) Record(ctx context.Context, event pep.AuditEvent) error {
	if database == nil || database.pool == nil || ctx == nil {
		return fmt.Errorf("append policy audit: database and context are required")
	}
	if err := event.Validate(); err != nil {
		return fmt.Errorf("append policy audit: invalid event: %w", err)
	}
	scope, err := auditEventScope(event)
	if err != nil {
		return fmt.Errorf("append policy audit: derive workspace scope: %w", err)
	}

	return database.InWorkspace(ctx, scope, func(tx pgx.Tx) error {
		entry := policyAuditEntry{
			format:     policyAuditFormatVersion,
			recordedAt: event.At.UTC().Truncate(time.Microsecond), traceID: event.TraceID,
			workspaceID: event.WorkspaceID, actor: event.Actor, role: event.Role, action: event.Action,
			verb: event.Verb, verdict: event.Verdict, reasonCode: event.ReasonCode,
			eventKind: policyDecisionEventKind,
		}
		return appendPolicyAuditEntryTx(ctx, tx, entry)
	})
}

func appendPolicyAuditEntryTx(ctx context.Context, tx pgx.Tx, entry policyAuditEntry) error {
	if ctx == nil || tx == nil {
		return fmt.Errorf("append policy audit entry: context and transaction are required")
	}
	if err := validatePolicyAuditEntry(entry); err != nil {
		return fmt.Errorf("append policy audit entry: %w", err)
	}
	workspaceID := entry.workspaceID
	if _, err := tx.Exec(ctx, `
			INSERT INTO sith.policy_audit_heads(workspace_id)
			VALUES ($1)
			ON CONFLICT (workspace_id) DO NOTHING
	`, workspaceID); err != nil {
		return fmt.Errorf("initialize policy audit chain: %w", err)
	}

	var lastSequence int64
	var lastHash []byte
	if err := tx.QueryRow(ctx, `
			SELECT last_sequence, last_hash
			FROM sith.policy_audit_heads
			WHERE workspace_id = $1
			FOR UPDATE
	`, workspaceID).Scan(&lastSequence, &lastHash); err != nil {
		return fmt.Errorf("lock policy audit chain: %w", err)
	}
	if lastSequence < 0 || lastSequence == math.MaxInt64 || len(lastHash) != sha256.Size {
		return fmt.Errorf("policy audit chain head is invalid")
	}

	entry.sequence = lastSequence + 1
	entry.previousHash = bytes.Clone(lastHash)
	entry.entryHash = policyAuditEntryHash(entry)
	if len(entry.entryHash) != sha256.Size {
		return fmt.Errorf("hash policy audit entry: portable audit hash is invalid")
	}

	if _, err := tx.Exec(ctx, `
			INSERT INTO sith.policy_audit_entries(
				workspace_id, sequence, format_version, recorded_at, trace_id, actor, role, action,
				verb, verdict, reason_code, event_kind, evidence_digest, previous_hash, entry_hash
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
		`, entry.workspaceID, entry.sequence, entry.format, entry.recordedAt, entry.traceID,
		entry.actor, entry.role, entry.action, entry.verb, entry.verdict, entry.reasonCode,
		entry.eventKind, entry.evidence, entry.previousHash, entry.entryHash); err != nil {
		return fmt.Errorf("insert policy audit entry: %w", err)
	}
	tag, err := tx.Exec(ctx, `
			UPDATE sith.policy_audit_heads
			SET last_sequence = $2, last_hash = $3
			WHERE workspace_id = $1 AND last_sequence = $4 AND last_hash = $5
		`, workspaceID, entry.sequence, entry.entryHash, lastSequence, lastHash)
	if err != nil {
		return fmt.Errorf("advance policy audit chain: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("advance policy audit chain: locked head changed unexpectedly")
	}
	return nil
}

// VerifyPolicyAuditChain checks the complete retained chain and head in one repeatable-read
// workspace snapshot. It detects retained-history mutation, deletion, reordering, and forked links;
// external anchoring is required to detect wholesale replacement by a privileged database owner.
func (database *AppDB) VerifyPolicyAuditChain(ctx context.Context, scope tenancy.Scope) error {
	if database == nil || database.pool == nil || ctx == nil {
		return fmt.Errorf("verify policy audit chain: database and context are required")
	}
	return database.inWorkspaceReadSnapshot(ctx, scope, func(tx pgx.Tx) error {
		_, _, _, err := readVerifiedPolicyAuditChainTx(ctx, tx, scope, 0)
		return err
	})
}

// ExportPolicyAuditChain returns one complete, verified, privacy-minimized workspace chain. The
// fixed entry ceiling bounds memory and scan cost, and inWorkspaceReadSnapshot commits before this
// method returns, so an HTTP caller cannot hold the database transaction open while encoding.
func (database *AppDB) ExportPolicyAuditChain(ctx context.Context, scope tenancy.Scope) (auditrecord.Export, error) {
	if database == nil || database.pool == nil || ctx == nil {
		return auditrecord.Export{}, fmt.Errorf("export policy audit chain: database and context are required")
	}
	if err := scope.Authorize(tenancy.ActionExportAudit); err != nil {
		return auditrecord.Export{}, fmt.Errorf("export policy audit chain: admin scope is required")
	}

	var result auditrecord.Export
	err := database.inWorkspaceReadSnapshot(ctx, scope, func(tx pgx.Tx) error {
		entries, headSequence, headHash, err := readVerifiedPolicyAuditChainTx(ctx, tx, scope, auditrecord.MaxEntries)
		if err != nil {
			return err
		}
		result = newPolicyAuditExport(scope.WorkspaceID(), entries, headSequence, headHash)
		return nil
	})
	if err != nil {
		return auditrecord.Export{}, fmt.Errorf("export policy audit chain: %w", err)
	}
	return result, nil
}

func readVerifiedPolicyAuditChainTx(
	ctx context.Context,
	tx pgx.Tx,
	scope tenancy.Scope,
	retainLimit int,
) ([]policyAuditEntry, int64, []byte, error) {
	var headSequence int64
	var headHash []byte
	if err := tx.QueryRow(ctx, `
		SELECT last_sequence, last_hash
		FROM sith.policy_audit_heads
		WHERE workspace_id = $1
	`, scope.WorkspaceID()).Scan(&headSequence, &headHash); err != nil {
		if err == pgx.ErrNoRows {
			return nil, 0, nil, fmt.Errorf("policy audit chain is not initialized")
		}
		return nil, 0, nil, fmt.Errorf("read policy audit chain head: %w", err)
	}
	if headSequence <= 0 || len(headHash) != sha256.Size {
		return nil, 0, nil, fmt.Errorf("policy audit chain head is invalid")
	}
	if retainLimit < 0 || (retainLimit > 0 && headSequence > int64(retainLimit)) {
		return nil, 0, nil, fmt.Errorf("policy audit chain exceeds the online export limit")
	}

	rows, err := tx.Query(ctx, `
		SELECT sequence, format_version, recorded_at, trace_id, workspace_id, actor, role,
		       action, verb, verdict, reason_code, event_kind, evidence_digest,
		       previous_hash, entry_hash
		FROM sith.policy_audit_entries
		WHERE workspace_id = $1
		ORDER BY sequence
	`, scope.WorkspaceID())
	if err != nil {
		return nil, 0, nil, fmt.Errorf("read policy audit chain: %w", err)
	}
	defer rows.Close()

	var retained []policyAuditEntry
	if retainLimit > 0 {
		retained = make([]policyAuditEntry, 0, int(headSequence))
	}
	expectedSequence := int64(1)
	expectedPrevious := make([]byte, sha256.Size)
	for rows.Next() {
		var entry policyAuditEntry
		if err := rows.Scan(&entry.sequence, &entry.format, &entry.recordedAt, &entry.traceID,
			&entry.workspaceID, &entry.actor, &entry.role, &entry.action, &entry.verb,
			&entry.verdict, &entry.reasonCode, &entry.eventKind, &entry.evidence,
			&entry.previousHash, &entry.entryHash); err != nil {
			return nil, 0, nil, fmt.Errorf("scan policy audit chain: %w", err)
		}
		if entry.sequence != expectedSequence ||
			entry.workspaceID != scope.WorkspaceID() || len(entry.previousHash) != sha256.Size ||
			len(entry.entryHash) != sha256.Size || !bytes.Equal(entry.previousHash, expectedPrevious) {
			return nil, 0, nil, fmt.Errorf("policy audit chain continuity is invalid at sequence %d", expectedSequence)
		}
		if err := validatePolicyAuditEntry(entry); err != nil {
			return nil, 0, nil, fmt.Errorf("policy audit chain event is invalid at sequence %d", expectedSequence)
		}
		calculated := policyAuditEntryHash(entry)
		if len(calculated) != sha256.Size || !bytes.Equal(entry.entryHash, calculated) {
			return nil, 0, nil, fmt.Errorf("policy audit chain hash is invalid at sequence %d", expectedSequence)
		}
		if retainLimit > 0 {
			if len(retained) == retainLimit {
				return nil, 0, nil, fmt.Errorf("policy audit chain exceeds the online export limit")
			}
			retained = append(retained, entry)
		}
		expectedPrevious = bytes.Clone(entry.entryHash)
		expectedSequence++
	}
	if err := rows.Err(); err != nil {
		return nil, 0, nil, fmt.Errorf("iterate policy audit chain: %w", err)
	}
	retainedSequence := expectedSequence - 1
	if retainedSequence != headSequence || !bytes.Equal(expectedPrevious, headHash) {
		return nil, 0, nil, fmt.Errorf("policy audit chain head does not match retained history")
	}
	return retained, headSequence, bytes.Clone(headHash), nil
}

func newPolicyAuditExport(
	workspaceID tenancy.WorkspaceID,
	entries []policyAuditEntry,
	headSequence int64,
	headHash []byte,
) auditrecord.Export {
	records := make([]auditrecord.Entry, len(entries))
	for index, entry := range entries {
		records[index] = portablePolicyAuditEntry(entry)
	}
	return auditrecord.Export{
		Schema: auditrecord.SchemaV1, WorkspaceID: string(workspaceID),
		Chain: auditrecord.Chain{
			HashAlgorithm: auditrecord.HashAlgorithm, HeadSequence: headSequence,
			HeadHash: auditHashString(headHash),
		},
		Entries: records,
	}
}

func auditHashString(value []byte) string {
	return "sha256:" + hex.EncodeToString(value)
}

func auditEventScope(event pep.AuditEvent) (tenancy.Scope, error) {
	principal, err := tenancy.NewPrincipal(event.Actor, map[tenancy.WorkspaceID]tenancy.Role{
		event.WorkspaceID: event.Role,
	})
	if err != nil {
		return tenancy.Scope{}, err
	}
	return principal.Scope(event.WorkspaceID)
}

func policyAuditEntryHash(entry policyAuditEntry) []byte {
	recomputed, err := auditrecord.RecomputeEntryHash(entry.workspaceID, portablePolicyAuditEntry(entry))
	if err != nil {
		return nil
	}
	decoded, err := hex.DecodeString(strings.TrimPrefix(recomputed, "sha256:"))
	if err != nil || len(decoded) != sha256.Size {
		return nil
	}
	return decoded
}

func portablePolicyAuditEntry(entry policyAuditEntry) auditrecord.Entry {
	return auditrecord.Entry{
		Sequence: entry.sequence, FormatVersion: entry.format,
		RecordedAt: entry.recordedAt.UTC().Truncate(time.Microsecond), TraceID: string(entry.traceID),
		Actor: entry.actor, Role: string(entry.role), Action: string(entry.action), Verb: string(entry.verb),
		Verdict: string(entry.verdict), ReasonCode: entry.reasonCode, EventKind: entry.eventKind,
		EvidenceDigest: entry.evidence, PreviousHash: auditHashString(entry.previousHash),
		EntryHash: auditHashString(entry.entryHash),
	}
}

func validatePolicyAuditEntry(entry policyAuditEntry) error {
	switch entry.format {
	case policyAuditFormatVersion:
		if entry.eventKind != policyDecisionEventKind || entry.evidence != "" {
			return fmt.Errorf("format 1 policy audit metadata is invalid")
		}
		event := pep.AuditEvent{
			At: entry.recordedAt, TraceID: entry.traceID, WorkspaceID: entry.workspaceID,
			Actor: entry.actor, Role: entry.role, Action: entry.action, Verb: entry.verb,
			Verdict: entry.verdict, ReasonCode: entry.reasonCode,
		}
		return event.Validate()
	case approvalAuditFormatVersion:
		if entry.recordedAt.IsZero() || entry.recordedAt.After(time.Now().Add(time.Minute)) ||
			!entry.traceID.Valid() || !approvalEvidencePattern.MatchString(entry.evidence) ||
			entry.verb != approvalAuditVerb || entry.verdict != pep.VerdictAllow || entry.reasonCode != entry.eventKind {
			return fmt.Errorf("approval lifecycle audit metadata is invalid")
		}
		principal, err := tenancy.NewPrincipal(entry.actor, map[tenancy.WorkspaceID]tenancy.Role{
			entry.workspaceID: entry.role,
		})
		if err != nil {
			return fmt.Errorf("approval lifecycle audit scope: %w", err)
		}
		if _, err := principal.Scope(entry.workspaceID); err != nil {
			return fmt.Errorf("approval lifecycle audit scope: %w", err)
		}
		switch entry.eventKind {
		case approvalCreatedEventKind:
			if entry.role != tenancy.RoleApprover || entry.action != tenancy.ActionApproveIntent {
				return fmt.Errorf("approval-created lifecycle actor is invalid")
			}
		case approvalConsumedEventKind:
			if entry.role != tenancy.RoleOperator || entry.action != tenancy.ActionProposeIntent {
				return fmt.Errorf("approval-consumed lifecycle actor is invalid")
			}
		default:
			return fmt.Errorf("approval lifecycle event kind is invalid")
		}
		return nil
	default:
		return fmt.Errorf("policy audit format is unsupported")
	}
}

func approvalGrantEvidenceDigest(
	workspaceID tenancy.WorkspaceID,
	identifier ApprovalGrantID,
	intentID, proposer, approver, resolvedDigest string,
	approvedAt time.Time,
) string {
	canonical := make([]byte, 0, 512)
	for _, value := range []string{
		approvalEvidenceHashDomain, string(workspaceID), identifier.String(), intentID,
		proposer, approver, resolvedDigest, approvedAt.UTC().Truncate(time.Microsecond).Format(time.RFC3339Nano),
	} {
		canonical = appendCanonicalString(canonical, value)
	}
	digest := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func appendCanonicalString(target []byte, value string) []byte {
	target = strconv.AppendInt(target, int64(len(value)), 10)
	target = append(target, ':')
	return append(target, value...)
}
