// SPDX-License-Identifier: Apache-2.0

package fleetcache

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ArdurAI/sith/internal/connector"
	"github.com/ArdurAI/sith/internal/fleet"
)

func TestStoreRejectsWorkspaceMismatchedMutationsAtomically(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 16, 18, 0, 0, 0, time.UTC)
	store := newStore(func() time.Time { return now }, time.Minute)
	if err := store.SetDiscovery("workspace-a", connector.Discovery{Scopes: []connector.Scope{{
		Name: "shared", Reachable: true, ObservedAt: now,
	}}}); err != nil {
		t.Fatal(err)
	}
	original := podFact(t, "shared", "api-0", "Running", "registry/api:original", now)
	original.Workspace = "workspace-a"
	if err := store.Replace("workspace-a", "Pod", fleet.QueryResult{Facts: []fleet.Fact{original}}); err != nil {
		t.Fatal(err)
	}

	foreign := podFact(t, "shared", "api-0", "Running", "registry/api:foreign", now)
	foreign.Workspace = "workspace-b"
	validUpdate := podFact(t, "shared", "api-0", "Running", "registry/api:valid-update", now)
	validUpdate.Workspace = "workspace-a"
	if err := store.Replace("workspace-a", "Pod", fleet.QueryResult{Facts: []fleet.Fact{foreign}}); err == nil ||
		!strings.Contains(err.Error(), "does not match mutation workspace") {
		t.Fatalf("Replace(foreign fact) error = %v, want workspace mismatch", err)
	}
	if err := store.Replace("workspace-a", "Pod", fleet.QueryResult{Facts: []fleet.Fact{validUpdate, foreign}}); err == nil ||
		!strings.Contains(err.Error(), "does not match mutation workspace") {
		t.Fatalf("Replace(mixed facts) error = %v, want workspace mismatch", err)
	}
	if err := store.ApplyWatchEvent(connector.WatchEvent{
		Type: connector.WatchUpsert, Workspace: "workspace-a", Kind: "Pod", Scope: "shared", Fact: foreign,
	}); err == nil || !strings.Contains(err.Error(), "does not match stream workspace") {
		t.Fatalf("ApplyWatchEvent(foreign fact) error = %v, want workspace mismatch", err)
	}
	if err := store.ApplyWatchEvent(connector.WatchEvent{
		Type: connector.WatchSnapshot, Workspace: "workspace-a", Kind: "Pod", Scope: "shared",
		Facts: []fleet.Fact{validUpdate, foreign},
	}); err == nil || !strings.Contains(err.Error(), "does not match stream workspace") {
		t.Fatalf("ApplyWatchEvent(mixed snapshot) error = %v, want workspace mismatch", err)
	}

	records := store.Query("workspace-a", Query{Kind: "Pod"}).Records
	if len(records) != 1 || len(records[0].Images) != 1 || records[0].Images[0] != "registry/api:original" {
		t.Fatalf("records after rejected mutations = %#v, want original row unchanged", records)
	}
	if records := store.Query("workspace-b", Query{Kind: "Pod"}).Records; len(records) != 0 {
		t.Fatalf("workspace B records = %#v, want no leaked mutation", records)
	}
}

