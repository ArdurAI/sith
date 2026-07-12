// SPDX-License-Identifier: Apache-2.0

package hubdb

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/ArdurAI/sith/internal/hubauth"
	"github.com/ArdurAI/sith/internal/tenancy"
)

// CreateCloudIdentityBinding maps one verified cloud identity to one current workspace member.
func (database *AppDB) CreateCloudIdentityBinding(
	ctx context.Context,
	scope tenancy.Scope,
	identity hubauth.CloudIdentity,
	memberSubject string,
) error {
	if err := scope.Authorize(tenancy.ActionManageWorkspace); err != nil {
		return fmt.Errorf("create cloud identity binding: %w", err)
	}
	if err := validateCloudIdentityBinding(scope.WorkspaceID(), identity, memberSubject); err != nil {
		return fmt.Errorf("create cloud identity binding: %w", err)
	}
	return database.InWorkspace(ctx, scope, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			"INSERT INTO sith.cloud_identity_bindings(workspace_id, provider, realm, upstream_subject, member_subject)\n"+
				"SELECT $1, $2, $3, $4, membership.subject\n"+
				"FROM sith.memberships membership\n"+
				"WHERE membership.workspace_id = $1 AND membership.subject = $5",
			scope.WorkspaceID(), identity.Provider, identity.Realm, identity.Subject, memberSubject,
		)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return fmt.Errorf("mapped subject is not a current workspace member")
		}
		return nil
	})
}

// LookupCloudIdentityMembership performs one fixed workspace-scoped bootstrap lookup.
func (database *AppDB) LookupCloudIdentityMembership(
	ctx context.Context,
	workspaceID tenancy.WorkspaceID,
	identity hubauth.CloudIdentity,
) (tenancy.Membership, error) {
	if database == nil || database.pool == nil || ctx == nil || tenancy.ValidateWorkspaceID(workspaceID) != nil ||
		identity.Validate() != nil {
		return tenancy.Membership{}, hubauth.ErrCloudBindingNotFound
	}
	var membership tenancy.Membership
	err := pgx.BeginTxFunc(ctx, database.pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		if err := setWorkspaceScope(ctx, tx, workspaceID); err != nil {
			return err
		}
		return tx.QueryRow(ctx,
			"SELECT binding.workspace_id, binding.member_subject, membership.role\n"+
				"FROM sith.cloud_identity_bindings binding\n"+
				"JOIN sith.memberships membership\n"+
				"  ON membership.workspace_id = binding.workspace_id\n"+
				" AND membership.subject = binding.member_subject\n"+
				"WHERE binding.workspace_id = $1 AND binding.provider = $2\n"+
				"  AND binding.realm = $3 AND binding.upstream_subject = $4",
			workspaceID, identity.Provider, identity.Realm, identity.Subject,
		).Scan(&membership.WorkspaceID, &membership.Subject, &membership.Role)
	})
	if err != nil {
		if err == pgx.ErrNoRows {
			return tenancy.Membership{}, hubauth.ErrCloudBindingNotFound
		}
		return tenancy.Membership{}, fmt.Errorf("lookup cloud identity binding: %w", err)
	}
	if err := membership.Validate(); err != nil {
		return tenancy.Membership{}, fmt.Errorf("lookup cloud identity binding: invalid stored membership: %w", err)
	}
	return membership, nil
}

func validateCloudIdentityBinding(
	workspaceID tenancy.WorkspaceID,
	identity hubauth.CloudIdentity,
	memberSubject string,
) error {
	if tenancy.ValidateWorkspaceID(workspaceID) != nil || identity.Validate() != nil {
		return fmt.Errorf("invalid workspace or cloud identity")
	}
	membership := tenancy.Membership{WorkspaceID: workspaceID, Subject: memberSubject, Role: tenancy.RoleReader}
	if err := membership.Validate(); err != nil {
		return err
	}
	return nil
}
