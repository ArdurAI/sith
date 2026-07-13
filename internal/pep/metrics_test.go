// SPDX-License-Identifier: Apache-2.0

package pep

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestEnforcerObservesClosedOutcomesWithoutChangingPolicyBehavior(t *testing.T) {
	tests := []struct {
		name         string
		hook         PolicyHook
		verb         Verb
		wantOutcome  DecisionOutcome
		wantError    bool
		wantObserved Verb
	}{
		{name: "allow", hook: AllowReadHook{}, verb: VerbFleetRead, wantOutcome: DecisionOutcomeAllow, wantObserved: VerbFleetRead},
		{name: "deny", hook: HookFunc(func(context.Context, Request) (Decision, error) {
			return Decision{Verdict: VerdictDeny, ReasonCode: "policy-deny"}, nil
		}), verb: VerbFleetRead, wantOutcome: DecisionOutcomeDeny, wantError: true, wantObserved: VerbFleetRead},
		{name: "approval", hook: HookFunc(func(context.Context, Request) (Decision, error) {
			return Decision{Verdict: VerdictRequireApproval, ReasonCode: "approval-required"}, nil
		}), verb: VerbFleetRead, wantOutcome: DecisionOutcomeRequireApproval, wantError: true, wantObserved: VerbFleetRead},
		{name: "hook error", hook: HookFunc(func(context.Context, Request) (Decision, error) {
			return Decision{}, errors.New("dependency unavailable")
		}), verb: VerbFleetRead, wantOutcome: DecisionOutcomeError, wantError: true, wantObserved: VerbFleetRead},
		{name: "invalid verb", hook: AllowReadHook{}, verb: "caller-supplied", wantOutcome: DecisionOutcomeDeny, wantError: true, wantObserved: "invalid"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			observer := &recordingDecisionObserver{}
			enforcer, err := NewEnforcer(Config{Hook: test.hook, Auditor: AuditFunc(func(context.Context, AuditEvent) error { return nil }), Observer: observer})
			if err != nil {
				t.Fatal(err)
			}
			err = enforcer.AuthorizeRead(context.Background(), testScope(t), NewReadInput(test.verb, nil))
			if (err != nil) != test.wantError {
				t.Fatalf("AuthorizeRead() error = %v, want error %t", err, test.wantError)
			}
			if len(observer.events) != 1 {
				t.Fatalf("observations = %#v, want one", observer.events)
			}
			event := observer.events[0]
			if event.verb != test.wantObserved || event.outcome != test.wantOutcome || event.duration < 0 {
				t.Fatalf("observation = %#v", event)
			}
		})
	}
}

func TestEnforcerRecoversFromPanickingObserver(t *testing.T) {
	enforcer, err := NewEnforcer(Config{
		Hook: AllowReadHook{}, Auditor: AuditFunc(func(context.Context, AuditEvent) error { return nil }),
		Observer: decisionObserverFunc(func(Verb, DecisionOutcome, time.Duration) { panic("metrics fault") }),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := enforcer.AuthorizeRead(context.Background(), testScope(t), NewReadInput(VerbFleetRead, nil)); err != nil {
		t.Fatalf("AuthorizeRead() changed because observer panicked: %v", err)
	}
}

type decisionObservation struct {
	verb     Verb
	outcome  DecisionOutcome
	duration time.Duration
}

type recordingDecisionObserver struct {
	events []decisionObservation
}

func (observer *recordingDecisionObserver) ObserveDecision(verb Verb, outcome DecisionOutcome, duration time.Duration) {
	observer.events = append(observer.events, decisionObservation{verb: verb, outcome: outcome, duration: duration})
}

type decisionObserverFunc func(Verb, DecisionOutcome, time.Duration)

func (function decisionObserverFunc) ObserveDecision(verb Verb, outcome DecisionOutcome, duration time.Duration) {
	function(verb, outcome, duration)
}
