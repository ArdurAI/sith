// SPDX-License-Identifier: Apache-2.0

package hubruntime

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/ArdurAI/sith/internal/pep"
)

type runtimeAuditObservation struct {
	sink    pep.AuditSink
	outcome pep.AuditOutcome
}

type runtimeAuditObserver struct {
	observations []runtimeAuditObservation
}

func (observer *runtimeAuditObserver) ObservePolicyAudit(sink pep.AuditSink, outcome pep.AuditOutcome, _ time.Duration) {
	observer.observations = append(observer.observations, runtimeAuditObservation{sink: sink, outcome: outcome})
}

func TestOrderedPolicyAuditorRequiresDurabilityBeforeProcessLog(t *testing.T) {
	t.Parallel()

	var calls []string
	observer := &runtimeAuditObserver{}
	durableFailure := errors.New("database unavailable")
	durable, err := pep.NewObservedAuditor(pep.AuditSinkDurable, pep.AuditFunc(func(context.Context, pep.AuditEvent) error {
		calls = append(calls, "durable")
		return durableFailure
	}), observer)
	if err != nil {
		t.Fatal(err)
	}
	process, err := pep.NewObservedAuditor(pep.AuditSinkProcess, pep.AuditFunc(func(context.Context, pep.AuditEvent) error {
		calls = append(calls, "process")
		return nil
	}), observer)
	if err != nil {
		t.Fatal(err)
	}
	auditor, err := newOrderedPolicyAuditor(
		durable,
		process,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := auditor.Record(context.Background(), pep.AuditEvent{}); !errors.Is(err, durableFailure) {
		t.Fatalf("Record() error = %v, want durable failure", err)
	}
	if !reflect.DeepEqual(calls, []string{"durable"}) {
		t.Fatalf("sink calls = %v, want durable sink only", calls)
	}
	wantObservations := []runtimeAuditObservation{{sink: pep.AuditSinkDurable, outcome: pep.AuditOutcomeError}}
	if !reflect.DeepEqual(observer.observations, wantObservations) {
		t.Fatalf("audit observations = %#v, want %#v", observer.observations, wantObservations)
	}
}

func TestOrderedPolicyAuditorReportsProcessFailureAfterDurableAppend(t *testing.T) {
	t.Parallel()

	var calls []string
	observer := &runtimeAuditObserver{}
	processFailure := errors.New("process sink unavailable")
	durable, err := pep.NewObservedAuditor(pep.AuditSinkDurable, pep.AuditFunc(func(context.Context, pep.AuditEvent) error {
		calls = append(calls, "durable")
		return nil
	}), observer)
	if err != nil {
		t.Fatal(err)
	}
	process, err := pep.NewObservedAuditor(pep.AuditSinkProcess, pep.AuditFunc(func(context.Context, pep.AuditEvent) error {
		calls = append(calls, "process")
		return processFailure
	}), observer)
	if err != nil {
		t.Fatal(err)
	}
	auditor, err := newOrderedPolicyAuditor(
		durable,
		process,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := auditor.Record(context.Background(), pep.AuditEvent{}); !errors.Is(err, processFailure) {
		t.Fatalf("Record() error = %v, want process failure", err)
	}
	if !reflect.DeepEqual(calls, []string{"durable", "process"}) {
		t.Fatalf("sink calls = %v, want durable then process", calls)
	}
	wantObservations := []runtimeAuditObservation{
		{sink: pep.AuditSinkDurable, outcome: pep.AuditOutcomeSuccess},
		{sink: pep.AuditSinkProcess, outcome: pep.AuditOutcomeError},
	}
	if !reflect.DeepEqual(observer.observations, wantObservations) {
		t.Fatalf("audit observations = %#v, want %#v", observer.observations, wantObservations)
	}
}

func TestOrderedPolicyAuditorRejectsMissingSinks(t *testing.T) {
	t.Parallel()

	if _, err := newOrderedPolicyAuditor(nil, nil); err == nil {
		t.Fatal("missing policy audit sinks accepted")
	}
	if err := (orderedPolicyAuditor{}).Record(context.Background(), pep.AuditEvent{}); err == nil {
		t.Fatal("zero-value ordered auditor accepted an event")
	}
}
