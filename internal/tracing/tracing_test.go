// SPDX-License-Identifier: Apache-2.0

package tracing

import (
	"context"
	"testing"
	"time"
)

func TestEnsureMintsAndPreservesOneOpaqueID(t *testing.T) {
	ctx, first, err := Ensure(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !first.Valid() {
		t.Fatalf("minted trace identifier is invalid: %q", first)
	}
	secondContext, second, err := Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if first != second || secondContext != ctx {
		t.Fatalf("Ensure() changed an existing trace context: %q/%q", first, second)
	}
	var nilContext context.Context
	if _, _, err := Ensure(nilContext); err == nil {
		t.Fatal("Ensure() accepted a nil context")
	}
}

func TestEventAndObserverRejectUnsafeTraceDataWithoutAffectingCallers(t *testing.T) {
	const traceID ID = "0123456789abcdef0123456789abcdef"
	valid := Event{TraceID: traceID, Stage: StagePEPDecision, Outcome: OutcomeSuccess, Duration: time.Millisecond}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid trace event rejected: %v", err)
	}
	for _, event := range []Event{
		{TraceID: "trace=secret", Stage: StagePEPDecision, Outcome: OutcomeSuccess, Duration: time.Millisecond},
		{TraceID: traceID, Stage: "workspace-a", Outcome: OutcomeSuccess, Duration: time.Millisecond},
		{TraceID: traceID, Stage: StagePEPDecision, Outcome: "token=secret", Duration: time.Millisecond},
		{TraceID: traceID, Stage: StagePEPDecision, Outcome: OutcomeSuccess, Duration: -time.Millisecond},
		{TraceID: traceID, Stage: StagePEPDecision, Outcome: OutcomeSuccess, Duration: time.Hour + time.Nanosecond},
	} {
		if err := event.Validate(); err == nil {
			t.Fatalf("Validate() accepted unsafe event %#v", event)
		}
	}
	called := false
	Observe(ObserverFunc(func(Event) { called = true; panic("observer fault") }), valid)
	if !called {
		t.Fatal("Observe() did not call a valid observer")
	}
	called = false
	Observe(ObserverFunc(func(Event) { called = true }), Event{TraceID: "token=secret"})
	if called {
		t.Fatal("Observe() delivered an invalid event")
	}
}
