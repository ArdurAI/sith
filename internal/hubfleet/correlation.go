// SPDX-License-Identifier: Apache-2.0

package hubfleet

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ArdurAI/sith/internal/fleet"
	"github.com/ArdurAI/sith/internal/tenancy"
)

// FleetQuerier is the narrow tenant-scoped read seam used by cross-cluster correlation.
type FleetQuerier interface {
	QueryFleet(
		ctx context.Context,
		scope tenancy.Scope,
		query fleet.Query,
		freshness time.Duration,
		now time.Time,
	) (fleet.QueryResult, error)
}

// CorrelationRequest names one exact resource condition across every registered spoke.
type CorrelationRequest struct {
	ResourceKind string `json:"resource_kind"`
	Name         string `json:"name"`
	Namespace    string `json:"namespace,omitempty"`
	HealthNot    string `json:"health_not"`
	Limit        int    `json:"limit,omitempty"`
}

// CorrelatorConfig defines a read-only, tenant-scoped correlation service.
type CorrelatorConfig struct {
	Querier   FleetQuerier
	Freshness time.Duration
	Now       func() time.Time
}

// Correlator resolves one exact health condition across the normalized multi-spoke fleet.
type Correlator struct {
	querier   FleetQuerier
	freshness time.Duration
	now       func() time.Time
}

// NewCorrelator constructs a bounded read-only correlation service.
func NewCorrelator(config CorrelatorConfig) (*Correlator, error) {
	if config.Querier == nil {
		return nil, fmt.Errorf("new fleet correlator: querier is required")
	}
	if config.Freshness == 0 {
		config.Freshness = defaultSnapshotAge
	}
	if config.Freshness < time.Second || config.Freshness > maxSnapshotAge {
		return nil, fmt.Errorf("new fleet correlator: freshness must be between 1s and %s", maxSnapshotAge)
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &Correlator{querier: config.Querier, freshness: config.Freshness, now: config.Now}, nil
}

// Correlate answers one coverage-honest, exact resource health question in the signed workspace.
func (correlator *Correlator) Correlate(
	ctx context.Context,
	scope tenancy.Scope,
	request CorrelationRequest,
) (fleet.QueryResult, error) {
	if correlator == nil || correlator.querier == nil || ctx == nil {
		return fleet.QueryResult{}, fmt.Errorf("correlate fleet: correlator and context are required")
	}
	if err := scope.Authorize(tenancy.ActionRead); err != nil {
		return fleet.QueryResult{}, fmt.Errorf("correlate fleet: %w", err)
	}
	if err := request.validate(); err != nil {
		return fleet.QueryResult{}, fmt.Errorf("correlate fleet: %w", err)
	}
	result, err := correlator.querier.QueryFleet(ctx, scope, fleet.Query{
		Kinds: []fleet.FactKind{fleet.FactHealth},
		Selector: fleet.Selector{
			ResourceKind: request.ResourceKind,
			Name:         request.Name,
			Namespace:    request.Namespace,
			HealthNot:    request.HealthNot,
		},
		Limit: request.Limit,
	}, correlator.freshness, correlator.now().UTC())
	if err != nil {
		return fleet.QueryResult{}, fmt.Errorf("correlate fleet: %w", err)
	}
	return result, nil
}

func (request CorrelationRequest) validate() error {
	for _, field := range []struct {
		name    string
		value   string
		maximum int
		empty   bool
	}{
		{name: "resource kind", value: request.ResourceKind, maximum: 128},
		{name: "resource name", value: request.Name, maximum: 256},
		{name: "resource namespace", value: request.Namespace, maximum: 256, empty: true},
	} {
		if err := validateText(field.name, field.value, field.maximum, field.empty); err != nil {
			return err
		}
	}
	if err := (fleet.Query{Kinds: []fleet.FactKind{fleet.FactHealth}, Selector: fleet.Selector{HealthNot: request.HealthNot}}).Validate(); err != nil {
		return fmt.Errorf("health-not condition must be one supported health status: %w", err)
	}
	if request.Limit < 0 || request.Limit > 1_000 {
		return fmt.Errorf("limit must be between 0 and 1000")
	}
	if strings.EqualFold(request.ResourceKind, "secret") {
		return fmt.Errorf("secret correlation is not allowed")
	}
	return nil
}
