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

// CVESearcherConfig defines a read-only, tenant-scoped immutable image CVE evidence service.
type CVESearcherConfig struct {
	Querier   FleetQuerier
	PEP       *pep.Enforcer
	Freshness time.Duration
	Now       func() time.Time
}

// CVEIdentifierSearchRequest names one canonical CVE identifier across every registered spoke.
type CVEIdentifierSearchRequest struct {
	Identifier string `json:"identifier"`
	Limit      int    `json:"limit,omitempty"`
}

// CVESearcher resolves normalized CVE facts for one exact immutable runtime image digest.
type CVESearcher struct {
	querier   FleetQuerier
	pep       *pep.Enforcer
	freshness time.Duration
	now       func() time.Time
}

// NewCVESearcher constructs a bounded read-only CVE evidence service.
func NewCVESearcher(config CVESearcherConfig) (*CVESearcher, error) {
	if config.Querier == nil || config.PEP == nil {
		return nil, fmt.Errorf("new fleet CVE searcher: querier and policy enforcer are required")
	}
	if config.Freshness == 0 {
		config.Freshness = defaultSnapshotAge
	}
	if config.Freshness < time.Second || config.Freshness > maxSnapshotAge {
		return nil, fmt.Errorf("new fleet CVE searcher: freshness must be between 1s and %s", maxSnapshotAge)
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &CVESearcher{querier: config.Querier, pep: config.PEP, freshness: config.Freshness, now: config.Now}, nil
}

// Search returns coverage-honest reported CVE observations for one immutable runtime image.
func (searcher *CVESearcher) Search(
	ctx context.Context,
	scope tenancy.Scope,
	request ImageSearchRequest,
) (fleet.QueryResult, error) {
	if searcher == nil || searcher.querier == nil || searcher.pep == nil || ctx == nil {
		return fleet.QueryResult{}, fmt.Errorf("search fleet CVEs: searcher, policy enforcer, and context are required")
	}
	traceContext, _, err := tracing.Ensure(ctx)
	if err != nil {
		return fleet.QueryResult{}, fmt.Errorf("search fleet CVEs: establish trace context: %w", err)
	}
	ctx = traceContext
	if err := scope.Authorize(tenancy.ActionRead); err != nil {
		return fleet.QueryResult{}, fmt.Errorf("search fleet CVEs: %w", err)
	}
	if err := request.validate(); err != nil {
		return fleet.QueryResult{}, fmt.Errorf("search fleet CVEs: %w", err)
	}
	canonicalArguments := request.Digest + "\x00" + strconv.Itoa(request.Limit)
	if err := searcher.pep.AuthorizeRead(ctx, scope, pep.NewReadInput(pep.VerbFleetCVESearch, []byte(canonicalArguments))); err != nil {
		return fleet.QueryResult{}, fmt.Errorf("search fleet CVEs: %w", err)
	}
	result, err := searcher.querier.QueryFleet(ctx, scope, fleet.Query{
		Kinds: []fleet.FactKind{fleet.FactCVE},
		Selector: fleet.Selector{
			ResourceKind: "Image",
			Image:        request.Digest,
		},
		Limit: request.Limit,
	}, searcher.freshness, searcher.now().UTC())
	if err != nil {
		return fleet.QueryResult{}, fmt.Errorf("search fleet CVEs: %w", err)
	}
	return result, nil
}

// SearchByIdentifier returns coverage-honest runtime-proven image observations for one exact CVE.
func (searcher *CVESearcher) SearchByIdentifier(
	ctx context.Context,
	scope tenancy.Scope,
	request CVEIdentifierSearchRequest,
) (fleet.QueryResult, error) {
	if searcher == nil || searcher.querier == nil || searcher.pep == nil || ctx == nil {
		return fleet.QueryResult{}, fmt.Errorf("search fleet CVE identifier: searcher, policy enforcer, and context are required")
	}
	traceContext, _, err := tracing.Ensure(ctx)
	if err != nil {
		return fleet.QueryResult{}, fmt.Errorf("search fleet CVE identifier: establish trace context: %w", err)
	}
	ctx = traceContext
	if err := scope.Authorize(tenancy.ActionRead); err != nil {
		return fleet.QueryResult{}, fmt.Errorf("search fleet CVE identifier: %w", err)
	}
	if err := request.validate(); err != nil {
		return fleet.QueryResult{}, fmt.Errorf("search fleet CVE identifier: %w", err)
	}
	canonicalArguments := request.Identifier + "\x00" + strconv.Itoa(request.Limit)
	if err := searcher.pep.AuthorizeRead(ctx, scope, pep.NewReadInput(pep.VerbFleetCVEIdentifierSearch, []byte(canonicalArguments))); err != nil {
		return fleet.QueryResult{}, fmt.Errorf("search fleet CVE identifier: %w", err)
	}
	result, err := searcher.querier.QueryFleet(ctx, scope, fleet.Query{
		Kinds: []fleet.FactKind{fleet.FactCVE},
		Selector: fleet.Selector{
			ResourceKind: "Image",
			CVE:          request.Identifier,
		},
		Limit: request.Limit,
	}, searcher.freshness, searcher.now().UTC())
	if err != nil {
		return fleet.QueryResult{}, fmt.Errorf("search fleet CVE identifier: %w", err)
	}
	return result, nil
}

func (request CVEIdentifierSearchRequest) validate() error {
	canonical, err := fleet.NormalizeCVEIdentifier(request.Identifier)
	if err != nil || canonical != request.Identifier {
		return fmt.Errorf("CVE identifier must be canonical")
	}
	if request.Limit < 0 || request.Limit > 1_000 {
		return fmt.Errorf("limit must be between 0 and 1000")
	}
	return nil
}
