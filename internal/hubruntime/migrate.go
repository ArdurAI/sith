// SPDX-License-Identifier: Apache-2.0

package hubruntime

import (
	"context"
	"fmt"
	"os"

	"github.com/ArdurAI/sith/internal/hubdb"
)

type migrationConfig struct {
	ownerDatabaseURL string
	applicationRole  string
}

// MigrateFromEnvironment applies the hub schema from a short-lived owner-credential process. It
// deliberately does not construct the TLS hub server, a Kubernetes client, or any read transport.
func MigrateFromEnvironment(ctx context.Context) error {
	config, err := loadMigrationConfig(os.LookupEnv)
	if err != nil {
		return err
	}
	return hubdb.Migrate(ctx, hubdb.MigrationConfig{
		OwnerURL:        config.ownerDatabaseURL,
		ApplicationRole: config.applicationRole,
	})
}

func loadMigrationConfig(lookup func(string) (string, bool)) (migrationConfig, error) {
	if lookup == nil {
		return migrationConfig{}, fmt.Errorf("load hub migration configuration: environment lookup is required")
	}
	ownerDatabaseURL, err := requiredEnvironment(lookup, "SITH_HUB_MIGRATION_OWNER_DATABASE_URL")
	if err != nil {
		return migrationConfig{}, err
	}
	applicationRole, err := requiredEnvironment(lookup, "SITH_HUB_APPLICATION_DATABASE_ROLE")
	if err != nil {
		return migrationConfig{}, err
	}
	return migrationConfig{ownerDatabaseURL: ownerDatabaseURL, applicationRole: applicationRole}, nil
}
