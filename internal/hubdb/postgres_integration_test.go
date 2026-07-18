// SPDX-License-Identifier: Apache-2.0
//go:build postgres

package hubdb

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/ArdurAI/sith/internal/auditrecord"
	"github.com/ArdurAI/sith/internal/fleet"
	"github.com/ArdurAI/sith/internal/hubauth"
	"github.com/ArdurAI/sith/internal/hubfleet"
	"github.com/ArdurAI/sith/internal/intent"
	"github.com/ArdurAI/sith/internal/pep"
	"github.com/ArdurAI/sith/internal/tenancy"
	"github.com/ArdurAI/sith/internal/tracing"
)

const (
	defaultPostgresImage = "postgres:18.4-alpine3.23@sha256:996d0920e4ff9df1fc19dacb904492f3c1ec0ec1cc338f0ad7123be7731c5f5e"
	membershipPolicy     = `CREATE POLICY workspace_isolation ON sith.memberships
		FOR ALL TO PUBLIC
		USING (workspace_id = current_setting('sith.workspace_id', true))
		WITH CHECK (workspace_id = current_setting('sith.workspace_id', true))`
	restoreMembershipPolicy = `ALTER POLICY workspace_isolation ON sith.memberships
		TO PUBLIC
		USING (workspace_id = current_setting('sith.workspace_id', true))
		WITH CHECK (workspace_id = current_setting('sith.workspace_id', true))`
)

