// SPDX-License-Identifier: Apache-2.0

package hubfleet

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/ArdurAI/sith/internal/fleet"
	"github.com/ArdurAI/sith/internal/tenancy"
	"github.com/ArdurAI/sith/internal/tracing"
)

var errRefreshFlightPanicked = errors.New("collect spoke snapshots: refresh flight panicked")

type refreshFlight struct {
	done     chan struct{}
	coverage fleet.Coverage
	err      error
}

type refreshCoordinator struct {
	mu      sync.Mutex
	flights map[tenancy.WorkspaceID]*refreshFlight
}

func newRefreshCoordinator() *refreshCoordinator {
	return &refreshCoordinator{flights: make(map[tenancy.WorkspaceID]*refreshFlight)}
}

func (coordinator *refreshCoordinator) collect(
	ctx context.Context,
	scope tenancy.Scope,
	collect func(context.Context, tenancy.Scope) (fleet.Coverage, error),
) (fleet.Coverage, error) {
	if coordinator == nil || ctx == nil || collect == nil {
		return fleet.Coverage{}, errors.New("collect spoke snapshots: refresh coordinator, context, and collector are required")
	}
	if err := ctx.Err(); err != nil {
		return fleet.Coverage{}, fmt.Errorf("collect spoke snapshots: %w", err)
	}

	workspaceID := scope.WorkspaceID()
	coordinator.mu.Lock()
	flight, exists := coordinator.flights[workspaceID]
	if !exists {
		flight = &refreshFlight{done: make(chan struct{})}
		coordinator.flights[workspaceID] = flight
		//nolint:gosec // A flight must outlive one caller; run supplies a fresh value-free context.
		go coordinator.run(workspaceID, flight, scope, collect)
	}
	coordinator.mu.Unlock()

	return waitForRefresh(ctx, flight)
}

func (coordinator *refreshCoordinator) run(
	workspaceID tenancy.WorkspaceID,
	flight *refreshFlight,
	scope tenancy.Scope,
	collect func(context.Context, tenancy.Scope) (fleet.Coverage, error),
) {
	coverage := fleet.Coverage{}
	var err error
	defer func() {
		if recover() != nil {
			coverage = fleet.Coverage{}
			err = errRefreshFlightPanicked
		}
		coordinator.finish(workspaceID, flight, coverage, err)
	}()

	flightContext, _, traceErr := tracing.Ensure(context.Background())
	if traceErr != nil {
		err = fmt.Errorf("collect spoke snapshots: establish refresh flight trace: %w", traceErr)
		return
	}
	coverage, err = collect(flightContext, scope)
}

func (coordinator *refreshCoordinator) finish(
	workspaceID tenancy.WorkspaceID,
	flight *refreshFlight,
	coverage fleet.Coverage,
	err error,
) {
	coordinator.mu.Lock()
	flight.coverage = cloneCoverage(coverage)
	flight.err = err
	if coordinator.flights[workspaceID] == flight {
		delete(coordinator.flights, workspaceID)
	}
	close(flight.done)
	coordinator.mu.Unlock()
}

func (coordinator *refreshCoordinator) activeFlights() int {
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	return len(coordinator.flights)
}

func waitForRefresh(ctx context.Context, flight *refreshFlight) (fleet.Coverage, error) {
	select {
	case <-flight.done:
		return cloneCoverage(flight.coverage), flight.err
	default:
	}

	select {
	case <-flight.done:
		return cloneCoverage(flight.coverage), flight.err
	case <-ctx.Done():
		select {
		case <-flight.done:
			return cloneCoverage(flight.coverage), flight.err
		default:
			return fleet.Coverage{}, fmt.Errorf("collect spoke snapshots: %w", ctx.Err())
		}
	}
}

func cloneCoverage(coverage fleet.Coverage) fleet.Coverage {
	coverage.Unreachable = append([]string(nil), coverage.Unreachable...)
	coverage.Stale = append([]string(nil), coverage.Stale...)
	coverage.Truncated = append([]string(nil), coverage.Truncated...)
	return coverage
}
