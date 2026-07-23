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
	now := time.Date(2026, 7, 18, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		result    fleet.FleetResult
		readerErr error
		want      FleetReadObservation
		wantErrIs error
	}{
		{
			name: "complete", result: fleet.FleetResult{
				Clusters: []fleet.Cluster{{Name: "spoke-a", ObservedAt: now.Add(-time.Minute)}, {Name: "spoke-b", ObservedAt: now.Add(-time.Minute)}},
				Coverage: fleet.Coverage{Requested: 2, Reachable: 2},
			}, want: FleetReadObservation{Outcome: FleetReadOutcomeComplete, Freshness: FleetFreshnessOutcomeFresh},
		},
		{
			name: "degraded stale", result: fleet.FleetResult{
				Clusters: []fleet.Cluster{{Name: "spoke-a", ObservedAt: now.Add(-time.Minute)}, {Name: "spoke-b", ObservedAt: now.Add(-10 * time.Minute)}},
				Coverage: fleet.Coverage{Requested: 2, Reachable: 2, Stale: []string{"spoke-b"}},
			}, want: FleetReadObservation{Outcome: FleetReadOutcomeDegraded, Freshness: FleetFreshnessOutcomeStale},
		},
		{
			name: "degraded never observed", result: fleet.FleetResult{
				Clusters: []fleet.Cluster{{Name: "spoke-a", ObservedAt: now.Add(-time.Minute)}, {Name: "spoke-b"}},
				Coverage: fleet.Coverage{Requested: 2, Reachable: 1, Unreachable: []string{"spoke-b"}},
			}, want: FleetReadObservation{Outcome: FleetReadOutcomeDegraded, Freshness: FleetFreshnessOutcomeUnknown},
		},
		{
			name: "degraded malformed empty", result: fleet.FleetResult{
				Coverage: fleet.Coverage{Requested: 0, Reachable: 1},
			}, want: FleetReadObservation{Outcome: FleetReadOutcomeDegraded, Freshness: FleetFreshnessOutcomeUnknown},
		},
		{
			name: "degraded result coverage mismatch", result: fleet.FleetResult{
				Clusters: []fleet.Cluster{{Name: "spoke-a", ObservedAt: now.Add(-time.Minute)}},
				Coverage: fleet.Coverage{},
			}, want: FleetReadObservation{Outcome: FleetReadOutcomeDegraded, Freshness: FleetFreshnessOutcomeUnknown},
		},
		{
			name: "degraded stale without retained observation", result: fleet.FleetResult{
				Clusters: []fleet.Cluster{{Name: "spoke-a", ObservedAt: now.Add(-time.Minute)}, {Name: "spoke-b"}},
				Coverage: fleet.Coverage{Requested: 2, Reachable: 2, Stale: []string{"spoke-b"}},
			}, want: FleetReadObservation{Outcome: FleetReadOutcomeDegraded, Freshness: FleetFreshnessOutcomeUnknown},
		},
		{name: "empty", result: fleet.FleetResult{Coverage: fleet.Coverage{}}, want: FleetReadObservation{
			Outcome: FleetReadOutcomeEmpty, Freshness: FleetFreshnessOutcomeEmpty,
		}},
		{name: "error", readerErr: readerFailure, want: FleetReadObservation{
			Outcome: FleetReadOutcomeError, Freshness: FleetFreshnessOutcomeError,
		}, wantErrIs: readerFailure},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var observed []FleetReadObservation
			source, err := NewSource(SourceConfig{
				Reader: fleetReaderFunc(func(context.Context, tenancy.Scope, time.Duration, time.Time) (fleet.FleetResult, error) {
					return test.result, test.readerErr
				}),
				Scope: readerScope(t, "workspace-a"), PEP: testReadPEP(t),
				Observer: fleetReadObserverFunc(func(observation FleetReadObservation) { observed = append(observed, observation) }),
				Now:      func() time.Time { return now },
			})
			if err != nil {
				t.Fatal(err)
			}
			_, err = source.Fleet(context.Background())
			if !errors.Is(err, test.wantErrIs) || (test.wantErrIs == nil && err != nil) {
				t.Fatalf("Fleet() error = %v, want errors.Is(%v)", err, test.wantErrIs)
			}
			if !slices.Equal(observed, []FleetReadObservation{test.want}) {
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
				Observer: fleetReadObserverFunc(func(FleetReadObservation) { panic("observer failure") }),
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

func TestFleetReadObservationRejectsContradictoryPairs(t *testing.T) {
	t.Parallel()

	for _, observation := range []FleetReadObservation{
		{Outcome: FleetReadOutcomeComplete, Freshness: FleetFreshnessOutcomeStale},
		{Outcome: FleetReadOutcomeDegraded, Freshness: FleetFreshnessOutcomeFresh},
		{Outcome: FleetReadOutcomeEmpty, Freshness: FleetFreshnessOutcomeUnknown},
		{Outcome: FleetReadOutcomeError, Freshness: FleetFreshnessOutcomeEmpty},
		{Outcome: FleetReadOutcome("workspace-a"), Freshness: FleetFreshnessOutcomeFresh},
	} {
		if observation.Valid() {
			t.Fatalf("FleetReadObservation.Valid() accepted contradictory pair %#v", observation)
		}
	}
}

type fleetReadObserverFunc func(FleetReadObservation)

func (function fleetReadObserverFunc) ObserveFleetRead(observation FleetReadObservation) {
	function(observation)
}
