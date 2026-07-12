// SPDX-License-Identifier: Apache-2.0

package fleetcache

import (
	"testing"
	"time"

	"github.com/ArdurAI/sith/internal/connector"
	"github.com/ArdurAI/sith/internal/fleet"
	"github.com/ArdurAI/sith/internal/tenancy"
)

func TestQueryScopedDerivesWorkspaceOnlyFromSignedScope(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	store := newStore(func() time.Time { return now }, time.Minute)
	store.SetDiscovery("workspace-a", connector.Discovery{Scopes: []connector.Scope{{Name: "cluster-a", Reachable: true, ObservedAt: now}}})
	store.SetDiscovery("workspace-b", connector.Discovery{Scopes: []connector.Scope{{Name: "cluster-b", Reachable: true, ObservedAt: now}}})
	factA := podFact(t, "cluster-a", "api-a", "Running", "image:a", now)
	factA.Workspace = "workspace-a"
	factB := podFact(t, "cluster-b", "api-b", "Running", "image:b", now)
	factB.Workspace = "workspace-b"
	if err := store.Replace("Pod", fleet.QueryResult{
		Facts: []fleet.Fact{factA, factB}, Coverage: fleet.Coverage{Requested: 2, Reachable: 2},
	}); err != nil {
		t.Fatal(err)
	}
	principal, err := tenancy.NewPrincipal("user:alice", map[tenancy.WorkspaceID]tenancy.Role{"workspace-a": tenancy.RoleReader})
	if err != nil {
		t.Fatal(err)
	}
	scope, err := principal.Scope("workspace-a")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.QueryScoped(scope, Query{})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Records) != 1 || snapshot.Records[0].Workspace != "workspace-a" || snapshot.Records[0].Name != "api-a" {
		t.Fatalf("scoped records = %#v", snapshot.Records)
	}
	for _, guessedName := range []string{"cluster-b", "does-not-exist"} {
		guessed, queryErr := store.QueryScoped(scope, Query{Scopes: []string{guessedName}})
		if queryErr != nil {
			t.Fatal(queryErr)
		}
		if len(guessed.Records) != 0 || len(guessed.Scopes) != 1 || guessed.Scopes[0].Name != guessedName ||
			guessed.Scopes[0].Reachable || !guessed.Scopes[0].ObservedAt.IsZero() || len(guessed.Scopes[0].Kinds) != 0 {
			t.Fatalf("guessed scope %q exposed observed metadata: %#v", guessedName, guessed)
		}
	}
}

func TestQueryScopedRejectsMissingBoundary(t *testing.T) {
	t.Parallel()

	if _, err := (*Store)(nil).QueryScoped(tenancy.Scope{}, Query{}); err == nil {
		t.Fatal("nil store unexpectedly queried")
	}
	if _, err := New().QueryScoped(tenancy.Scope{}, Query{}); err == nil {
		t.Fatal("empty workspace scope unexpectedly queried")
	}
}
