// SPDX-License-Identifier: Apache-2.0

package pep

import (
	"context"
	"time"

	"github.com/ArdurAI/sith/internal/tracing"
)

// DecisionOutcome is the bounded self-observability result of one policy read attempt.
// It intentionally carries no workspace, actor, selector, credential, or reason-code material.
type DecisionOutcome string

// Closed policy-observability outcomes.
const (
	DecisionOutcomeAllow           DecisionOutcome = "allow"
	DecisionOutcomeDeny            DecisionOutcome = "deny"
	DecisionOutcomeRequireApproval DecisionOutcome = "require-approval"
	DecisionOutcomeError           DecisionOutcome = "error"
)

// DecisionObserver receives passive, bounded measurements for policy reads. Implementations must
// not block or mutate the authorization path. The enforcer isolates observer panics defensively.
type DecisionObserver interface {
	ObserveDecision(verb Verb, outcome DecisionOutcome, duration time.Duration)
}

type noopDecisionObserver struct{}

func (noopDecisionObserver) ObserveDecision(Verb, DecisionOutcome, time.Duration) {}

func (enforcer *Enforcer) observeDecision(verb Verb, outcome DecisionOutcome, duration time.Duration) {
	if enforcer == nil || enforcer.observer == nil {
		return
	}
	defer func() {
		_ = recover()
	}()
	enforcer.observer.ObserveDecision(normalizedObservedVerb(verb), outcome, duration)
}

func (enforcer *Enforcer) observeTrace(ctx context.Context, outcome DecisionOutcome, duration time.Duration) {
	if enforcer == nil || enforcer.tracer == nil {
		return
	}
	traceID, ok := tracing.FromContext(ctx)
	if !ok {
		return
	}
	tracing.Observe(enforcer.tracer, tracing.Event{
		TraceID: traceID, Stage: tracing.StagePEPDecision, Outcome: traceOutcome(outcome), Duration: duration,
	})
}

func traceOutcome(outcome DecisionOutcome) tracing.Outcome {
	switch outcome {
	case DecisionOutcomeAllow:
		return tracing.OutcomeSuccess
	case DecisionOutcomeDeny, DecisionOutcomeRequireApproval:
		return tracing.OutcomeRefused
	default:
		return tracing.OutcomeFailure
	}
}

func normalizedObservedVerb(verb Verb) Verb {
	if verb.Valid() {
		return verb
	}
	return "invalid"
}
