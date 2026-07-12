// SPDX-License-Identifier: Apache-2.0

package hubdb

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	"github.com/jackc/pgx/v5"

	"github.com/ArdurAI/sith/internal/hubauth"
	"github.com/ArdurAI/sith/internal/tenancy"
)

// CreateOIDCBinding maps a pinned upstream identity to one current workspace member.
func (database *AppDB) CreateOIDCBinding(
	ctx context.Context,
	scope tenancy.Scope,
	issuer, upstreamSubject, memberSubject string,
) error {
	if err := scope.Authorize(tenancy.ActionManageWorkspace); err != nil {
		return fmt.Errorf("create OIDC binding: %w", err)
	}
	if err := validateOIDCBinding(scope.WorkspaceID(), issuer, upstreamSubject, memberSubject); err != nil {
		return fmt.Errorf("create OIDC binding: %w", err)
	}
	return database.InWorkspace(ctx, scope, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			INSERT INTO sith.oidc_bindings(workspace_id, issuer, upstream_subject, member_subject)
			SELECT $1, $2, $3, membership.subject
			FROM sith.memberships membership
			WHERE membership.workspace_id = $1 AND membership.subject = $4
		`, scope.WorkspaceID(), issuer, upstreamSubject, memberSubject)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return fmt.Errorf("mapped subject is not a current workspace member")
		}
		return nil
	})
}

// LookupOIDCMembership performs one fixed workspace-scoped bootstrap lookup.
func (database *AppDB) LookupOIDCMembership(
	ctx context.Context,
	workspaceID tenancy.WorkspaceID,
	issuer, upstreamSubject string,
) (tenancy.Membership, error) {
	if database == nil || database.pool == nil || ctx == nil ||
		validateOIDCLookup(workspaceID, issuer, upstreamSubject) != nil {
		return tenancy.Membership{}, hubauth.ErrOIDCBindingNotFound
	}
	var membership tenancy.Membership
	err := pgx.BeginTxFunc(ctx, database.pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		if err := setWorkspaceScope(ctx, tx, workspaceID); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `
			SELECT binding.workspace_id, binding.member_subject, membership.role
			FROM sith.oidc_bindings binding
			JOIN sith.memberships membership
			  ON membership.workspace_id = binding.workspace_id
			 AND membership.subject = binding.member_subject
			WHERE binding.workspace_id = $1 AND binding.issuer = $2 AND binding.upstream_subject = $3
		`, workspaceID, issuer, upstreamSubject).Scan(&membership.WorkspaceID, &membership.Subject, &membership.Role)
	})
	if err != nil {
		if err == pgx.ErrNoRows {
			return tenancy.Membership{}, hubauth.ErrOIDCBindingNotFound
		}
		return tenancy.Membership{}, fmt.Errorf("lookup OIDC binding: %w", err)
	}
	if err := membership.Validate(); err != nil {
		return tenancy.Membership{}, fmt.Errorf("lookup OIDC binding: invalid stored membership: %w", err)
	}
	return membership, nil
}

func validateOIDCBinding(workspaceID tenancy.WorkspaceID, issuer, upstreamSubject, memberSubject string) error {
	if err := validateOIDCLookup(workspaceID, issuer, upstreamSubject); err != nil {
		return err
	}
	membership := tenancy.Membership{WorkspaceID: workspaceID, Subject: memberSubject, Role: tenancy.RoleReader}
	if err := membership.Validate(); err != nil {
		return err
	}
	return nil
}

func validateOIDCLookup(workspaceID tenancy.WorkspaceID, issuer, upstreamSubject string) error {
	if tenancy.ValidateWorkspaceID(workspaceID) != nil {
		return fmt.Errorf("invalid workspace")
	}
	if !strings.HasPrefix(issuer, "https://") || strings.TrimSpace(issuer) != issuer || len(issuer) > 2048 ||
		strings.IndexFunc(issuer, unicode.IsControl) >= 0 {
		return fmt.Errorf("invalid issuer")
	}
	upstream := tenancy.Membership{WorkspaceID: workspaceID, Subject: upstreamSubject, Role: tenancy.RoleReader}
	if upstream.Validate() != nil {
		return fmt.Errorf("invalid upstream subject")
	}
	return nil
}
