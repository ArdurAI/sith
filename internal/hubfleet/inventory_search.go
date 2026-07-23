// SPDX-License-Identifier: Apache-2.0

package hubfleet

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/ArdurAI/sith/internal/fleet"
	"github.com/ArdurAI/sith/internal/pep"
	"github.com/ArdurAI/sith/internal/tenancy"
	"github.com/ArdurAI/sith/internal/tracing"
)

// InventorySearchRequest selects one closed normalized OCM resource kind, optionally narrowed by
// an exact namespace and exact name. It deliberately excludes prefixes, labels, and raw selectors.
type InventorySearchRequest struct {
	ResourceKind string `json:"resource_kind"`
	Namespace    string `json:"namespace,omitempty"`
	Name         string `json:"name,omitempty"`
	Limit        int    `json:"limit,omitempty"`
}

// InventorySearcherConfig defines a read-only, tenant-scoped normalized inventory service.
type InventorySearcherConfig struct {
	Querier   FleetQuerier
	PEP       *pep.Enforcer
	Freshness time.Duration
	Now       func() time.Time
}

// InventorySearcher resolves bounded normalized OCM inventory from persisted Hub state.
type InventorySearcher struct {
	querier   FleetQuerier
	pep       *pep.Enforcer
	freshness time.Duration
	now       func() time.Time
}

// NewInventorySearcher constructs a bounded read-only inventory service.
func NewInventorySearcher(config InventorySearcherConfig) (*InventorySearcher, error) {
	if config.Querier == nil || config.PEP == nil {
		return nil, fmt.Errorf("new fleet inventory searcher: querier and policy enforcer are required")
	}
	if config.Freshness == 0 {
		config.Freshness = defaultSnapshotAge
	}
	if config.Freshness < time.Second || config.Freshness > maxSnapshotAge {
		return nil, fmt.Errorf("new fleet inventory searcher: freshness must be between 1s and %s", maxSnapshotAge)
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &InventorySearcher{querier: config.Querier, pep: config.PEP, freshness: config.Freshness, now: config.Now}, nil
}

// Search returns one coverage-honest persisted inventory selection in the signed workspace.
func (searcher *InventorySearcher) Search(ctx context.Context, scope tenancy.Scope, request InventorySearchRequest) (fleet.QueryResult, error) {
	if searcher == nil || searcher.querier == nil || searcher.pep == nil || ctx == nil {
		return fleet.QueryResult{}, fmt.Errorf("search fleet inventory: searcher, policy enforcer, and context are required")
	}
	traceContext, _, err := tracing.Ensure(ctx)
	if err != nil {
		return fleet.QueryResult{}, fmt.Errorf("search fleet inventory: establish trace context: %w", err)
	}
	ctx = traceContext
	if err := scope.Authorize(tenancy.ActionRead); err != nil {
		return fleet.QueryResult{}, fmt.Errorf("search fleet inventory: %w", err)
	}
	if err := request.Validate(); err != nil {
		return fleet.QueryResult{}, fmt.Errorf("search fleet inventory: %w", err)
	}
	canonicalArguments := strings.Join([]string{request.ResourceKind, request.Namespace, request.Name, strconv.Itoa(request.Limit)}, "\x00")
	if err := searcher.pep.AuthorizeRead(ctx, scope, pep.NewReadInput(pep.VerbFleetInventorySearch, []byte(canonicalArguments))); err != nil {
		return fleet.QueryResult{}, fmt.Errorf("search fleet inventory: %w", err)
	}
	result, err := searcher.querier.QueryFleet(ctx, scope, fleet.Query{
		Kinds:    []fleet.FactKind{fleet.FactInventory},
		Selector: fleet.Selector{ResourceKind: request.ResourceKind, Namespace: request.Namespace, Name: request.Name},
		Limit:    request.Limit,
	}, searcher.freshness, searcher.now().UTC())
	if err != nil {
		return fleet.QueryResult{}, fmt.Errorf("search fleet inventory: %w", err)
	}
	return result, nil
}

// Validate rejects malformed or unsupported inventory inputs before policy or storage access.
func (request InventorySearchRequest) Validate() error {
	for _, field := range []struct {
		name    string
		value   string
		maximum int
		empty   bool
	}{
		{name: "resource kind", value: request.ResourceKind, maximum: 128},
		{name: "resource namespace", value: request.Namespace, maximum: 256, empty: true},
		{name: "resource name", value: request.Name, maximum: 256, empty: true},
	} {
		if err := validateText(field.name, field.value, field.maximum, field.empty); err != nil {
			return err
		}
	}
	switch request.ResourceKind {
	case "Deployment", "Pod", "Rollout":
	default:
		return fmt.Errorf("resource kind is not supported for Hub inventory")
	}
	if request.Limit < 0 || request.Limit > 1_000 {
		return fmt.Errorf("limit must be between 0 and 1000")
	}
	return nil
}
