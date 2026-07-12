// SPDX-License-Identifier: Apache-2.0

package hubdb

import (
	"context"
	"crypto/sha256"
	"fmt"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ArdurAI/sith/internal/hubauth"
	"github.com/ArdurAI/sith/internal/tenancy"
)

var apiKeyIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{22}$`)

// CreateAPIKey persists only a verifier and requires a current membership in the active workspace.
func (database *AppDB) CreateAPIKey(ctx context.Context, scope tenancy.Scope, record hubauth.APIKeyRecord) error {
	if err := scope.Authorize(tenancy.ActionManageWorkspace); err != nil {
		return fmt.Errorf("create API key: %w", err)
	}
	if err := validateAPIKeyRecord(scope.WorkspaceID(), record); err != nil {
		return fmt.Errorf("create API key: %w", err)
	}
	return database.InWorkspace(ctx, scope, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			INSERT INTO sith.api_keys(workspace_id, id, subject, verifier, created_at, expires_at)
			SELECT $1, $2, membership.subject, $3, $4, $5
			FROM sith.memberships membership
			WHERE membership.workspace_id = $1 AND membership.subject = $6
		`, record.WorkspaceID, record.ID, record.Verifier, record.CreatedAt, record.ExpiresAt, record.Subject)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return fmt.Errorf("subject is not a current member of the workspace")
		}
		return nil
	})
}

// LookupAPIKey performs one fixed, workspace-scoped bootstrap query without exposing a transaction callback.
func (database *AppDB) LookupAPIKey(ctx context.Context, workspaceID tenancy.WorkspaceID, keyID string) (hubauth.APIKeyRecord, tenancy.Membership, error) {
	if database == nil || database.pool == nil || ctx == nil || tenancy.ValidateWorkspaceID(workspaceID) != nil || !apiKeyIDPattern.MatchString(keyID) {
		return hubauth.APIKeyRecord{}, tenancy.Membership{}, hubauth.ErrAPIKeyNotFound
	}
	var (
		record     hubauth.APIKeyRecord
		membership tenancy.Membership
	)
	err := pgx.BeginTxFunc(ctx, database.pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		if err := setWorkspaceScope(ctx, tx, workspaceID); err != nil {
			return err
		}
		err := tx.QueryRow(ctx, `
			SELECT key.workspace_id, key.id, key.subject, key.verifier, key.created_at, key.expires_at,
			       key.retire_at, key.revoked_at, COALESCE(key.replaced_by, ''), membership.role
			FROM sith.api_keys key
			JOIN sith.memberships membership
			  ON membership.workspace_id = key.workspace_id AND membership.subject = key.subject
			WHERE key.workspace_id = $1 AND key.id = $2
		`, workspaceID, keyID).Scan(
			&record.WorkspaceID, &record.ID, &record.Subject, &record.Verifier, &record.CreatedAt, &record.ExpiresAt,
			&record.RetireAt, &record.RevokedAt, &record.ReplacedBy, &membership.Role,
		)
		if err != nil {
			return err
		}
		membership.WorkspaceID = record.WorkspaceID
		membership.Subject = record.Subject
		return nil
	})
	if err != nil {
		if err == pgx.ErrNoRows {
			return hubauth.APIKeyRecord{}, tenancy.Membership{}, hubauth.ErrAPIKeyNotFound
		}
		return hubauth.APIKeyRecord{}, tenancy.Membership{}, fmt.Errorf("lookup API key: %w", err)
	}
	return record, membership, nil
}

