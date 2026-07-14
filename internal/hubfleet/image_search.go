// SPDX-License-Identifier: Apache-2.0

package hubfleet

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/ArdurAI/sith/internal/fleet"
	"github.com/ArdurAI/sith/internal/pep"
	"github.com/ArdurAI/sith/internal/tenancy"
	"github.com/ArdurAI/sith/internal/tracing"
)

// ImageSearchRequest names one immutable runtime image digest across every registered spoke.
type ImageSearchRequest struct {
	Digest string `json:"digest"`
	Limit  int    `json:"limit,omitempty"`
}

// ImageSearcherConfig defines a read-only, tenant-scoped immutable image evidence service.
type ImageSearcherConfig struct {
	Querier   FleetQuerier
	PEP       *pep.Enforcer
	Freshness time.Duration
	Now       func() time.Time
}

// ImageSearcher resolves one exact immutable image digest across normalized Pod inventory.
type ImageSearcher struct {
	querier   FleetQuerier
	pep       *pep.Enforcer
	freshness time.Duration
	now       func() time.Time
}

// NewImageSearcher constructs a bounded read-only immutable image evidence service.
func NewImageSearcher(config ImageSearcherConfig) (*ImageSearcher, error) {
	if config.Querier == nil || config.PEP == nil {
		return nil, fmt.Errorf("new fleet image searcher: querier and policy enforcer are required")
	}
	if config.Freshness == 0 {
		config.Freshness = defaultSnapshotAge
	}
	if config.Freshness < time.Second || config.Freshness > maxSnapshotAge {
		return nil, fmt.Errorf("new fleet image searcher: freshness must be between 1s and %s", maxSnapshotAge)
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &ImageSearcher{querier: config.Querier, pep: config.PEP, freshness: config.Freshness, now: config.Now}, nil
}

// Search returns coverage-honest Pod inventory containing the requested immutable digest.
func (searcher *ImageSearcher) Search(
	ctx context.Context,
	scope tenancy.Scope,
	request ImageSearchRequest,
) (fleet.QueryResult, error) {
	if searcher == nil || searcher.querier == nil || searcher.pep == nil || ctx == nil {
		return fleet.QueryResult{}, fmt.Errorf("search fleet image: searcher, policy enforcer, and context are required")
	}
	traceContext, _, err := tracing.Ensure(ctx)
	if err != nil {
		return fleet.QueryResult{}, fmt.Errorf("search fleet image: establish trace context: %w", err)
	}
	ctx = traceContext
	if err := scope.Authorize(tenancy.ActionRead); err != nil {
		return fleet.QueryResult{}, fmt.Errorf("search fleet image: %w", err)
	}
	if err := request.validate(); err != nil {
		return fleet.QueryResult{}, fmt.Errorf("search fleet image: %w", err)
	}
	canonicalArguments := request.Digest + "\x00" + strconv.Itoa(request.Limit)
	if err := searcher.pep.AuthorizeRead(ctx, scope, pep.NewReadInput(pep.VerbFleetImageSearch, []byte(canonicalArguments))); err != nil {
		return fleet.QueryResult{}, fmt.Errorf("search fleet image: %w", err)
	}
	result, err := searcher.querier.QueryFleet(ctx, scope, fleet.Query{
		Kinds: []fleet.FactKind{fleet.FactInventory},
		Selector: fleet.Selector{
			ResourceKind: "Pod",
			Image:        request.Digest,
		},
		Limit: request.Limit,
	}, searcher.freshness, searcher.now().UTC())
	if err != nil {
		return fleet.QueryResult{}, fmt.Errorf("search fleet image: %w", err)
	}
	return result, nil
}

func (request ImageSearchRequest) validate() error {
	if err := fleet.ValidateImageDigest(request.Digest); err != nil {
		return fmt.Errorf("image digest: %w", err)
	}
	if request.Limit < 0 || request.Limit > 1_000 {
		return fmt.Errorf("limit must be between 0 and 1000")
	}
	return nil
}
