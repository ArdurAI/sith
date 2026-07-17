// SPDX-License-Identifier: Apache-2.0

package hubdb

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ArdurAI/sith/internal/pep"
	"github.com/ArdurAI/sith/internal/tenancy"
	"github.com/ArdurAI/sith/internal/tracing"
)

const (
	policyAuditFormatVersion int16 = 1
	policyAuditHashDomain          = "sith-policy-audit-chain/v1"
)

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
		workspaceID := event.WorkspaceID
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

		entry := policyAuditEntry{
			sequence: lastSequence + 1, format: policyAuditFormatVersion,
			recordedAt: event.At.UTC().Truncate(time.Microsecond), traceID: event.TraceID,
			workspaceID: event.WorkspaceID, actor: event.Actor, role: event.Role, action: event.Action,
			verb: event.Verb, verdict: event.Verdict, reasonCode: event.ReasonCode,
			previousHash: bytes.Clone(lastHash),
		}
		entry.entryHash = policyAuditEntryHash(entry)

		if _, err := tx.Exec(ctx, `
			INSERT INTO sith.policy_audit_entries(
				workspace_id, sequence, format_version, recorded_at, trace_id, actor, role, action,
				verb, verdict, reason_code, previous_hash, entry_hash
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		`, entry.workspaceID, entry.sequence, entry.format, entry.recordedAt, entry.traceID,
			entry.actor, entry.role, entry.action, entry.verb, entry.verdict, entry.reasonCode,
			entry.previousHash, entry.entryHash); err != nil {
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
	})
}

// VerifyPolicyAuditChain checks the complete retained chain and head in one repeatable-read
// workspace snapshot. It detects retained-history mutation, deletion, reordering, and forked links;
// external anchoring is required to detect wholesale replacement by a privileged database owner.
func (database *AppDB) VerifyPolicyAuditChain(ctx context.Context, scope tenancy.Scope) error {
	if database == nil || database.pool == nil || ctx == nil {
		return fmt.Errorf("verify policy audit chain: database and context are required")
	}
	return database.inWorkspaceReadSnapshot(ctx, scope, func(tx pgx.Tx) error {
		var headSequence int64
		var headHash []byte
		if err := tx.QueryRow(ctx, `
			SELECT last_sequence, last_hash
			FROM sith.policy_audit_heads
			WHERE workspace_id = $1
		`, scope.WorkspaceID()).Scan(&headSequence, &headHash); err != nil {
			if err == pgx.ErrNoRows {
				return fmt.Errorf("policy audit chain is not initialized")
			}
			return fmt.Errorf("read policy audit chain head: %w", err)
		}
		if headSequence <= 0 || len(headHash) != sha256.Size {
			return fmt.Errorf("policy audit chain head is invalid")
		}

		rows, err := tx.Query(ctx, `
			SELECT sequence, format_version, recorded_at, trace_id, workspace_id, actor, role,
			       action, verb, verdict, reason_code, previous_hash, entry_hash
			FROM sith.policy_audit_entries
			WHERE workspace_id = $1
			ORDER BY sequence
		`, scope.WorkspaceID())
		if err != nil {
			return fmt.Errorf("read policy audit chain: %w", err)
		}
		defer rows.Close()

		expectedSequence := int64(1)
		expectedPrevious := make([]byte, sha256.Size)
		for rows.Next() {
			var entry policyAuditEntry
			if err := rows.Scan(&entry.sequence, &entry.format, &entry.recordedAt, &entry.traceID,
				&entry.workspaceID, &entry.actor, &entry.role, &entry.action, &entry.verb,
				&entry.verdict, &entry.reasonCode, &entry.previousHash, &entry.entryHash); err != nil {
				return fmt.Errorf("scan policy audit chain: %w", err)
			}
			if entry.sequence != expectedSequence || entry.format != policyAuditFormatVersion ||
				entry.workspaceID != scope.WorkspaceID() || len(entry.previousHash) != sha256.Size ||
				len(entry.entryHash) != sha256.Size || !bytes.Equal(entry.previousHash, expectedPrevious) {
				return fmt.Errorf("policy audit chain continuity is invalid at sequence %d", expectedSequence)
			}
			event := pep.AuditEvent{
				At: entry.recordedAt, TraceID: entry.traceID, WorkspaceID: entry.workspaceID,
				Actor: entry.actor, Role: entry.role, Action: entry.action, Verb: entry.verb,
				Verdict: entry.verdict, ReasonCode: entry.reasonCode,
			}
			if err := event.Validate(); err != nil {
				return fmt.Errorf("policy audit chain event is invalid at sequence %d", expectedSequence)
			}
			calculated := policyAuditEntryHash(entry)
			if !bytes.Equal(entry.entryHash, calculated) {
				return fmt.Errorf("policy audit chain hash is invalid at sequence %d", expectedSequence)
			}
			expectedPrevious = bytes.Clone(entry.entryHash)
			expectedSequence++
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate policy audit chain: %w", err)
		}
		retainedSequence := expectedSequence - 1
		if retainedSequence != headSequence || !bytes.Equal(expectedPrevious, headHash) {
			return fmt.Errorf("policy audit chain head does not match retained history")
		}
		return nil
	})
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
	canonical := make([]byte, 0, 512)
	canonical = appendCanonicalString(canonical, policyAuditHashDomain)
	canonical = appendCanonicalString(canonical, strconv.FormatInt(int64(entry.format), 10))
	canonical = appendCanonicalString(canonical, strconv.FormatInt(entry.sequence, 10))
	canonical = append(canonical, entry.previousHash...)
	for _, value := range []string{
		entry.recordedAt.UTC().Truncate(time.Microsecond).Format(time.RFC3339Nano),
		string(entry.traceID), string(entry.workspaceID), entry.actor, string(entry.role),
		string(entry.action), string(entry.verb), string(entry.verdict), entry.reasonCode,
	} {
		canonical = appendCanonicalString(canonical, value)
	}
	digest := sha256.Sum256(canonical)
	return digest[:]
}

func appendCanonicalString(target []byte, value string) []byte {
	target = strconv.AppendInt(target, int64(len(value)), 10)
	target = append(target, ':')
	return append(target, value...)
}
