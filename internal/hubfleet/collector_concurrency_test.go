// SPDX-License-Identifier: Apache-2.0

package hubfleet

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ArdurAI/sith/internal/fleet"
	"github.com/ArdurAI/sith/internal/tenancy"
)

func TestCollectorBoundsParallelSpokesAndSortsCoverage(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 16, 12, 0, 0, 0, time.UTC)
	store := newMemoryStore(testSpokes("spoke-f", "spoke-a", "spoke-e", "spoke-b", "spoke-d", "spoke-c"))
	release := make(chan struct{})
	started := make(chan string, len(store.spokes))
	var active atomic.Int64
	var maximum atomic.Int64
	collector, err := NewCollector(CollectorConfig{
		Store: store,
		PEP:   testReadPEP(t),
		Transport: transportFunc(func(_ context.Context, _ tenancy.WorkspaceID, spoke Spoke) (Snapshot, error) {
			current := active.Add(1)
			defer active.Add(-1)
			for previous := maximum.Load(); current > previous && !maximum.CompareAndSwap(previous, current); previous = maximum.Load() {
			}
			started <- spoke.ID
			<-release
			if spoke.ID == "spoke-b" || spoke.ID == "spoke-d" || spoke.ID == "spoke-f" {
				return Snapshot{}, errors.New("unavailable")
			}
			return validSnapshot(spoke.ID, now), nil
		}),
		MaxConcurrentSpokes: 2,
		Now:                 func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}

	result := make(chan collectorResult, 1)
	scope := readerScope(t, "workspace-a")
	go func() {
		coverage, collectErr := collector.Collect(context.Background(), scope)
		result <- collectorResult{coverage: coverage, err: collectErr}
	}()
	for range 2 {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("worker did not start")
		}
	}
	select {
	case spokeID := <-started:
		t.Fatalf("worker bound exceeded before release by %q", spokeID)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)

	completed := <-result
	if completed.err != nil {
		t.Fatalf("Collect() error = %v", completed.err)
	}
	if maximum.Load() != 2 {
		t.Fatalf("maximum concurrency = %d, want 2", maximum.Load())
	}
	if completed.coverage.Requested != 6 || completed.coverage.Reachable != 3 ||
		!slices.Equal(completed.coverage.Unreachable, []string{"spoke-b", "spoke-d", "spoke-f"}) {
		t.Fatalf("coverage = %#v", completed.coverage)
	}
}

