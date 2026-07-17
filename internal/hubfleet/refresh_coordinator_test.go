// SPDX-License-Identifier: Apache-2.0

package hubfleet

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ArdurAI/sith/internal/fleet"
	"github.com/ArdurAI/sith/internal/pep"
	"github.com/ArdurAI/sith/internal/tenancy"
)

func TestCollectorCoalescesAuthorizedRequestsForOneWorkspace(t *testing.T) {
	t.Parallel()

	const callers = 24
	now := time.Date(2026, time.July, 16, 20, 0, 0, 0, time.UTC)
	started := make(chan struct{})
	release := make(chan struct{})
	store := &memoryStore{
		spokes:    []Spoke{{ID: "spoke-a", ManagedClusterRef: "ocm/spoke-a"}},
		snapshots: make(map[string]Snapshot),
		failures:  make(map[string]FailureKind),
	}
	var authorizations atomic.Int32
	enforcer, err := pep.NewEnforcer(pep.Config{
		Hook: pep.HookFunc(func(context.Context, pep.Request) (pep.Decision, error) {
			authorizations.Add(1)
			return pep.Decision{Verdict: pep.VerdictAllow, ReasonCode: "test-allow"}, nil
		}),
		Auditor: pep.AuditFunc(func(context.Context, pep.AuditEvent) error { return nil }),
	})
	if err != nil {
		t.Fatal(err)
	}
	collector, err := NewCollector(CollectorConfig{
		Store: store,
		PEP:   enforcer,
		Transport: transportFunc(func(context.Context, tenancy.WorkspaceID, Spoke) (Snapshot, error) {
			select {
			case <-started:
			default:
				close(started)
			}
			<-release
			return validSnapshot("spoke-a", now), nil
		}),
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}

	results := make(chan fleet.Coverage, callers)
	errors := make(chan error, callers)
	contexts := make([]*observedDoneContext, callers)
	var waiters sync.WaitGroup
	for index := range callers {
		contexts[index] = newObservedDoneContext(context.Background())
		waiters.Add(1)
		go func(ctx context.Context) {
			defer waiters.Done()
			coverage, collectErr := collector.Collect(ctx, readerScope(t, "workspace-a"))
			results <- coverage
			errors <- collectErr
		}(contexts[index])
	}

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("workspace refresh did not start")
	}
	eventually(t, func() bool { return authorizations.Load() == callers })
	for _, ctx := range contexts {
		<-ctx.observed
	}
	if got := collector.refreshes.activeFlights(); got != 1 {
		t.Fatalf("active refresh flights = %d, want 1", got)
	}
	close(release)
	waiters.Wait()
	close(results)
	close(errors)

	for collectErr := range errors {
		if collectErr != nil {
			t.Fatalf("Collect() error = %v", collectErr)
		}
	}
	for coverage := range results {
		if coverage.Requested != 1 || coverage.Reachable != 1 {
			t.Fatalf("shared coverage = %#v", coverage)
		}
	}
	store.mu.Lock()
	registeredCalls := store.registeredCalls
	store.mu.Unlock()
	if registeredCalls != 1 {
		t.Fatalf("RegisteredSpokes() calls = %d, want 1", registeredCalls)
	}
	if got := collector.refreshes.activeFlights(); got != 0 {
		t.Fatalf("completed refresh flights = %d, want 0", got)
	}
}

