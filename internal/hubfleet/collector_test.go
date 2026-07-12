// SPDX-License-Identifier: Apache-2.0

package hubfleet

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/ArdurAI/sith/internal/fleet"
	"github.com/ArdurAI/sith/internal/pep"
	"github.com/ArdurAI/sith/internal/tenancy"
)

func TestCollectorIsolatesSpokeFailuresAndRetainsStaleSnapshots(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 12, 15, 0, 0, 0, time.UTC)
	store := &memoryStore{
		spokes: []Spoke{
			{ID: "spoke-beta", ManagedClusterRef: "ocm/spoke-beta"},
			{ID: "spoke-alpha", ManagedClusterRef: "ocm/spoke-alpha"},
		},
		snapshots: map[string]Snapshot{"spoke-beta": validSnapshot("spoke-beta", now.Add(-time.Minute))},
		failures:  make(map[string]FailureKind),
	}
	collector, err := NewCollector(CollectorConfig{
		Store: store,
		PEP:   testReadPEP(t),
		Transport: transportFunc(func(_ context.Context, workspaceID tenancy.WorkspaceID, spoke Spoke) (Snapshot, error) {
			if workspaceID != "workspace-a" {
				return Snapshot{}, errors.New("wrong workspace")
			}
			if spoke.ID == "spoke-beta" {
				return Snapshot{}, errors.New("proxy unavailable")
			}
			return validSnapshot(spoke.ID, now), nil
		}),
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}

	coverage, err := collector.Collect(context.Background(), readerScope(t, "workspace-a"))
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if coverage.Requested != 2 || coverage.Reachable != 1 || !slices.Equal(coverage.Unreachable, []string{"spoke-beta"}) ||
		!slices.Equal(coverage.Stale, []string{"spoke-beta"}) {
		t.Fatalf("coverage = %#v", coverage)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.failures["spoke-beta"] != FailureTransport {
		t.Fatalf("failure = %q, want transport", store.failures["spoke-beta"])
	}
	if _, exists := store.snapshots["spoke-alpha"]; !exists {
		t.Fatal("healthy spoke snapshot was not persisted")
	}
	if got := store.snapshots["spoke-beta"].ObservedAt; !got.Equal(now.Add(-time.Minute)) {
		t.Fatalf("stale snapshot observation = %s, want retained prior observation", got)
	}
}

func TestCollectorBoundsEveryTransportCall(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 12, 15, 0, 0, 0, time.UTC)
	store := &memoryStore{
		spokes:    []Spoke{{ID: "spoke-a", ManagedClusterRef: "ocm/spoke-a"}},
		snapshots: make(map[string]Snapshot),
		failures:  make(map[string]FailureKind),
	}
	collector, err := NewCollector(CollectorConfig{
		Store: store,
		PEP:   testReadPEP(t),
		Transport: transportFunc(func(ctx context.Context, _ tenancy.WorkspaceID, _ Spoke) (Snapshot, error) {
			deadline, exists := ctx.Deadline()
			if !exists || time.Until(deadline) > 1100*time.Millisecond {
				return Snapshot{}, errors.New("missing bounded deadline")
			}
			return Snapshot{}, context.DeadlineExceeded
		}),
		SpokeTimeout: time.Second,
		Now:          func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}

	coverage, err := collector.Collect(context.Background(), readerScope(t, "workspace-a"))
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if coverage.Reachable != 0 || !slices.Equal(coverage.Unreachable, []string{"spoke-a"}) || len(coverage.Stale) != 0 {
		t.Fatalf("coverage = %#v", coverage)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.failures["spoke-a"] != FailureDeadline {
		t.Fatalf("failure = %q, want deadline", store.failures["spoke-a"])
	}
}

func TestValidateSnapshotRejectsUnsafeOrAmbiguousEvidence(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 12, 15, 0, 0, 0, time.UTC)
	spoke := Spoke{ID: "spoke-a", ManagedClusterRef: "ocm/spoke-a"}
	duplicate := validSnapshot("spoke-a", now)
	duplicate.Facts = append(duplicate.Facts, duplicate.Facts[0])
	tests := []struct {
		name     string
		snapshot Snapshot
	}{
		{name: "unsupported kind", snapshot: snapshotWith(func(fact *fleet.Evidence) { fact.Kind = fleet.FactAlert }, now)},
		{name: "cross spoke source", snapshot: snapshotWith(func(fact *fleet.Evidence) { fact.Source = "spoke-b" }, now)},
		{name: "endpoint payload", snapshot: snapshotWith(func(fact *fleet.Evidence) { fact.Observed = json.RawMessage(`{"endpoint":"https://private.example"}`) }, now)},
		{name: "camel token payload", snapshot: snapshotWith(func(fact *fleet.Evidence) { fact.Observed = json.RawMessage(`{"accessToken":"secret"}`) }, now)},
		{name: "non-normalized payload", snapshot: snapshotWith(func(fact *fleet.Evidence) { fact.Observed = json.RawMessage(`{"raw_object":"rejected"}`) }, now)},
		{name: "duplicate payload key", snapshot: snapshotWith(func(fact *fleet.Evidence) {
			fact.Observed = json.RawMessage(`{"status":"Healthy","status":"Degraded"}`)
		}, now)},
		{name: "secret inventory", snapshot: snapshotWith(func(fact *fleet.Evidence) { fact.Ref.Kind = "Secret" }, now)},
		{name: "deep link", snapshot: snapshotWith(func(fact *fleet.Evidence) { fact.Provenance.DeepLink = "https://private.example" }, now)},
		{name: "untyped display", snapshot: snapshotWith(func(fact *fleet.Evidence) { fact.Display = []fleet.DisplayField{{Name: "Status", Value: "Healthy"}} }, now)},
		{name: "raw native identifier", snapshot: snapshotWith(func(fact *fleet.Evidence) { fact.Provenance.NativeID = "opaque-source-value" }, now)},
		{name: "duplicate normalized fact", snapshot: duplicate},
		{name: "old observation", snapshot: validSnapshot("spoke-a", now.Add(-6*time.Minute))},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := ValidateSnapshot(spoke, test.snapshot, now); err == nil {
				t.Fatal("ValidateSnapshot() unexpectedly accepted unsafe snapshot")
			}
		})
	}
}

