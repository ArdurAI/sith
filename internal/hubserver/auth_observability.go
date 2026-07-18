// SPDX-License-Identifier: Apache-2.0

package hubserver

import "fmt"

// AuthOutcome is the closed self-observability result of one pre-principal
// authentication attempt. It intentionally does not distinguish credential failure modes.
type AuthOutcome string

// AuthOutcomeRefused is emitted for every request the bearer-token middleware rejects.
const AuthOutcomeRefused AuthOutcome = "refused"

// AuthEvent is one passive, sanitized authentication observation. It deliberately has no
// request, credential, verifier-error, principal, path, network, or correlation fields: none are
// trusted before authentication succeeds.
type AuthEvent struct {
	Outcome AuthOutcome
}

// Validate rejects unsupported outcome values before an observer can emit them.
func (event AuthEvent) Validate() error {
	if event.Outcome != AuthOutcomeRefused {
		return fmt.Errorf("authentication event outcome is unsupported")
	}
	return nil
}

// AuthObserver receives passive, already-sanitized authentication events. Implementations must
// never alter authentication behavior; ObserveAuth isolates faulty observers defensively.
type AuthObserver interface {
	ObserveAuth(AuthEvent)
}

// AuthObserverFunc adapts a function to AuthObserver.
type AuthObserverFunc func(AuthEvent)

// ObserveAuth calls function.
func (function AuthObserverFunc) ObserveAuth(event AuthEvent) {
	function(event)
}

type authObserverFanout struct {
	observers []AuthObserver
}

// NewAuthObserverFanout composes two or more required authentication observers. Each destination
// remains independently panic-isolated, so a faulty observer cannot suppress later destinations.
func NewAuthObserverFanout(observers ...AuthObserver) (AuthObserver, error) {
	if len(observers) < 2 {
		return nil, fmt.Errorf("construct authentication observer fanout: at least two observers are required")
	}
	for _, observer := range observers {
		if observer == nil {
			return nil, fmt.Errorf("construct authentication observer fanout: observers are required")
		}
	}
	return &authObserverFanout{observers: append([]AuthObserver(nil), observers...)}, nil
}

func (fanout *authObserverFanout) ObserveAuth(event AuthEvent) {
	if fanout == nil {
		return
	}
	for _, observer := range fanout.observers {
		ObserveAuth(observer, event)
	}
}

// ObserveAuth sends a valid event to a passive observer. Invalid events and observer panics are
// intentionally ignored so observability cannot alter the uniform unauthorized response.
func ObserveAuth(observer AuthObserver, event AuthEvent) {
	if observer == nil || event.Validate() != nil {
		return
	}
	defer func() {
		_ = recover()
	}()
	observer.ObserveAuth(event)
}
