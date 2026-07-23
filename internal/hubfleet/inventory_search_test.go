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

func TestInventorySearcherUsesExactTenantScopedInventoryQuery(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 17, 18, 0, 0, 0, time.UTC)
	querier := &recordingFleetQuerier{result: fleet.QueryResult{Facts: []fleet.Fact{{
		Evidence: fleet.Evidence{Ref: fleet.ResourceRef{Scope: "spoke-a", Kind: "Deployment", Name: "payments"}}, Workspace: "workspace-a",
	}}}}
	searcher, err := NewInventorySearcher(InventorySearcherConfig{Querier: querier, PEP: testReadPEP(t), Freshness: time.Minute, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	result, err := searcher.Search(context.Background(), readerScope(t, "workspace-a"), InventorySearchRequest{
		ResourceKind: "Deployment", Namespace: "payments", Name: "payments", Limit: 257,
	})
	if err != nil || len(result.Facts) != 1 {
		t.Fatalf("Search() result = %#v, error = %v", result, err)
	}
	if querier.scope.WorkspaceID() != "workspace-a" || len(querier.query.Kinds) != 1 || querier.query.Kinds[0] != fleet.FactInventory ||
		querier.query.Selector.ResourceKind != "Deployment" || querier.query.Selector.Namespace != "payments" ||
		querier.query.Selector.Name != "payments" || querier.query.Selector.NamePrefix != "" || len(querier.query.Selector.Labels) != 0 ||
		querier.query.Limit != 257 || querier.freshness != time.Minute || !querier.now.Equal(now) {
		t.Fatalf("querier call = %#v, freshness = %s, now = %s", querier.query, querier.freshness, querier.now)
	}
}

func TestInventorySearcherRejectsUnsafeRequestsBeforeQuery(t *testing.T) {
	t.Parallel()
	calls := 0
	searcher, err := NewInventorySearcher(InventorySearcherConfig{
		Querier: fleetQuerierFunc(func(context.Context, tenancy.Scope, fleet.Query, time.Duration, time.Time) (fleet.QueryResult, error) {
			calls++
			return fleet.QueryResult{}, errors.New("unexpected query")
		}),
		PEP: testReadPEP(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range []InventorySearchRequest{
		{}, {ResourceKind: "Secret"}, {ResourceKind: "Service"},
		{ResourceKind: "Deployment", Namespace: " payments"}, {ResourceKind: "Pod", Name: "api", Limit: 1_001},
	} {
		if _, err := searcher.Search(context.Background(), readerScope(t, "workspace-a"), request); err == nil {
			t.Fatalf("Search(%#v) unexpectedly succeeded", request)
		}
	}
	if calls != 0 {
		t.Fatalf("invalid requests reached fleet query %d times", calls)
	}
}

func TestNewInventorySearcherRejectsUnsafeConfiguration(t *testing.T) {
	t.Parallel()
	if _, err := NewInventorySearcher(InventorySearcherConfig{}); err == nil {
		t.Fatal("NewInventorySearcher accepted missing dependencies")
	}
	if _, err := NewInventorySearcher(InventorySearcherConfig{Querier: &recordingFleetQuerier{}, PEP: testReadPEP(t), Freshness: time.Hour + time.Second}); err == nil {
		t.Fatal("NewInventorySearcher accepted excessive freshness")
	}
}
