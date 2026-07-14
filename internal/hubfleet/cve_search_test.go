// SPDX-License-Identifier: Apache-2.0

package hubfleet

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ArdurAI/sith/internal/fleet"
	"github.com/ArdurAI/sith/internal/pep"
	"github.com/ArdurAI/sith/internal/tenancy"
)

func TestCVESearcherUsesExactImageFactAndClosedPEPVerb(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 14, 19, 0, 0, 0, time.UTC)
	querier := &recordingFleetQuerier{result: fleet.QueryResult{Coverage: fleet.Coverage{Requested: 1, Reachable: 1}}}
	searcher, err := NewCVESearcher(CVESearcherConfig{
		Querier: querier, PEP: testReadPEP(t), Freshness: time.Minute, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	digest := "sha256:" + strings.Repeat("a", 64)
	result, err := searcher.Search(context.Background(), readerScope(t, "workspace-a"), ImageSearchRequest{Digest: digest, Limit: 12})
	if err != nil || result.Coverage.Reachable != 1 {
		t.Fatalf("Search() result = %#v, error = %v", result, err)
	}
	if querier.scope.WorkspaceID() != "workspace-a" || len(querier.query.Kinds) != 1 || querier.query.Kinds[0] != fleet.FactCVE ||
		querier.query.Selector.ResourceKind != "Image" || querier.query.Selector.Image != digest || querier.query.Limit != 12 ||
		querier.freshness != time.Minute || !querier.now.Equal(now) {
		t.Fatalf("CVE query = %#v, freshness = %s, now = %s", querier.query, querier.freshness, querier.now)
	}
}

func TestCVESearcherRejectsUntrustedArgumentsBeforeQuery(t *testing.T) {
	t.Parallel()

	querier := &recordingFleetQuerier{}
	searcher, err := NewCVESearcher(CVESearcherConfig{Querier: querier, PEP: testReadPEP(t)})
	if err != nil {
		t.Fatal(err)
	}
	_, err = searcher.Search(context.Background(), readerScope(t, "workspace-a"), ImageSearchRequest{Digest: "registry.example/api:latest"})
	if err == nil || querier.calls != 0 {
		t.Fatalf("Search() error/calls = %v/%d", err, querier.calls)
	}
}

func TestCVESearcherUsesDedicatedPEPVerb(t *testing.T) {
	t.Parallel()

	var verb pep.Verb
	enforcer, err := pep.NewEnforcer(pep.Config{
		Hook: pep.HookFunc(func(_ context.Context, request pep.Request) (pep.Decision, error) {
			verb = request.Verb
			return pep.Decision{Verdict: pep.VerdictAllow, ReasonCode: "allow"}, nil
		}),
		Auditor: pep.AuditFunc(func(context.Context, pep.AuditEvent) error { return nil }),
	})
	if err != nil {
		t.Fatal(err)
	}
	searcher, err := NewCVESearcher(CVESearcherConfig{Querier: fleetQuerierFunc(func(context.Context, tenancy.Scope, fleet.Query, time.Duration, time.Time) (fleet.QueryResult, error) {
		return fleet.QueryResult{}, nil
	}), PEP: enforcer})
	if err != nil {
		t.Fatal(err)
	}
	_, err = searcher.Search(context.Background(), readerScope(t, "workspace-a"), ImageSearchRequest{Digest: "sha256:" + strings.Repeat("a", 64)})
	if err != nil || verb != pep.VerbFleetCVESearch {
		t.Fatalf("Search() error/verb = %v/%q", err, verb)
	}
}
