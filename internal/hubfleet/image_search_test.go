// SPDX-License-Identifier: Apache-2.0

package hubfleet

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ArdurAI/sith/internal/fleet"
	"github.com/ArdurAI/sith/internal/tenancy"
)

func TestImageSearcherUsesExactTenantScopedPodInventoryQuery(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 14, 15, 0, 0, 0, time.UTC)
	digest := "sha256:" + strings.Repeat("a", 64)
	querier := &recordingFleetQuerier{result: fleet.QueryResult{Facts: []fleet.Fact{{
		Evidence:  fleet.Evidence{Ref: fleet.ResourceRef{Scope: "spoke-b", Kind: "Pod", Name: "payments"}},
		Workspace: "workspace-a",
	}}}}
	searcher, err := NewImageSearcher(ImageSearcherConfig{
		Querier: querier, PEP: testReadPEP(t), Freshness: time.Minute, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := searcher.Search(context.Background(), readerScope(t, "workspace-a"), ImageSearchRequest{Digest: digest, Limit: 12})
	if err != nil || len(result.Facts) != 1 {
		t.Fatalf("Search() result = %#v, error = %v", result, err)
	}
	if querier.scope.WorkspaceID() != "workspace-a" || querier.query.Kinds[0] != fleet.FactInventory ||
		querier.query.Selector.ResourceKind != "Pod" || querier.query.Selector.Image != digest ||
		querier.query.Selector.Name != "" || querier.query.Selector.Namespace != "" || querier.query.Limit != 12 ||
		querier.freshness != time.Minute || !querier.now.Equal(now) {
		t.Fatalf("querier call = %#v, freshness = %s, now = %s", querier.query, querier.freshness, querier.now)
	}
}

func TestImageSearcherRejectsUnsafeRequestsBeforeQuery(t *testing.T) {
	t.Parallel()

	searcher, err := NewImageSearcher(ImageSearcherConfig{
		Querier: fleetQuerierFunc(func(context.Context, tenancy.Scope, fleet.Query, time.Duration, time.Time) (fleet.QueryResult, error) {
			return fleet.QueryResult{}, errors.New("unexpected query")
		}),
		PEP: testReadPEP(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range []ImageSearchRequest{
		{Digest: "registry.example/payments:latest"},
		{Digest: "sha256:" + strings.Repeat("A", 64)},
		{Digest: "sha256:" + strings.Repeat("a", 64), Limit: 1_001},
	} {
		if _, err := searcher.Search(context.Background(), readerScope(t, "workspace-a"), request); err == nil {
			t.Fatalf("Search(%#v) unexpectedly succeeded", request)
		}
	}
}

func TestNewImageSearcherRejectsUnsafeConfiguration(t *testing.T) {
	t.Parallel()

	if _, err := NewImageSearcher(ImageSearcherConfig{}); err == nil {
		t.Fatal("NewImageSearcher accepted missing dependencies")
	}
}
