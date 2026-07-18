// SPDX-License-Identifier: Apache-2.0

package hubfleet

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/ArdurAI/sith/internal/fleet"
	"github.com/ArdurAI/sith/internal/tenancy"
)

func TestSourceObservesOneBoundedOutcomeAfterAuthorizedRead(t *testing.T) {
	t.Parallel()

	readerFailure := errors.New("reader unavailable")
	tests := []struct {
		name      string
		result    fleet.FleetResult
		readerErr error
		want      FleetReadOutcome
		wantErrIs error
	}{
		{
			name: "complete", result: fleet.FleetResult{
				Clusters: []fleet.Cluster{{Name: "spoke-a"}, {Name: "spoke-b"}},
				Coverage: fleet.Coverage{Requested: 2, Reachable: 2},
			}, want: FleetReadOutcomeComplete,
		},
		{
			name: "degraded stale", result: fleet.FleetResult{
				Coverage: fleet.Coverage{Requested: 2, Reachable: 2, Stale: []string{"spoke-b"}},
			}, want: FleetReadOutcomeDegraded,
		},
		{
			name: "degraded malformed empty", result: fleet.FleetResult{
				Coverage: fleet.Coverage{Requested: 0, Reachable: 1},
			}, want: FleetReadOutcomeDegraded,
		},
		{
			name: "degraded result coverage mismatch", result: fleet.FleetResult{
				Clusters: []fleet.Cluster{{Name: "spoke-a"}},
				Coverage: fleet.Coverage{},
			}, want: FleetReadOutcomeDegraded,
		},
		{name: "empty", result: fleet.FleetResult{Coverage: fleet.Coverage{}}, want: FleetReadOutcomeEmpty},
		{name: "error", readerErr: readerFailure, want: FleetReadOutcomeError, wantErrIs: readerFailure},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var observed []FleetReadOutcome
			source, err := NewSource(SourceConfig{
				Reader: fleetReaderFunc(func(context.Context, tenancy.Scope, time.Duration, time.Time) (fleet.FleetResult, error) {
					return test.result, test.readerErr
				}),
				Scope: readerScope(t, "workspace-a"), PEP: testReadPEP(t),
				Observer: fleetReadObserverFunc(func(outcome FleetReadOutcome) { observed = append(observed, outcome) }),
			})
			if err != nil {
				t.Fatal(err)
			}
			_, err = source.Fleet(context.Background())
			if !errors.Is(err, test.wantErrIs) || (test.wantErrIs == nil && err != nil) {
				t.Fatalf("Fleet() error = %v, want errors.Is(%v)", err, test.wantErrIs)
			}
			if !slices.Equal(observed, []FleetReadOutcome{test.want}) {
				t.Fatalf("observed outcomes = %q, want %q", observed, test.want)
			}
		})
	}
}

func TestSourceReadResultIsIndependentOfObserverFailure(t *testing.T) {
	t.Parallel()

	readerFailure := errors.New("reader unavailable")
	for _, test := range []struct {
		name      string
		readerErr error
	}{
		{name: "success"},
		{name: "reader error", readerErr: readerFailure},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			source, err := NewSource(SourceConfig{
				Reader: fleetReaderFunc(func(context.Context, tenancy.Scope, time.Duration, time.Time) (fleet.FleetResult, error) {
					return fleet.FleetResult{Coverage: fleet.Coverage{Requested: 1, Reachable: 1}}, test.readerErr
				}),
				Scope: readerScope(t, "workspace-a"), PEP: testReadPEP(t),
				Observer: fleetReadObserverFunc(func(FleetReadOutcome) { panic("observer failure") }),
			})
			if err != nil {
				t.Fatal(err)
			}
			result, err := source.Fleet(context.Background())
			if test.readerErr == nil && (err != nil || !result.Coverage.Complete()) {
				t.Fatalf("successful read changed by observer panic: result %#v, error %v", result, err)
			}
			if test.readerErr != nil && !errors.Is(err, test.readerErr) {
				t.Fatalf("reader error changed by observer panic: %v", err)
			}
		})
	}
}

type fleetReadObserverFunc func(FleetReadOutcome)

func (function fleetReadObserverFunc) ObserveFleetRead(outcome FleetReadOutcome) {
	function(outcome)
}
