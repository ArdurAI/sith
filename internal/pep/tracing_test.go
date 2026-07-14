// SPDX-License-Identifier: Apache-2.0

package pep

import (
	"context"
	"testing"

	"github.com/ArdurAI/sith/internal/tracing"
)

func TestEnforcerCorrelatesAuditAndTraceWithOneLocalID(t *testing.T) {
	var audits []AuditEvent
	var events []tracing.Event
	enforcer, err := NewEnforcer(Config{
		Hook: AllowReadHook{},
		Auditor: AuditFunc(func(_ context.Context, event AuditEvent) error {
			audits = append(audits, event)
			return nil
		}),
		TraceObserver: tracing.ObserverFunc(func(event tracing.Event) { events = append(events, event) }),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := enforcer.AuthorizeRead(context.Background(), testScope(t), NewReadInput(VerbFleetRead, nil)); err != nil {
		t.Fatalf("AuthorizeRead() error = %v", err)
	}
	if len(audits) != 1 || !audits[0].TraceID.Valid() {
		t.Fatalf("audit events = %#v", audits)
	}
	if len(events) != 1 || events[0].TraceID != audits[0].TraceID || events[0].Stage != tracing.StagePEPDecision ||
		events[0].Outcome != tracing.OutcomeSuccess || events[0].Duration < 0 {
		t.Fatalf("trace events = %#v", events)
	}
}

func TestEnforcerSurvivesPanickingTraceObserver(t *testing.T) {
	enforcer, err := NewEnforcer(Config{
		Hook: AllowReadHook{}, Auditor: AuditFunc(func(context.Context, AuditEvent) error { return nil }),
		TraceObserver: tracing.ObserverFunc(func(tracing.Event) { panic("trace recorder fault") }),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := enforcer.AuthorizeRead(context.Background(), testScope(t), NewReadInput(VerbFleetRead, nil)); err != nil {
		t.Fatalf("AuthorizeRead() changed because tracing panicked: %v", err)
	}
}
