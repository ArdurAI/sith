// SPDX-License-Identifier: Apache-2.0

package hubdb

import (
	"context"
	"fmt"
	"net/netip"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ArdurAI/sith/internal/tenancy"
)

// AppConfig defines the least-privilege application connection boundary.
type AppConfig struct {
	URL           string
	MaxConns      int32
	AllowInsecure bool
}

// AppDB deliberately hides its raw pool so production callers can only query inside a workspace transaction.
type AppDB struct {
	pool *pgxpool.Pool
}

// OpenAppDB opens a pool and verifies every physical connection uses a non-owner, non-bypass role.
func OpenAppDB(ctx context.Context, config AppConfig) (*AppDB, error) {
	if ctx == nil || config.URL == "" {
		return nil, fmt.Errorf("open hub database: context and URL are required")
	}
	poolConfig, err := pgxpool.ParseConfig(config.URL)
	if err != nil {
		return nil, fmt.Errorf("open hub database: parse connection configuration: %w", err)
	}
	if !secureTransport(poolConfig.ConnConfig) && (!config.AllowInsecure || !localTransport(poolConfig.ConnConfig)) {
		return nil, fmt.Errorf("open hub database: TLS without plaintext fallback is required for non-local connections")
	}
	if config.MaxConns < 0 {
		return nil, fmt.Errorf("open hub database: maximum connections cannot be negative")
	}
	if config.MaxConns > 0 {
		poolConfig.MaxConns = config.MaxConns
	}
	poolConfig.ConnConfig.RuntimeParams["application_name"] = "sith-hub"
	poolConfig.ConnConfig.RuntimeParams["row_security"] = "on"
	poolConfig.ConnConfig.RuntimeParams["search_path"] = "pg_catalog"
	poolConfig.AfterConnect = func(connectCtx context.Context, connection *pgx.Conn) error {
		return verifyAppConnection(connectCtx, connection)
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("open hub database: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("open hub database: verify application role: %w", err)
	}
	return &AppDB{pool: pool}, nil
}

// Close releases all pooled PostgreSQL connections.
func (database *AppDB) Close() {
	if database != nil && database.pool != nil {
		database.pool.Close()
	}
}

// InWorkspace runs one callback in an explicit transaction with transaction-local RLS scope.
func (database *AppDB) InWorkspace(ctx context.Context, scope tenancy.Scope, run func(pgx.Tx) error) error {
	if database == nil || database.pool == nil || ctx == nil {
		return fmt.Errorf("run workspace transaction: database and context are required")
	}
	if run == nil {
		return fmt.Errorf("run workspace transaction: callback is required")
	}
	workspaceID := scope.WorkspaceID()
	if scope.Subject() == "" || !scope.Role().Valid() || scope.RequireWorkspace(workspaceID) != nil {
		return fmt.Errorf("run workspace transaction: validated workspace scope is required")
	}
	return pgx.BeginTxFunc(ctx, database.pool, pgx.TxOptions{
		IsoLevel:   pgx.ReadCommitted,
		AccessMode: pgx.ReadWrite,
	}, func(tx pgx.Tx) error {
		var configured string
		if err := tx.QueryRow(ctx, `SELECT set_config('sith.workspace_id', $1, true)`, string(workspaceID)).Scan(&configured); err != nil {
			return fmt.Errorf("set workspace transaction scope: %w", err)
		}
		if configured != string(workspaceID) {
			return fmt.Errorf("set workspace transaction scope: database returned a mismatched value")
		}
		if err := run(tx); err != nil {
			return fmt.Errorf("run workspace transaction: %w", err)
		}
		return nil
	})
}

func secureTransport(config *pgx.ConnConfig) bool {
	if config == nil || config.TLSConfig == nil {
		return false
	}
	for _, fallback := range config.Fallbacks {
		if fallback.TLSConfig == nil {
			return false
		}
	}
	return true
}

func localTransport(config *pgx.ConnConfig) bool {
	if config == nil || !localHost(config.Host) {
		return false
	}
	for _, fallback := range config.Fallbacks {
		if !localHost(fallback.Host) {
			return false
		}
	}
	return true
}

func localHost(host string) bool {
	if strings.EqualFold(host, "localhost") || strings.HasPrefix(host, "/") {
		return true
	}
	address, err := netip.ParseAddr(host)
	return err == nil && address.IsLoopback()
}

func verifyAppConnection(ctx context.Context, connection *pgx.Conn) error {
	if connection == nil {
		return fmt.Errorf("application connection is nil")
	}
	var (
		roleName       string
		superuser      bool
		bypassRLS      bool
		createRole     bool
		createDatabase bool
		replication    bool
		inherits       bool
		ownsSithObject bool
		hasMemberships bool
	)
	err := connection.QueryRow(ctx, `
		SELECT r.rolname, r.rolsuper, r.rolbypassrls, r.rolcreaterole, r.rolcreatedb, r.rolreplication,
		       r.rolinherit,
		       EXISTS (
		           SELECT 1
		           FROM pg_class c
		           JOIN pg_namespace n ON n.oid = c.relnamespace
		           WHERE n.nspname = 'sith' AND c.relowner = r.oid
		       ),
		       EXISTS (
		           SELECT 1 FROM pg_auth_members membership WHERE membership.member = r.oid
		       )
		FROM pg_roles r
		WHERE r.rolname = current_user
	`).Scan(&roleName, &superuser, &bypassRLS, &createRole, &createDatabase, &replication, &inherits, &ownsSithObject, &hasMemberships)
	if err != nil {
		return fmt.Errorf("inspect application role: %w", err)
	}
	if superuser || bypassRLS || createRole || createDatabase || replication || inherits || ownsSithObject || hasMemberships {
		return fmt.Errorf("application role %q is privileged or owns the Sith schema", roleName)
	}
	return nil
}
