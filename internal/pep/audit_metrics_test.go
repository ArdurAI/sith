// SPDX-License-Identifier: Apache-2.0

package pep

import (
	"context"
	"errors"
	"testing"
	"time"
)

type auditObservation struct {
	sink     AuditSink
	outcome  AuditOutcome
	duration time.Duration
}

type recordingAuditObserver struct {
	observations []auditObservation
	panic        bool
}

func (observer *recordingAuditObserver) ObservePolicyAudit(sink AuditSink, outcome AuditOutcome, duration time.Duration) {
	if observer.panic {
		panic("observer failure")
	}
	observer.observations = append(observer.observations, auditObservation{sink: sink, outcome: outcome, duration: duration})
}

func TestObservedAuditorReportsSuccessAndPreservesCall(t *testing.T) {
	t.Parallel()

	observer := &recordingAuditObserver{}
	ctx := context.Background()
	event := AuditEvent{}
	var receivedContext context.Context
	var receivedEvent AuditEvent
	auditor, err := NewObservedAuditor(AuditSinkDurable, AuditFunc(func(callContext context.Context, callEvent AuditEvent) error {
		receivedContext = callContext
		receivedEvent = callEvent
		return nil
	}), observer)
	if err != nil {
		t.Fatal(err)
	}
	if err := auditor.Record(ctx, event); err != nil {
		t.Fatal(err)
	}
	if receivedContext != ctx || receivedEvent != event {
		t.Fatal("observed auditor changed the underlying call")
	}
	if len(observer.observations) != 1 || observer.observations[0].sink != AuditSinkDurable ||
		observer.observations[0].outcome != AuditOutcomeSuccess || observer.observations[0].duration < 0 {
		t.Fatalf("observations = %#v", observer.observations)
	}
}

func TestObservedAuditorReportsAndPreservesFailure(t *testing.T) {
	t.Parallel()

	want := errors.New("sink unavailable")
	observer := &recordingAuditObserver{}
	auditor, err := NewObservedAuditor(AuditSinkProcess, AuditFunc(func(context.Context, AuditEvent) error {
		return want
	}), observer)
	if err != nil {
		t.Fatal(err)
	}
	if err := auditor.Record(context.Background(), AuditEvent{}); !errors.Is(err, want) {
		t.Fatalf("Record() error = %v, want original failure", err)
	}
	if len(observer.observations) != 1 || observer.observations[0].sink != AuditSinkProcess ||
		observer.observations[0].outcome != AuditOutcomeError {
		t.Fatalf("observations = %#v", observer.observations)
	}
}

func TestObservedAuditorIsolatesObserverPanic(t *testing.T) {
	t.Parallel()

	want := errors.New("authoritative audit failure")
	observer := &recordingAuditObserver{panic: true}
	auditor, err := NewObservedAuditor(AuditSinkDurable, AuditFunc(func(context.Context, AuditEvent) error {
		return want
	}), observer)
	if err != nil {
		t.Fatal(err)
	}
	if err := auditor.Record(context.Background(), AuditEvent{}); !errors.Is(err, want) {
		t.Fatalf("Record() error = %v, want original failure", err)
	}
}

func TestObservedAuditorRejectsUnsafeConfiguration(t *testing.T) {
	t.Parallel()

	observer := &recordingAuditObserver{}
	sink := AuditFunc(func(context.Context, AuditEvent) error { return nil })
	for _, test := range []struct {
		name     string
		sink     AuditSink
		auditor  Auditor
		observer AuditObserver
	}{
		{name: "invalid sink", sink: AuditSink("workspace-a/token=secret"), auditor: sink, observer: observer},
		{name: "missing auditor", sink: AuditSinkDurable, observer: observer},
		{name: "missing observer", sink: AuditSinkDurable, auditor: sink},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := NewObservedAuditor(test.sink, test.auditor, test.observer); err == nil {
				t.Fatal("unsafe observed-auditor configuration accepted")
			}
		})
	}
	if err := (observedAuditor{}).Record(context.Background(), AuditEvent{}); err == nil {
		t.Fatal("zero-value observed auditor accepted an event")
	}
}
