// SPDX-License-Identifier: Apache-2.0

package hubfleet

import "time"

// SnapshotOutcome is the bounded self-observability result of one spoke snapshot attempt. It does
// not include spoke, workspace, endpoint, token, raw error, or snapshot data.
type SnapshotOutcome string

// Closed snapshot-observability outcomes.
const (
	SnapshotOutcomeSuccess         SnapshotOutcome = "success"
	SnapshotOutcomeTransport       SnapshotOutcome = "transport"
	SnapshotOutcomeDeadline        SnapshotOutcome = "deadline"
	SnapshotOutcomeInvalidSnapshot SnapshotOutcome = "invalid-snapshot"
	SnapshotOutcomeStoreError      SnapshotOutcome = "store-error"
	SnapshotOutcomeCanceled        SnapshotOutcome = "canceled"
)

// SnapshotObserver receives passive, bounded measurements for completed snapshot attempts.
// Implementations must not block or mutate collection behavior. The collector isolates observer
// panics defensively.
type SnapshotObserver interface {
	ObserveSpokeSnapshot(outcome SnapshotOutcome, duration time.Duration)
}

type noopSnapshotObserver struct{}

func (noopSnapshotObserver) ObserveSpokeSnapshot(SnapshotOutcome, time.Duration) {}

func (collector *Collector) observeSnapshot(outcome SnapshotOutcome, duration time.Duration) {
	if collector == nil || collector.observer == nil {
		return
	}
	defer func() {
		_ = recover()
	}()
	collector.observer.ObserveSpokeSnapshot(outcome, duration)
}

func snapshotOutcomeForFailure(failure FailureKind) SnapshotOutcome {
	switch failure {
	case FailureDeadline:
		return SnapshotOutcomeDeadline
	case FailureInvalidSnapshot:
		return SnapshotOutcomeInvalidSnapshot
	default:
		return SnapshotOutcomeTransport
	}
}
