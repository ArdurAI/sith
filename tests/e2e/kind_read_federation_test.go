// SPDX-License-Identifier: Apache-2.0
//go:build e2e && kind

package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/ArdurAI/sith/internal/connector/kubeconfig"
	"github.com/ArdurAI/sith/internal/fleet"
	"github.com/ArdurAI/sith/internal/hubfleet"
	"github.com/ArdurAI/sith/internal/tenancy"
)

func exerciseReadFederationSnapshots(
	ctx context.Context,
	t *testing.T,
	adapter *kubeconfig.Adapter,
	clusterNames []string,
) {
	t.Helper()
	if len(clusterNames) != 2 {
		t.Fatalf("read federation needs two kind spokes, got %d", len(clusterNames))
	}
	store := &kindSnapshotStore{
		spokes: []hubfleet.Spoke{
			{ID: "spoke-a", ManagedClusterRef: "kind-" + clusterNames[0]},
			{ID: "spoke-b", ManagedClusterRef: "kind-" + clusterNames[1]},
		},
		snapshots: make(map[string]hubfleet.Snapshot),
		failures:  make(map[string]hubfleet.FailureKind),
	}
	collector, err := hubfleet.NewCollector(hubfleet.CollectorConfig{
		Store:     store,
		Transport: kindSnapshotTransport{adapter: adapter},
	})
	if err != nil {
		t.Fatalf("construct read-federation collector: %v", err)
	}
	scope := e2eReaderScope(t)
	coverage, err := collector.Collect(ctx, scope)
	if err != nil || coverage.Requested != 2 || coverage.Reachable != 2 || len(coverage.Unreachable) != 0 || len(coverage.Stale) != 0 {
		t.Fatalf("collect two real kind spoke snapshots = %#v, error = %v", coverage, err)
	}
	store.mu.Lock()
	for _, spokeID := range []string{"spoke-a", "spoke-b"} {
		snapshot, exists := store.snapshots[spokeID]
		if !exists || len(snapshot.Facts) < 2 || snapshot.Facts[0].Ref.SourceKind != hubfleet.SourceKind ||
			snapshot.Facts[0].Source != spokeID {
			store.mu.Unlock()
			t.Fatalf("real kind snapshot for %s = %#v", spokeID, snapshot)
		}
	}
	store.spokes[1].ManagedClusterRef = "kind-sith-e2e-unreachable"
	store.mu.Unlock()
	coverage, err = collector.Collect(ctx, scope)
	if err != nil || coverage.Reachable != 1 || len(coverage.Unreachable) != 1 || coverage.Unreachable[0] != "spoke-b" ||
		len(coverage.Stale) != 1 || coverage.Stale[0] != "spoke-b" {
		t.Fatalf("degraded real spoke collection = %#v, error = %v", coverage, err)
	}
}

type kindSnapshotTransport struct {
	adapter *kubeconfig.Adapter
}

func (transport kindSnapshotTransport) Snapshot(
	ctx context.Context,
	_ tenancy.WorkspaceID,
	spoke hubfleet.Spoke,
) (hubfleet.Snapshot, error) {
	if transport.adapter == nil {
		return hubfleet.Snapshot{}, fmt.Errorf("kind snapshot transport has no adapter")
	}
	result, err := transport.adapter.Query(ctx, fleet.Query{
		Kinds:  []fleet.FactKind{fleet.FactInventory},
		Scopes: []string{spoke.ManagedClusterRef},
		Selector: fleet.Selector{
			ResourceKind: "Pod", Namespace: "default", NamePrefix: "sith-",
		},
	})
	if err != nil {
		return hubfleet.Snapshot{}, err
	}
	if result.Coverage.Requested != 1 || result.Coverage.Reachable != 1 || len(result.Coverage.Unreachable) != 0 {
		return hubfleet.Snapshot{}, fmt.Errorf("spoke %q did not return a complete bounded read", spoke.ID)
	}
	now := time.Now().UTC()
	facts := make([]fleet.Evidence, 0, len(result.Facts)*2)
	for _, fact := range result.Facts {
		if fact.Ref.Scope != spoke.ManagedClusterRef {
			return hubfleet.Snapshot{}, fmt.Errorf("transport received a fact from another spoke")
		}
		resourceRef := fleet.ResourceRef{
			SourceKind: hubfleet.SourceKind,
			Scope:      spoke.ID,
			Kind:       fact.Ref.Kind,
			Namespace:  fact.Ref.Namespace,
			Name:       fact.Ref.Name,
		}
		facts = append(facts,
			fleet.Evidence{
				Ref:        resourceRef,
				Kind:       fleet.FactInventory,
				Observed:   json.RawMessage(`{"resource":"Pod"}`),
				ObservedAt: now,
				Source:     spoke.ID,
				Provenance: fleet.Provenance{Adapter: hubfleet.SourceKind, ProtocolV: "1.0.0"},
			},
			fleet.Evidence{
				Ref:        resourceRef,
				Kind:       fleet.FactHealth,
				Observed:   json.RawMessage(`{"status":"Healthy"}`),
				ObservedAt: now,
				Source:     spoke.ID,
				Provenance: fleet.Provenance{Adapter: hubfleet.SourceKind, ProtocolV: "1.0.0"},
			},
		)
	}
	return hubfleet.Snapshot{ObservedAt: now, Facts: facts}, nil
}

type kindSnapshotStore struct {
	mu        sync.Mutex
	spokes    []hubfleet.Spoke
	snapshots map[string]hubfleet.Snapshot
	failures  map[string]hubfleet.FailureKind
}

func (store *kindSnapshotStore) RegisteredSpokes(_ context.Context, _ tenancy.Scope) ([]hubfleet.Spoke, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return append([]hubfleet.Spoke(nil), store.spokes...), nil
}

func (store *kindSnapshotStore) ReplaceSnapshot(
	_ context.Context,
	_ tenancy.Scope,
	spoke hubfleet.Spoke,
	snapshot hubfleet.Snapshot,
	_ time.Time,
) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.snapshots[spoke.ID] = snapshot
	delete(store.failures, spoke.ID)
	return nil
}

func (store *kindSnapshotStore) MarkSnapshotFailure(
	_ context.Context,
	_ tenancy.Scope,
	spoke hubfleet.Spoke,
	failure hubfleet.FailureKind,
	_ time.Time,
) (bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	_, retained := store.snapshots[spoke.ID]
	store.failures[spoke.ID] = failure
	return retained, nil
}

func e2eReaderScope(t *testing.T) tenancy.Scope {
	t.Helper()
	principal, err := tenancy.NewPrincipal("user:e2e", map[tenancy.WorkspaceID]tenancy.Role{"workspace-e2e": tenancy.RoleReader})
	if err != nil {
		t.Fatal(err)
	}
	scope, err := principal.Scope("workspace-e2e")
	if err != nil {
		t.Fatal(err)
	}
	return scope
}