func TestPostgresRLSBackstop(t *testing.T) {
	adminURL := startPostgres(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	admin := connectPostgres(t, ctx, adminURL)
	defer admin.Close(context.Background())
	ownerRole, appRole := "sith_owner", "sith_app"
	ownerPassword, appPassword := randomHex(t, 24), randomHex(t, 24)
	createTestRole(t, ctx, admin, ownerRole, ownerPassword)
	createTestRole(t, ctx, admin, appRole, appPassword)
	if _, err := admin.Exec(ctx, `GRANT CREATE ON DATABASE sith_test TO `+pgx.Identifier{ownerRole}.Sanitize()); err != nil {
		t.Fatal(err)
	}

	ownerURL := databaseURL(adminURL, ownerRole, ownerPassword)
	owner := connectPostgres(t, ctx, ownerURL)
	defer owner.Close(context.Background())
	if err := Migrate(ctx, MigrationConfig{OwnerURL: ownerURL, ApplicationRole: appRole, AllowInsecureLocal: true}); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	if err := Migrate(ctx, MigrationConfig{OwnerURL: ownerURL, ApplicationRole: appRole, AllowInsecureLocal: true}); err != nil {
		t.Fatalf("idempotent Migrate() error = %v", err)
	}
	if _, err := owner.Exec(ctx, `GRANT UPDATE (actor) ON sith.policy_audit_entries TO `+pgx.Identifier{appRole}.Sanitize()); err != nil {
		t.Fatalf("grant column-level audit mutation fixture: %v", err)
	}
	if err := AuditIsolation(ctx, owner, appRole); err == nil || !strings.Contains(err.Error(), "immutable application privilege contract is invalid") {
		t.Fatalf("column-level audit mutation escaped catalog audit: %v", err)
	}
	if err := Migrate(ctx, MigrationConfig{OwnerURL: ownerURL, ApplicationRole: appRole, AllowInsecureLocal: true}); err != nil {
		t.Fatalf("Migrate() did not repair column-level audit privilege drift: %v", err)
	}
	var canUpdateAuditColumn bool
	if err := owner.QueryRow(ctx, `
		SELECT has_any_column_privilege($1, 'sith.policy_audit_entries', 'UPDATE')
	`, appRole).Scan(&canUpdateAuditColumn); err != nil {
		t.Fatalf("inspect repaired column-level audit privileges: %v", err)
	}
	if canUpdateAuditColumn {
		t.Fatal("migration retained column-level UPDATE on immutable audit entries")
	}
	if err := AuditIsolation(ctx, owner, appRole); err != nil {
		t.Fatalf("repaired AuditIsolation() error = %v", err)
	}
	seedTenantRows(t, ctx, admin)

	if _, err := OpenAppDB(ctx, AppConfig{URL: databaseURL(adminURL, appRole, appPassword)}); err == nil {
		t.Fatal("plaintext application connection unexpectedly accepted by default")
	}
	if privileged, err := OpenAppDB(ctx, AppConfig{URL: ownerURL, AllowInsecure: true}); err == nil {
		privileged.Close()
		t.Fatal("table-owner application connection unexpectedly accepted")
	}
	if privileged, err := OpenAppDB(ctx, AppConfig{URL: adminURL, AllowInsecure: true}); err == nil {
		privileged.Close()
		t.Fatal("superuser application connection unexpectedly accepted")
	}

	database, err := OpenAppDB(ctx, AppConfig{
		URL: databaseURL(adminURL, appRole, appPassword), MaxConns: 2, AllowInsecure: true,
	})
	if err != nil {
		t.Fatalf("OpenAppDB() error = %v", err)
	}
	defer database.Close()

	principal, err := tenancy.NewPrincipal("user:alice", map[tenancy.WorkspaceID]tenancy.Role{
		"workspace-a": tenancy.RoleReader,
	})
	if err != nil {
		t.Fatal(err)
	}
	scope, err := principal.Scope("workspace-a")
	if err != nil {
		t.Fatal(err)
	}

	if err := assertWorkspaceAIsolation(ctx, database, scope); err != nil {
		t.Fatalf("workspace A query: %v", err)
	}

	tables := []string{
		"workspaces", "memberships", "clusters", "fleet_facts", "api_keys", "oidc_bindings",
		"cloud_identity_bindings", "policy_audit_heads", "policy_audit_entries", "approval_grants",
	}
	for _, table := range tables {
		var count int
		if err := database.pool.QueryRow(ctx, `SELECT count(*) FROM sith.`+pgx.Identifier{table}.Sanitize()).Scan(&count); err != nil {
			t.Fatalf("unscoped %s query: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("transaction-local scope leaked: unscoped %s count = %d", table, count)
		}
	}

	foreignWrites := []struct {
		name      string
		statement string
	}{
		{name: "workspace", statement: `INSERT INTO sith.workspaces(id, name, tenant_key)
			VALUES ('workspace-c', 'Workspace C', 'shared-display-key')`},
		{name: "membership", statement: `INSERT INTO sith.memberships(workspace_id, subject, role)
			VALUES ('workspace-b', 'user:mallory', 'admin')`},
		{name: "cluster", statement: `INSERT INTO sith.clusters(workspace_id, id, managed_cluster_ref)
			VALUES ('workspace-b', 'cluster-b2', 'ocm/cluster-b2')`},
		{name: "fleet fact", statement: `INSERT INTO sith.fleet_facts(workspace_id, cluster_id, kind, payload, observed_at)
			VALUES ('workspace-b', 'cluster-b', 'health', '{}', now())`},
		{name: "API key", statement: `INSERT INTO sith.api_keys(workspace_id, id, subject, verifier, created_at, expires_at)
			VALUES ('workspace-b', 'CCCCCCCCCCCCCCCCCCCCCC', 'user:bob', decode(repeat('cc', 32), 'hex'), now(), now() + interval '1 day')`},
		{name: "OIDC binding", statement: `INSERT INTO sith.oidc_bindings(workspace_id, issuer, upstream_subject, member_subject)
			VALUES ('workspace-b', 'https://idp.example', 'upstream:mallory', 'user:bob')`},
		{name: "cloud identity binding", statement: "INSERT INTO sith.cloud_identity_bindings(workspace_id, provider, realm, upstream_subject, member_subject)\n\t\t\tVALUES ('workspace-b', 'aws', '222222222222', 'AROAX:mallory', 'user:bob')"},
		{name: "approval grant", statement: `INSERT INTO sith.approval_grants(
			workspace_id, id, intent_id, proposer, approver, resolved_digest, approved_at
		) VALUES ('workspace-b', 'ZZZZZZZZZZZZZZZZZZZZZZ', 'intent-foreign', 'user:operator',
			'user:approver', 'sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', now())`},
	}
	for _, test := range foreignWrites {
		t.Run("foreign "+test.name+" write denied", func(t *testing.T) {
			foreignWriteErr := database.InWorkspace(ctx, scope, func(tx pgx.Tx) error {
				_, err := tx.Exec(ctx, test.statement)
				return err
			})
			var postgresErr *pgconn.PgError
			if !errors.As(foreignWriteErr, &postgresErr) || postgresErr.Code != "42501" {
				t.Fatalf("foreign write error = %v, want RLS policy violation 42501", foreignWriteErr)
			}
		})
	}

	if err := database.InWorkspace(ctx, scope, func(tx pgx.Tx) error {
		for _, statement := range []string{
			`UPDATE sith.memberships SET role = 'reader' WHERE workspace_id = 'workspace-b'`,
			`DELETE FROM sith.clusters WHERE workspace_id = 'workspace-b'`,
		} {
			tag, err := tx.Exec(ctx, statement)
			if err != nil {
				return err
			}
			if tag.RowsAffected() != 0 {
				return fmt.Errorf("foreign mutation affected %d rows", tag.RowsAffected())
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("foreign update/delete isolation: %v", err)
	}

	truncateErr := database.InWorkspace(ctx, scope, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `TRUNCATE sith.memberships`)
		return err
	})
	if truncateErr == nil {
		t.Fatal("application role unexpectedly truncated an RLS table")
	}

	var ownerVisible int
	if err := owner.QueryRow(ctx, `SELECT count(*) FROM sith.memberships`).Scan(&ownerVisible); err != nil {
		t.Fatalf("owner FORCE RLS query: %v", err)
	}
	if ownerVisible != 0 {
		t.Fatalf("table owner bypassed FORCE RLS and saw %d rows", ownerVisible)
	}
	if err := AuditIsolation(ctx, owner, appRole); err != nil {
		t.Fatalf("AuditIsolation() error = %v", err)
	}

	drift, err := owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := drift.Exec(ctx, `ALTER TABLE sith.memberships NO FORCE ROW LEVEL SECURITY`); err != nil {
		t.Fatal(err)
	}
	if err := AuditIsolation(ctx, drift, appRole); err == nil || !strings.Contains(err.Error(), "RLS is not forced") {
		t.Fatalf("catalog guard did not detect RLS drift: %v", err)
	}
	if err := drift.Rollback(ctx); err != nil {
		t.Fatal(err)
	}

	if _, err := owner.Exec(ctx, `DROP POLICY workspace_isolation ON sith.memberships`); err != nil {
		t.Fatal(err)
	}
	if err := assertWorkspaceAIsolation(ctx, database, scope); err == nil || !strings.Contains(err.Error(), "memberships count = 0") {
		t.Fatalf("removed-policy negative control did not fail the isolation invariant: %v", err)
	}
	if err := AuditIsolation(ctx, owner, appRole); err == nil || !strings.Contains(err.Error(), "complete workspace policy is missing") {
		t.Fatalf("removed-policy negative control escaped catalog audit: %v", err)
	}
	if _, err := owner.Exec(ctx, membershipPolicy); err != nil {
		t.Fatal(err)
	}
	if err := assertWorkspaceAIsolation(ctx, database, scope); err != nil {
		t.Fatalf("restored policy did not restore isolation invariant: %v", err)
	}

	if _, err := owner.Exec(ctx, `ALTER POLICY workspace_isolation ON sith.memberships
		USING (true) WITH CHECK (true)`); err != nil {
		t.Fatal(err)
	}
	if err := assertWorkspaceAIsolation(ctx, database, scope); err == nil || !strings.Contains(err.Error(), "memberships count = 2") {
		t.Fatalf("permissive-policy mutation did not expose the isolation invariant: %v", err)
	}
	if err := AuditIsolation(ctx, owner, appRole); err == nil || !strings.Contains(err.Error(), "complete workspace policy is missing") {
		t.Fatalf("permissive-policy mutation escaped catalog audit: %v", err)
	}
	if _, err := owner.Exec(ctx, restoreMembershipPolicy); err != nil {
		t.Fatal(err)
	}
	if err := assertWorkspaceAIsolation(ctx, database, scope); err != nil {
		t.Fatalf("exact policy restoration did not recover invariant: %v", err)
	}
	if err := AuditIsolation(ctx, owner, appRole); err != nil {
		t.Fatalf("restored AuditIsolation() error = %v", err)
	}

	if _, err := owner.Exec(ctx, `CREATE POLICY workspace_bypass ON sith.memberships
		AS PERMISSIVE FOR ALL TO PUBLIC USING (true) WITH CHECK (true)`); err != nil {
		t.Fatal(err)
	}
	if err := assertWorkspaceAIsolation(ctx, database, scope); err == nil || !strings.Contains(err.Error(), "memberships count = 2") {
		t.Fatalf("additional-policy negative control did not expose the isolation invariant: %v", err)
	}
	if err := AuditIsolation(ctx, owner, appRole); err == nil || !strings.Contains(err.Error(), "complete workspace policy is missing") {
		t.Fatalf("additional-policy negative control escaped catalog audit: %v", err)
	}
	if _, err := owner.Exec(ctx, `DROP POLICY workspace_bypass ON sith.memberships`); err != nil {
		t.Fatal(err)
	}
	if err := assertWorkspaceAIsolation(ctx, database, scope); err != nil {
		t.Fatalf("additional-policy removal did not recover invariant: %v", err)
	}
	if err := AuditIsolation(ctx, owner, appRole); err != nil {
		t.Fatalf("additional-policy removal AuditIsolation() error = %v", err)
	}

	assertAPIKeyStoreIntegration(t, ctx, database, admin)
	assertOIDCStoreIntegration(t, ctx, database)
	assertCloudIdentityStoreIntegration(t, ctx, database)
	assertFleetStoreIntegration(t, ctx, database)
	assertPolicyAuditChainIntegration(t, ctx, database, admin, appRole)
	assertApprovalGrantIntegration(t, ctx, database, admin, appRole)
}

func assertWorkspaceAIsolation(ctx context.Context, database *AppDB, scope tenancy.Scope) error {
	return database.InWorkspace(ctx, scope, func(tx pgx.Tx) error {
		for _, table := range []string{"workspaces", "memberships", "clusters", "fleet_facts", "api_keys", "oidc_bindings", "cloud_identity_bindings", "approval_grants"} {
			var count int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM sith.`+pgx.Identifier{table}.Sanitize()).Scan(&count); err != nil {
				return err
			}
			if count != 1 {
				return fmt.Errorf("scoped %s count = %d, want 1", table, count)
			}
		}
		var foreignCount int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM sith.memberships WHERE workspace_id = 'workspace-b'`).Scan(&foreignCount); err != nil {
			return err
		}
		if foreignCount != 0 {
			return fmt.Errorf("direct foreign query returned %d rows", foreignCount)
		}
		return nil
	})
}

func assertPolicyAuditChainIntegration(
	t *testing.T,
	ctx context.Context,
	database *AppDB,
	admin *pgx.Conn,
	appRole string,
) {
	t.Helper()

	workspaceA := testScope(t, "user:alice", "workspace-a", tenancy.RoleAdmin)
	workspaceB := testScope(t, "user:bob", "workspace-b", tenancy.RoleAdmin)
	if err := database.Record(ctx, newPolicyAuditEvent(t, workspaceA)); err != nil {
		t.Fatalf("Record(workspace A) error = %v", err)
	}
	if err := database.Record(ctx, newPolicyAuditEvent(t, workspaceB)); err != nil {
		t.Fatalf("Record(workspace B) error = %v", err)
	}

	const concurrentAppends = 20
	errorsByAppend := make(chan error, concurrentAppends)
	var writers sync.WaitGroup
	for range concurrentAppends {
		event := newPolicyAuditEvent(t, workspaceA)
		writers.Add(1)
		go func(event pep.AuditEvent) {
			defer writers.Done()
			errorsByAppend <- database.Record(ctx, event)
		}(event)
	}
	writers.Wait()
	close(errorsByAppend)
	for err := range errorsByAppend {
		if err != nil {
			t.Fatalf("concurrent Record() error = %v", err)
		}
	}

	for name, scope := range map[string]tenancy.Scope{"workspace A": workspaceA, "workspace B": workspaceB} {
		if err := database.VerifyPolicyAuditChain(ctx, scope); err != nil {
			t.Fatalf("VerifyPolicyAuditChain(%s) error = %v", name, err)
		}
	}
	enforcer, err := pep.NewEnforcer(pep.Config{Hook: pep.AllowReadHook{}, Auditor: database})
	if err != nil {
		t.Fatal(err)
	}
	if err := enforcer.AuthorizeAuditExport(ctx, workspaceA); err != nil {
		t.Fatalf("AuthorizeAuditExport(workspace A) error = %v", err)
	}
	exportedA, err := database.ExportPolicyAuditChain(ctx, workspaceA)
	if err != nil {
		t.Fatalf("ExportPolicyAuditChain(workspace A) error = %v", err)
	}
	if exportedA.Schema != "sith.policy-audit-export/v1" || exportedA.WorkspaceID != "workspace-a" ||
		exportedA.Chain.HeadSequence != concurrentAppends+2 || len(exportedA.Entries) != concurrentAppends+2 {
		t.Fatalf("workspace A export shape = %#v", exportedA)
	}
	last := exportedA.Entries[len(exportedA.Entries)-1]
	if last.Action != string(tenancy.ActionExportAudit) || last.Verb != string(pep.VerbAuditExport) ||
		last.Verdict != string(pep.VerdictAllow) || last.ReasonCode != "phase-1-audit-export" ||
		last.EntryHash != exportedA.Chain.HeadHash {
		t.Fatalf("workspace A export did not include its authorizing decision: %#v", last)
	}
	exportedB, err := database.ExportPolicyAuditChain(ctx, workspaceB)
	if err != nil || len(exportedB.Entries) != 1 || exportedB.WorkspaceID != "workspace-b" ||
		exportedB.Entries[0].Actor != "user:bob" || strings.Contains(fmt.Sprintf("%#v", exportedB), "user:alice") {
		t.Fatalf("workspace B export/error = %#v/%v", exportedB, err)
	}
	if err := database.InWorkspace(ctx, workspaceA, func(tx pgx.Tx) error {
		var headCount, entryCount, foreignCount int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM sith.policy_audit_heads`).Scan(&headCount); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM sith.policy_audit_entries`).Scan(&entryCount); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM sith.policy_audit_entries WHERE workspace_id = 'workspace-b'
		`).Scan(&foreignCount); err != nil {
			return err
		}
		if headCount != 1 || entryCount != concurrentAppends+2 || foreignCount != 0 {
			return fmt.Errorf("workspace A audit view = heads %d entries %d foreign %d", headCount, entryCount, foreignCount)
		}
		return nil
	}); err != nil {
		t.Fatalf("policy audit RLS isolation: %v", err)
	}

	var canSelect, canInsert, canUpdate, canDelete bool
	if err := admin.QueryRow(ctx, `
		SELECT has_table_privilege($1, 'sith.policy_audit_entries', 'SELECT'),
		       has_table_privilege($1, 'sith.policy_audit_entries', 'INSERT'),
		       has_any_column_privilege($1, 'sith.policy_audit_entries', 'UPDATE'),
		       has_table_privilege($1, 'sith.policy_audit_entries', 'DELETE')
	`, appRole).Scan(&canSelect, &canInsert, &canUpdate, &canDelete); err != nil {
		t.Fatalf("inspect policy audit privileges: %v", err)
	}
	if !canSelect || !canInsert || canUpdate || canDelete {
		t.Fatalf("immutable audit privileges = select:%t insert:%t update:%t delete:%t", canSelect, canInsert, canUpdate, canDelete)
	}
	for name, statement := range map[string]string{
		"update entry": `UPDATE sith.policy_audit_entries SET actor = 'user:mallory' WHERE sequence = 1`,
		"delete entry": `DELETE FROM sith.policy_audit_entries WHERE sequence = 1`,
		"delete head":  `DELETE FROM sith.policy_audit_heads`,
	} {
		err := database.InWorkspace(ctx, workspaceA, func(tx pgx.Tx) error {
			_, execErr := tx.Exec(ctx, statement)
			return execErr
		})
		var postgresErr *pgconn.PgError
		if !errors.As(err, &postgresErr) || postgresErr.Code != "42501" {
			t.Fatalf("%s error = %v, want privilege denial 42501", name, err)
		}
	}

	var originalPrevious, originalEntryHash, originalHeadHash []byte
	if err := admin.QueryRow(ctx, `
		SELECT previous_hash, entry_hash
		FROM sith.policy_audit_entries
		WHERE workspace_id = 'workspace-a' AND sequence = 2
	`).Scan(&originalPrevious, &originalEntryHash); err != nil {
		t.Fatalf("read policy audit tamper fixture: %v", err)
	}
	if err := admin.QueryRow(ctx, `
		SELECT last_hash FROM sith.policy_audit_heads WHERE workspace_id = 'workspace-a'
	`).Scan(&originalHeadHash); err != nil {
		t.Fatalf("read policy audit head fixture: %v", err)
	}

	assertTamperDetected := func(name, mutate, restore string, mutateArgs, restoreArgs []any) {
		t.Helper()
		if _, err := admin.Exec(ctx, mutate, mutateArgs...); err != nil {
			t.Fatalf("%s mutation: %v", name, err)
		}
		if err := database.VerifyPolicyAuditChain(ctx, workspaceA); err == nil {
			t.Fatalf("%s was not detected", name)
		}
		if _, err := database.ExportPolicyAuditChain(ctx, workspaceA); err == nil {
			t.Fatalf("%s was exported", name)
		}
		if _, err := admin.Exec(ctx, restore, restoreArgs...); err != nil {
			t.Fatalf("%s restore: %v", name, err)
		}
		if err := database.VerifyPolicyAuditChain(ctx, workspaceA); err != nil {
			t.Fatalf("%s restore did not recover chain: %v", name, err)
		}
	}
	assertTamperDetected(
		"field mutation",
		`UPDATE sith.policy_audit_entries SET actor = 'user:mallory' WHERE workspace_id = 'workspace-a' AND sequence = 2`,
		`UPDATE sith.policy_audit_entries SET actor = 'user:alice' WHERE workspace_id = 'workspace-a' AND sequence = 2`,
		nil, nil,
	)
	assertTamperDetected(
		"sequence reordering",
		`UPDATE sith.policy_audit_entries SET sequence = 100 WHERE workspace_id = 'workspace-a' AND sequence = 2`,
		`UPDATE sith.policy_audit_entries SET sequence = 2 WHERE workspace_id = 'workspace-a' AND sequence = 100`,
		nil, nil,
	)
	assertTamperDetected(
		"previous hash mutation",
		`UPDATE sith.policy_audit_entries SET previous_hash = decode(repeat('00', 32), 'hex') WHERE workspace_id = 'workspace-a' AND sequence = 2`,
		`UPDATE sith.policy_audit_entries SET previous_hash = $1 WHERE workspace_id = 'workspace-a' AND sequence = 2`,
		nil, []any{originalPrevious},
	)
	assertTamperDetected(
		"entry hash mutation",
		`UPDATE sith.policy_audit_entries SET entry_hash = decode(repeat('00', 32), 'hex') WHERE workspace_id = 'workspace-a' AND sequence = 2`,
		`UPDATE sith.policy_audit_entries SET entry_hash = $1 WHERE workspace_id = 'workspace-a' AND sequence = 2`,
		nil, []any{originalEntryHash},
	)
	assertTamperDetected(
		"head mismatch",
		`UPDATE sith.policy_audit_heads SET last_hash = decode(repeat('00', 32), 'hex') WHERE workspace_id = 'workspace-a'`,
		`UPDATE sith.policy_audit_heads SET last_hash = $1 WHERE workspace_id = 'workspace-a'`,
		nil, []any{originalHeadHash},
	)
	if _, err := admin.Exec(ctx, `
		CREATE TEMP TABLE policy_audit_deleted_fixture AS
		SELECT * FROM sith.policy_audit_entries
		WHERE workspace_id = 'workspace-a' AND sequence = 2
	`); err != nil {
		t.Fatalf("save retained policy audit entry: %v", err)
	}
	if _, err := admin.Exec(ctx, `
		DELETE FROM sith.policy_audit_entries WHERE workspace_id = 'workspace-a' AND sequence = 2
	`); err != nil {
		t.Fatalf("delete retained policy audit entry: %v", err)
	}
	if err := database.VerifyPolicyAuditChain(ctx, workspaceA); err == nil {
		t.Fatal("deleted retained policy audit entry was not detected")
	}
	if _, err := admin.Exec(ctx, `
		INSERT INTO sith.policy_audit_entries
		SELECT * FROM policy_audit_deleted_fixture;
		DROP TABLE policy_audit_deleted_fixture
	`); err != nil {
		t.Fatalf("restore retained policy audit entry: %v", err)
	}
	if err := database.VerifyPolicyAuditChain(ctx, workspaceA); err != nil {
		t.Fatalf("deleted entry restore did not recover chain: %v", err)
	}
	if _, err := database.ExportPolicyAuditChain(ctx, testScope(t, "user:alice", "workspace-a", tenancy.RoleReader)); err == nil {
		t.Fatal("reader scope exported the policy audit chain")
	}

	for sequence := exportedA.Chain.HeadSequence + 1; sequence <= auditrecord.MaxEntries; sequence++ {
		if err := database.Record(ctx, newPolicyAuditEvent(t, workspaceA)); err != nil {
			t.Fatalf("fill bounded export at sequence %d: %v", sequence, err)
		}
	}
	bounded, err := database.ExportPolicyAuditChain(ctx, workspaceA)
	if err != nil || len(bounded.Entries) != auditrecord.MaxEntries || bounded.Chain.HeadSequence != auditrecord.MaxEntries {
		t.Fatalf("512-entry export/error = %d/%d/%v", len(bounded.Entries), bounded.Chain.HeadSequence, err)
	}
	if err := database.Record(ctx, newPolicyAuditEvent(t, workspaceA)); err != nil {
		t.Fatalf("append export overflow sentinel: %v", err)
	}
	if oversized, err := database.ExportPolicyAuditChain(ctx, workspaceA); err == nil || oversized.Schema != "" || len(oversized.Entries) != 0 {
		t.Fatalf("513-entry export/error = %#v/%v", oversized, err)
	}

	firstRequest, err := auditrecord.FirstPage(workspaceA.WorkspaceID())
	if err != nil {
		t.Fatal(err)
	}
	firstPage, err := database.ExportPolicyAuditPage(ctx, workspaceA, firstRequest)
	if err != nil || len(firstPage.Entries) != auditrecord.MaxEntries || firstPage.StartSequence != 1 ||
		firstPage.Snapshot.HeadSequence != auditrecord.MaxEntries+1 || firstPage.NextCursor == "" {
		t.Fatalf("first audit page/error = %#v/%v", firstPage, err)
	}
	if err := database.Record(ctx, newPolicyAuditEvent(t, workspaceA)); err != nil {
		t.Fatalf("append after fixed audit snapshot: %v", err)
	}
	continuation, err := auditrecord.ContinuePage(workspaceA.WorkspaceID(), firstPage.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	finalPage, err := database.ExportPolicyAuditPage(ctx, workspaceA, continuation)
	if err != nil || len(finalPage.Entries) != 1 || finalPage.StartSequence != auditrecord.MaxEntries+1 ||
		finalPage.Snapshot != firstPage.Snapshot || finalPage.NextCursor != "" {
		t.Fatalf("final audit page/error = %#v/%v", finalPage, err)
	}
	var pageVerifier auditrecord.PageSequenceVerifier
	if err := pageVerifier.Add(firstPage); err != nil {
		t.Fatal(err)
	}
	if err := pageVerifier.Add(finalPage); err != nil {
		t.Fatal(err)
	}
	if result, err := pageVerifier.Finish(); err != nil || result.Entries != auditrecord.MaxEntries+1 {
		t.Fatalf("paged PostgreSQL verification = %#v/%v", result, err)
	}
	foreignRequest, err := auditrecord.FirstPage("workspace-b")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExportPolicyAuditPage(ctx, workspaceA, foreignRequest); err == nil {
		t.Fatal("foreign page request crossed workspace storage boundary")
	}
	if _, err := database.ExportPolicyAuditPage(
		ctx, testScope(t, "user:alice", "workspace-a", tenancy.RoleReader), firstRequest,
	); err == nil {
		t.Fatal("reader scope exported an audit page")
	}

	cursorPayload, err := base64.RawURLEncoding.DecodeString(firstPage.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	for name, offset := range map[string]int{"snapshot head": 41, "previous anchor": len(cursorPayload) - 1} {
		t.Run("paged export rejects altered "+name, func(t *testing.T) {
			altered := append([]byte(nil), cursorPayload...)
			altered[offset] ^= 1
			request, err := auditrecord.ContinuePage(
				workspaceA.WorkspaceID(), base64.RawURLEncoding.EncodeToString(altered),
			)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := database.ExportPolicyAuditPage(ctx, workspaceA, request); err == nil {
				t.Fatal("altered continuation reached a successful page")
			}
		})
	}
}

func assertApprovalGrantIntegration(
	t *testing.T,
	ctx context.Context,
	database *AppDB,
	admin *pgx.Conn,
	appRole string,
) {
	t.Helper()

	if _, err := admin.Exec(ctx, `INSERT INTO sith.memberships(workspace_id, subject, role) VALUES
		('workspace-a', 'user:operator-a', 'operator'),
		('workspace-a', 'user:approver-a', 'approver'),
		('workspace-a', 'user:stale-approver', 'reader'),
		('workspace-b', 'user:operator-b', 'operator'),
		('workspace-b', 'user:approver-b', 'approver')`); err != nil {
		t.Fatalf("seed approval memberships: %v", err)
	}

	proposerA := testScope(t, "user:operator-a", "workspace-a", tenancy.RoleOperator)
	approverA := testScope(t, "user:approver-a", "workspace-a", tenancy.RoleApprover)
	proposerB := testScope(t, "user:operator-b", "workspace-b", tenancy.RoleOperator)
	approverB := testScope(t, "user:approver-b", "workspace-b", tenancy.RoleApprover)
	now := time.Now().UTC().Truncate(time.Microsecond)

	create := func(intentID, arguments string) (pep.ApprovalBinding, ApprovalGrantID) {
		t.Helper()
		binding := postgresApprovalBinding(t, intentID, "workspace-a", proposerA.Subject(), arguments)
		identifier, err := database.CreateApprovalGrant(ctx, approverA, binding, now)
		if err != nil {
			t.Fatalf("CreateApprovalGrant(%s) error = %v", intentID, err)
		}
		if !approvalGrantIDPattern.MatchString(identifier.String()) {
			t.Fatalf("CreateApprovalGrant(%s) identifier = %q", intentID, identifier)
		}
		return binding, identifier
	}

	baseBinding, baseID := create("intent-250-base", "replicas=3")
	if _, err := database.CreateApprovalGrant(ctx, approverA, baseBinding, now); !errors.Is(err, ErrApprovalGrantUnavailable) {
		t.Fatalf("duplicate approval error = %v", err)
	}

	for name, scope := range map[string]tenancy.Scope{
		"reader":   testScope(t, "user:reader", "workspace-a", tenancy.RoleReader),
		"operator": proposerA,
		"admin":    testScope(t, "user:admin", "workspace-a", tenancy.RoleAdmin),
	} {
		if _, err := database.CreateApprovalGrant(ctx, scope, postgresApprovalBinding(t,
			"intent-role-"+name, "workspace-a", proposerA.Subject(), name), now); !errors.Is(err, ErrApprovalGrantUnavailable) {
			t.Fatalf("%s approval error = %v", name, err)
		}
	}
	selfScope := testScope(t, "user:self", "workspace-a", tenancy.RoleApprover)
	if _, err := database.CreateApprovalGrant(ctx, selfScope, postgresApprovalBinding(
		t, "intent-self", "workspace-a", selfScope.Subject(), "self"), now); !errors.Is(err, ErrApprovalGrantUnavailable) {
		t.Fatalf("self approval error = %v", err)
	}
	staleScope := testScope(t, "user:stale-approver", "workspace-a", tenancy.RoleApprover)
	if _, err := database.CreateApprovalGrant(ctx, staleScope, postgresApprovalBinding(
		t, "intent-stale-role", "workspace-a", proposerA.Subject(), "stale"), now); !errors.Is(err, ErrApprovalGrantUnavailable) {
		t.Fatalf("stale approver role error = %v", err)
	}
	if _, err := database.CreateApprovalGrant(ctx, approverA, postgresApprovalBinding(
		t, "intent-missing-proposer", "workspace-a", "user:missing", "missing"), now); !errors.Is(err, ErrApprovalGrantUnavailable) {
		t.Fatalf("missing proposer membership error = %v", err)
	}
	if _, err := database.CreateApprovalGrant(ctx, approverA, postgresApprovalBinding(
		t, "intent-future-approval", "workspace-a", proposerA.Subject(), "future"),
		now.Add(2*time.Minute)); !errors.Is(err, ErrApprovalGrantUnavailable) {
		t.Fatalf("future approval time error = %v", err)
	}

	wrongDigestBinding := postgresApprovalBinding(t, baseBinding.IntentID(), "workspace-a", proposerA.Subject(), "replicas=4")
	if err := database.ConsumeApprovalGrant(ctx, proposerA, wrongDigestBinding, baseID, now.Add(time.Second)); !errors.Is(err, ErrApprovalGrantUnavailable) {
		t.Fatalf("wrong digest consume error = %v", err)
	}
	wrongIntentBinding := postgresApprovalBinding(t, "intent-250-other", "workspace-a", proposerA.Subject(), "replicas=3")
	if err := database.ConsumeApprovalGrant(ctx, proposerA, wrongIntentBinding, baseID, now.Add(time.Second)); !errors.Is(err, ErrApprovalGrantUnavailable) {
		t.Fatalf("wrong intent consume error = %v", err)
	}
	if err := database.ConsumeApprovalGrant(ctx, proposerB, baseBinding, baseID, now.Add(time.Second)); !errors.Is(err, ErrApprovalGrantUnavailable) {
		t.Fatalf("foreign workspace consume error = %v", err)
	}
	if err := database.ConsumeApprovalGrant(ctx, proposerA, baseBinding, "XXXXXXXXXXXXXXXXXXXXXX", now.Add(time.Second)); !errors.Is(err, ErrApprovalGrantUnavailable) {
		t.Fatalf("unknown approval consume error = %v", err)
	}
	if err := database.ConsumeApprovalGrant(ctx, proposerA, baseBinding, baseID, now.Add(-time.Second)); !errors.Is(err, ErrApprovalGrantUnavailable) {
		t.Fatalf("pre-approval consume error = %v", err)
	}
	if err := database.ConsumeApprovalGrant(ctx, proposerA, baseBinding, baseID, now.Add(time.Second)); err != nil {
		t.Fatalf("exact approval consume error = %v", err)
	}
	if err := database.ConsumeApprovalGrant(ctx, proposerA, baseBinding, baseID, now.Add(2*time.Second)); !errors.Is(err, ErrApprovalGrantUnavailable) {
		t.Fatalf("replayed approval consume error = %v", err)
	}

	concurrentBinding, concurrentID := create("intent-250-concurrent", "image=v2")
	results := make(chan error, 2)
	var consumers sync.WaitGroup
	for range 2 {
		consumers.Add(1)
		go func() {
			defer consumers.Done()
			results <- database.ConsumeApprovalGrant(ctx, proposerA, concurrentBinding, concurrentID, now.Add(time.Second))
		}()
	}
	consumers.Wait()
	close(results)
	var successes, refusals int
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrApprovalGrantUnavailable):
			refusals++
		default:
			t.Fatalf("concurrent approval consume error = %v", err)
		}
	}
	if successes != 1 || refusals != 1 {
		t.Fatalf("concurrent consumes = successes %d refusals %d", successes, refusals)
	}

	var originalHeadSequence int64
	var originalHeadHash []byte
	readAndCorruptHead := func() {
		t.Helper()
		if err := admin.QueryRow(ctx, `
			SELECT last_sequence, last_hash
			FROM sith.policy_audit_heads WHERE workspace_id = 'workspace-a'
		`).Scan(&originalHeadSequence, &originalHeadHash); err != nil {
			t.Fatalf("read approval audit head fixture: %v", err)
		}
		if _, err := admin.Exec(ctx, `
			UPDATE sith.policy_audit_heads SET last_sequence = 9223372036854775807
			WHERE workspace_id = 'workspace-a'
		`); err != nil {
			t.Fatalf("corrupt approval audit head fixture: %v", err)
		}
	}
	restoreHead := func() {
		t.Helper()
		if _, err := admin.Exec(ctx, `
			UPDATE sith.policy_audit_heads SET last_sequence = $1, last_hash = $2
			WHERE workspace_id = 'workspace-a'
		`, originalHeadSequence, originalHeadHash); err != nil {
			t.Fatalf("restore approval audit head fixture: %v", err)
		}
	}

	rollbackCreateBinding := postgresApprovalBinding(
		t, "intent-252-create-rollback", "workspace-a", proposerA.Subject(), "rollback=create",
	)
	readAndCorruptHead()
	_, rollbackCreateErr := database.CreateApprovalGrant(ctx, approverA, rollbackCreateBinding, now)
	restoreHead()
	if rollbackCreateErr == nil || errors.Is(rollbackCreateErr, ErrApprovalGrantUnavailable) {
		t.Fatalf("audit-failed create error = %v, want operational audit failure", rollbackCreateErr)
	}
	var rollbackCreateCount int
	if err := admin.QueryRow(ctx, `
		SELECT count(*) FROM sith.approval_grants
		WHERE workspace_id = 'workspace-a' AND intent_id = 'intent-252-create-rollback'
	`).Scan(&rollbackCreateCount); err != nil {
		t.Fatalf("inspect audit-failed approval create: %v", err)
	}
	if rollbackCreateCount != 0 {
		t.Fatalf("audit-failed approval create retained %d grants", rollbackCreateCount)
	}

	rollbackConsumeBinding, rollbackConsumeID := create("intent-252-consume-rollback", "rollback=consume")
	if err := database.ConsumeApprovalGrant(
		ctx, proposerA, rollbackConsumeBinding, rollbackConsumeID, now.Add(2*time.Minute),
	); !errors.Is(err, ErrApprovalGrantUnavailable) {
		t.Fatalf("future consume time error = %v", err)
	}
	readAndCorruptHead()
	rollbackConsumeErr := database.ConsumeApprovalGrant(
		ctx, proposerA, rollbackConsumeBinding, rollbackConsumeID, now.Add(time.Second),
	)
	restoreHead()
	if rollbackConsumeErr == nil || errors.Is(rollbackConsumeErr, ErrApprovalGrantUnavailable) {
		t.Fatalf("audit-failed consume error = %v, want operational audit failure", rollbackConsumeErr)
	}
	var rollbackConsumedAt *time.Time
	if err := admin.QueryRow(ctx, `
		SELECT consumed_at FROM sith.approval_grants
		WHERE workspace_id = 'workspace-a' AND id = $1
	`, rollbackConsumeID).Scan(&rollbackConsumedAt); err != nil {
		t.Fatalf("inspect audit-failed approval consume: %v", err)
	}
	if rollbackConsumedAt != nil {
		t.Fatalf("audit-failed approval consume retained timestamp %s", rollbackConsumedAt)
	}
	if err := database.ConsumeApprovalGrant(
		ctx, proposerA, rollbackConsumeBinding, rollbackConsumeID, now.Add(time.Second),
	); err != nil {
		t.Fatalf("consume after audit rollback error = %v", err)
	}

	bindingB := postgresApprovalBinding(t, "intent-250-b", "workspace-b", proposerB.Subject(), "region=west")
	identifierB, err := database.CreateApprovalGrant(ctx, approverB, bindingB, now)
	if err != nil {
		t.Fatalf("CreateApprovalGrant(workspace B) error = %v", err)
	}
	if err := database.InWorkspace(ctx, proposerA, func(tx pgx.Tx) error {
		var foreignCount int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM sith.approval_grants WHERE workspace_id = 'workspace-b'`).Scan(&foreignCount); err != nil {
			return err
		}
		if foreignCount != 0 {
			return fmt.Errorf("approval RLS exposed %d foreign grants", foreignCount)
		}
		tag, err := tx.Exec(ctx, `UPDATE sith.approval_grants SET consumed_at = $1
			WHERE workspace_id = 'workspace-b' AND id = $2`, now.Add(time.Second), identifierB)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 0 {
			return fmt.Errorf("approval RLS mutated %d foreign grants", tag.RowsAffected())
		}
		return nil
	}); err != nil {
		t.Fatalf("approval RLS isolation: %v", err)
	}

	var canConsume, canMutateProposer, canDelete bool
	if err := admin.QueryRow(ctx, `
		SELECT has_column_privilege($1, 'sith.approval_grants', 'consumed_at', 'UPDATE'),
		       has_column_privilege($1, 'sith.approval_grants', 'proposer', 'UPDATE'),
		       has_table_privilege($1, 'sith.approval_grants', 'DELETE')
	`, appRole).Scan(&canConsume, &canMutateProposer, &canDelete); err != nil {
		t.Fatalf("inspect approval privileges: %v", err)
	}
	if !canConsume || canMutateProposer || canDelete {
		t.Fatalf("approval privileges = consume:%t mutate-proposer:%t delete:%t", canConsume, canMutateProposer, canDelete)
	}
	for name, statement := range map[string]string{
		"mutate proposer": `UPDATE sith.approval_grants SET proposer = 'user:mallory' WHERE id = 'GGGGGGGGGGGGGGGGGGGGGG'`,
		"delete grant":    `DELETE FROM sith.approval_grants WHERE id = 'GGGGGGGGGGGGGGGGGGGGGG'`,
	} {
		err := database.InWorkspace(ctx, proposerA, func(tx pgx.Tx) error {
			_, execErr := tx.Exec(ctx, statement)
			return execErr
		})
		var postgresErr *pgconn.PgError
		if !errors.As(err, &postgresErr) || postgresErr.Code != "42501" {
			t.Fatalf("%s error = %v, want privilege denial 42501", name, err)
		}
	}
	if err := AuditIsolation(ctx, admin, appRole); err != nil {
		t.Fatalf("approval AuditIsolation() error = %v", err)
	}

	rows, err := admin.Query(ctx, `
		SELECT event_kind, evidence_digest, actor, role, action, verb, verdict, reason_code
		FROM sith.policy_audit_entries
		WHERE workspace_id = 'workspace-a' AND format_version = 2
		ORDER BY sequence
	`)
	if err != nil {
		t.Fatalf("read approval lifecycle audit entries: %v", err)
	}
	defer rows.Close()
	kindCounts := map[string]int{}
	kindsByEvidence := map[string]map[string]bool{}
	for rows.Next() {
		var eventKind, evidence, actor, role, action, verb, verdict, reason string
		if err := rows.Scan(&eventKind, &evidence, &actor, &role, &action, &verb, &verdict, &reason); err != nil {
			t.Fatalf("scan approval lifecycle audit entry: %v", err)
		}
		if !approvalEvidencePattern.MatchString(evidence) || verb != string(approvalAuditVerb) ||
			verdict != string(pep.VerdictAllow) || reason != eventKind {
			t.Fatalf("invalid approval lifecycle audit metadata for %s", eventKind)
		}
		switch eventKind {
		case approvalCreatedEventKind:
			if actor != approverA.Subject() || role != string(tenancy.RoleApprover) || action != string(tenancy.ActionApproveIntent) {
				t.Fatalf("invalid approval-created actor metadata: %s/%s/%s", actor, role, action)
			}
		case approvalConsumedEventKind:
			if actor != proposerA.Subject() || role != string(tenancy.RoleOperator) || action != string(tenancy.ActionProposeIntent) {
				t.Fatalf("invalid approval-consumed actor metadata: %s/%s/%s", actor, role, action)
			}
		default:
			t.Fatalf("unexpected approval lifecycle event kind %q", eventKind)
		}
		kindCounts[eventKind]++
		if kindsByEvidence[evidence] == nil {
			kindsByEvidence[evidence] = map[string]bool{}
		}
		kindsByEvidence[evidence][eventKind] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate approval lifecycle audit entries: %v", err)
	}
	if kindCounts[approvalCreatedEventKind] != 3 || kindCounts[approvalConsumedEventKind] != 3 || len(kindsByEvidence) != 3 {
		t.Fatalf("approval lifecycle audit counts = %#v across %d grants, want 3 complete pairs", kindCounts, len(kindsByEvidence))
	}
	for evidence, kinds := range kindsByEvidence {
		if !kinds[approvalCreatedEventKind] || !kinds[approvalConsumedEventKind] || len(kinds) != 2 {
			t.Fatalf("approval lifecycle evidence %s has events %#v, want one create and one consume", evidence, kinds)
		}
	}
	if err := database.VerifyPolicyAuditChain(ctx, proposerA); err != nil {
		t.Fatalf("mixed-version workspace A audit chain error = %v", err)
	}
	if err := database.VerifyPolicyAuditChain(ctx, proposerB); err != nil {
		t.Fatalf("mixed-version workspace B audit chain error = %v", err)
	}
	mixedExport, err := database.ExportPolicyAuditChain(
		ctx, testScope(t, "user:bob", "workspace-b", tenancy.RoleAdmin),
	)
	if err != nil {
		t.Fatalf("mixed-version workspace B export error = %v", err)
	}
	if len(mixedExport.Entries) != 2 || mixedExport.Entries[0].FormatVersion != policyAuditFormatVersion ||
		mixedExport.Entries[1].FormatVersion != approvalAuditFormatVersion {
		t.Fatalf("mixed-version workspace B export shape = %#v", mixedExport)
	}
	if err := mixedExport.Verify(); err != nil {
		t.Fatalf("mixed-version PostgreSQL export offline verification error = %v", err)
	}
}

func postgresApprovalBinding(
	t *testing.T,
	intentID string,
	workspaceID tenancy.WorkspaceID,
	proposer string,
	arguments string,
) pep.ApprovalBinding {
	t.Helper()
	digest := sha256.Sum256([]byte(arguments))
	input, err := pep.NewProposalInput(
		intentID, workspaceID, proposer, intent.VerbDeploymentRestart,
		fleet.ResourceRef{SourceKind: "argocd", Scope: "cluster-a", Kind: "deployment", Namespace: "payments", Name: "payments"},
		"sha256:"+hex.EncodeToString(digest[:]),
	)
	if err != nil {
		t.Fatalf("NewProposalInput() error = %v", err)
	}
	binding, err := input.ApprovalBinding()
	if err != nil {
		t.Fatalf("ApprovalBinding() error = %v", err)
	}
	return binding
}

func testScope(
	t *testing.T,
	subject string,
	workspaceID tenancy.WorkspaceID,
	role tenancy.Role,
) tenancy.Scope {
	t.Helper()
	principal, err := tenancy.NewPrincipal(subject, map[tenancy.WorkspaceID]tenancy.Role{workspaceID: role})
	if err != nil {
		t.Fatal(err)
	}
	scope, err := principal.Scope(workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	return scope
}

func newPolicyAuditEvent(t *testing.T, scope tenancy.Scope) pep.AuditEvent {
	t.Helper()
	traceID, err := tracing.NewID()
	if err != nil {
		t.Fatal(err)
	}
	return pep.AuditEvent{
		At: time.Now().UTC(), TraceID: traceID, WorkspaceID: scope.WorkspaceID(), Actor: scope.Subject(),
		Role: scope.Role(), Action: tenancy.ActionRead, Verb: pep.VerbFleetRead,
		Verdict: pep.VerdictAllow, ReasonCode: "phase-1-read",
	}
}

func startPostgres(t *testing.T) string {
	t.Helper()
	docker := os.Getenv("DOCKER_BIN")
	if docker == "" {
		docker = "docker"
	}
	image := os.Getenv("POSTGRES_IMAGE")
	if image == "" {
		image = defaultPostgresImage
	}
	name := "sith-rls-" + randomHex(t, 8)
	password := randomHex(t, 24)
	command := exec.Command(docker, "run", "--detach", "--rm", "--name", name,
		"--env", "POSTGRES_PASSWORD="+password, "--env", "POSTGRES_DB=sith_test",
		"--publish", "127.0.0.1::5432", image)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("start PostgreSQL container: %v: %s", err, output)
	}
	t.Cleanup(func() {
		_ = exec.Command(docker, "rm", "--force", name).Run()
	})

	deadline := time.Now().Add(45 * time.Second)
	var address string
	for time.Now().Before(deadline) {
		output, err := exec.Command(docker, "port", name, "5432/tcp").Output()
		if err == nil {
			address = strings.TrimSpace(string(output))
			if address != "" {
				break
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	if address == "" {
		t.Fatal("PostgreSQL container did not publish a port")
	}
	connection := &url.URL{
		Scheme: "postgres", User: url.UserPassword("postgres", password), Host: address, Path: "/sith_test",
		RawQuery: url.Values{"sslmode": []string{"disable"}}.Encode(),
	}
	return connection.String()
}

func connectPostgres(t *testing.T, ctx context.Context, connectionURL string) *pgx.Conn {
	t.Helper()
	deadline := time.Now().Add(45 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		connection, err := pgx.Connect(ctx, connectionURL)
		if err == nil {
			if pingErr := connection.Ping(ctx); pingErr == nil {
				return connection
			} else {
				lastErr = pingErr
			}
			_ = connection.Close(context.Background())
		} else {
			lastErr = err
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("connect to PostgreSQL: %v", lastErr)
	return nil
}

func createTestRole(t *testing.T, ctx context.Context, admin *pgx.Conn, role, password string) {
	t.Helper()
	statement := fmt.Sprintf("CREATE ROLE %s LOGIN PASSWORD '%s' NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS",
		pgx.Identifier{role}.Sanitize(), password)
	if _, err := admin.Exec(ctx, statement); err != nil {
		t.Fatal(err)
	}
}

func databaseURL(original, role, password string) string {
	parsed, _ := url.Parse(original)
	parsed.User = url.UserPassword(role, password)
	return parsed.String()
}

func seedTenantRows(t *testing.T, ctx context.Context, admin *pgx.Conn) {
	t.Helper()
	statements := []string{
		`INSERT INTO sith.workspaces(id, name, tenant_key) VALUES
			('workspace-a', 'Workspace A', 'shared-display-key'),
			('workspace-b', 'Workspace B', 'shared-display-key')`,
		`INSERT INTO sith.memberships(workspace_id, subject, role) VALUES
			('workspace-a', 'user:alice', 'admin'), ('workspace-b', 'user:bob', 'admin')`,
		`INSERT INTO sith.clusters(workspace_id, id, managed_cluster_ref) VALUES
			('workspace-a', 'cluster-a', 'ocm/cluster-a'), ('workspace-b', 'cluster-b', 'ocm/cluster-b')`,
		`INSERT INTO sith.fleet_facts(workspace_id, cluster_id, kind, payload, observed_at) VALUES
			('workspace-a', 'cluster-a', 'health', '{"status":"ok"}', now()),
			('workspace-b', 'cluster-b', 'health', '{"status":"degraded"}', now())`,
		`INSERT INTO sith.api_keys(workspace_id, id, subject, verifier, created_at, expires_at) VALUES
			('workspace-a', 'AAAAAAAAAAAAAAAAAAAAAA', 'user:alice', decode(repeat('aa', 32), 'hex'), now(), now() + interval '1 day'),
			('workspace-b', 'BBBBBBBBBBBBBBBBBBBBBB', 'user:bob', decode(repeat('bb', 32), 'hex'), now(), now() + interval '1 day')`,
		`INSERT INTO sith.oidc_bindings(workspace_id, issuer, upstream_subject, member_subject) VALUES
			('workspace-a', 'https://idp.example', 'upstream:alice', 'user:alice'),
			('workspace-b', 'https://idp.example', 'upstream:bob', 'user:bob')`,
		"INSERT INTO sith.cloud_identity_bindings(workspace_id, provider, realm, upstream_subject, member_subject) VALUES\n" +
			"('workspace-a', 'aws', '111111111111', 'AROAX:alice', 'user:alice'),\n" +
			"('workspace-b', 'aws', '222222222222', 'AROAX:bob', 'user:bob')",
		`INSERT INTO sith.approval_grants(
			workspace_id, id, intent_id, proposer, approver, resolved_digest, approved_at
		) VALUES
			('workspace-a', 'GGGGGGGGGGGGGGGGGGGGGG', 'intent-seed-a', 'user:seed-operator',
			 'user:seed-approver', 'sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', now()),
			('workspace-b', 'HHHHHHHHHHHHHHHHHHHHHH', 'intent-seed-b', 'user:seed-operator',
			 'user:seed-approver', 'sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb', now())`,
	}
	for _, statement := range statements {
		if _, err := admin.Exec(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
}

func assertAPIKeyStoreIntegration(t *testing.T, ctx context.Context, database *AppDB, admin *pgx.Conn) {
	t.Helper()
	seeded, membership, err := database.LookupAPIKey(ctx, "workspace-a", "AAAAAAAAAAAAAAAAAAAAAA")
	if err != nil || membership.Subject != "user:alice" || len(seeded.Verifier) != 32 {
		t.Fatalf("scoped API key lookup record = %#v, membership = %#v, error = %v", seeded, membership, err)
	}
	if _, _, err := database.LookupAPIKey(ctx, "workspace-a", "BBBBBBBBBBBBBBBBBBBBBB"); !errors.Is(err, hubauth.ErrAPIKeyNotFound) {
		t.Fatalf("cross-workspace key lookup error = %v", err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	privateKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x42}, ed25519.SeedSize))
	issuer, err := hubauth.NewSessionIssuer(hubauth.SessionIssuerConfig{
		Issuer: "https://issuer.sith.test", Audience: "https://hub.sith.test", KeyID: "postgres-session",
		PrivateKey: privateKey, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := hubauth.NewAPIKeyService(hubauth.APIKeyServiceConfig{
		Store: database, Issuer: issuer, Pepper: bytes.Repeat([]byte{0x24}, 32), Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	principal, err := tenancy.NewPrincipal("user:alice", map[tenancy.WorkspaceID]tenancy.Role{"workspace-a": tenancy.RoleAdmin})
	if err != nil {
		t.Fatal(err)
	}
	scope, err := principal.Scope("workspace-a")
	if err != nil {
		t.Fatal(err)
	}
	raw, record, err := service.Issue(ctx, scope, "user:alice")
	if err != nil {
		t.Fatalf("issue PostgreSQL-backed API key: %v", err)
	}
	stored, _, err := database.LookupAPIKey(ctx, "workspace-a", record.ID)
	if err != nil || bytes.Contains(stored.Verifier, []byte(raw)) {
		t.Fatalf("stored API key verifier leaked plaintext or lookup failed: %v", err)
	}
	if _, err := admin.Exec(ctx, `UPDATE sith.memberships SET role = 'operator'
		WHERE workspace_id = 'workspace-a' AND subject = 'user:alice'`); err != nil {
		t.Fatal(err)
	}
	session, err := service.Exchange(ctx, raw)
	if err != nil {
		t.Fatalf("exchange PostgreSQL-backed API key: %v", err)
	}
	verifier, err := hubauth.NewJWTVerifier(hubauth.JWTConfig{
		Issuer: "https://issuer.sith.test", Audience: "https://hub.sith.test",
		Keys: map[string]ed25519.PublicKey{"postgres-session": privateKey.Public().(ed25519.PublicKey)},
		Now:  func() time.Time { return now }, MaxLifetime: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	verified, err := verifier.Verify(ctx, session.AccessToken)
	if err != nil {
		t.Fatalf("verify exchanged PostgreSQL session: %v", err)
	}
	currentScope, err := verified.Scope("workspace-a")
	if err != nil || currentScope.Role() != tenancy.RoleOperator {
		t.Fatalf("exchanged current role = %q, error = %v", currentScope.Role(), err)
	}
	replacement, replacementRecord, err := service.Rotate(ctx, scope, record.ID, "user:alice", time.Minute)
	if err != nil {
		t.Fatalf("rotate PostgreSQL-backed API key: %v", err)
	}
	if _, _, err := service.Rotate(ctx, scope, record.ID, "user:alice", time.Minute); err == nil {
		t.Fatal("already-rotated PostgreSQL key accepted a second replacement")
	}
	if _, err := service.Exchange(ctx, raw); err != nil {
		t.Fatalf("original key rejected during PostgreSQL overlap: %v", err)
	}
	now = now.Add(time.Minute)
	if _, err := service.Exchange(ctx, raw); !errors.Is(err, hubauth.ErrInvalidAPIKey) {
		t.Fatalf("retired PostgreSQL key error = %v", err)
	}
	if err := service.Revoke(ctx, scope, replacementRecord.ID); err != nil {
		t.Fatalf("revoke PostgreSQL-backed API key: %v", err)
	}
	if _, err := service.Exchange(ctx, replacement); !errors.Is(err, hubauth.ErrInvalidAPIKey) {
		t.Fatalf("revoked PostgreSQL key error = %v", err)
	}
}

func assertOIDCStoreIntegration(t *testing.T, ctx context.Context, database *AppDB) {
	t.Helper()
	membership, err := database.LookupOIDCMembership(ctx, "workspace-a", "https://idp.example", "upstream:alice")
	if err != nil || membership.Subject != "user:alice" || membership.Role != tenancy.RoleOperator {
		t.Fatalf("OIDC membership = %#v, error = %v", membership, err)
	}
	if _, err := database.LookupOIDCMembership(ctx, "workspace-a", "https://idp.example", "upstream:bob"); !errors.Is(err, hubauth.ErrOIDCBindingNotFound) {
		t.Fatalf("cross-workspace OIDC lookup error = %v", err)
	}
	principal, err := tenancy.NewPrincipal("user:alice", map[tenancy.WorkspaceID]tenancy.Role{"workspace-a": tenancy.RoleAdmin})
	if err != nil {
		t.Fatal(err)
	}
	scope, err := principal.Scope("workspace-a")
	if err != nil {
		t.Fatal(err)
	}
	if err := database.CreateOIDCBinding(ctx, scope, "https://idp.example", "upstream:alice-secondary", "user:alice"); err != nil {
		t.Fatalf("create OIDC binding: %v", err)
	}
	secondary, err := database.LookupOIDCMembership(ctx, "workspace-a", "https://idp.example", "upstream:alice-secondary")
	if err != nil || secondary.Subject != "user:alice" || secondary.Role != tenancy.RoleOperator {
		t.Fatalf("secondary OIDC membership = %#v, error = %v", secondary, err)
	}
}

func assertCloudIdentityStoreIntegration(t *testing.T, ctx context.Context, database *AppDB) {
	t.Helper()
	identity := hubauth.CloudIdentity{Provider: hubauth.CloudProviderAWS, Realm: "111111111111", Subject: "AROAX:alice"}
	membership, err := database.LookupCloudIdentityMembership(ctx, "workspace-a", identity)
	if err != nil || membership.Subject != "user:alice" || !membership.Role.Valid() {
		t.Fatalf("cloud identity membership = %#v, error = %v", membership, err)
	}
	foreignIdentity := hubauth.CloudIdentity{Provider: hubauth.CloudProviderAWS, Realm: "222222222222", Subject: "AROAX:bob"}
	if _, err := database.LookupCloudIdentityMembership(ctx, "workspace-a", foreignIdentity); !errors.Is(err, hubauth.ErrCloudBindingNotFound) {
		t.Fatalf("cross-workspace cloud identity lookup error = %v", err)
	}
	principal, err := tenancy.NewPrincipal("user:alice", map[tenancy.WorkspaceID]tenancy.Role{"workspace-a": tenancy.RoleAdmin})
	if err != nil {
		t.Fatal(err)
	}
	scope, err := principal.Scope("workspace-a")
	if err != nil {
		t.Fatal(err)
	}
	secondary := hubauth.CloudIdentity{Provider: hubauth.CloudProviderAWS, Realm: "111111111111", Subject: "AROAX:alice-secondary"}
	if err := database.CreateCloudIdentityBinding(ctx, scope, secondary, "user:alice"); err != nil {
		t.Fatalf("create cloud identity binding: %v", err)
	}
	created, err := database.LookupCloudIdentityMembership(ctx, "workspace-a", secondary)
	if err != nil || created.Subject != "user:alice" || !created.Role.Valid() {
		t.Fatalf("secondary cloud identity membership = %#v, error = %v", created, err)
	}
}

func assertFleetStoreIntegration(t *testing.T, ctx context.Context, database *AppDB) {
	t.Helper()
	now := time.Date(2026, time.July, 12, 15, 0, 0, 0, time.UTC)
	digest := "sha256:" + strings.Repeat("a", 64)
	principal, err := tenancy.NewPrincipal("user:alice", map[tenancy.WorkspaceID]tenancy.Role{"workspace-a": tenancy.RoleReader})
	if err != nil {
		t.Fatal(err)
	}
	scope, err := principal.Scope("workspace-a")
	if err != nil {
		t.Fatal(err)
	}
	spokes, err := database.RegisteredSpokes(ctx, scope)
	if err != nil || len(spokes) != 1 || spokes[0].ID != "cluster-a" || spokes[0].ManagedClusterRef != "ocm/cluster-a" {
		t.Fatalf("workspace-a registered spokes = %#v, error = %v", spokes, err)
	}
	snapshot := hubfleet.Snapshot{ObservedAt: now, Facts: []fleet.Evidence{
		{
			Ref:        fleet.ResourceRef{SourceKind: hubfleet.SourceKind, Scope: "cluster-a", Kind: "Deployment", Namespace: "payments", Name: "payments"},
			Kind:       fleet.FactInventory,
			Observed:   []byte(`{"replicas":3}`),
			ObservedAt: now,
			Source:     "cluster-a",
			Provenance: fleet.Provenance{Adapter: hubfleet.SourceKind, ProtocolV: "1.0.0"},
		},
		{
			Ref:        fleet.ResourceRef{SourceKind: hubfleet.SourceKind, Scope: "cluster-a", Kind: "Deployment", Namespace: "payments", Name: "payments"},
			Kind:       fleet.FactHealth,
			Observed:   []byte(`{"status":"Degraded"}`),
			ObservedAt: now,
			Source:     "cluster-a",
			Provenance: fleet.Provenance{Adapter: hubfleet.SourceKind, ProtocolV: "1.0.0"},
		},
		{
			Ref:        fleet.ResourceRef{SourceKind: hubfleet.SourceKind, Scope: "cluster-a", Kind: "Deployment", Namespace: "payments", Name: "payments-canary"},
			Kind:       fleet.FactHealth,
			Observed:   []byte(`{"status":"Degraded"}`),
			ObservedAt: now,
			Source:     "cluster-a",
			Provenance: fleet.Provenance{Adapter: hubfleet.SourceKind, ProtocolV: "1.0.0"},
		},
		{
			Ref:        fleet.ResourceRef{SourceKind: hubfleet.SourceKind, Scope: "cluster-a", Kind: "Pod", Namespace: "payments", Name: "payments-a"},
			Kind:       fleet.FactInventory,
			Observed:   []byte(`{"resource":"Pod","ready":1,"generation":1,"image_digests":["` + digest + `"]}`),
			ObservedAt: now,
			Source:     "cluster-a",
			Provenance: fleet.Provenance{Adapter: hubfleet.SourceKind, ProtocolV: "1.0.0"},
		},
		{
			Ref:        fleet.ResourceRef{SourceKind: hubfleet.SourceKind, Scope: "cluster-a", Kind: "Image", Name: digest},
			Kind:       fleet.FactCVE,
			Observed:   []byte(`{"image":"` + digest + `","ids":["CVE-2026-0001"],"severity":"high"}`),
			ObservedAt: now,
			Source:     "cluster-a",
			Provenance: fleet.Provenance{Adapter: hubfleet.SourceKind, ProtocolV: "1.0.0"},
		},
	}}
	if err := database.ReplaceSnapshot(ctx, scope, spokes[0], snapshot, now); err != nil {
		t.Fatalf("replace workspace-a snapshot: %v", err)
	}
	fleetResult, err := database.ReadFleet(ctx, scope, time.Minute, now)
	if err != nil || len(fleetResult.Clusters) != 1 || fleetResult.Clusters[0].Name != "cluster-a" ||
		!fleetResult.Clusters[0].Reachable || fleetResult.Coverage.Requested != 1 || fleetResult.Coverage.Reachable != 1 {
		t.Fatalf("workspace-a fleet = %#v, error = %v", fleetResult, err)
	}
	queryResult, err := database.QueryFleet(ctx, scope, fleet.Query{
		Kinds:  []fleet.FactKind{fleet.FactHealth},
		Scopes: []string{"cluster-a"},
		Selector: fleet.Selector{
			ResourceKind: "Deployment", Namespace: "payments", Name: "payments", HealthNot: "Healthy",
		},
	}, time.Minute, now)
	if err != nil || len(queryResult.Facts) != 1 || queryResult.Facts[0].Workspace != "workspace-a" || queryResult.Facts[0].Stale {
		t.Fatalf("workspace-a health query = %#v, error = %v", queryResult, err)
	}
	assertFleetQuerySnapshotIsolation(t, ctx, database, scope)
	if err := database.InWorkspace(ctx, scope, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO sith.clusters(workspace_id, id, managed_cluster_ref)
			VALUES ($1, $2, $3)`, scope.WorkspaceID(), "cluster-a2", "ocm/cluster-a2")
		return err
	}); err != nil {
		t.Fatalf("register second workspace-a spoke: %v", err)
	}
	spokes, err = database.RegisteredSpokes(ctx, scope)
	if err != nil || len(spokes) != 2 {
		t.Fatalf("two workspace-a registered spokes = %#v, error = %v", spokes, err)
	}
	secondSnapshot := hubfleet.Snapshot{ObservedAt: now, Facts: []fleet.Evidence{
		{
			Ref:        fleet.ResourceRef{SourceKind: hubfleet.SourceKind, Scope: "cluster-a2", Kind: "Deployment", Namespace: "payments", Name: "payments"},
			Kind:       fleet.FactHealth,
			Observed:   []byte(`{"status":"Healthy"}`),
			ObservedAt: now,
			Source:     "cluster-a2",
			Provenance: fleet.Provenance{Adapter: hubfleet.SourceKind, ProtocolV: "1.0.0"},
		},
		{
			Ref:        fleet.ResourceRef{SourceKind: hubfleet.SourceKind, Scope: "cluster-a2", Kind: "Pod", Namespace: "payments", Name: "payments-b"},
			Kind:       fleet.FactInventory,
			Observed:   []byte(`{"resource":"Pod","ready":1,"generation":1,"image_digests":["` + digest + `"]}`),
			ObservedAt: now,
			Source:     "cluster-a2",
			Provenance: fleet.Provenance{Adapter: hubfleet.SourceKind, ProtocolV: "1.0.0"},
		},
		{
			Ref:        fleet.ResourceRef{SourceKind: hubfleet.SourceKind, Scope: "cluster-a2", Kind: "Image", Name: digest},
			Kind:       fleet.FactCVE,
			Observed:   []byte(`{"image":"` + digest + `","ids":["CVE-2026-0001","CVE-2026-0002"],"severity":"critical"}`),
			ObservedAt: now,
			Source:     "cluster-a2",
			Provenance: fleet.Provenance{Adapter: hubfleet.SourceKind, ProtocolV: "1.0.0"},
		},
	}}
	if err := database.ReplaceSnapshot(ctx, scope, spokes[1], secondSnapshot, now); err != nil {
		t.Fatalf("replace second workspace-a snapshot: %v", err)
	}
	correlator, err := hubfleet.NewCorrelator(hubfleet.CorrelatorConfig{
		Querier: database, PEP: postgresReadPEP(t), Freshness: time.Minute, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	cveSearcher, err := hubfleet.NewCVESearcher(hubfleet.CVESearcherConfig{
		Querier: database, PEP: postgresReadPEP(t), Freshness: time.Minute, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	imageSearcher, err := hubfleet.NewImageSearcher(hubfleet.ImageSearcherConfig{
		Querier: database, PEP: postgresReadPEP(t), Freshness: time.Minute, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	inventorySearcher, err := hubfleet.NewInventorySearcher(hubfleet.InventorySearcherConfig{
		Querier: database, PEP: postgresReadPEP(t), Freshness: time.Minute, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	inventory, err := inventorySearcher.Search(ctx, scope, hubfleet.InventorySearchRequest{ResourceKind: "Pod", Namespace: "payments"})
	if err != nil || len(inventory.Facts) != 2 || inventory.Facts[0].Ref.Scope != "cluster-a" || inventory.Facts[1].Ref.Scope != "cluster-a2" ||
		inventory.Coverage.Requested != 2 || inventory.Coverage.Reachable != 2 || len(inventory.Coverage.Stale) != 0 {
		t.Fatalf("two-spoke exact inventory search = %#v, error = %v", inventory, err)
	}
	images, err := imageSearcher.Search(ctx, scope, hubfleet.ImageSearchRequest{Digest: digest})
	if err != nil || len(images.Facts) != 2 || images.Facts[0].Ref.Scope != "cluster-a" || images.Facts[1].Ref.Scope != "cluster-a2" ||
		images.Coverage.Requested != 2 || images.Coverage.Reachable != 2 || len(images.Coverage.Stale) != 0 {
		t.Fatalf("two-spoke exact image search = %#v, error = %v", images, err)
	}
	cves, err := cveSearcher.Search(ctx, scope, hubfleet.ImageSearchRequest{Digest: digest})
	if err != nil || len(cves.Facts) != 2 || cves.Facts[0].Ref.Scope != "cluster-a" || cves.Facts[1].Ref.Scope != "cluster-a2" ||
		cves.Coverage.Requested != 2 || cves.Coverage.Reachable != 2 || len(cves.Coverage.Stale) != 0 {
		t.Fatalf("two-spoke exact CVE search = %#v, error = %v", cves, err)
	}
	identifierCVEs, err := cveSearcher.SearchByIdentifier(ctx, scope, hubfleet.CVEIdentifierSearchRequest{Identifier: "CVE-2026-0001"})
	if err != nil || len(identifierCVEs.Facts) != 2 || identifierCVEs.Facts[0].Ref.Scope != "cluster-a" || identifierCVEs.Facts[1].Ref.Scope != "cluster-a2" ||
		identifierCVEs.Coverage.Requested != 2 || identifierCVEs.Coverage.Reachable != 2 || len(identifierCVEs.Coverage.Stale) != 0 {
		t.Fatalf("two-spoke exact CVE identifier search = %#v, error = %v", identifierCVEs, err)
	}
	correlated, err := correlator.Correlate(ctx, scope, hubfleet.CorrelationRequest{
		ResourceKind: "Deployment", Name: "payments", Namespace: "payments", HealthNot: "Healthy",
	})
	if err != nil || len(correlated.Facts) != 1 || correlated.Facts[0].Ref.Scope != "cluster-a" ||
		correlated.Coverage.Requested != 2 || correlated.Coverage.Reachable != 2 || len(correlated.Coverage.Stale) != 0 {
		t.Fatalf("two-spoke correlation = %#v, error = %v", correlated, err)
	}
	foreignPrincipal, err := tenancy.NewPrincipal("user:bob", map[tenancy.WorkspaceID]tenancy.Role{"workspace-b": tenancy.RoleReader})
	if err != nil {
		t.Fatal(err)
	}
	foreignScope, err := foreignPrincipal.Scope("workspace-b")
	if err != nil {
		t.Fatal(err)
	}
	foreignCorrelation, err := correlator.Correlate(ctx, foreignScope, hubfleet.CorrelationRequest{
		ResourceKind: "Deployment", Name: "payments", Namespace: "payments", HealthNot: "Healthy",
	})
	if err != nil || len(foreignCorrelation.Facts) != 0 {
		t.Fatalf("cross-workspace correlation = %#v, error = %v", foreignCorrelation, err)
	}
	foreignInventory, err := inventorySearcher.Search(ctx, foreignScope, hubfleet.InventorySearchRequest{ResourceKind: "Pod", Namespace: "payments"})
	if err != nil || len(foreignInventory.Facts) != 0 || foreignInventory.Coverage.Requested != 1 || len(foreignInventory.Coverage.Unreachable) != 1 {
		t.Fatalf("cross-workspace inventory search = %#v, error = %v", foreignInventory, err)
	}
	foreignImages, err := imageSearcher.Search(ctx, foreignScope, hubfleet.ImageSearchRequest{Digest: digest})
	if err != nil || len(foreignImages.Facts) != 0 || foreignImages.Coverage.Requested != 1 || len(foreignImages.Coverage.Unreachable) != 1 {
		t.Fatalf("cross-workspace image search = %#v, error = %v", foreignImages, err)
	}
	foreignCVEs, err := cveSearcher.Search(ctx, foreignScope, hubfleet.ImageSearchRequest{Digest: digest})
	if err != nil || len(foreignCVEs.Facts) != 0 || foreignCVEs.Coverage.Requested != 1 || len(foreignCVEs.Coverage.Unreachable) != 1 {
		t.Fatalf("cross-workspace CVE search = %#v, error = %v", foreignCVEs, err)
	}
	foreignIdentifierCVEs, err := cveSearcher.SearchByIdentifier(ctx, foreignScope, hubfleet.CVEIdentifierSearchRequest{Identifier: "CVE-2026-0001"})
	if err != nil || len(foreignIdentifierCVEs.Facts) != 0 || foreignIdentifierCVEs.Coverage.Requested != 1 || len(foreignIdentifierCVEs.Coverage.Unreachable) != 1 {
		t.Fatalf("cross-workspace CVE identifier search = %#v, error = %v", foreignIdentifierCVEs, err)
	}
	foreignResult, err := database.QueryFleet(ctx, foreignScope, fleet.Query{Scopes: []string{"cluster-a"}}, time.Minute, now)
	if err != nil || len(foreignResult.Facts) != 0 || foreignResult.Coverage.Requested != 1 ||
		len(foreignResult.Coverage.Unreachable) != 1 || foreignResult.Coverage.Unreachable[0] != "cluster-a" {
		t.Fatalf("cross-workspace fleet query = %#v, error = %v", foreignResult, err)
	}
	foreignSnapshot := hubfleet.Snapshot{ObservedAt: now, Facts: []fleet.Evidence{{
		Ref:        fleet.ResourceRef{SourceKind: hubfleet.SourceKind, Scope: "cluster-b", Kind: "Deployment", Name: "foreign"},
		Kind:       fleet.FactInventory,
		Observed:   []byte(`{"replicas":1}`),
		ObservedAt: now,
		Source:     "cluster-b",
		Provenance: fleet.Provenance{Adapter: hubfleet.SourceKind, ProtocolV: "1.0.0"},
	}}}
	if err := database.ReplaceSnapshot(ctx, scope, hubfleet.Spoke{ID: "cluster-b", ManagedClusterRef: "ocm/cluster-b"}, foreignSnapshot, now); !errors.Is(err, ErrSpokeNotRegistered) {
		t.Fatalf("cross-workspace snapshot replacement error = %v", err)
	}
	retained, err := database.MarkSnapshotFailure(ctx, scope, spokes[0], hubfleet.FailureDeadline, now.Add(time.Second))
	if err != nil || !retained {
		t.Fatalf("mark retained snapshot stale = %t, error = %v", retained, err)
	}
	if _, err := database.MarkSnapshotFailure(ctx, scope, spokes[1], hubfleet.FailureDeadline, now.Add(time.Second)); err != nil {
		t.Fatalf("mark healthy non-match spoke stale: %v", err)
	}
	staleCorrelation, err := correlator.Correlate(ctx, scope, hubfleet.CorrelationRequest{
		ResourceKind: "Deployment", Name: "payments", Namespace: "payments", HealthNot: "Healthy",
	})
	if err != nil || len(staleCorrelation.Facts) != 1 || staleCorrelation.Facts[0].Ref.Scope != "cluster-a" ||
		len(staleCorrelation.Coverage.Stale) != 2 || staleCorrelation.Coverage.Stale[0] != "cluster-a" ||
		staleCorrelation.Coverage.Stale[1] != "cluster-a2" {
		t.Fatalf("stale two-spoke correlation = %#v, error = %v", staleCorrelation, err)
	}
	staleResult, err := database.QueryFleet(ctx, scope, fleet.Query{Scopes: []string{"cluster-a"}}, time.Minute, now.Add(time.Second))
	if err != nil || len(staleResult.Facts) != 5 || !staleResult.Facts[0].Stale || staleResult.Facts[0].StaleFor != "collection failed" ||
		staleResult.Coverage.Reachable != 0 || len(staleResult.Coverage.Unreachable) != 1 || len(staleResult.Coverage.Stale) != 1 {
		t.Fatalf("retained stale query = %#v, error = %v", staleResult, err)
	}
}

func assertFleetQuerySnapshotIsolation(t *testing.T, ctx context.Context, database *AppDB, scope tenancy.Scope) {
	t.Helper()
	spoke := hubfleet.Spoke{ID: "cluster-snapshot", ManagedClusterRef: "ocm/cluster-snapshot"}
	if err := database.InWorkspace(ctx, scope, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO sith.clusters(workspace_id, id, managed_cluster_ref)
			VALUES ($1, $2, $3)`, scope.WorkspaceID(), spoke.ID, spoke.ManagedClusterRef)
		return err
	}); err != nil {
		t.Fatalf("register snapshot-isolation spoke: %v", err)
	}

	newObserved := time.Date(2026, time.July, 12, 17, 0, 0, 0, time.UTC)
	oldObserved := newObserved.Add(-2 * time.Hour)
	snapshot := func(observed time.Time, generation int) hubfleet.Snapshot {
		return hubfleet.Snapshot{ObservedAt: observed, Facts: []fleet.Evidence{{
			Ref: fleet.ResourceRef{
				SourceKind: hubfleet.SourceKind,
				Scope:      spoke.ID,
				Kind:       "Deployment",
				Namespace:  "payments",
				Name:       "snapshot-proof",
			},
			Kind:       fleet.FactInventory,
			Observed:   []byte(fmt.Sprintf(`{"generation":%d}`, generation)),
			ObservedAt: observed,
			Source:     spoke.ID,
			Provenance: fleet.Provenance{Adapter: hubfleet.SourceKind, ProtocolV: "1.0.0"},
		}}}
	}
	if err := database.ReplaceSnapshot(ctx, scope, spoke, snapshot(oldObserved, 1), oldObserved); err != nil {
		t.Fatalf("store old snapshot: %v", err)
	}

	replaced := false
	result, err := database.queryFleet(ctx, scope, fleet.Query{
		Kinds:  []fleet.FactKind{fleet.FactInventory},
		Scopes: []string{spoke.ID},
	}, 30*time.Minute, newObserved, queryFleetHooks{
		afterClusterStates: func(tx pgx.Tx) error {
			var isolation, readOnly, configuredWorkspace string
			if err := tx.QueryRow(ctx, `SHOW transaction_isolation`).Scan(&isolation); err != nil {
				return err
			}
			if err := tx.QueryRow(ctx, `SHOW transaction_read_only`).Scan(&readOnly); err != nil {
				return err
			}
			if err := tx.QueryRow(ctx, `SELECT current_setting('sith.workspace_id', true)`).Scan(&configuredWorkspace); err != nil {
				return err
			}
			if isolation != "repeatable read" || readOnly != "on" || configuredWorkspace != string(scope.WorkspaceID()) {
				return fmt.Errorf("query transaction = isolation %q, read only %q, workspace %q", isolation, readOnly, configuredWorkspace)
			}
			var foreignClusters int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM sith.clusters WHERE workspace_id = 'workspace-b'`).Scan(&foreignClusters); err != nil {
				return err
			}
			if foreignClusters != 0 {
				return fmt.Errorf("query snapshot exposed %d foreign clusters", foreignClusters)
			}
			if err := database.ReplaceSnapshot(ctx, scope, spoke, snapshot(newObserved, 2), newObserved); err != nil {
				return err
			}
			replaced = true
			return nil
		},
	})
	if err != nil {
		t.Fatalf("query during committed replacement: %v", err)
	}
	if !replaced || len(result.Facts) != 1 || result.Coverage.Requested != 1 || result.Coverage.Reachable != 1 ||
		len(result.Coverage.Unreachable) != 0 {
		t.Fatalf("snapshot query = %#v, replacement committed = %t", result, replaced)
	}
	var payload struct {
		Generation int `json:"generation"`
	}
	if err := json.Unmarshal(result.Facts[0].Observed, &payload); err != nil {
		t.Fatalf("decode snapshot generation: %v", err)
	}
	oldPair := payload.Generation == 1 && result.Facts[0].ObservedAt.Equal(oldObserved) && result.Facts[0].Stale &&
		len(result.Coverage.Stale) == 1 && result.Coverage.Stale[0] == spoke.ID
	newPair := payload.Generation == 2 && result.Facts[0].ObservedAt.Equal(newObserved) && !result.Facts[0].Stale &&
		len(result.Coverage.Stale) == 0
	if !oldPair && !newPair {
		t.Fatalf("mixed cluster state and fact snapshot: generation = %d, result = %#v", payload.Generation, result)
	}

	if err := database.InWorkspace(ctx, scope, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM sith.clusters WHERE workspace_id = $1 AND id = $2`, scope.WorkspaceID(), spoke.ID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return fmt.Errorf("deleted %d snapshot-isolation spokes", tag.RowsAffected())
		}
		return nil
	}); err != nil {
		t.Fatalf("remove snapshot-isolation spoke: %v", err)
	}
}

func postgresReadPEP(t *testing.T) *pep.Enforcer {
	t.Helper()
	enforcer, err := pep.NewEnforcer(pep.Config{
		Hook: pep.AllowReadHook{}, Auditor: pep.AuditFunc(func(context.Context, pep.AuditEvent) error { return nil }),
	})
	if err != nil {
		t.Fatal(err)
	}
	return enforcer
}

func randomHex(t *testing.T, bytes int) string {
	t.Helper()
	value := make([]byte, bytes)
	if _, err := rand.Read(value); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(value)
}
