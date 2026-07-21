// SPDX-License-Identifier: Apache-2.0

package hubdb

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/ArdurAI/sith/internal/pep"
	"github.com/ArdurAI/sith/internal/tenancy"
	"github.com/ArdurAI/sith/internal/tracing"
)

const (
	approvalGrantIDBytes         = 16
	approvalGrantEvidenceVersion = 2
)

var (
	approvalGrantIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{22}$`)

	// ErrApprovalGrantUnavailable deliberately covers missing, foreign, mismatched, unauthorized,
	// legacy, expired, and already-consumed grants so callers cannot use this boundary as an oracle.
	ErrApprovalGrantUnavailable = errors.New("approval grant unavailable")
)

// ApprovalGrantID is one opaque server-minted approval identifier. It is not a credential: use
// still requires the authenticated proposal scope and the exact immutable proposal binding.
type ApprovalGrantID string

// String returns the canonical opaque identifier.
func (identifier ApprovalGrantID) String() string { return string(identifier) }

// CreateApprovalGrant persists one same-workspace, separation-of-duty approval for an exact
// validated proposal. PostgreSQL mints its immutable ten-minute lifetime; no caller controls the
// clock. The row stores no raw target, argument, justification, token, or elicitation data.
func (database *AppDB) CreateApprovalGrant(
	ctx context.Context,
	approver tenancy.Scope,
	binding pep.ApprovalBinding,
) (ApprovalGrantID, error) {
	return database.createApprovalGrant(ctx, approver, binding, rand.Reader)
}

func (database *AppDB) createApprovalGrant(
	ctx context.Context,
	approver tenancy.Scope,
	binding pep.ApprovalBinding,
	random io.Reader,
) (ApprovalGrantID, error) {
	if database == nil || database.pool == nil || ctx == nil {
		return "", fmt.Errorf("create approval grant: database and context are required")
	}
	if binding.Validate() != nil || random == nil ||
		approver.Authorize(tenancy.ActionApproveIntent) != nil ||
		approver.RequireWorkspace(binding.WorkspaceID()) != nil || approver.Subject() == binding.Proposer() {
		return "", fmt.Errorf("create approval grant: %w", ErrApprovalGrantUnavailable)
	}

	identifier, err := newApprovalGrantID(random)
	if err != nil {
		return "", fmt.Errorf("create approval grant: mint identifier: %w", err)
	}
	traceContext, traceID, err := tracing.Ensure(ctx)
	if err != nil {
		return "", fmt.Errorf("create approval grant: establish trace context: %w", err)
	}
	err = database.InWorkspace(traceContext, approver, func(tx pgx.Tx) error {
		var proposerRole, currentApproverRole tenancy.Role
		if err := tx.QueryRow(traceContext, `
			SELECT proposer.role, approver.role
			FROM sith.memberships proposer
			JOIN sith.memberships approver ON approver.workspace_id = proposer.workspace_id
			WHERE proposer.workspace_id = $1 AND proposer.subject = $2 AND approver.subject = $3
			FOR SHARE OF proposer, approver
		`, binding.WorkspaceID(), binding.Proposer(), approver.Subject()).Scan(&proposerRole, &currentApproverRole); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrApprovalGrantUnavailable
			}
			return fmt.Errorf("verify current approval memberships: %w", err)
		}
		if !proposerRole.Allows(tenancy.ActionProposeIntent) || currentApproverRole != approver.Role() ||
			!currentApproverRole.Allows(tenancy.ActionApproveIntent) {
			return ErrApprovalGrantUnavailable
		}
		var approvedAt, expiresAt time.Time
		err := tx.QueryRow(traceContext, `
			INSERT INTO sith.approval_grants(
				workspace_id, id, intent_id, proposer, approver, resolved_digest,
				evidence_version, approved_at, expires_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, statement_timestamp(),
				statement_timestamp() + interval '10 minutes')
			RETURNING approved_at, expires_at
		`, binding.WorkspaceID(), identifier, binding.IntentID(), binding.Proposer(), approver.Subject(),
			binding.ResolvedDigest(), approvalGrantEvidenceVersion).Scan(&approvedAt, &expiresAt)
		if err != nil {
			var postgresErr *pgconn.PgError
			if errors.As(err, &postgresErr) && postgresErr.Code == "23505" {
				return ErrApprovalGrantUnavailable
			}
			return fmt.Errorf("persist exact approval grant: %w", err)
		}
		evidence := expiringApprovalGrantEvidenceDigest(
			binding.WorkspaceID(), identifier, binding.IntentID(), binding.Proposer(),
			approver.Subject(), binding.ResolvedDigest(), approvedAt, expiresAt,
		)
		return appendPolicyAuditEntryTx(traceContext, tx, policyAuditEntry{
			format: approvalExpiryAuditFormatVersion, recordedAt: approvedAt.UTC().Truncate(time.Microsecond),
			traceID: traceID, workspaceID: binding.WorkspaceID(), actor: approver.Subject(),
			role: approver.Role(), action: tenancy.ActionApproveIntent, verb: approvalAuditVerb,
			verdict: pep.VerdictAllow, reasonCode: approvalCreatedEventKind,
			eventKind: approvalCreatedEventKind, evidence: evidence,
		})
	})
	if err != nil {
		if errors.Is(err, ErrApprovalGrantUnavailable) {
			return "", fmt.Errorf("create approval grant: %w", ErrApprovalGrantUnavailable)
		}
		return "", fmt.Errorf("create approval grant: %w", err)
	}
	return identifier, nil
}

// ConsumeApprovalGrant atomically spends one exact, unexpired grant using PostgreSQL statement
// time. Concurrent, replaying, legacy, expired, missing, and mismatched consumers share one stable
// refusal; exactly one conditional update can win.
func (database *AppDB) ConsumeApprovalGrant(
	ctx context.Context,
	proposer tenancy.Scope,
	binding pep.ApprovalBinding,
	identifier ApprovalGrantID,
) error {
	if database == nil || database.pool == nil || ctx == nil {
		return fmt.Errorf("consume approval grant: database and context are required")
	}
	if binding.Validate() != nil || !approvalGrantIDPattern.MatchString(identifier.String()) ||
		proposer.Authorize(tenancy.ActionProposeIntent) != nil ||
		proposer.RequireWorkspace(binding.WorkspaceID()) != nil || proposer.Subject() != binding.Proposer() {
		return fmt.Errorf("consume approval grant: %w", ErrApprovalGrantUnavailable)
	}

	traceContext, traceID, err := tracing.Ensure(ctx)
	if err != nil {
		return fmt.Errorf("consume approval grant: establish trace context: %w", err)
	}
	err = database.InWorkspace(traceContext, proposer, func(tx pgx.Tx) error {
		var currentRole tenancy.Role
		if err := tx.QueryRow(traceContext, `
			SELECT role FROM sith.memberships
			WHERE workspace_id = $1 AND subject = $2
			FOR SHARE
		`, binding.WorkspaceID(), binding.Proposer()).Scan(&currentRole); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrApprovalGrantUnavailable
			}
			return fmt.Errorf("verify current proposer membership: %w", err)
		}
		if currentRole != proposer.Role() || !currentRole.Allows(tenancy.ActionProposeIntent) {
			return ErrApprovalGrantUnavailable
		}
		var (
			returned           ApprovalGrantID
			returnedApprover   string
			returnedApprovedAt time.Time
			returnedExpiresAt  time.Time
			returnedConsumedAt time.Time
		)
		err := tx.QueryRow(traceContext, `
			UPDATE sith.approval_grants
			SET consumed_at = statement_timestamp()
			WHERE workspace_id = $1 AND id = $2 AND intent_id = $3 AND proposer = $4
			  AND resolved_digest = $5 AND evidence_version = $6 AND consumed_at IS NULL
			  AND approved_at <= statement_timestamp() AND statement_timestamp() < expires_at
			RETURNING id, approver, approved_at, expires_at, consumed_at
		`, binding.WorkspaceID(), identifier, binding.IntentID(), binding.Proposer(), binding.ResolvedDigest(),
			approvalGrantEvidenceVersion).Scan(
			&returned, &returnedApprover, &returnedApprovedAt, &returnedExpiresAt, &returnedConsumedAt,
		)
		if errors.Is(err, pgx.ErrNoRows) || err == nil && returned != identifier {
			return ErrApprovalGrantUnavailable
		}
		if err != nil {
			return fmt.Errorf("atomically consume exact approval grant: %w", err)
		}
		evidence := expiringApprovalGrantEvidenceDigest(
			binding.WorkspaceID(), returned, binding.IntentID(), binding.Proposer(),
			returnedApprover, binding.ResolvedDigest(), returnedApprovedAt, returnedExpiresAt,
		)
		return appendPolicyAuditEntryTx(traceContext, tx, policyAuditEntry{
			format: approvalExpiryAuditFormatVersion, recordedAt: returnedConsumedAt.UTC().Truncate(time.Microsecond),
			traceID: traceID, workspaceID: binding.WorkspaceID(), actor: proposer.Subject(),
			role: proposer.Role(), action: tenancy.ActionProposeIntent, verb: approvalAuditVerb,
			verdict: pep.VerdictAllow, reasonCode: approvalConsumedEventKind,
			eventKind: approvalConsumedEventKind, evidence: evidence,
		})
	})
	if err != nil {
		if errors.Is(err, ErrApprovalGrantUnavailable) {
			return fmt.Errorf("consume approval grant: %w", ErrApprovalGrantUnavailable)
		}
		return fmt.Errorf("consume approval grant: %w", err)
	}
	return nil
}

func newApprovalGrantID(random io.Reader) (ApprovalGrantID, error) {
	if random == nil {
		return "", fmt.Errorf("random source is required")
	}
	payload := make([]byte, approvalGrantIDBytes)
	if _, err := io.ReadFull(random, payload); err != nil {
		return "", fmt.Errorf("read cryptographic entropy: %w", err)
	}
	identifier := ApprovalGrantID(base64.RawURLEncoding.EncodeToString(payload))
	if !approvalGrantIDPattern.MatchString(identifier.String()) {
		return "", fmt.Errorf("minted identifier is not canonical")
	}
	return identifier, nil
}
