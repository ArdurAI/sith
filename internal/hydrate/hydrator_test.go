// SPDX-License-Identifier: Apache-2.0

package hydrate

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/ArdurAI/sith/internal/connector"
	"github.com/ArdurAI/sith/internal/fleet"
	"github.com/ArdurAI/sith/internal/fleetcache"
)

func TestNewValidatesOptionsAndPreservesLensOrder(t *testing.T) {
	t.Parallel()
	reader := &fakeReader{}
	store := fleetcache.New()
	for _, test := range []struct {
		name   string
		reader connector.Reader
		store  *fleetcache.Store
		option Option
	}{
		{name: "nil reader", store: store},
		{name: "nil store", reader: reader},
		{name: "nil option", reader: reader, store: store, option: nil},
		{name: "empty kinds", reader: reader, store: store, option: WithKinds()},
		{name: "blank kind", reader: reader, store: store, option: WithKinds(" ")},
		{name: "zero concurrency", reader: reader, store: store, option: WithMaxConcurrency(0)},
		{name: "zero resync", reader: reader, store: store, option: WithResyncInterval(0)},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			options := []Option{}
			if test.option != nil || test.name == "nil option" {
				options = append(options, test.option)
			}
			if _, err := New(test.reader, test.store, options...); err == nil {
				t.Fatal("New() error = nil")
			}
		})
	}

	hydrator, err := New(reader, store, WithKinds("pods", "deploy", "pods", "events"))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if !slices.Equal(hydrator.Kinds(), []string{"Pod", "Deployment", "Event"}) {
		t.Fatalf("Kinds() = %v", hydrator.Kinds())
	}
}

func TestSyncOnceReconcilesKindsConcurrently(t *testing.T) {
	t.Parallel()
	reader := &fakeReader{delay: 20 * time.Millisecond}
	store := fleetcache.New()
	hydrator, err := New(reader, store, WithMaxConcurrency(2))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := hydrator.SyncOnce(context.Background()); err != nil {
		t.Fatalf("SyncOnce() error = %v", err)
	}
	if reader.maximumActive() != 2 {
		t.Fatalf("maximum active queries = %d, want 2", reader.maximumActive())
	}
	for _, kind := range TierOneKinds() {
		snapshot := store.Query(fleetcache.Query{Kind: kind})
		if len(snapshot.Records) != 2 || !snapshot.Coverage.Complete() {
			t.Errorf("%s snapshot = %#v, want two complete records", kind, snapshot)
		}
	}
}

func TestSyncOnceKeepsSuccessfulLensesOnPartialFailure(t *testing.T) {
	t.Parallel()
	reader := &fakeReader{failures: map[string]error{"Event": errors.New("events forbidden")}}
	store := fleetcache.New()
	hydrator, err := New(reader, store, WithKinds("Pod", "Event"))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	err = hydrator.SyncOnce(context.Background())
	if err == nil || !errors.Is(err, reader.failures["Event"]) {
		t.Fatalf("SyncOnce() error = %v, want event failure", err)
	}
	if snapshot := store.Query(fleetcache.Query{Kind: "Pod"}); len(snapshot.Records) != 2 {
		t.Fatalf("pod records = %#v, want successful lens retained", snapshot.Records)
	}
	if snapshot := store.Query(fleetcache.Query{Kind: "Event"}); snapshot.LastError == "" {
		t.Fatalf("event snapshot = %#v, want visible sync error", snapshot)
	}
}

func TestSyncKindsReconcilesGenericLens(t *testing.T) {
	t.Parallel()
	reader := &fakeReader{}
	store := fleetcache.New()
	hydrator, err := New(reader, store)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := hydrator.SyncKinds(context.Background(), "widgets", "widgets"); err != nil {
		t.Fatalf("SyncKinds() error = %v", err)
	}
	snapshot := store.Query(fleetcache.Query{Kind: "Widgets"})
	if len(snapshot.Records) != 2 || !snapshot.Coverage.Complete() {
		t.Fatalf("generic snapshot = %#v, want two complete records", snapshot)
	}
	if err := hydrator.SyncKinds(context.Background()); err == nil {
		t.Fatal("SyncKinds() error = nil, want empty-kind validation")
	}
}

func TestSyncOnceRejectsPauseAndDuplicateRun(t *testing.T) {
	t.Parallel()
	reader := &fakeReader{block: make(chan struct{}), started: make(chan struct{}, 1)}
	store := fleetcache.New()
	hydrator, err := New(reader, store, WithKinds("Pod"))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- hydrator.SyncOnce(context.Background())
	}()
	<-reader.started
	if err := hydrator.SyncOnce(context.Background()); !errors.Is(err, ErrSyncInProgress) {
		t.Fatalf("second SyncOnce() error = %v, want ErrSyncInProgress", err)
	}
	close(reader.block)
	if err := <-firstDone; err != nil {
		t.Fatalf("first SyncOnce() error = %v", err)
	}

	store.SetPaused(true)
	if err := hydrator.SyncOnce(context.Background()); !errors.Is(err, ErrPaused) {
		t.Fatalf("paused SyncOnce() error = %v, want ErrPaused", err)
	}
}

