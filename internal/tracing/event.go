// SPDX-License-Identifier: Apache-2.0

package tracing

import (
	"fmt"
	"time"
)

const maxDuration = time.Hour

// Stage is the closed local-trace stage vocabulary. It intentionally names system behavior, not
// a tenant, resource, target, actor, or spoke.
type Stage string

// Supported local trace stages.
const (
	StagePEPDecision   Stage = "pep.decision"
	StageSpokeSnapshot Stage = "spoke.snapshot"
)

// Outcome is the closed result vocabulary for a trace stage.
type Outcome string

// Supported trace outcomes.
const (
	OutcomeSuccess  Outcome = "success"
	OutcomeRefused  Outcome = "refused"
	OutcomeFailure  Outcome = "failure"
	OutcomeCanceled Outcome = "canceled"
)

// Event is one passive, local trace-stage observation. It intentionally has no generic attribute
// map: adding request or response data requires a reviewed schema change instead of an accidental
// logging path.
type Event struct {
	TraceID  ID
	Stage    Stage
	Outcome  Outcome
	Duration time.Duration
}

// Validate rejects unbounded or caller-controlled trace data before an observer can emit it.
func (event Event) Validate() error {
	if !event.TraceID.Valid() {
		return fmt.Errorf("trace event identifier is invalid")
	}
	switch event.Stage {
	case StagePEPDecision, StageSpokeSnapshot:
	default:
		return fmt.Errorf("trace event stage is unsupported")
	}
	switch event.Outcome {
	case OutcomeSuccess, OutcomeRefused, OutcomeFailure, OutcomeCanceled:
	default:
		return fmt.Errorf("trace event outcome is unsupported")
	}
	if event.Duration < 0 || event.Duration > maxDuration {
		return fmt.Errorf("trace event duration is outside the accepted bound")
	}
	return nil
}

// Observer receives passive, already-sanitized trace events. Implementations must never alter the
// governed operation; Observe isolates a faulty observer defensively.
type Observer interface {
	ObserveTrace(Event)
}

// ObserverFunc adapts a function to Observer.
type ObserverFunc func(Event)

// ObserveTrace calls function.
func (function ObserverFunc) ObserveTrace(event Event) {
	function(event)
}

type noopObserver struct{}

func (noopObserver) ObserveTrace(Event) {}

// NoopObserver returns the safe no-op observer used when a composition root does not attach a
// local trace recorder.
func NoopObserver() Observer { return noopObserver{} }

// Observe sends a validated event to a passive observer. Invalid events and observer panics are
// intentionally ignored so observability cannot mutate authorization or collection behavior.
func Observe(observer Observer, event Event) {
	if observer == nil || event.Validate() != nil {
		return
	}
	defer func() {
		_ = recover()
	}()
	observer.ObserveTrace(event)
}
