// SPDX-License-Identifier: Apache-2.0

package hubdb

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

type catalogQueryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

// AuditIsolation fails when any Sith table lacks the complete workspace RLS contract.
func AuditIsolation(ctx context.Context, database catalogQueryer, appRole string) error {
	if ctx == nil || database == nil {
		return fmt.Errorf("audit database isolation: context and database are required")
	}
	if err := validateRoleName(appRole); err != nil {
		return fmt.Errorf("audit database isolation: %w", err)
	}
	if err := requireSafeAppRole(ctx, database, appRole); err != nil {
		return err
	}
	var (
		canUseSchema bool
		canCreate    bool
		canUseMeta   bool
	)
	if err := database.QueryRow(ctx, `
		SELECT has_schema_privilege($1, 'sith', 'USAGE'),
		       has_schema_privilege($1, 'sith', 'CREATE'),
		       has_schema_privilege($1, 'sith_meta', 'USAGE')
	`, appRole).Scan(&canUseSchema, &canCreate, &canUseMeta); err != nil {
		return fmt.Errorf("audit database isolation: query schema privileges: %w", err)
	}
	var violations []string
	if !canUseSchema {
		violations = append(violations, "application role cannot use Sith schema")
	}
	if canCreate {
		violations = append(violations, "application role can create in Sith schema")
	}
	if canUseMeta {
		violations = append(violations, "application role can read migration metadata")
	}
	rows, err := database.Query(ctx, `
		SELECT c.relname,
		       c.relrowsecurity,
		       c.relforcerowsecurity,
		       pg_get_userbyid(c.relowner) = $1 AS app_owns_table,
		       EXISTS (
		           SELECT 1 FROM pg_attribute a
		           WHERE a.attrelid = c.oid
		             AND a.attname = CASE WHEN c.relname = 'workspaces' THEN 'id' ELSE 'workspace_id' END
		             AND a.attnotnull
		             AND a.atttypid = 'text'::regtype
		             AND NOT a.attisdropped
		       ) AS has_scope_column,
		       COALESCE(policy.using_expression, '') AS using_expression,
		       COALESCE(policy.check_expression, '') AS check_expression,
		       has_table_privilege($1, c.oid, 'SELECT') AS can_select,
		       has_table_privilege($1, c.oid, 'INSERT') AS can_insert,
		       has_any_column_privilege($1, c.oid, 'UPDATE') AS can_update,
		       has_table_privilege($1, c.oid, 'DELETE') AS can_delete,
		       has_table_privilege($1, c.oid, 'TRUNCATE')
		           OR has_any_column_privilege($1, c.oid, 'REFERENCES')
		           OR has_table_privilege($1, c.oid, 'TRIGGER')
		           OR has_table_privilege($1, c.oid, 'MAINTAIN') AS has_unsafe_privilege
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		LEFT JOIN LATERAL (
		    SELECT pg_get_expr(p.polqual, p.polrelid) AS using_expression,
		           pg_get_expr(p.polwithcheck, p.polrelid) AS check_expression
		    FROM pg_policy p
		    WHERE p.polrelid = c.oid
		      AND p.polname = 'workspace_isolation'
		      AND p.polcmd = '*'
		      AND p.polpermissive
		      AND p.polroles = ARRAY[0]::oid[]
		) policy ON true
		WHERE n.nspname = 'sith' AND c.relkind IN ('r', 'p')
		ORDER BY c.relname
	`, appRole)
	if err != nil {
		return fmt.Errorf("audit database isolation: query catalog: %w", err)
	}
	defer rows.Close()
	tableCount := 0
	for rows.Next() {
		tableCount++
		var (
			table              string
			rlsEnabled         bool
			rlsForced          bool
			appOwns            bool
			hasScopeColumn     bool
			usingExpression    string
			checkExpression    string
			canSelect          bool
			canInsert          bool
			canUpdate          bool
			canDelete          bool
			hasUnsafePrivilege bool
		)
		if err := rows.Scan(&table, &rlsEnabled, &rlsForced, &appOwns, &hasScopeColumn, &usingExpression, &checkExpression,
			&canSelect, &canInsert, &canUpdate, &canDelete, &hasUnsafePrivilege); err != nil {
			return fmt.Errorf("audit database isolation: scan catalog: %w", err)
		}
		if !rlsEnabled {
			violations = append(violations, table+": RLS is disabled")
		}
		if !rlsForced {
			violations = append(violations, table+": RLS is not forced")
		}
		if appOwns {
			violations = append(violations, table+": application role owns table")
		}
		if !hasScopeColumn {
			violations = append(violations, table+": required workspace scope column is missing")
		}
		scopeColumn := "workspace_id"
		if table == "workspaces" {
			scopeColumn = "id"
		}
		if !validPolicyExpression(usingExpression, scopeColumn) || !validPolicyExpression(checkExpression, scopeColumn) {
			violations = append(violations, table+": complete workspace policy is missing")
		}
		switch table {
		case "policy_audit_entries":
			if !canSelect || !canInsert || canUpdate || canDelete {
				violations = append(violations, table+": immutable application privilege contract is invalid")
			}
		case "policy_audit_heads":
			if !canSelect || !canInsert || !canUpdate || canDelete {
				violations = append(violations, table+": chain-head application privilege contract is invalid")
			}
		default:
			if !canSelect || !canInsert || !canUpdate || !canDelete {
				violations = append(violations, table+": application DML grant is incomplete")
			}
		}
		if hasUnsafePrivilege {
			violations = append(violations, table+": application role has unsafe table privileges")
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("audit database isolation: iterate catalog: %w", err)
	}
	if tableCount == 0 {
		violations = append(violations, "Sith schema contains no tables")
	}
	if len(violations) > 0 {
		return fmt.Errorf("audit database isolation: %s", strings.Join(violations, "; "))
	}
	return nil
}

func validPolicyExpression(expression, column string) bool {
	normalized := strings.Join(strings.Fields(expression), " ")
	expected := []string{
		"(" + column + " = current_setting('sith.workspace_id'::text, true))",
		column + " = current_setting('sith.workspace_id'::text, true)",
		"(" + column + " = current_setting('sith.workspace_id', true))",
		column + " = current_setting('sith.workspace_id', true)",
	}
	for _, candidate := range expected {
		if normalized == candidate {
			return true
		}
	}
	return false
}

func requireSafeAppRole(ctx context.Context, database catalogQueryer, appRole string) error {
	var (
		exists         bool
		superuser      bool
		bypassRLS      bool
		createRole     bool
		createDatabase bool
		replication    bool
		inherits       bool
		hasMemberships bool
	)
	err := database.QueryRow(ctx, `
		SELECT true, r.rolsuper, r.rolbypassrls, r.rolcreaterole, r.rolcreatedb, r.rolreplication,
		       r.rolinherit,
		       EXISTS (
		           SELECT 1 FROM pg_auth_members membership WHERE membership.member = r.oid
		       )
		FROM pg_roles r WHERE r.rolname = $1
	`, appRole).Scan(&exists, &superuser, &bypassRLS, &createRole, &createDatabase, &replication, &inherits, &hasMemberships)
	if err != nil {
		if err == pgx.ErrNoRows {
			return fmt.Errorf("application role %q does not exist", appRole)
		}
		return fmt.Errorf("inspect application role %q: %w", appRole, err)
	}
	if !exists || superuser || bypassRLS || createRole || createDatabase || replication || inherits || hasMemberships {
		return fmt.Errorf("application role %q is not least-privilege", appRole)
	}
	return nil
}
