// SPDX-License-Identifier: Apache-2.0

package hubfleet

import (
	"context"
	"fmt"
	"time"

	"github.com/ArdurAI/sith/internal/fleet"
	"github.com/ArdurAI/sith/internal/pep"
	"github.com/ArdurAI/sith/internal/tenancy"
)

// FleetReader provides a tenant-scoped fleet snapshot from persisted spoke observations.
type FleetReader interface {
	ReadFleet(ctx context.Context, scope tenancy.Scope, freshness time.Duration, now time.Time) (fleet.FleetResult, error)
}

// SourceConfig fixes a signed tenancy scope to a source-abstract hub reader.
type SourceConfig struct {
	Reader    FleetReader
	Scope     tenancy.Scope
	PEP       *pep.Enforcer
	Freshness time.Duration
	Now       func() time.Time
}

// Source adapts a tenant-scoped persisted OCM fleet to the common fleet.Source seam.
type Source struct {
	reader    FleetReader
	scope     tenancy.Scope
	pep       *pep.Enforcer
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
	return &Source{reader: config.Reader, scope: config.Scope, pep: config.PEP, freshness: config.Freshness, now: config.Now}, nil
}

// Kind identifies OCM-brokered spoke snapshots at the common fleet.Source seam.
func (*Source) Kind() string { return SourceKind }

// Fleet returns the caller's tenant-scoped persisted spoke fleet.
func (source *Source) Fleet(ctx context.Context) (fleet.FleetResult, error) {
	if source == nil || source.reader == nil || source.pep == nil || ctx == nil {
		return fleet.FleetResult{}, fmt.Errorf("read OCM spoke fleet: source, policy enforcer, and context are required")
	}
	if err := source.pep.AuthorizeRead(ctx, source.scope, pep.NewReadInput(pep.VerbFleetRead, nil)); err != nil {
		return fleet.FleetResult{}, fmt.Errorf("read OCM spoke fleet: %w", err)
	}
	result, err := source.reader.ReadFleet(ctx, source.scope, source.freshness, source.now().UTC())
	if err != nil {
		return fleet.FleetResult{}, fmt.Errorf("read OCM spoke fleet: %w", err)
	}
	return result, nil
}
