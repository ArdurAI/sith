// SPDX-License-Identifier: Apache-2.0

package hubfleet

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ArdurAI/sith/internal/fleet"
	"github.com/ArdurAI/sith/internal/pep"
	"github.com/ArdurAI/sith/internal/tenancy"
	"github.com/ArdurAI/sith/internal/tracing"
)

// FleetReader provides a tenant-scoped fleet snapshot from persisted spoke observations.
type FleetReader interface {
	ReadFleet(ctx context.Context, scope tenancy.Scope, freshness time.Duration, now time.Time) (fleet.FleetResult, error)
}

// FleetReadOutcome is the bounded self-observability result of one authorized fleet read. It
// intentionally carries no workspace, spoke, resource, selector, principal, trace, or raw error.
type FleetReadOutcome string

// Closed fleet-read outcomes.
const (
	FleetReadOutcomeComplete FleetReadOutcome = "complete"
	FleetReadOutcomeDegraded FleetReadOutcome = "degraded"
	FleetReadOutcomeEmpty    FleetReadOutcome = "empty"
	FleetReadOutcomeError    FleetReadOutcome = "error"
)

// Valid reports whether the outcome belongs to the closed fleet-read vocabulary.
func (outcome FleetReadOutcome) Valid() bool {
	switch outcome {
	case FleetReadOutcomeComplete, FleetReadOutcomeDegraded, FleetReadOutcomeEmpty, FleetReadOutcomeError:
		return true
	default:
		return false
	}
}

// FleetFreshnessOutcome is the bounded request-time freshness result of one authorized fleet
// read. It intentionally describes only the aggregate returned view and carries no workspace,
// spoke, resource, selector, principal, trace, age, or raw error.
type FleetFreshnessOutcome string

// Closed request-time fleet freshness outcomes.
const (
	FleetFreshnessOutcomeFresh   FleetFreshnessOutcome = "fresh"
	FleetFreshnessOutcomeStale   FleetFreshnessOutcome = "stale"
	FleetFreshnessOutcomeUnknown FleetFreshnessOutcome = "unknown"
	FleetFreshnessOutcomeEmpty   FleetFreshnessOutcome = "empty"
	FleetFreshnessOutcomeError   FleetFreshnessOutcome = "error"
)

// Valid reports whether the freshness outcome belongs to the closed vocabulary.
func (outcome FleetFreshnessOutcome) Valid() bool {
	switch outcome {
	case FleetFreshnessOutcomeFresh, FleetFreshnessOutcomeStale, FleetFreshnessOutcomeUnknown,
		FleetFreshnessOutcomeEmpty, FleetFreshnessOutcomeError:
		return true
	default:
		return false
	}
}

// FleetReadObservation is one privacy-bounded pair of aggregate coverage and request-time
// freshness outcomes for an authorized fleet read.
type FleetReadObservation struct {
	Outcome   FleetReadOutcome
	Freshness FleetFreshnessOutcome
}

// Valid reports whether both dimensions belong to their closed vocabularies.
func (observation FleetReadObservation) Valid() bool {
	if !observation.Outcome.Valid() || !observation.Freshness.Valid() {
		return false
	}
	switch observation.Outcome {
	case FleetReadOutcomeComplete:
		return observation.Freshness == FleetFreshnessOutcomeFresh
	case FleetReadOutcomeDegraded:
		return observation.Freshness == FleetFreshnessOutcomeStale ||
			observation.Freshness == FleetFreshnessOutcomeUnknown
	case FleetReadOutcomeEmpty:
		return observation.Freshness == FleetFreshnessOutcomeEmpty
	case FleetReadOutcomeError:
		return observation.Freshness == FleetFreshnessOutcomeError
	default:
		return false
	}
}

// FleetReadObserver receives one passive result for an authorized fleet read. Implementations
// must not block or mutate read behavior; Source isolates observer panics defensively.
type FleetReadObserver interface {
	ObserveFleetRead(FleetReadObservation)
}

// SourceConfig fixes a signed tenancy scope to a source-abstract hub reader.
type SourceConfig struct {
	Reader    FleetReader
	Scope     tenancy.Scope
	PEP       *pep.Enforcer
	Observer  FleetReadObserver
	Freshness time.Duration
	Now       func() time.Time
}

// Source adapts a tenant-scoped persisted OCM fleet to the common fleet.Source seam.
type Source struct {
	reader    FleetReader
	scope     tenancy.Scope
	pep       *pep.Enforcer
	observer  FleetReadObserver
	freshness time.Duration
	now       func() time.Time
}

var _ fleet.Source = (*Source)(nil)