// RotateAPIKey atomically creates a replacement and schedules retirement of the original.
func (database *AppDB) RotateAPIKey(ctx context.Context, scope tenancy.Scope, keyID string, replacement hubauth.APIKeyRecord, retireAt time.Time) error {
	if err := scope.Authorize(tenancy.ActionManageWorkspace); err != nil {
		return fmt.Errorf("rotate API key: %w", err)
	}
	if !apiKeyIDPattern.MatchString(keyID) || retireAt.Before(replacement.CreatedAt) {
		return fmt.Errorf("rotate API key: invalid identifier or retirement time")
	}
	if err := validateAPIKeyRecord(scope.WorkspaceID(), replacement); err != nil {
		return fmt.Errorf("rotate API key: %w", err)
	}
	return database.InWorkspace(ctx, scope, func(tx pgx.Tx) error {
		var subject string
		if err := tx.QueryRow(ctx, `
			SELECT subject FROM sith.api_keys
			WHERE workspace_id = $1 AND id = $2 AND revoked_at IS NULL
			  AND retire_at IS NULL AND replaced_by IS NULL AND expires_at > $3
			FOR UPDATE
		`, scope.WorkspaceID(), keyID, replacement.CreatedAt).Scan(&subject); err != nil {
			return err
		}
		if subject != replacement.Subject {
			return fmt.Errorf("replacement subject does not match original key")
		}
		tag, err := tx.Exec(ctx, `
			INSERT INTO sith.api_keys(workspace_id, id, subject, verifier, created_at, expires_at)
			SELECT $1, $2, membership.subject, $3, $4, $5
			FROM sith.memberships membership
			WHERE membership.workspace_id = $1 AND membership.subject = $6
		`, replacement.WorkspaceID, replacement.ID, replacement.Verifier, replacement.CreatedAt, replacement.ExpiresAt, replacement.Subject)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return fmt.Errorf("subject is not a current member of the workspace")
		}
		_, err = tx.Exec(ctx, `
			UPDATE sith.api_keys SET retire_at = $3, replaced_by = $4
			WHERE workspace_id = $1 AND id = $2
		`, scope.WorkspaceID(), keyID, retireAt, replacement.ID)
		return err
	})
}

// RevokeAPIKey permanently marks a workspace key unusable.
func (database *AppDB) RevokeAPIKey(ctx context.Context, scope tenancy.Scope, keyID string, revokedAt time.Time) error {
	if err := scope.Authorize(tenancy.ActionManageWorkspace); err != nil {
		return fmt.Errorf("revoke API key: %w", err)
	}
	if !apiKeyIDPattern.MatchString(keyID) {
		return fmt.Errorf("revoke API key: invalid identifier")
	}
	return database.InWorkspace(ctx, scope, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE sith.api_keys SET revoked_at = $3
			WHERE workspace_id = $1 AND id = $2 AND revoked_at IS NULL
		`, scope.WorkspaceID(), keyID, revokedAt)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return hubauth.ErrAPIKeyNotFound
		}
		return nil
	})
}

func validateAPIKeyRecord(workspaceID tenancy.WorkspaceID, record hubauth.APIKeyRecord) error {
	if record.WorkspaceID != workspaceID || tenancy.ValidateWorkspaceID(record.WorkspaceID) != nil {
		return fmt.Errorf("workspace scope mismatch")
	}
	if !apiKeyIDPattern.MatchString(record.ID) || len(record.Verifier) != sha256.Size {
		return fmt.Errorf("invalid identifier or verifier")
	}
	membership := tenancy.Membership{WorkspaceID: record.WorkspaceID, Subject: record.Subject, Role: tenancy.RoleReader}
	if err := membership.Validate(); err != nil {
		return err
	}
	if record.CreatedAt.IsZero() || !record.ExpiresAt.After(record.CreatedAt) || record.RetireAt != nil || record.RevokedAt != nil || record.ReplacedBy != "" {
		return fmt.Errorf("invalid initial credential state")
	}
	return nil
}

func setWorkspaceScope(ctx context.Context, tx pgx.Tx, workspaceID tenancy.WorkspaceID) error {
	var configured string
	if err := tx.QueryRow(ctx, `SELECT set_config('sith.workspace_id', $1, true)`, string(workspaceID)).Scan(&configured); err != nil {
		return fmt.Errorf("set workspace transaction scope: %w", err)
	}
	if configured != string(workspaceID) {
		return fmt.Errorf("set workspace transaction scope: database returned a mismatched value")
	}
	return nil
}