func TestCollectorCollapsesSerialTimeoutsIntoParallelWave(t *testing.T) {
	t.Parallel()

	store := newMemoryStore(testSpokes("spoke-a", "spoke-b", "spoke-c", "spoke-d"))
	collector, err := NewCollector(CollectorConfig{
		Store: store,
		PEP:   testReadPEP(t),
		Transport: transportFunc(func(ctx context.Context, _ tenancy.WorkspaceID, _ Spoke) (Snapshot, error) {
			<-ctx.Done()
			return Snapshot{}, ctx.Err()
		}),
		SpokeTimeout:        time.Second,
		MaxConcurrentSpokes: 4,
		Now:                 func() time.Time { return time.Date(2026, time.July, 16, 12, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}

	startedAt := time.Now()
	coverage, err := collector.Collect(context.Background(), readerScope(t, "workspace-a"))
	elapsed := time.Since(startedAt)
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if elapsed >= 2500*time.Millisecond {
		t.Fatalf("four one-second timeouts completed in %s, want one bounded parallel wave", elapsed)
	}
	if coverage.Reachable != 0 || len(coverage.Unreachable) != 4 {
		t.Fatalf("coverage = %#v", coverage)
	}
}

func TestCollectorPersistsHealthyPeerWhileAnotherWorkerIsBlocked(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 16, 12, 0, 0, 0, time.UTC)
	store := newMemoryStore(testSpokes("spoke-blocked", "spoke-healthy"))
	releaseBlocked := make(chan struct{})
	blockedStarted := make(chan struct{})
	collector, err := NewCollector(CollectorConfig{
		Store: store,
		PEP:   testReadPEP(t),
		Transport: transportFunc(func(_ context.Context, _ tenancy.WorkspaceID, spoke Spoke) (Snapshot, error) {
			if spoke.ID == "spoke-blocked" {
				close(blockedStarted)
				<-releaseBlocked
			}
			return validSnapshot(spoke.ID, now), nil
		}),
		MaxConcurrentSpokes: 2,
		Now:                 func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}

	result := make(chan error, 1)
	scope := readerScope(t, "workspace-a")
	go func() {
		_, collectErr := collector.Collect(context.Background(), scope)
		result <- collectErr
	}()
	select {
	case <-blockedStarted:
	case <-time.After(time.Second):
		t.Fatal("blocked spoke did not start")
	}
	if !waitForStoredSnapshot(store, "spoke-healthy", time.Second) {
		t.Fatal("healthy peer was not persisted while another worker was blocked")
	}
	close(releaseBlocked)
	if err := <-result; err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
}

func TestCollectWorkspaceCancellationStopsAdmissionAndWorkers(t *testing.T) {
	t.Parallel()

	store := newMemoryStore(testSpokes("spoke-a", "spoke-b", "spoke-c", "spoke-d", "spoke-e"))
	started := make(chan string, len(store.spokes))
	var active atomic.Int64
	observer := &recordingSnapshotObserver{}
	collector, err := NewCollector(CollectorConfig{
		Store:    store,
		PEP:      testReadPEP(t),
		Observer: observer,
		Transport: transportFunc(func(ctx context.Context, _ tenancy.WorkspaceID, spoke Spoke) (Snapshot, error) {
			active.Add(1)
			defer active.Add(-1)
			started <- spoke.ID
			<-ctx.Done()
			return Snapshot{}, ctx.Err()
		}),
		MaxConcurrentSpokes: 2,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan collectorResult, 1)
	scope := readerScope(t, "workspace-a")
	go func() {
		coverage, collectErr := collector.collectWorkspace(ctx, scope)
		result <- collectorResult{coverage: coverage, err: collectErr}
	}()
	for range 2 {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("worker did not start")
		}
	}
	cancel()
	completed := <-result
	if !errors.Is(completed.err, context.Canceled) {
		t.Fatalf("collectWorkspace() error = %v, want cancellation", completed.err)
	}
	if completed.coverage.Requested != len(store.spokes) || completed.coverage.Reachable != 0 {
		t.Fatalf("coverage = %#v", completed.coverage)
	}
	if active.Load() != 0 {
		t.Fatalf("active workers after return = %d", active.Load())
	}
	if len(observer.events) != 2 {
		t.Fatalf("cancellation observations = %#v, want two admitted spokes", observer.events)
	}
	for _, event := range observer.events {
		if event.outcome != SnapshotOutcomeCanceled || event.duration < 0 {
			t.Fatalf("cancellation observation = %#v", event)
		}
	}
	select {
	case spokeID := <-started:
		t.Fatalf("spoke %q was admitted after cancellation", spokeID)
	default:
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.snapshots) != 0 || len(store.failures) != 0 {
		t.Fatalf("store mutated after cancellation: snapshots=%v failures=%v", store.snapshots, store.failures)
	}
}

func TestCollectorStoreFailureCancelsWorkersAndLaterAdmission(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 16, 12, 0, 0, 0, time.UTC)
	baseStore := newMemoryStore(testSpokes("spoke-a", "spoke-b", "spoke-c", "spoke-d"))
	store := &failingReplaceStore{memoryStore: baseStore, failSpoke: "spoke-a"}
	started := make(chan string, len(baseStore.spokes))
	secondWorkerStarted := make(chan struct{})
	blockedCanceled := make(chan struct{})
	collector, err := NewCollector(CollectorConfig{
		Store: store,
		PEP:   testReadPEP(t),
		Transport: transportFunc(func(ctx context.Context, _ tenancy.WorkspaceID, spoke Spoke) (Snapshot, error) {
			started <- spoke.ID
			if spoke.ID == "spoke-a" {
				<-secondWorkerStarted
			}
			if spoke.ID == "spoke-b" {
				close(secondWorkerStarted)
				<-ctx.Done()
				close(blockedCanceled)
				return Snapshot{}, ctx.Err()
			}
			return validSnapshot(spoke.ID, now), nil
		}),
		MaxConcurrentSpokes: 2,
		Now:                 func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = collector.Collect(context.Background(), readerScope(t, "workspace-a"))
	if err == nil || !strings.Contains(err.Error(), `persist "spoke-a"`) {
		t.Fatalf("Collect() error = %v, want fail-closed store error", err)
	}
	select {
	case <-blockedCanceled:
	case <-time.After(time.Second):
		t.Fatal("blocked worker was not canceled before return")
	}
	var admitted []string
	for {
		select {
		case spokeID := <-started:
			admitted = append(admitted, spokeID)
		default:
			if slices.Contains(admitted, "spoke-c") || slices.Contains(admitted, "spoke-d") {
				t.Fatalf("later spokes admitted after store failure: %v", admitted)
			}
			return
		}
	}
}

func TestCollectorRecoversPanickingTransportWorkerAndLaterRefresh(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 16, 12, 0, 0, 0, time.UTC)
	store := newMemoryStore(testSpokes("spoke-a", "spoke-b", "spoke-c"))
	secondWorkerStarted := make(chan struct{})
	blockedCanceled := make(chan struct{})
	var panicFlight atomic.Bool
	panicFlight.Store(true)
	collector, err := NewCollector(CollectorConfig{
		Store: store,
		PEP:   testReadPEP(t),
		Transport: transportFunc(func(ctx context.Context, _ tenancy.WorkspaceID, spoke Spoke) (Snapshot, error) {
			if panicFlight.Load() {
				switch spoke.ID {
				case "spoke-a":
					<-secondWorkerStarted
					panicFlight.Store(false)
					panic("untrusted transport panic")
				case "spoke-b":
					close(secondWorkerStarted)
					<-ctx.Done()
					close(blockedCanceled)
					return Snapshot{}, ctx.Err()
				}
			}
			return validSnapshot(spoke.ID, now), nil
		}),
		MaxConcurrentSpokes: 2,
		Now:                 func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := collector.Collect(context.Background(), readerScope(t, "workspace-a")); !errors.Is(err, errRefreshFlightPanicked) {
		t.Fatalf("panicking Collect() error = %v", err)
	}
	select {
	case <-blockedCanceled:
	case <-time.After(time.Second):
		t.Fatal("peer worker was not canceled after transport panic")
	}
	coverage, err := collector.Collect(context.Background(), readerScope(t, "workspace-a"))
	if err != nil || coverage.Reachable != len(store.spokes) {
		t.Fatalf("later Collect() coverage/error = %#v/%v", coverage, err)
	}
}

func TestCollectorJoinsWorkersWhenStorePanicsAndLaterRefresh(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 16, 12, 0, 0, 0, time.UTC)
	store := &panickingReplaceStore{memoryStore: newMemoryStore(testSpokes("spoke-a", "spoke-b", "spoke-c"))}
	store.panicNext.Store(true)
	secondWorkerStarted := make(chan struct{})
	blockedCanceled := make(chan struct{})
	collector, err := NewCollector(CollectorConfig{
		Store: store,
		PEP:   testReadPEP(t),
		Transport: transportFunc(func(ctx context.Context, _ tenancy.WorkspaceID, spoke Spoke) (Snapshot, error) {
			if store.panicNext.Load() {
				switch spoke.ID {
				case "spoke-a":
					<-secondWorkerStarted
				case "spoke-b":
					close(secondWorkerStarted)
					<-ctx.Done()
					close(blockedCanceled)
					return Snapshot{}, ctx.Err()
				}
			}
			return validSnapshot(spoke.ID, now), nil
		}),
		MaxConcurrentSpokes: 2,
		Now:                 func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := collector.Collect(context.Background(), readerScope(t, "workspace-a")); !errors.Is(err, errRefreshFlightPanicked) {
		t.Fatalf("panicking store Collect() error = %v", err)
	}
	select {
	case <-blockedCanceled:
	case <-time.After(time.Second):
		t.Fatal("peer worker was not joined after store panic")
	}
	coverage, err := collector.Collect(context.Background(), readerScope(t, "workspace-a"))
	if err != nil || coverage.Reachable != len(store.spokes) {
		t.Fatalf("later Collect() coverage/error = %#v/%v", coverage, err)
	}
}

type collectorResult struct {
	coverage fleet.Coverage
	err      error
}

type failingReplaceStore struct {
	*memoryStore
	failSpoke string
}

type panickingReplaceStore struct {
	*memoryStore
	panicNext atomic.Bool
}

func (store *panickingReplaceStore) ReplaceSnapshot(
	ctx context.Context,
	scope tenancy.Scope,
	spoke Spoke,
	snapshot Snapshot,
	attemptedAt time.Time,
) error {
	if store.panicNext.CompareAndSwap(true, false) {
		panic("store panic")
	}
	return store.memoryStore.ReplaceSnapshot(ctx, scope, spoke, snapshot, attemptedAt)
}

func (store *failingReplaceStore) ReplaceSnapshot(
	ctx context.Context,
	scope tenancy.Scope,
	spoke Spoke,
	snapshot Snapshot,
	attemptedAt time.Time,
) error {
	if spoke.ID == store.failSpoke {
		return errors.New("forced store failure")
	}
	return store.memoryStore.ReplaceSnapshot(ctx, scope, spoke, snapshot, attemptedAt)
}

func newMemoryStore(spokes []Spoke) *memoryStore {
	return &memoryStore{spokes: spokes, snapshots: make(map[string]Snapshot), failures: make(map[string]FailureKind)}
}

func testSpokes(ids ...string) []Spoke {
	spokes := make([]Spoke, 0, len(ids))
	for _, id := range ids {
		spokes = append(spokes, Spoke{ID: id, ManagedClusterRef: "ocm/" + id})
	}
	return spokes
}

func waitForStoredSnapshot(store *memoryStore, spokeID string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		store.mu.Lock()
		_, found := store.snapshots[spokeID]
		store.mu.Unlock()
		if found {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return false
}

var _ Store = (*failingReplaceStore)(nil)
var _ Store = (*panickingReplaceStore)(nil)