// NewSource constructs an OCM-spoke source without exposing a raw database handle to callers.
func NewSource(config SourceConfig) (*Source, error) {
	if config.Reader == nil || config.Scope.WorkspaceID() == "" || config.PEP == nil {
		return nil, fmt.Errorf("new OCM spoke source: reader, workspace scope, and policy enforcer are required")
	}
	if err := config.Scope.Authorize(tenancy.ActionRead); err != nil {
		return nil, fmt.Errorf("new OCM spoke source: %w", err)
	}
	if config.Freshness == 0 {
		config.Freshness = defaultSnapshotAge
	}
	if config.Freshness < time.Second || config.Freshness > maxSnapshotAge {
		return nil, fmt.Errorf("new OCM spoke source: freshness must be between 1s and %s", maxSnapshotAge)
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &Source{
		reader: config.Reader, scope: config.Scope, pep: config.PEP, observer: config.Observer,
		freshness: config.Freshness, now: config.Now,
	}, nil
}

// Kind identifies OCM-brokered spoke snapshots at the common fleet.Source seam.
func (*Source) Kind() string { return SourceKind }

// Fleet returns the caller's tenant-scoped persisted spoke fleet.
func (source *Source) Fleet(ctx context.Context) (fleet.FleetResult, error) {
	if source == nil || source.reader == nil || source.pep == nil || ctx == nil {
		return fleet.FleetResult{}, fmt.Errorf("read OCM spoke fleet: source, policy enforcer, and context are required")
	}
	traceContext, _, err := tracing.Ensure(ctx)
	if err != nil {
		return fleet.FleetResult{}, fmt.Errorf("read OCM spoke fleet: establish trace context: %w", err)
	}
	ctx = traceContext
	if err := source.pep.AuthorizeRead(ctx, source.scope, pep.NewReadInput(pep.VerbFleetRead, nil)); err != nil {
		return fleet.FleetResult{}, fmt.Errorf("read OCM spoke fleet: %w", err)
	}
	result, err := source.reader.ReadFleet(ctx, source.scope, source.freshness, source.now().UTC())
	if err != nil {
		source.observeFleetRead(FleetReadObservation{
			Outcome: FleetReadOutcomeError, Freshness: FleetFreshnessOutcomeError,
		})
		return fleet.FleetResult{}, fmt.Errorf("read OCM spoke fleet: %w", err)
	}
	assessment := result.Coverage.Assessment()
	observation := FleetReadObservation{Freshness: fleetFreshnessOutcome(result, assessment)}
	switch {
	case !assessment.Complete || len(result.Clusters) != result.Coverage.Requested:
		observation.Outcome = FleetReadOutcomeDegraded
	case result.Coverage.Requested == 0:
		observation.Outcome = FleetReadOutcomeEmpty
	default:
		observation.Outcome = FleetReadOutcomeComplete
	}
	source.observeFleetRead(observation)
	return result, nil
}

func fleetFreshnessOutcome(result fleet.FleetResult, assessment fleet.CoverageAssessment) FleetFreshnessOutcome {
	observed, validClusters := observedFleetScopes(result)
	switch {
	case assessment.Inconsistent || !validClusters:
		return FleetFreshnessOutcomeUnknown
	case len(assessment.Stale) != 0:
		for _, scope := range assessment.Stale {
			if observedAt, exists := observed[scope]; !exists || observedAt.IsZero() {
				return FleetFreshnessOutcomeUnknown
			}
		}
		return FleetFreshnessOutcomeStale
	case result.Coverage.Requested == 0:
		return FleetFreshnessOutcomeEmpty
	case !assessment.Complete:
		return FleetFreshnessOutcomeUnknown
	}
	for _, observedAt := range observed {
		if observedAt.IsZero() {
			return FleetFreshnessOutcomeUnknown
		}
	}
	return FleetFreshnessOutcomeFresh
}

func observedFleetScopes(result fleet.FleetResult) (map[string]time.Time, bool) {
	if len(result.Clusters) != result.Coverage.Requested {
		return nil, false
	}
	observed := make(map[string]time.Time, len(result.Clusters))
	for _, cluster := range result.Clusters {
		if strings.TrimSpace(cluster.Name) == "" {
			return nil, false
		}
		if _, exists := observed[cluster.Name]; exists {
			return nil, false
		}
		observed[cluster.Name] = cluster.ObservedAt
	}
	return observed, true
}

func (source *Source) observeFleetRead(observation FleetReadObservation) {
	if source == nil || source.observer == nil || !observation.Valid() {
		return
	}
	defer func() {
		_ = recover()
	}()
	source.observer.ObserveFleetRead(observation)
}
