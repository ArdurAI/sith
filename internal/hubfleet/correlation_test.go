// SPDX-License-Identifier: Apache-2.0

package hubfleet

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ArdurAI/sith/internal/fleet"
	"github.com/ArdurAI/sith/internal/tenancy"
)

func TestCorrelatorUsesExactTenantScopedHealthQuery(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 12, 15, 0, 0, 0, time.UTC)
	querier := &recordingFleetQuerier{result: fleet.QueryResult{Facts: []fleet.Fact{{
		Evidence:  fleet.Evidence{Ref: fleet.ResourceRef{Scope: "spoke-b", Kind: "Deployment", Name: "payments"}},
		Workspace: "workspace-a",
	}}}}
	correlator, err := NewCorrelator(CorrelatorConfig{Querier: querier, PEP: testReadPEP(t), Freshness: time.Minute, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	result, err := correlator.Correlate(context.Background(), readerScope(t, "workspace-a"), CorrelationRequest{
		ResourceKind: "Deployment", Name: "payments", Namespace: "payments", HealthNot: "Healthy", Limit: 12,
	})
	if err != nil || len(result.Facts) != 1 {
		t.Fatalf("Correlate() result = %#v, error = %v", result, err)
	}
	if querier.scope.WorkspaceID() != "workspace-a" || querier.query.Kinds[0] != fleet.FactHealth ||
		querier.query.Selector.ResourceKind != "Deployment" || querier.query.Selector.Name != "payments" ||
		querier.query.Selector.Namespace != "payments" || querier.query.Selector.HealthNot != "Healthy" ||
		querier.query.Limit != 12 || querier.freshness != time.Minute || !querier.now.Equal(now) {
		t.Fatalf("querier call = %#v, freshness = %s, now = %s", querier.query, querier.freshness, querier.now)
	}
}

func TestCorrelatorRejectsUnsafeRequests(t *testing.T) {
	t.Parallel()

	correlator, err := NewCorrelator(CorrelatorConfig{Querier: fleetQuerierFunc(func(context.Context, tenancy.Scope, fleet.Query, time.Duration, time.Time) (fleet.QueryResult, error) {
		return fleet.QueryResult{}, errors.New("unexpected query")
	}), PEP: testReadPEP(t)})
	if err != nil {
		t.Fatal(err)
	}
	scope := readerScope(t, "workspace-a")
	tests := []CorrelationRequest{
		{ResourceKind: "Deployment", Name: "payments", HealthNot: "Broken"},
		{ResourceKind: "Secret", Name: "credentials", HealthNot: "Healthy"},
		{ResourceKind: "Deployment", Name: "payments", HealthNot: "Healthy", Limit: 1_001},
	}
	for _, request := range tests {
		if _, err := correlator.Correlate(context.Background(), scope, request); err == nil {
			t.Fatalf("Correlate(%#v) unexpectedly succeeded", request)
		}
	}
}

type fleetQuerierFunc func(context.Context, tenancy.Scope, fleet.Query, time.Duration, time.Time) (fleet.QueryResult, error)

func (function fleetQuerierFunc) QueryFleet(
	ctx context.Context,
	scope tenancy.Scope,
	query fleet.Query,
	freshness time.Duration,
	now time.Time,
) (fleet.QueryResult, error) {
	return function(ctx, scope, query, freshness, now)
}

type recordingFleetQuerier struct {
	result    fleet.QueryResult
	scope     tenancy.Scope
	query     fleet.Query
	freshness time.Duration
	now       time.Time
	calls     int
}

func (querier *recordingFleetQuerier) QueryFleet(
	_ context.Context,
	scope tenancy.Scope,
	query fleet.Query,
	freshness time.Duration,
	now time.Time,
) (fleet.QueryResult, error) {
	querier.calls++
	querier.scope = scope
	querier.query = query
	querier.freshness = freshness
	querier.now = now
	return querier.result, nil
}