func TestCollectorCopiesTransportOwnedSnapshot(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 12, 15, 0, 0, 0, time.UTC)
	transportSnapshot := validSnapshot("spoke-a", now)
	store := &memoryStore{
		spokes:    []Spoke{{ID: "spoke-a", ManagedClusterRef: "ocm/spoke-a"}},
		snapshots: make(map[string]Snapshot),
		failures:  make(map[string]FailureKind),
	}
	collector, err := NewCollector(CollectorConfig{
		Store: store,
		PEP:   testReadPEP(t),
		Transport: transportFunc(func(context.Context, tenancy.WorkspaceID, Spoke) (Snapshot, error) {
			return transportSnapshot, nil
		}),
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := collector.Collect(context.Background(), readerScope(t, "workspace-a")); err != nil {
		t.Fatal(err)
	}
	transportSnapshot.Facts[0].Observed[2] = 'X'
	store.mu.Lock()
	defer store.mu.Unlock()
	if string(store.snapshots["spoke-a"].Facts[0].Observed) != `{"status":"Healthy"}` {
		t.Fatalf("stored snapshot aliases transport memory: %s", store.snapshots["spoke-a"].Facts[0].Observed)
	}
}

func TestCollectorAndSourceRejectUnsafeConfiguration(t *testing.T) {
	t.Parallel()

	store := &memoryStore{snapshots: make(map[string]Snapshot), failures: make(map[string]FailureKind)}
	if _, err := NewCollector(CollectorConfig{Store: store, Transport: transportFunc(func(context.Context, tenancy.WorkspaceID, Spoke) (Snapshot, error) {
		return Snapshot{}, nil
	}), PEP: testReadPEP(t), SpokeTimeout: 999 * time.Millisecond}); err == nil {
		t.Fatal("sub-second collector timeout unexpectedly accepted")
	}
	if _, err := NewSource(SourceConfig{Reader: fleetReaderFunc(func(context.Context, tenancy.Scope, time.Duration, time.Time) (fleet.FleetResult, error) {
		return fleet.FleetResult{}, nil
	}), Scope: tenancy.Scope{}}); err == nil {
		t.Fatal("source without signed scope unexpectedly accepted")
	}
}

func TestSourceUsesCommonFleetModel(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 12, 15, 0, 0, 0, time.UTC)
	reader := &recordingFleetReader{result: fleet.FleetResult{Clusters: []fleet.Cluster{{Name: "spoke-a", SourceKind: SourceKind}}}}
	source, err := NewSource(SourceConfig{
		Reader: reader, Scope: readerScope(t, "workspace-a"), PEP: testReadPEP(t), Freshness: time.Minute, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := source.Fleet(context.Background())
	if err != nil || source.Kind() != SourceKind || len(result.Clusters) != 1 {
		t.Fatalf("Fleet() result = %#v, error = %v", result, err)
	}
	if reader.scope.WorkspaceID() != "workspace-a" || reader.freshness != time.Minute || !reader.now.Equal(now) {
		t.Fatalf("reader invocation = %#v, freshness = %s, now = %s", reader.scope, reader.freshness, reader.now)
	}
}

type transportFunc func(context.Context, tenancy.WorkspaceID, Spoke) (Snapshot, error)

func (function transportFunc) Snapshot(ctx context.Context, workspaceID tenancy.WorkspaceID, spoke Spoke) (Snapshot, error) {
	return function(ctx, workspaceID, spoke)
}

type memoryStore struct {
	mu              sync.Mutex
	spokes          []Spoke
	snapshots       map[string]Snapshot
	failures        map[string]FailureKind
	registeredCalls int
}

func (store *memoryStore) RegisteredSpokes(_ context.Context, _ tenancy.Scope) ([]Spoke, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.registeredCalls++
	return append([]Spoke(nil), store.spokes...), nil
}

func (store *memoryStore) ReplaceSnapshot(
	_ context.Context,
	_ tenancy.Scope,
	spoke Spoke,
	snapshot Snapshot,
	_ time.Time,
) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.snapshots[spoke.ID] = snapshot
	delete(store.failures, spoke.ID)
	return nil
}

func (store *memoryStore) MarkSnapshotFailure(
	_ context.Context,
	_ tenancy.Scope,
	spoke Spoke,
	failure FailureKind,
	_ time.Time,
) (bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	_, retained := store.snapshots[spoke.ID]
	store.failures[spoke.ID] = failure
	return retained, nil
}

type fleetReaderFunc func(context.Context, tenancy.Scope, time.Duration, time.Time) (fleet.FleetResult, error)

func (function fleetReaderFunc) ReadFleet(
	ctx context.Context,
	scope tenancy.Scope,
	freshness time.Duration,
	now time.Time,
) (fleet.FleetResult, error) {
	return function(ctx, scope, freshness, now)
}

type recordingFleetReader struct {
	result    fleet.FleetResult
	scope     tenancy.Scope
	freshness time.Duration
	now       time.Time
	calls     int
}

func (reader *recordingFleetReader) ReadFleet(
	_ context.Context,
	scope tenancy.Scope,
	freshness time.Duration,
	now time.Time,
) (fleet.FleetResult, error) {
	reader.calls++
	reader.scope = scope
	reader.freshness = freshness
	reader.now = now
	return reader.result, nil
}

func readerScope(t *testing.T, workspaceID tenancy.WorkspaceID) tenancy.Scope {
	t.Helper()
	principal, err := tenancy.NewPrincipal("user:reader", map[tenancy.WorkspaceID]tenancy.Role{workspaceID: tenancy.RoleReader})
	if err != nil {
		t.Fatal(err)
	}
	scope, err := principal.Scope(workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	return scope
}

func testReadPEP(t *testing.T) *pep.Enforcer {
	t.Helper()
	enforcer, err := pep.NewEnforcer(pep.Config{
		Hook: pep.AllowReadHook{}, Auditor: pep.AuditFunc(func(context.Context, pep.AuditEvent) error { return nil }),
	})
	if err != nil {
		t.Fatal(err)
	}
	return enforcer
}

func validSnapshot(spokeID string, observedAt time.Time) Snapshot {
	return Snapshot{ObservedAt: observedAt, Facts: []fleet.Evidence{
		{
			Ref:        fleet.ResourceRef{SourceKind: SourceKind, Scope: spokeID, Kind: "Deployment", Name: "payments"},
			Kind:       fleet.FactHealth,
			Observed:   json.RawMessage(`{"status":"Healthy"}`),
			ObservedAt: observedAt,
			Source:     spokeID,
			Provenance: fleet.Provenance{Adapter: SourceKind, ProtocolV: protocolVersion},
		},
	}}
}

func snapshotWith(change func(*fleet.Evidence), observedAt time.Time) Snapshot {
	snapshot := validSnapshot("spoke-a", observedAt)
	change(&snapshot.Facts[0])
	return snapshot
}