func TestCollectorRefusesCallerBeforeJoiningWorkspaceFlight(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 16, 20, 0, 0, 0, time.UTC)
	started := make(chan struct{})
	release := make(chan struct{})
	store := &memoryStore{
		spokes:    []Spoke{{ID: "spoke-a", ManagedClusterRef: "ocm/spoke-a"}},
		snapshots: make(map[string]Snapshot),
		failures:  make(map[string]FailureKind),
	}
	enforcer, err := pep.NewEnforcer(pep.Config{
		Hook: pep.HookFunc(func(_ context.Context, request pep.Request) (pep.Decision, error) {
			if request.Actor == "user:denied" {
				return pep.Decision{Verdict: pep.VerdictDeny, ReasonCode: "test-deny"}, nil
			}
			return pep.Decision{Verdict: pep.VerdictAllow, ReasonCode: "test-allow"}, nil
		}),
		Auditor: pep.AuditFunc(func(context.Context, pep.AuditEvent) error { return nil }),
	})
	if err != nil {
		t.Fatal(err)
	}
	collector, err := NewCollector(CollectorConfig{
		Store: store,
		PEP:   enforcer,
		Transport: transportFunc(func(context.Context, tenancy.WorkspaceID, Spoke) (Snapshot, error) {
			close(started)
			<-release
			return validSnapshot("spoke-a", now), nil
		}),
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}

	allowedResult := make(chan error, 1)
	go func() {
		_, collectErr := collector.Collect(context.Background(), readerScopeFor(t, "user:allowed", "workspace-a"))
		allowedResult <- collectErr
	}()
	<-started
	deniedContext := newObservedDoneContext(context.Background())
	if _, err := collector.Collect(deniedContext, readerScopeFor(t, "user:denied", "workspace-a")); err == nil {
		t.Fatal("denied refresh unexpectedly joined the active workspace flight")
	}
	select {
	case <-deniedContext.observed:
		t.Fatal("denied refresh reached the workspace coordinator")
	default:
	}
	close(release)
	if err := <-allowedResult; err != nil {
		t.Fatalf("authorized refresh error = %v", err)
	}
}

func TestRefreshCoordinatorKeepsWorkspacesIndependent(t *testing.T) {
	t.Parallel()

	coordinator := newRefreshCoordinator()
	releaseA := make(chan struct{})
	startedA := make(chan struct{})
	startedB := make(chan struct{})
	collect := func(_ context.Context, scope tenancy.Scope) (fleet.Coverage, error) {
		switch scope.WorkspaceID() {
		case "workspace-a":
			close(startedA)
			<-releaseA
		case "workspace-b":
			close(startedB)
		default:
			t.Fatalf("unexpected workspace %q", scope.WorkspaceID())
		}
		return fleet.Coverage{Requested: 1, Reachable: 1}, nil
	}

	resultA := make(chan error, 1)
	go func() {
		_, err := coordinator.collect(context.Background(), readerScope(t, "workspace-a"), collect)
		resultA <- err
	}()
	<-startedA
	resultB := make(chan error, 1)
	go func() {
		_, err := coordinator.collect(context.Background(), readerScope(t, "workspace-b"), collect)
		resultB <- err
	}()
	select {
	case <-startedB:
	case <-time.After(5 * time.Second):
		t.Fatal("workspace-b was serialized behind workspace-a")
	}
	if err := <-resultB; err != nil {
		t.Fatalf("workspace-b collect error = %v", err)
	}
	close(releaseA)
	if err := <-resultA; err != nil {
		t.Fatalf("workspace-a collect error = %v", err)
	}
}

func TestRefreshCoordinatorDetachesLeaderAndWaiterCancellation(t *testing.T) {
	t.Parallel()

	coordinator := newRefreshCoordinator()
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	collect := func(ctx context.Context, _ tenancy.Scope) (fleet.Coverage, error) {
		calls.Add(1)
		if ctx.Err() != nil {
			t.Fatalf("detached refresh context error = %v", ctx.Err())
		}
		close(started)
		<-release
		return fleet.Coverage{Requested: 2, Reachable: 2}, nil
	}

	leaderContext, cancelLeader := context.WithCancel(context.Background())
	leaderResult := make(chan error, 1)
	go func() {
		_, err := coordinator.collect(leaderContext, readerScope(t, "workspace-a"), collect)
		leaderResult <- err
	}()
	<-started
	cancelLeader()
	if err := <-leaderResult; !errors.Is(err, context.Canceled) {
		t.Fatalf("leader cancellation error = %v, want context canceled", err)
	}

	waiterContext, cancelWaiter := context.WithCancel(context.Background())
	observedWaiterContext := newObservedDoneContext(waiterContext)
	waiterResult := make(chan error, 1)
	go func() {
		_, err := coordinator.collect(observedWaiterContext, readerScope(t, "workspace-a"), collect)
		waiterResult <- err
	}()
	<-observedWaiterContext.observed
	cancelWaiter()
	if err := <-waiterResult; !errors.Is(err, context.Canceled) {
		t.Fatalf("waiter cancellation error = %v, want context canceled", err)
	}

	survivorResult := make(chan struct {
		coverage fleet.Coverage
		err      error
	}, 1)
	survivorContext := newObservedDoneContext(context.Background())
	go func() {
		coverage, err := coordinator.collect(survivorContext, readerScope(t, "workspace-a"), collect)
		survivorResult <- struct {
			coverage fleet.Coverage
			err      error
		}{coverage: coverage, err: err}
	}()
	<-survivorContext.observed
	close(release)
	survivor := <-survivorResult
	if survivor.err != nil || survivor.coverage.Reachable != 2 {
		t.Fatalf("surviving waiter result = %#v, error = %v", survivor.coverage, survivor.err)
	}
	if calls.Load() != 1 || coordinator.activeFlights() != 0 {
		t.Fatalf("refresh calls/flights = %d/%d, want 1/0", calls.Load(), coordinator.activeFlights())
	}
}

