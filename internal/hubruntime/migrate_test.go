// SPDX-License-Identifier: Apache-2.0

package hubruntime

import "testing"

func TestLoadMigrationConfigRequiresDistinctDeploymentInputs(t *testing.T) {
	t.Parallel()

	if _, err := loadMigrationConfig(func(string) (string, bool) { return "", false }); err == nil {
		t.Fatal("loadMigrationConfig accepted missing inputs")
	}
	values := map[string]string{
		"SITH_HUB_MIGRATION_OWNER_DATABASE_URL": "postgres://sith_owner@db.sith.svc/sith?sslmode=require",
		"SITH_HUB_APPLICATION_DATABASE_ROLE":    "sith_app",
	}
	config, err := loadMigrationConfig(func(name string) (string, bool) { value, ok := values[name]; return value, ok })
	if err != nil || config.ownerDatabaseURL != values["SITH_HUB_MIGRATION_OWNER_DATABASE_URL"] || config.applicationRole != "sith_app" {
		t.Fatalf("config/error = %#v/%v", config, err)
	}
	values["SITH_HUB_APPLICATION_DATABASE_ROLE"] = " sith_app"
	if _, err := loadMigrationConfig(func(name string) (string, bool) { value, ok := values[name]; return value, ok }); err == nil {
		t.Fatal("loadMigrationConfig accepted whitespace-padded application role")
	}
}
