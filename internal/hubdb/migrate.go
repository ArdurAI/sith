// SPDX-License-Identifier: Apache-2.0

package hubdb

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"fmt"
	"io/fs"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"
)

const (
	migrationLockID      int64 = 0x53495448524c53
	migrationCloseWindow       = 5 * time.Second
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// MigrationConfig defines the short-lived owner-credential boundary used only to evolve the hub
// schema. The application database role is intentionally distinct and is never used here as the
// migration owner.
type MigrationConfig struct {
	OwnerURL           string
	ApplicationRole    string
	AllowInsecureLocal bool
}

// Migrate connects once with the deployment-provided schema-owner URL, applies the embedded
// migrations, and attempts to close that connection. Production callers leave
// AllowInsecureLocal false; the exception exists solely for hermetic local PostgreSQL integration
// tests.
func Migrate(ctx context.Context, config MigrationConfig) error {
	if ctx == nil {
		return fmt.Errorf("migrate hub database: context is required")
	}
	if config.OwnerURL == "" || strings.TrimSpace(config.OwnerURL) != config.OwnerURL {
		return fmt.Errorf("migrate hub database: owner database URL is required")
	}
	if err := validateRoleName(config.ApplicationRole); err != nil {
		return fmt.Errorf("migrate hub database: %w", err)
	}

	ownerConfig, err := pgx.ParseConfig(config.OwnerURL)
	if err != nil || ownerConfig.User == "" || strings.TrimSpace(ownerConfig.User) != ownerConfig.User {
		return fmt.Errorf("migrate hub database: owner database URL is invalid")
	}
	if ownerConfig.User == config.ApplicationRole {
		return fmt.Errorf("migrate hub database: owner and application roles must differ")
	}
	if !secureTransport(ownerConfig) && (!config.AllowInsecureLocal || !localTransport(ownerConfig)) {
		return fmt.Errorf("migrate hub database: TLS without plaintext fallback is required for non-local connections")
	}

	owner, err := pgx.ConnectConfig(ctx, ownerConfig)
	if err != nil {
		return fmt.Errorf("migrate hub database: owner connection is unavailable")
	}
	defer func() {
		// Commit is the migration success boundary. Closing this short-lived connection is best-effort
		// because a transport error after commit cannot be retried or rolled back.
		closeCtx, cancel := context.WithTimeout(context.Background(), migrationCloseWindow)
		defer cancel()
		_ = owner.Close(closeCtx)
	}()
	if err := ApplyMigrations(ctx, owner, config.ApplicationRole); err != nil {
		return fmt.Errorf("migrate hub database: %w", err)
	}
	return nil
}

// ApplyMigrations applies versioned schema changes as a role distinct from the application role.
func ApplyMigrations(ctx context.Context, owner *pgx.Conn, appRole string) error {
	if ctx == nil || owner == nil {
		return fmt.Errorf("apply hub migrations: context and owner connection are required")
	}
	if err := validateRoleName(appRole); err != nil {
		return fmt.Errorf("apply hub migrations: %w", err)
	}
	return pgx.BeginTxFunc(ctx, owner, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS sith_meta`); err != nil {
			return fmt.Errorf("create migration schema: %w", err)
		}
		if _, err := tx.Exec(ctx, `REVOKE ALL ON SCHEMA sith_meta FROM PUBLIC`); err != nil {
			return fmt.Errorf("protect migration schema: %w", err)
		}
		if _, err := tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS sith_meta.schema_migrations (
			version text PRIMARY KEY,
			checksum bytea NOT NULL,
			applied_at timestamptz NOT NULL DEFAULT transaction_timestamp()
		)`); err != nil {
			return fmt.Errorf("create migration ledger: %w", err)
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, migrationLockID); err != nil {
			return fmt.Errorf("lock migration ledger: %w", err)
		}
		var currentUser string
		if err := tx.QueryRow(ctx, `SELECT current_user`).Scan(&currentUser); err != nil {
			return fmt.Errorf("read migration role: %w", err)
		}
		if currentUser == appRole {
			return fmt.Errorf("application role must not own database objects")
		}
		if err := requireSafeAppRole(ctx, tx, appRole); err != nil {
			return err
		}
		entries, err := fs.ReadDir(migrationFiles, "migrations")
		if err != nil {
			return fmt.Errorf("read embedded migrations: %w", err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
				continue
			}
			migration, err := migrationFiles.ReadFile("migrations/" + entry.Name())
			if err != nil {
				return fmt.Errorf("read migration %s: %w", entry.Name(), err)
			}
			checksum := sha256.Sum256(migration)
			var storedChecksum []byte
			err = tx.QueryRow(ctx, `SELECT checksum FROM sith_meta.schema_migrations WHERE version = $1`, entry.Name()).Scan(&storedChecksum)
			if err == nil {
				if !bytes.Equal(storedChecksum, checksum[:]) {
					return fmt.Errorf("migration %s checksum does not match the applied ledger", entry.Name())
				}
				continue
			}
			if err != pgx.ErrNoRows {
				return fmt.Errorf("check migration %s: %w", entry.Name(), err)
			}
			if _, err := tx.Exec(ctx, string(migration)); err != nil {
				return fmt.Errorf("execute migration %s: %w", entry.Name(), err)
			}
			if _, err := tx.Exec(ctx, `INSERT INTO sith_meta.schema_migrations(version, checksum) VALUES ($1, $2)`, entry.Name(), checksum[:]); err != nil {
				return fmt.Errorf("record migration %s: %w", entry.Name(), err)
			}
		}
		if err := grantApplicationPrivileges(ctx, tx, appRole); err != nil {
			return err
		}
		if err := AuditIsolation(ctx, tx, appRole); err != nil {
			return fmt.Errorf("audit migrated isolation boundary: %w", err)
		}
		return nil
	})
}

func grantApplicationPrivileges(ctx context.Context, tx pgx.Tx, appRole string) error {
	identifier := pgx.Identifier{appRole}.Sanitize()
	statements := []string{
		"GRANT USAGE ON SCHEMA sith TO " + identifier,
		"GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA sith TO " + identifier,
		"GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA sith TO " + identifier,
		"ALTER DEFAULT PRIVILEGES IN SCHEMA sith GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO " + identifier,
		"ALTER DEFAULT PRIVILEGES IN SCHEMA sith GRANT USAGE, SELECT ON SEQUENCES TO " + identifier,
		"REVOKE UPDATE, DELETE ON sith.policy_audit_entries FROM " + identifier,
		"REVOKE UPDATE (workspace_id, sequence, format_version, recorded_at, trace_id, actor, role, action, verb, verdict, reason_code, previous_hash, entry_hash) ON sith.policy_audit_entries FROM " + identifier,
		"REVOKE DELETE ON sith.policy_audit_heads FROM " + identifier,
	}
	for _, statement := range statements {
		if _, err := tx.Exec(ctx, statement); err != nil {
			return fmt.Errorf("grant application privileges: %w", err)
		}
	}
	return nil
}

func validateRoleName(role string) error {
	if role == "" || strings.TrimSpace(role) != role || len(role) > 63 {
		return fmt.Errorf("application role must be a non-empty, trimmed PostgreSQL identifier")
	}
	for _, character := range role {
		if unicode.IsControl(character) {
			return fmt.Errorf("application role contains a control character")
		}
	}
	return nil
}