func TestRefreshCoordinatorSharesFailuresAndRecoversAfterPanic(t *testing.T) {
	t.Parallel()

	coordinator := newRefreshCoordinator()
	sharedFailure := errors.New("closed store failure")
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	collectFailure := func(context.Context, tenancy.Scope) (fleet.Coverage, error) {
		calls.Add(1)
		close(started)
		<-release
		return fleet.Coverage{Requested: 1, Unreachable: []string{"spoke-a"}}, sharedFailure
	}

	type refreshResult struct {
		coverage fleet.Coverage
		err      error
	}
	results := make(chan refreshResult, 2)
	go func() {
		coverage, err := coordinator.collect(context.Background(), readerScope(t, "workspace-a"), collectFailure)
		results <- refreshResult{coverage: coverage, err: err}
	}()
	<-started
	secondContext := newObservedDoneContext(context.Background())
	go func() {
		coverage, err := coordinator.collect(secondContext, readerScope(t, "workspace-a"), collectFailure)
		results <- refreshResult{coverage: coverage, err: err}
	}()
	<-secondContext.observed
	close(release)
	first := <-results
	second := <-results
	for _, result := range []refreshResult{first, second} {
		if !errors.Is(result.err, sharedFailure) {
			t.Fatalf("shared error = %v, want closed failure", result.err)
		}
	}
	first.coverage.Unreachable[0] = "mutated-by-caller"
	if second.coverage.Unreachable[0] != "spoke-a" {
		t.Fatalf("shared coverage aliases caller memory: %#v", second.coverage)
	}
	if calls.Load() != 1 || coordinator.activeFlights() != 0 {
		t.Fatalf("failure calls/flights = %d/%d, want 1/0", calls.Load(), coordinator.activeFlights())
	}

	if _, err := coordinator.collect(context.Background(), readerScope(t, "workspace-a"), func(context.Context, tenancy.Scope) (fleet.Coverage, error) {
		panic("credential-bearing panic must not escape")
	}); !errors.Is(err, errRefreshFlightPanicked) {
		t.Fatalf("panic result = %v, want closed panic error", err)
	}
	if coordinator.activeFlights() != 0 {
		t.Fatalf("panic retained %d refresh flights", coordinator.activeFlights())
	}

	coverage, err := coordinator.collect(context.Background(), readerScope(t, "workspace-a"), func(context.Context, tenancy.Scope) (fleet.Coverage, error) {
		return fleet.Coverage{Requested: 1, Reachable: 1}, nil
	})
	if err != nil || coverage.Reachable != 1 {
		t.Fatalf("post-panic refresh = %#v, error = %v", coverage, err)
	}
}

func eventually(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("condition did not become true")
		}
		time.Sleep(time.Millisecond)
	}
}

func readerScopeFor(t *testing.T, subject string, workspaceID tenancy.WorkspaceID) tenancy.Scope {
	t.Helper()
	principal, err := tenancy.NewPrincipal(subject, map[tenancy.WorkspaceID]tenancy.Role{workspaceID: tenancy.RoleReader})
	if err != nil {
		t.Fatal(err)
	}
	scope, err := principal.Scope(workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	return scope
}

type observedDoneContext struct {
	context.Context
	once     sync.Once
	observed chan struct{}
}

func newObservedDoneContext(ctx context.Context) *observedDoneContext {
	return &observedDoneContext{Context: ctx, observed: make(chan struct{})}
}

func (ctx *observedDoneContext) Done() <-chan struct{} {
	ctx.once.Do(func() { close(ctx.observed) })
	return ctx.Context.Done()
}