func TestStoreWatchSnapshotAndDeleteAreWorkspaceIsolated(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 16, 18, 0, 0, 0, time.UTC)
	store := newStore(func() time.Time { return now }, time.Minute)
	for _, workspace := range []string{"workspace-a", "workspace-b"} {
		if err := store.SetDiscovery(workspace, connector.Discovery{Scopes: []connector.Scope{{
			Name: "shared", Reachable: true, ObservedAt: now,
		}}}); err != nil {
			t.Fatal(err)
		}
		fact := podFact(t, "shared", "api-0", "Running", "registry/api:"+workspace, now)
		fact.Workspace = workspace
		if err := store.Replace(workspace, "Pod", fleet.QueryResult{Facts: []fleet.Fact{fact}}); err != nil {
			t.Fatal(err)
		}
	}
	updatedA := podFact(t, "shared", "api-0", "Running", "registry/api:workspace-a-updated", now)
	updatedA.Workspace = "workspace-a"
	if err := store.ApplyWatchEvent(connector.WatchEvent{
		Type: connector.WatchUpsert, Workspace: "workspace-a", Kind: "Pod", Scope: "shared", Fact: updatedA,
	}); err != nil {
		t.Fatal(err)
	}
	recordsB := store.Query("workspace-b", Query{Kind: "Pod"}).Records
	if len(recordsB) != 1 || recordsB[0].Images[0] != "registry/api:workspace-b" {
		t.Fatalf("workspace B records after workspace A upsert = %#v, want untouched original", recordsB)
	}

	replacement := podFact(t, "shared", "api-1", "Running", "registry/api:replacement", now)
	replacement.Workspace = "workspace-a"
	if err := store.ApplyWatchEvent(connector.WatchEvent{
		Type: connector.WatchSnapshot, Workspace: "workspace-a", Kind: "Pod", Scope: "shared",
		Facts: []fleet.Fact{replacement}, ObservedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.ApplyWatchEvent(connector.WatchEvent{
		Type: connector.WatchDelete, Workspace: "workspace-a", Kind: "Pod", Scope: "shared",
		Ref: fleet.ResourceRef{Scope: "shared", Namespace: "apps", Name: "api-1"}, ObservedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	if records := store.Query("workspace-a", Query{Kind: "Pod"}).Records; len(records) != 0 {
		t.Fatalf("workspace A records after delete = %#v, want empty", records)
	}
	records := store.Query("workspace-b", Query{Kind: "Pod"}).Records
	if len(records) != 1 || records[0].Name != "api-0" || records[0].Images[0] != "registry/api:workspace-b" {
		t.Fatalf("workspace B records = %#v, want untouched original", records)
	}
}

func TestStoreSyncLifecycleIsWorkspaceIsolated(t *testing.T) {
	t.Parallel()

	store := New()
	if !store.BeginSync("workspace-a", "Pod") {
		t.Fatal("BeginSync(workspace-a) = false")
	}
	if snapshot := store.Query("workspace-a", Query{Kind: "Pod"}); !snapshot.Syncing || snapshot.State != StateWarming {
		t.Fatalf("workspace A snapshot = %#v, want warming", snapshot)
	}
	if snapshot := store.Query("workspace-b", Query{Kind: "Pod"}); snapshot.Syncing || snapshot.State != StateCold || snapshot.Version != 0 {
		t.Fatalf("workspace B snapshot = %#v, want independent cold state", snapshot)
	}
	if err := store.EndSync("workspace-a", errors.New("sync failed")); err != nil {
		t.Fatal(err)
	}
	if snapshot := store.Query("workspace-b", Query{Kind: "Pod"}); snapshot.LastError != "" || snapshot.State != StateCold {
		t.Fatalf("workspace B snapshot = %#v, want no foreign sync error", snapshot)
	}
	if err := store.SetPaused("workspace-a", true); err != nil {
		t.Fatal(err)
	}
	if snapshot := store.Query("workspace-a", Query{}); !snapshot.Paused || snapshot.State != StatePaused {
		t.Fatalf("workspace A snapshot = %#v, want paused", snapshot)
	}
	if snapshot := store.Query("workspace-b", Query{}); snapshot.Paused || snapshot.State == StatePaused {
		t.Fatalf("workspace B snapshot = %#v, want pause isolation", snapshot)
	}
}

func TestStoreRejectsMissingWorkspaceMutationBoundaries(t *testing.T) {
	t.Parallel()

	store := New()
	if store.BeginSync("", "Pod") {
		t.Fatal("BeginSync(empty workspace) = true")
	}
	if err := store.SetDiscovery("", connector.Discovery{}); err == nil {
		t.Fatal("SetDiscovery(empty workspace) error = nil")
	}
	if err := store.Replace("", "Pod", fleet.QueryResult{}); err == nil {
		t.Fatal("Replace(empty workspace) error = nil")
	}
	if err := store.ApplyWatchEvent(connector.WatchEvent{Type: connector.WatchError, Kind: "Pod", Scope: "shared", Err: errors.New("failed")}); err == nil {
		t.Fatal("ApplyWatchEvent(empty workspace) error = nil")
	}
	if err := store.EndSync("", nil); err == nil {
		t.Fatal("EndSync(empty workspace) error = nil")
	}
	if err := store.SetPaused("", true); err == nil {
		t.Fatal("SetPaused(empty workspace) error = nil")
	}
	if _, err := store.WaitForChange(t.Context(), "", 0); err == nil {
		t.Fatal("WaitForChange(empty workspace) error = nil")
	}
}

func TestStorePreservesIdenticalRecordsAcrossWorkspaces(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 16, 18, 0, 0, 0, time.UTC)
	store := newStore(func() time.Time { return now }, time.Minute)
	for _, workspace := range []string{"workspace-a", "workspace-b"} {
		if err := store.SetDiscovery(workspace, connector.Discovery{Scopes: []connector.Scope{{
			Name: "shared", Reachable: true, ObservedAt: now,
		}}}); err != nil {
			t.Fatal(err)
		}
	}
	factA := podFact(t, "shared", "api-0", "Running", "registry/api:a", now)
	factA.Workspace = "workspace-a"
	factB := podFact(t, "shared", "api-0", "Running", "registry/api:b", now)
	factB.Workspace = "workspace-b"
	for workspace, fact := range map[string]fleet.Fact{"workspace-a": factA, "workspace-b": factB} {
		if err := store.Replace(workspace, "Pod", fleet.QueryResult{
			Facts: []fleet.Fact{fact}, Coverage: fleet.Coverage{Requested: 1, Reachable: 1},
		}); err != nil {
			t.Fatal(err)
		}
	}

	for workspace, wantImage := range map[string]string{
		"workspace-a": "registry/api:a",
		"workspace-b": "registry/api:b",
	} {
		records := store.Query(workspace, Query{Kind: "Pod"}).Records
		if len(records) != 1 || records[0].Workspace != workspace || len(records[0].Images) != 1 || records[0].Images[0] != wantImage {
			t.Fatalf("%s records = %#v, want independent image %q", workspace, records, wantImage)
		}
	}
}

func TestStoreWatchFailureDoesNotDegradeAnotherWorkspace(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 16, 18, 0, 0, 0, time.UTC)
	store := newStore(func() time.Time { return now }, time.Minute)
	if err := store.SetDiscovery("workspace-a", connector.Discovery{Scopes: []connector.Scope{{
		Name: "cluster-a", Reachable: true, ObservedAt: now,
	}}}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetDiscovery("workspace-b", connector.Discovery{Scopes: []connector.Scope{{
		Name: "cluster-b", Reachable: true, ObservedAt: now,
	}}}); err != nil {
		t.Fatal(err)
	}
	factA := podFact(t, "cluster-a", "api-a", "Running", "registry/api:a", now)
	factA.Workspace = "workspace-a"
	factB := podFact(t, "cluster-b", "api-b", "Running", "registry/api:b", now)
	factB.Workspace = "workspace-b"
	for workspace, fact := range map[string]fleet.Fact{"workspace-a": factA, "workspace-b": factB} {
		if err := store.Replace(workspace, "Pod", fleet.QueryResult{
			Facts: []fleet.Fact{fact}, Coverage: fleet.Coverage{Requested: 1, Reachable: 1},
		}); err != nil {
			t.Fatal(err)
		}
	}
	deployment := objectFact(t, "Deployment", map[string]any{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata":   map[string]any{"name": "payments", "namespace": "apps"},
		"spec":       map[string]any{"replicas": 1},
		"status":     map[string]any{"availableReplicas": 1, "updatedReplicas": 1},
	}, now)
	deployment.Workspace = "workspace-a"
	deployment.Ref.Scope = "cluster-a"
	deployment.Ref.Namespace = "apps"
	deployment.Ref.Name = "payments"
	if err := store.Replace("workspace-a", "Deployment", fleet.QueryResult{
		Facts: []fleet.Fact{deployment}, Coverage: fleet.Coverage{Requested: 1, Reachable: 1},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.ApplyWatchEvent(connector.WatchEvent{
		Type: connector.WatchError, Workspace: "workspace-a",
		Kind: "Pod", Scope: "cluster-a", Err: errors.New("watch unavailable"),
	}); err != nil {
		t.Fatal(err)
	}

	snapshotA := store.Query("workspace-a", Query{Kind: "Pod"})
	if snapshotA.LastError == "" || snapshotA.State != StateOffline || len(snapshotA.Scopes) != 1 || !snapshotA.Scopes[0].Reachable ||
		len(snapshotA.Records) != 1 || snapshotA.Records[0].Name != "api-a" || !snapshotA.Records[0].Stale {
		t.Fatalf("workspace A snapshot = %#v, want isolated watch failure", snapshotA)
	}
	snapshotB := store.Query("workspace-b", Query{Kind: "Pod"})
	if snapshotB.LastError != "" || snapshotB.State != StateWarm || !snapshotB.Coverage.Complete() ||
		len(snapshotB.Scopes) != 1 || !snapshotB.Scopes[0].Reachable {
		t.Fatalf("workspace B snapshot = %#v, want unaffected warm state", snapshotB)
	}
	deploymentSnapshot := store.Query("workspace-a", Query{Kind: "Deployment"})
	if deploymentSnapshot.State != StateWarm || !deploymentSnapshot.Coverage.Complete() ||
		len(deploymentSnapshot.Records) != 1 || deploymentSnapshot.Records[0].Name != "payments" {
		t.Fatalf("workspace A deployment snapshot = %#v, want healthy kind preserved", deploymentSnapshot)
	}
}

func TestStoreKindAliasesAreWorkspaceIsolated(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 16, 18, 0, 0, 0, time.UTC)
	store := newStore(func() time.Time { return now }, time.Minute)
	for workspace, kind := range map[string]string{"workspace-a": "Widget", "workspace-b": "WIDGET"} {
		if err := store.SetDiscovery(workspace, connector.Discovery{Scopes: []connector.Scope{{
			Name: "shared", Reachable: true, ObservedAt: now,
		}}}); err != nil {
			t.Fatal(err)
		}
		fact := objectFact(t, kind, map[string]any{
			"apiVersion": "example.sith.dev/v1",
			"kind":       kind,
			"metadata":   map[string]any{"name": "object"},
		}, now)
		fact.Workspace = workspace
		fact.Ref.Scope = "shared"
		if err := store.Replace(workspace, kind, fleet.QueryResult{
			Facts: []fleet.Fact{fact}, Coverage: fleet.Coverage{Requested: 1, Reachable: 1},
		}); err != nil {
			t.Fatal(err)
		}
	}

	for workspace, wantKind := range map[string]string{"workspace-a": "Widget", "workspace-b": "WIDGET"} {
		records := store.Query(workspace, Query{Kind: "widget"}).Records
		if len(records) != 1 || records[0].Workspace != workspace || records[0].Kind != wantKind {
			t.Fatalf("%s alias records = %#v, want kind %q", workspace, records, wantKind)
		}
	}
}

func TestStoreChangeNotificationsAreWorkspaceIsolated(t *testing.T) {
	t.Parallel()

	store := New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	waitDone := make(chan error, 1)
	go func() {
		_, err := store.WaitForChange(ctx, "workspace-b", 0)
		waitDone <- err
	}()

	deadline := time.Now().Add(time.Second)
	for {
		store.mu.RLock()
		registered := store.changed["workspace-b"] != nil
		store.mu.RUnlock()
		if registered {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("workspace B change waiter did not register")
		}
		time.Sleep(time.Millisecond)
	}

	if err := store.SetDiscovery("workspace-a", connector.Discovery{}); err != nil {
		t.Fatal(err)
	}
	if version := store.Query("workspace-a", Query{}).Version; version != 1 {
		t.Fatalf("workspace A version = %d, want 1", version)
	}
	if version := store.Query("workspace-b", Query{}).Version; version != 0 {
		t.Fatalf("workspace B version = %d, want 0", version)
	}
	select {
	case err := <-waitDone:
		t.Fatalf("workspace B waiter returned after workspace A mutation: %v", err)
	default:
	}
	cancel()
	select {
	case err := <-waitDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("workspace B waiter error = %v, want context cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("workspace B waiter did not return after cancellation")
	}
}

func FuzzStoreForeignWorkspaceMutationCannotChangeEitherWorkspace(f *testing.F) {
	for _, workspace := range []string{"workspace-b", "", " workspace-b", "workspace-b\x00suffix", strings.Repeat("x", 257)} {
		f.Add(workspace)
	}
	f.Fuzz(func(t *testing.T, factWorkspace string) {
		if factWorkspace == "workspace-a" {
			factWorkspace = "workspace-b"
		}
		now := time.Date(2026, time.July, 16, 18, 0, 0, 0, time.UTC)
		store := newStore(func() time.Time { return now }, time.Minute)
		for _, workspace := range []string{"workspace-a", "workspace-b"} {
			if err := store.SetDiscovery(workspace, connector.Discovery{Scopes: []connector.Scope{{
				Name: "shared", Reachable: true, ObservedAt: now,
			}}}); err != nil {
				t.Fatal(err)
			}
			fact := podFact(t, "shared", "api-0", "Running", "registry/api:"+workspace, now)
			fact.Workspace = workspace
			if err := store.Replace(workspace, "Pod", fleet.QueryResult{Facts: []fleet.Fact{fact}}); err != nil {
				t.Fatal(err)
			}
		}

		foreign := podFact(t, "shared", "api-0", "Running", "registry/api:foreign", now)
		foreign.Workspace = factWorkspace
		if err := store.Replace("workspace-a", "Pod", fleet.QueryResult{Facts: []fleet.Fact{foreign}}); err == nil {
			t.Fatal("Replace(foreign workspace) error = nil")
		}
		if err := store.ApplyWatchEvent(connector.WatchEvent{
			Type: connector.WatchUpsert, Workspace: "workspace-a", Kind: "Pod", Scope: "shared", Fact: foreign,
		}); err == nil {
			t.Fatal("ApplyWatchEvent(foreign workspace) error = nil")
		}

		for _, workspace := range []string{"workspace-a", "workspace-b"} {
			records := store.Query(workspace, Query{Kind: "Pod"}).Records
			wantImage := "registry/api:" + workspace
			if len(records) != 1 || len(records[0].Images) != 1 || records[0].Images[0] != wantImage {
				t.Fatalf("%s records after rejected foreign mutation = %#v, want %q", workspace, records, wantImage)
			}
		}
	})
}
