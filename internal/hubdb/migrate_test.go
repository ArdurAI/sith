// SPDX-License-Identifier: Apache-2.0

package hubdb

import (
	"context"
	"strings"
	"testing"
)

func TestMigrateRejectsUnsafeConfigurationBeforeConnecting(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		config MigrationConfig
		want   string
	}{
		{name: "nil context", config: MigrationConfig{}, want: "context is required"},
		{name: "missing owner URL", config: MigrationConfig{ApplicationRole: "sith_app"}, want: "owner database URL is required"},
		{name: "invalid application role", config: MigrationConfig{OwnerURL: "postgres://sith_owner:password@db.example/sith?sslmode=require"}, want: "application role"},
		{name: "malformed owner URL", config: MigrationConfig{OwnerURL: "postgres://%", ApplicationRole: "sith_app"}, want: "owner database URL is invalid"},
		{name: "application role as owner", config: MigrationConfig{OwnerURL: "postgres://sith_app:password@db.example/sith?sslmode=require", ApplicationRole: "sith_app"}, want: "roles must differ"},
		{name: "remote plaintext", config: MigrationConfig{OwnerURL: "postgres://sith_owner:password@192.0.2.1/sith?sslmode=disable", ApplicationRole: "sith_app", AllowInsecureLocal: true}, want: "TLS"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			if test.name == "nil context" {
				ctx = nil
			}
			err := Migrate(ctx, test.config)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Migrate() error = %v, want %q", err, test.want)
			}
		})
	}
}
