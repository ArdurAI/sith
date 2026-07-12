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
	"unicode"

	"github.com/jackc/pgx/v5"
)

const migrationLockID int64 = 0x53495448524c53

//go:embed migrations/*.sql
var migrationFiles embed.FS

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
