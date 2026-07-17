// SPDX-License-Identifier: Apache-2.0

package pep

import (
	"context"
	"fmt"
	"time"
)

// AuditSink is the closed production destination for one policy-audit attempt.
type AuditSink string

// Closed production policy-audit sinks.
const (
	AuditSinkDurable AuditSink = "durable"
	AuditSinkProcess AuditSink = "process"
)

// Valid reports whether sink belongs to the closed policy-audit vocabulary.
func (sink AuditSink) Valid() bool {
	return sink == AuditSinkDurable || sink == AuditSinkProcess
}

// AuditOutcome is the bounded result of one policy-audit sink attempt.
type AuditOutcome string

// Closed policy-audit attempt outcomes.
const (
	AuditOutcomeSuccess AuditOutcome = "success"
	AuditOutcomeError   AuditOutcome = "error"
)

// Valid reports whether outcome belongs to the closed policy-audit vocabulary.
func (outcome AuditOutcome) Valid() bool {
	return outcome == AuditOutcomeSuccess || outcome == AuditOutcomeError
}

// AuditObserver receives passive measurements after a policy-audit sink attempt. Implementations
// must not block or mutate the audit path. The observed auditor isolates observer panics.
type AuditObserver interface {
	ObservePolicyAudit(AuditSink, AuditOutcome, time.Duration)
}

type observedAuditor struct {
	sink     AuditSink
	auditor  Auditor
	observer AuditObserver
}

// NewObservedAuditor decorates one closed policy-audit sink with passive measurements. The
// underlying auditor result remains authoritative: observer panics cannot suppress or create an
// audit failure.
func NewObservedAuditor(sink AuditSink, auditor Auditor, observer AuditObserver) (Auditor, error) {
	if !sink.Valid() || auditor == nil || observer == nil {
		return nil, fmt.Errorf("construct observed policy auditor: valid sink, auditor, and observer are required")
	}
	return observedAuditor{sink: sink, auditor: auditor, observer: observer}, nil
}

func (auditor observedAuditor) Record(ctx context.Context, event AuditEvent) error {
	if !auditor.sink.Valid() || auditor.auditor == nil || auditor.observer == nil {
		return fmt.Errorf("record observed policy audit: valid sink, auditor, and observer are required")
	}
	startedAt := time.Now()
	err := auditor.auditor.Record(ctx, event)
	outcome := AuditOutcomeSuccess
	if err != nil {
		outcome = AuditOutcomeError
	}
	auditor.observe(outcome, time.Since(startedAt))
	return err
}

func (auditor observedAuditor) observe(outcome AuditOutcome, duration time.Duration) {
	defer func() {
		_ = recover()
	}()
	auditor.observer.ObservePolicyAudit(auditor.sink, outcome, duration)
}
