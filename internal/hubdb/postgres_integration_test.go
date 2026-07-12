// SPDX-License-Identifier: Apache-2.0
//go:build postgres

package hubdb

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/ArdurAI/sith/internal/fleet"
	"github.com/ArdurAI/sith/internal/hubauth"
	"github.com/ArdurAI/sith/internal/hubfleet"
	"github.com/ArdurAI/sith/internal/tenancy"
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
	if err := ApplyMigrations(ctx, owner, appRole); err != nil {
		t.Fatalf("ApplyMigrations() error = %v", err)
	}
	if err := ApplyMigrations(ctx, owner, appRole); err != nil {
		t.Fatalf("idempotent ApplyMigrations() error = %v", err)
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
		URL: databaseURL(adminURL, appRole, appPassword), MaxConns: 1, AllowInsecure: true,
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

	tables := []string{"workspaces", "memberships", "clusters", "fleet_facts", "api_keys", "oidc_bindings", "cloud_identity_bindings"}
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

	assertAPIKeyStoreIntegration(t, ctx, database, admin)
	assertOIDCStoreIntegration(t, ctx, database)
	assertCloudIdentityStoreIntegration(t, ctx, database)
	assertFleetStoreIntegration(t, ctx, database)
}

func assertWorkspaceAIsolation(ctx context.Context, database *AppDB, scope tenancy.Scope) error {
	return database.InWorkspace(ctx, scope, func(tx pgx.Tx) error {
		for _, table := range []string{"workspaces", "memberships", "clusters", "fleet_facts", "api_keys", "oidc_bindings", "cloud_identity_bindings"} {
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
			ResourceKind: "Deployment", Namespace: "payments", NamePrefix: "pay", Health: "Degraded",
		},
	}, time.Minute, now)
	if err != nil || len(queryResult.Facts) != 1 || queryResult.Facts[0].Workspace != "workspace-a" || queryResult.Facts[0].Stale {
		t.Fatalf("workspace-a health query = %#v, error = %v", queryResult, err)
	}
	foreignPrincipal, err := tenancy.NewPrincipal("user:bob", map[tenancy.WorkspaceID]tenancy.Role{"workspace-b": tenancy.RoleReader})
	if err != nil {
		t.Fatal(err)
	}
	foreignScope, err := foreignPrincipal.Scope("workspace-b")
	if err != nil {
		t.Fatal(err)
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
	staleResult, err := database.QueryFleet(ctx, scope, fleet.Query{Scopes: []string{"cluster-a"}}, time.Minute, now.Add(time.Second))
	if err != nil || len(staleResult.Facts) != 2 || !staleResult.Facts[0].Stale || staleResult.Facts[0].StaleFor != "collection failed" ||
		staleResult.Coverage.Reachable != 0 || len(staleResult.Coverage.Unreachable) != 1 || len(staleResult.Coverage.Stale) != 1 {
		t.Fatalf("retained stale query = %#v, error = %v", staleResult, err)
	}
}

func randomHex(t *testing.T, bytes int) string {
	t.Helper()
	value := make([]byte, bytes)
	if _, err := rand.Read(value); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(value)
}
