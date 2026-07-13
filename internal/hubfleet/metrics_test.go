// SPDX-License-Identifier: Apache-2.0

package hubfleet

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ArdurAI/sith/internal/tenancy"
)

func TestCollectorObservesClosedSnapshotOutcomes(t *testing.T) {
	now := time.Date(2026, time.July, 12, 20, 0, 0, 0, time.UTC)
	tests := []struct {
		name        string
		transport   transportFunc
		wantOutcome SnapshotOutcome
	}{
		{
			name: "success",
			transport: transportFunc(func(context.Context, tenancy.WorkspaceID, Spoke) (Snapshot, error) {
				return validSnapshot("spoke-a", now), nil
			}),
			wantOutcome: SnapshotOutcomeSuccess,
		},
		{
			name: "transport failure",
			transport: transportFunc(func(context.Context, tenancy.WorkspaceID, Spoke) (Snapshot, error) {
				return Snapshot{}, errors.New("proxy unavailable")
			}),
			wantOutcome: SnapshotOutcomeTransport,
		},
		{
			name: "invalid snapshot",
			transport: transportFunc(func(context.Context, tenancy.WorkspaceID, Spoke) (Snapshot, error) {
				return validSnapshot("spoke-b", now), nil
			}),
			wantOutcome: SnapshotOutcomeInvalidSnapshot,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			observer := &recordingSnapshotObserver{}
			store := &memoryStore{
				spokes: []Spoke{{ID: "spoke-a", ManagedClusterRef: "ocm/spoke-a"}}, snapshots: make(map[string]Snapshot), failures: make(map[string]FailureKind),
			}
			collector, err := NewCollector(CollectorConfig{
				Store: store, Transport: test.transport, PEP: testReadPEP(t), Observer: observer, Now: func() time.Time { return now },
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := collector.Collect(context.Background(), readerScope(t, "workspace-a")); err != nil {
				t.Fatal(err)
			}
			if len(observer.events) != 1 || observer.events[0].outcome != test.wantOutcome || observer.events[0].duration < 0 {
				t.Fatalf("observations = %#v", observer.events)
			}
		})
	}
}

func TestCollectorRecoversFromPanickingSnapshotObserver(t *testing.T) {
	now := time.Date(2026, time.July, 12, 20, 0, 0, 0, time.UTC)
	collector, err := NewCollector(CollectorConfig{
		Store: &memoryStore{
			spokes: []Spoke{{ID: "spoke-a", ManagedClusterRef: "ocm/spoke-a"}}, snapshots: make(map[string]Snapshot), failures: make(map[string]FailureKind),
		},
		Transport: transportFunc(func(context.Context, tenancy.WorkspaceID, Spoke) (Snapshot, error) {
			return validSnapshot("spoke-a", now), nil
		}),
		PEP:      testReadPEP(t),
		Observer: snapshotObserverFunc(func(SnapshotOutcome, time.Duration) { panic("metrics fault") }),
		Now:      func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	coverage, err := collector.Collect(context.Background(), readerScope(t, "workspace-a"))
	if err != nil || coverage.Reachable != 1 {
		t.Fatalf("Collect() coverage = %#v, error = %v", coverage, err)
	}
}

type snapshotObservation struct {
	outcome  SnapshotOutcome
	duration time.Duration
}

type recordingSnapshotObserver struct {
	events []snapshotObservation
}

func (observer *recordingSnapshotObserver) ObserveSpokeSnapshot(outcome SnapshotOutcome, duration time.Duration) {
	observer.events = append(observer.events, snapshotObservation{outcome: outcome, duration: duration})
}

type snapshotObserverFunc func(SnapshotOutcome, time.Duration)

func (function snapshotObserverFunc) ObserveSpokeSnapshot(outcome SnapshotOutcome, duration time.Duration) {
	function(outcome, duration)
}
