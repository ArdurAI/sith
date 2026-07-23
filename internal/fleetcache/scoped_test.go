// SPDX-License-Identifier: Apache-2.0

package fleetcache

import (
	"errors"
	"slices"
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
	for workspace, fact := range map[string]fleet.Fact{"workspace-a": factA, "workspace-b": factB} {
		if err := store.Replace(workspace, "Pod", fleet.QueryResult{
			Facts: []fleet.Fact{fact}, Coverage: fleet.Coverage{Requested: 1, Reachable: 1},
		}); err != nil {
			t.Fatal(err)
		}
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

func TestQueryScopedIsolatesSameNamedScopeMetadataByWorkspace(t *testing.T) {
	t.Parallel()

	observedA := time.Date(2026, time.July, 16, 12, 0, 0, 0, time.UTC)
	observedB := observedA.Add(time.Minute)
	store := newStore(func() time.Time { return observedB }, time.Hour)
	store.SetDiscovery("workspace-a", connector.Discovery{Scopes: []connector.Scope{{
		Name: "shared", DisplayName: "A shared", Kinds: []string{"Pod"}, Reachable: true, ObservedAt: observedA,
	}}})
	store.SetDiscovery("workspace-b", connector.Discovery{Scopes: []connector.Scope{{
		Name: "shared", DisplayName: "B shared", Kinds: []string{"Deployment"}, ObservedAt: observedB,
	}}, Unreachable: []string{"shared"}})
	principal, err := tenancy.NewPrincipal("user:alice", map[tenancy.WorkspaceID]tenancy.Role{
		"workspace-a": tenancy.RoleReader,
		"workspace-b": tenancy.RoleReader,
	})
	if err != nil {
		t.Fatal(err)
	}
	scopeA, err := principal.Scope("workspace-a")
	if err != nil {
		t.Fatal(err)
	}
	scopeB, err := principal.Scope("workspace-b")
	if err != nil {
		t.Fatal(err)
	}

	snapshotA, err := store.QueryScoped(scopeA, Query{})
	if err != nil {
		t.Fatal(err)
	}
	snapshotB, err := store.QueryScoped(scopeB, Query{})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshotA.Scopes) != 1 || snapshotA.Scopes[0].DisplayName != "A shared" ||
		!snapshotA.Scopes[0].Reachable || snapshotA.Scopes[0].ObservedAt != observedA ||
		!slices.Equal(snapshotA.Scopes[0].Kinds, []string{"Pod"}) {
		t.Fatalf("workspace A scope = %#v, want independent reachable metadata", snapshotA.Scopes)
	}
	if len(snapshotB.Scopes) != 1 || snapshotB.Scopes[0].DisplayName != "B shared" ||
		snapshotB.Scopes[0].Reachable || snapshotB.Scopes[0].ObservedAt != observedB ||
		!slices.Equal(snapshotB.Scopes[0].Kinds, []string{"Deployment"}) {
		t.Fatalf("workspace B scope = %#v, want independent unreachable metadata", snapshotB.Scopes)
	}
	if snapshotA.Coverage.Reachable != 1 || len(snapshotA.Coverage.Unreachable) != 0 ||
		snapshotB.Coverage.Reachable != 0 || !slices.Equal(snapshotB.Coverage.Unreachable, []string{"shared"}) {
		t.Fatalf("coverage A = %#v, B = %#v, want workspace-isolated fail-closed coverage", snapshotA.Coverage, snapshotB.Coverage)
	}
	factA := podFact(t, "shared", "api-a", "Running", "image:a", observedB)
	factA.Workspace = "workspace-a"
	if err := store.ApplyWatchEvent(connector.WatchEvent{
		Type: connector.WatchError, Workspace: "workspace-b",
		Kind: "Pod", Scope: "shared", Err: errors.New("watch unavailable"),
	}); err != nil {
		t.Fatalf("ApplyWatchEvent(error) error = %v", err)
	}
	if err := store.ApplyWatchEvent(connector.WatchEvent{
		Type: connector.WatchUpsert, Workspace: "workspace-a",
		Kind: "Pod", Scope: "shared", Fact: factA, ObservedAt: observedB.Add(time.Minute),
	}); err != nil {
		t.Fatalf("ApplyWatchEvent() error = %v", err)
	}
	snapshotA, err = store.QueryScoped(scopeA, Query{})
	if err != nil {
		t.Fatal(err)
	}
	snapshotB, err = store.QueryScoped(scopeB, Query{})
	if err != nil {
		t.Fatal(err)
	}
	if snapshotA.Scopes[0].ObservedAt != observedB.Add(time.Minute) || !snapshotA.Scopes[0].Reachable ||
		snapshotB.Scopes[0].ObservedAt != observedB || snapshotB.Scopes[0].Reachable {
		t.Fatalf("workspace-qualified watch changed the wrong shared metadata: A = %#v, B = %#v", snapshotA.Scopes, snapshotB.Scopes)
	}
	if snapshotA.Coverage.Reachable != 1 || len(snapshotA.Coverage.Unreachable) != 0 ||
		snapshotB.Coverage.Reachable != 0 || !slices.Equal(snapshotB.Coverage.Unreachable, []string{"shared"}) ||
		snapshotA.LastError != "" || snapshotB.LastError == "" {
		t.Fatalf("workspace-qualified success crossed failure state: A = %#v, B = %#v", snapshotA, snapshotB)
	}

	store.SetDiscovery("workspace-b", connector.Discovery{Scopes: []connector.Scope{{
		Name: "cluster-b", DisplayName: "B replacement", Reachable: true, ObservedAt: observedB,
	}}})
	snapshotA, err = store.QueryScoped(scopeA, Query{})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshotA.Scopes) != 1 || snapshotA.Scopes[0].DisplayName != "A shared" || !snapshotA.Scopes[0].Reachable {
		t.Fatalf("workspace B refresh changed workspace A metadata: %#v", snapshotA.Scopes)
	}
	guessedB, err := store.QueryScoped(scopeB, Query{Scopes: []string{"shared"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(guessedB.Scopes) != 1 || guessedB.Scopes[0].Reachable || !guessedB.Scopes[0].ObservedAt.IsZero() || len(guessedB.Scopes[0].Kinds) != 0 {
		t.Fatalf("removed workspace B scope exposed workspace A metadata: %#v", guessedB.Scopes)
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

func FuzzQueryScopedNeverLeaksForeignWorkspace(f *testing.F) {
	for _, seed := range []string{"cluster-a", "cluster-b", "does-not-exist", "*", "cluster-*", "[", "\x00"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, selector string) {
		if len(selector) > 1024 {
			return
		}
		now := time.Date(2026, time.July, 11, 12, 0, 0, 0, time.UTC)
		store := newStore(func() time.Time { return now }, time.Minute)
		store.SetDiscovery("workspace-a", connector.Discovery{Scopes: []connector.Scope{{Name: "cluster-a", Reachable: true, ObservedAt: now}}})
		store.SetDiscovery("workspace-b", connector.Discovery{Scopes: []connector.Scope{{Name: "cluster-b", Reachable: true, ObservedAt: now}}})
		factA := podFact(t, "cluster-a", "api-a", "Running", "image:a", now)
		factA.Workspace = "workspace-a"
		factB := podFact(t, "cluster-b", "api-b", "Running", "image:b", now)
		factB.Workspace = "workspace-b"
		for workspace, fact := range map[string]fleet.Fact{"workspace-a": factA, "workspace-b": factB} {
			if err := store.Replace(workspace, "Pod", fleet.QueryResult{Facts: []fleet.Fact{fact}}); err != nil {
				t.Fatal(err)
			}
		}
		principal, err := tenancy.NewPrincipal("user:alice", map[tenancy.WorkspaceID]tenancy.Role{"workspace-a": tenancy.RoleReader})
		if err != nil {
			t.Fatal(err)
		}
		scope, err := principal.Scope("workspace-a")
		if err != nil {
			t.Fatal(err)
		}
		snapshot, err := store.QueryScoped(scope, Query{Scopes: []string{selector}})
		if err != nil {
			t.Fatal(err)
		}
		for _, record := range snapshot.Records {
			if record.Workspace != "workspace-a" || record.Cluster != "cluster-a" || record.Name != "api-a" {
				t.Fatalf("selector %q leaked foreign record: %#v", selector, record)
			}
		}
		for _, selectedScope := range snapshot.Scopes {
			if selectedScope.Name != "cluster-a" && (selectedScope.Reachable || !selectedScope.ObservedAt.IsZero() || len(selectedScope.Kinds) != 0) {
				t.Fatalf("selector %q leaked foreign scope metadata: %#v", selector, selectedScope)
			}
		}
	})
}
