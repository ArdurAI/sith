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
)

const approvalGrantIDBytes = 16

var (
	approvalGrantIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{22}$`)

	// ErrApprovalGrantUnavailable deliberately covers missing, foreign, mismatched, unauthorized,
	// and already-consumed grants so callers cannot use the approval boundary as an oracle.
	ErrApprovalGrantUnavailable = errors.New("approval grant unavailable")
)

// ApprovalGrantID is one opaque server-minted approval identifier. It is not a credential: use
// still requires the authenticated proposal scope and the exact immutable proposal binding.
type ApprovalGrantID string

// String returns the canonical opaque identifier.
func (identifier ApprovalGrantID) String() string { return string(identifier) }

// CreateApprovalGrant persists one same-workspace, separation-of-duty approval for an exact
// validated proposal. It stores no raw target, argument, justification, token, or elicitation data.
func (database *AppDB) CreateApprovalGrant(
	ctx context.Context,
	approver tenancy.Scope,
	binding pep.ApprovalBinding,
	approvedAt time.Time,
) (ApprovalGrantID, error) {
	return database.createApprovalGrant(ctx, approver, binding, approvedAt, rand.Reader)
}

func (database *AppDB) createApprovalGrant(
	ctx context.Context,
	approver tenancy.Scope,
	binding pep.ApprovalBinding,
	approvedAt time.Time,
	random io.Reader,
) (ApprovalGrantID, error) {
	if database == nil || database.pool == nil || ctx == nil {
		return "", fmt.Errorf("create approval grant: database and context are required")
	}
	if binding.Validate() != nil || approvedAt.IsZero() || random == nil ||
		approver.Authorize(tenancy.ActionApproveIntent) != nil ||
		approver.RequireWorkspace(binding.WorkspaceID()) != nil || approver.Subject() == binding.Proposer() {
		return "", fmt.Errorf("create approval grant: %w", ErrApprovalGrantUnavailable)
	}

	identifier, err := newApprovalGrantID(random)
	if err != nil {
		return "", fmt.Errorf("create approval grant: mint identifier: %w", err)
	}
	err = database.InWorkspace(ctx, approver, func(tx pgx.Tx) error {
		var proposerRole, currentApproverRole tenancy.Role
		if err := tx.QueryRow(ctx, `
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
		_, err := tx.Exec(ctx, `
			INSERT INTO sith.approval_grants(
				workspace_id, id, intent_id, proposer, approver, resolved_digest, approved_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7)
		`, binding.WorkspaceID(), identifier, binding.IntentID(), binding.Proposer(), approver.Subject(),
			binding.ResolvedDigest(), approvedAt.UTC())
		if err != nil {
			var postgresErr *pgconn.PgError
			if errors.As(err, &postgresErr) && postgresErr.Code == "23505" {
				return ErrApprovalGrantUnavailable
			}
			return fmt.Errorf("persist exact approval grant: %w", err)
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrApprovalGrantUnavailable) {
			return "", fmt.Errorf("create approval grant: %w", ErrApprovalGrantUnavailable)
		}
		return "", fmt.Errorf("create approval grant: %w", err)
	}
	return identifier, nil
}

// ConsumeApprovalGrant atomically spends one exact grant. A concurrent or replaying consumer sees
// the same stable refusal as a missing or mismatched grant; exactly one conditional update wins.
func (database *AppDB) ConsumeApprovalGrant(
	ctx context.Context,
	proposer tenancy.Scope,
	binding pep.ApprovalBinding,
	identifier ApprovalGrantID,
	consumedAt time.Time,
) error {
	if database == nil || database.pool == nil || ctx == nil {
		return fmt.Errorf("consume approval grant: database and context are required")
	}
	if binding.Validate() != nil || !approvalGrantIDPattern.MatchString(identifier.String()) || consumedAt.IsZero() ||
		proposer.Authorize(tenancy.ActionProposeIntent) != nil ||
		proposer.RequireWorkspace(binding.WorkspaceID()) != nil || proposer.Subject() != binding.Proposer() {
		return fmt.Errorf("consume approval grant: %w", ErrApprovalGrantUnavailable)
	}

	err := database.InWorkspace(ctx, proposer, func(tx pgx.Tx) error {
		var currentRole tenancy.Role
		if err := tx.QueryRow(ctx, `
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
		var returned ApprovalGrantID
		err := tx.QueryRow(ctx, `
			UPDATE sith.approval_grants
			SET consumed_at = $6
			WHERE workspace_id = $1 AND id = $2 AND intent_id = $3 AND proposer = $4
			  AND resolved_digest = $5 AND consumed_at IS NULL AND approved_at <= $6
			RETURNING id
		`, binding.WorkspaceID(), identifier, binding.IntentID(), binding.Proposer(), binding.ResolvedDigest(),
			consumedAt.UTC()).Scan(&returned)
		if errors.Is(err, pgx.ErrNoRows) || err == nil && returned != identifier {
			return ErrApprovalGrantUnavailable
		}
		if err != nil {
			return fmt.Errorf("atomically consume exact approval grant: %w", err)
		}
		return nil
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
