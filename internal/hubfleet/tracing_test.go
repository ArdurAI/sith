// SPDX-License-Identifier: Apache-2.0

package hubfleet

import (
	"context"
	"testing"
	"time"

	"github.com/ArdurAI/sith/internal/pep"
	"github.com/ArdurAI/sith/internal/tenancy"
	"github.com/ArdurAI/sith/internal/tracing"
)

func TestCollectorSeparatesCallerAuthorizationTraceFromRefreshFlight(t *testing.T) {
	now := time.Date(2026, time.July, 14, 13, 0, 0, 0, time.UTC)
	var audits []pep.AuditEvent
	var events []tracing.Event
	tracer := tracing.ObserverFunc(func(event tracing.Event) { events = append(events, event) })
	enforcer, err := pep.NewEnforcer(pep.Config{
		Hook: pep.AllowReadHook{},
		Auditor: pep.AuditFunc(func(_ context.Context, event pep.AuditEvent) error {
			audits = append(audits, event)
			return nil
		}),
		TraceObserver: tracer,
	})
	if err != nil {
		t.Fatal(err)
	}
	var transportTrace tracing.ID
	type callerValueKey struct{}
	var callerValueLeaked bool
	store := &memoryStore{
		spokes: []Spoke{{ID: "spoke-a", ManagedClusterRef: "ocm/spoke-a"}}, snapshots: make(map[string]Snapshot), failures: make(map[string]FailureKind),
	}
	collector, err := NewCollector(CollectorConfig{
		LifecycleContext: t.Context(),
		Store:            store,
		Transport: transportFunc(func(ctx context.Context, _ tenancy.WorkspaceID, spoke Spoke) (Snapshot, error) {
			callerValueLeaked = ctx.Value(callerValueKey{}) != nil
			var ok bool
			transportTrace, ok = tracing.FromContext(ctx)
			if !ok {
				t.Fatal("snapshot transport received no trace context")
			}
			return validSnapshot(spoke.ID, now), nil
		}),
		PEP: enforcer, TraceObserver: tracer, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	callerContext, callerTrace, err := tracing.Ensure(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	callerContext = context.WithValue(callerContext, callerValueKey{}, "request-only-value")
	if coverage, err := collector.Collect(callerContext, readerScope(t, "workspace-a")); err != nil || coverage.Reachable != 1 {
		t.Fatalf("Collect() coverage = %#v, error = %v", coverage, err)
	}
	if callerValueLeaked || !transportTrace.Valid() || transportTrace == callerTrace || len(audits) != 1 || audits[0].TraceID != callerTrace {
		t.Fatalf("trace isolation = caller %q transport %q audits %#v", callerTrace, transportTrace, audits)
	}
	if len(events) != 2 || events[0].Stage != tracing.StagePEPDecision || events[1].Stage != tracing.StageSpokeSnapshot ||
		events[0].TraceID != callerTrace || events[1].TraceID != transportTrace || events[1].Outcome != tracing.OutcomeSuccess {
		t.Fatalf("trace events = %#v", events)
	}
}

func TestCollectorSurvivesPanickingTraceObserver(t *testing.T) {
	now := time.Date(2026, time.July, 14, 13, 0, 0, 0, time.UTC)
	tracer := tracing.ObserverFunc(func(tracing.Event) { panic("trace recorder fault") })
	enforcer, err := pep.NewEnforcer(pep.Config{
		Hook: pep.AllowReadHook{}, Auditor: pep.AuditFunc(func(context.Context, pep.AuditEvent) error { return nil }), TraceObserver: tracer,
	})
	if err != nil {
		t.Fatal(err)
	}
	collector, err := NewCollector(CollectorConfig{
		LifecycleContext: t.Context(),
		Store:            &memoryStore{spokes: []Spoke{{ID: "spoke-a", ManagedClusterRef: "ocm/spoke-a"}}, snapshots: make(map[string]Snapshot), failures: make(map[string]FailureKind)},
		Transport: transportFunc(func(context.Context, tenancy.WorkspaceID, Spoke) (Snapshot, error) {
			return validSnapshot("spoke-a", now), nil
		}),
		PEP: enforcer, TraceObserver: tracer, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	if coverage, err := collector.Collect(context.Background(), readerScope(t, "workspace-a")); err != nil || coverage.Reachable != 1 {
		t.Fatalf("Collect() changed because tracing panicked: coverage %#v, error %v", coverage, err)
	}
}
