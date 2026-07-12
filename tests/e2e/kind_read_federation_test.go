// SPDX-License-Identifier: Apache-2.0
//go:build e2e && kind

package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/ArdurAI/sith/internal/connector/kubeconfig"
	"github.com/ArdurAI/sith/internal/fleet"
	"github.com/ArdurAI/sith/internal/hubfleet"
	"github.com/ArdurAI/sith/internal/pep"
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
		PEP:       e2eReadPEP(t),
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
	correlator, err := hubfleet.NewCorrelator(hubfleet.CorrelatorConfig{Querier: store, PEP: e2eReadPEP(t), Freshness: time.Minute})
	if err != nil {
		store.mu.Unlock()
		t.Fatalf("construct kind correlator: %v", err)
	}
	store.mu.Unlock()
	correlated, err := correlator.Correlate(ctx, scope, hubfleet.CorrelationRequest{
		ResourceKind: "Pod", Name: "sith-worker-sample", Namespace: "default", HealthNot: "Healthy",
	})
	if err != nil || len(correlated.Facts) != 1 || correlated.Facts[0].Ref.Scope != "spoke-b" ||
		correlated.Facts[0].Stale || correlated.Coverage.Requested != 2 || correlated.Coverage.Reachable != 2 {
		t.Fatalf("real two-spoke correlation = %#v, error = %v", correlated, err)
	}
	store.mu.Lock()
	store.spokes[1].ManagedClusterRef = "kind-sith-e2e-unreachable"
	store.mu.Unlock()
	coverage, err = collector.Collect(ctx, scope)
	if err != nil || coverage.Reachable != 1 || len(coverage.Unreachable) != 1 || coverage.Unreachable[0] != "spoke-b" ||
		len(coverage.Stale) != 1 || coverage.Stale[0] != "spoke-b" {
		t.Fatalf("degraded real spoke collection = %#v, error = %v", coverage, err)
	}
	staleCorrelation, err := correlator.Correlate(ctx, scope, hubfleet.CorrelationRequest{
		ResourceKind: "Pod", Name: "sith-worker-sample", Namespace: "default", HealthNot: "Healthy",
	})
	if err != nil || len(staleCorrelation.Facts) != 1 || staleCorrelation.Facts[0].Ref.Scope != "spoke-b" ||
		!staleCorrelation.Facts[0].Stale || len(staleCorrelation.Coverage.Stale) != 1 ||
		staleCorrelation.Coverage.Stale[0] != "spoke-b" {
		t.Fatalf("stale real two-spoke correlation = %#v, error = %v", staleCorrelation, err)
	}
}

func e2eReadPEP(t *testing.T) *pep.Enforcer {
	t.Helper()
	enforcer, err := pep.NewEnforcer(pep.Config{
		Hook: pep.AllowReadHook{}, Auditor: pep.AuditFunc(func(context.Context, pep.AuditEvent) error { return nil }),
	})
	if err != nil {
		t.Fatal(err)
	}
	return enforcer
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
				Observed:   healthObservation(spoke.ID, fact.Ref.Name),
				ObservedAt: now,
				Source:     spoke.ID,
				Provenance: fleet.Provenance{Adapter: hubfleet.SourceKind, ProtocolV: "1.0.0"},
			},
		)
	}
	return hubfleet.Snapshot{ObservedAt: now, Facts: facts}, nil
}

func healthObservation(spokeID, resourceName string) json.RawMessage {
	if spokeID == "spoke-b" && resourceName == "sith-worker-sample" {
		return json.RawMessage(`{"status":"Degraded"}`)
	}
	return json.RawMessage(`{"status":"Healthy"}`)
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

func (store *kindSnapshotStore) QueryFleet(
	_ context.Context,
	scope tenancy.Scope,
	query fleet.Query,
	_ time.Duration,
	_ time.Time,
) (fleet.QueryResult, error) {
	if err := scope.Authorize(tenancy.ActionRead); err != nil {
		return fleet.QueryResult{}, err
	}
	if err := query.Validate(); err != nil {
		return fleet.QueryResult{}, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	result := fleet.QueryResult{Facts: []fleet.Fact{}, Coverage: fleet.Coverage{Requested: len(store.spokes)}}
	for _, spoke := range store.spokes {
		snapshot, hasSnapshot := store.snapshots[spoke.ID]
		failure := store.failures[spoke.ID]
		if !hasSnapshot || failure != "" {
			result.Coverage.Unreachable = append(result.Coverage.Unreachable, spoke.ID)
			if hasSnapshot {
				result.Coverage.Stale = append(result.Coverage.Stale, spoke.ID)
			}
		} else {
			result.Coverage.Reachable++
		}
		for _, evidence := range snapshot.Facts {
			if !matchesKindCorrelation(query, evidence) {
				continue
			}
			fact := fleet.Fact{Evidence: evidence, Workspace: string(scope.WorkspaceID())}
			if failure != "" {
				fact.Stale = true
				fact.StaleFor = "collection failed"
			}
			result.Facts = append(result.Facts, fact)
		}
	}
	sort.Strings(result.Coverage.Unreachable)
	sort.Strings(result.Coverage.Stale)
	return result, nil
}

func matchesKindCorrelation(query fleet.Query, evidence fleet.Evidence) bool {
	if len(query.Kinds) != 1 || query.Kinds[0] != evidence.Kind ||
		(query.Selector.ResourceKind != "" && query.Selector.ResourceKind != evidence.Ref.Kind) ||
		(query.Selector.Name != "" && query.Selector.Name != evidence.Ref.Name) ||
		(query.Selector.Namespace != "" && query.Selector.Namespace != evidence.Ref.Namespace) {
		return false
	}
	var observed struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(evidence.Observed, &observed); err != nil {
		return false
	}
	if query.Selector.Health != "" && observed.Status != query.Selector.Health {
		return false
	}
	return query.Selector.HealthNot == "" || observed.Status != query.Selector.HealthNot
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