func TestRunAppliesWatchDeltasAndAddsGenericKinds(t *testing.T) {
	t.Parallel()
	reader := &watchingReader{
		fakeReader: &fakeReader{},
		calls:      make(chan []string, 4),
		events:     make(chan connector.WatchEvent, 4),
	}
	store := fleetcache.New()
	hydrator, err := New(reader, store, WithResyncInterval(time.Hour))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- hydrator.Run(ctx) }()
	if kinds := receiveKinds(t, reader.calls); !slices.Equal(kinds, TierOneKinds()) {
		t.Fatalf("initial watch kinds = %v", kinds)
	}

	now := time.Now().UTC()
	reader.events <- connector.WatchEvent{
		Type: connector.WatchUpsert, Kind: "Pod", Scope: "alpha",
		Fact: fakeFact("Pod", "alpha", now), ObservedAt: now,
	}
	waitForCondition(t, func() bool {
		return len(store.Query(fleetcache.Query{Kind: "Pod"}).Records) == 2
	})

	if err := hydrator.SyncKinds(ctx, "widgets"); err != nil {
		t.Fatalf("SyncKinds() error = %v", err)
	}
	if kinds := receiveKinds(t, reader.calls); !slices.Contains(kinds, "Widgets") {
		t.Fatalf("updated watch kinds = %v, want Widgets", kinds)
	}
	reader.events <- connector.WatchEvent{
		Type: connector.WatchError, Kind: "Pod", Scope: "beta", Err: errors.New("connection reset"),
	}
	waitForCondition(t, func() bool {
		return slices.Contains(store.Query(fleetcache.Query{Kind: "Pod"}).Coverage.Unreachable, "beta")
	})

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

type fakeReader struct {
	mu       sync.Mutex
	active   int
	max      int
	delay    time.Duration
	block    chan struct{}
	started  chan struct{}
	failures map[string]error
}

type watchingReader struct {
	*fakeReader
	calls  chan []string
	events chan connector.WatchEvent
}

func (reader *watchingReader) Watch(ctx context.Context, kinds ...string) (<-chan connector.WatchEvent, error) {
	reader.calls <- append([]string(nil), kinds...)
	output := make(chan connector.WatchEvent)
	go func() {
		defer close(output)
		for {
			select {
			case <-ctx.Done():
				return
			case event := <-reader.events:
				select {
				case output <- event:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return output, nil
}

func (*fakeReader) Kind() string { return "fake" }

func (*fakeReader) Capabilities() []connector.Capability {
	return []connector.Capability{connector.CapDiscover, connector.CapRead, connector.CapQuery}
}

func (reader *fakeReader) Descriptor() connector.Descriptor {
	return connector.Descriptor{
		Kind:         reader.Kind(),
		ConnKind:     connector.KindReadAdapter,
		ProtocolV:    "1.0.0",
		Owner:        "test",
		Capabilities: reader.Capabilities(),
	}
}

func (*fakeReader) Discover(_ context.Context) (connector.Discovery, error) {
	now := time.Now().UTC()
	return connector.Discovery{Scopes: []connector.Scope{
		{Name: "alpha", Reachable: true, ObservedAt: now},
		{Name: "beta", Reachable: true, ObservedAt: now},
	}}, nil
}

func (*fakeReader) Read(_ context.Context, _ fleet.ResourceRef) (fleet.Evidence, error) {
	return fleet.Evidence{}, errors.New("not used")
}

func (reader *fakeReader) Query(ctx context.Context, query fleet.Query) (fleet.QueryResult, error) {
	kind := query.Selector.ResourceKind
	reader.mu.Lock()
	reader.active++
	if reader.active > reader.max {
		reader.max = reader.active
	}
	delay := reader.delay
	block := reader.block
	started := reader.started
	failure := reader.failures[kind]
	reader.mu.Unlock()
	defer func() {
		reader.mu.Lock()
		reader.active--
		reader.mu.Unlock()
	}()
	if started != nil {
		select {
		case started <- struct{}{}:
		default:
		}
	}
	if delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return fleet.QueryResult{}, ctx.Err()
		case <-timer.C:
		}
	}
	if block != nil {
		select {
		case <-ctx.Done():
			return fleet.QueryResult{}, ctx.Err()
		case <-block:
		}
	}
	if failure != nil {
		return fleet.QueryResult{}, failure
	}
	now := time.Now().UTC()
	return fleet.QueryResult{
		Facts: []fleet.Fact{
			fakeFact(kind, "alpha", now),
			fakeFact(kind, "beta", now),
		},
		Coverage: fleet.Coverage{Requested: 2, Reachable: 2},
	}, nil
}

func (reader *fakeReader) maximumActive() int {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	return reader.max
}

func fakeFact(kind, scope string, observed time.Time) fleet.Fact {
	object := map[string]any{
		"apiVersion": "v1",
		"kind":       kind,
		"metadata":   map[string]any{"name": stringsLower(kind), "namespace": "apps"},
		"status":     map[string]any{"phase": "Running"},
	}
	payload, _ := json.Marshal(object)
	return fleet.Fact{Evidence: fleet.Evidence{
		Ref: fleet.ResourceRef{
			SourceKind: "fake",
			Scope:      scope,
			Kind:       kind,
			Namespace:  "apps",
			Name:       stringsLower(kind),
		},
		Kind:       fleet.FactInventory,
		Observed:   payload,
		ObservedAt: observed,
		Source:     scope,
	}, Workspace: fleet.LocalWorkspace}
}

func stringsLower(value string) string {
	result := make([]rune, 0, len(value))
	for _, character := range value {
		if character >= 'A' && character <= 'Z' {
			character += 'a' - 'A'
		}
		result = append(result, character)
	}
	return string(result)
}

func receiveKinds(t *testing.T, calls <-chan []string) []string {
	t.Helper()
	select {
	case kinds := <-calls:
		return kinds
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for watch call")
		return nil
	}
}

func waitForCondition(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition did not become true")
}
