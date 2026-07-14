// SPDX-License-Identifier: Apache-2.0

package hubfleet

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ArdurAI/sith/internal/fleet"
	"github.com/ArdurAI/sith/internal/pep"
	"github.com/ArdurAI/sith/internal/tenancy"
)

func TestHubReadEntrypointsStopBeforeDependenciesWhenPolicyRefuses(t *testing.T) {
	scope := readerScope(t, "workspace-a")
	refusal := &policyRefusal{}

	store := &memoryStore{snapshots: make(map[string]Snapshot), failures: make(map[string]FailureKind)}
	collector, err := NewCollector(CollectorConfig{
		Store: store, PEP: refusal.enforcer(t),
		Transport: transportFunc(func(context.Context, tenancy.WorkspaceID, Spoke) (Snapshot, error) {
			return Snapshot{}, errors.New("transport must not run")
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := collector.Collect(context.Background(), scope); err == nil {
		t.Fatal("Collect() unexpectedly bypassed policy refusal")
	}
	if store.registeredCalls != 0 {
		t.Fatalf("collector reached registered-spoke store %d times after refusal", store.registeredCalls)
	}

	reader := &recordingFleetReader{}
	source, err := NewSource(SourceConfig{Reader: reader, Scope: scope, PEP: refusal.enforcer(t)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.Fleet(context.Background()); err == nil {
		t.Fatal("Fleet() unexpectedly bypassed policy refusal")
	}
	if reader.calls != 0 {
		t.Fatalf("source reached fleet reader %d times after refusal", reader.calls)
	}

	querier := &recordingFleetQuerier{}
	correlator, err := NewCorrelator(CorrelatorConfig{Querier: querier, PEP: refusal.enforcer(t), Freshness: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := correlator.Correlate(context.Background(), scope, CorrelationRequest{
		ResourceKind: "Deployment", Name: "payments", HealthNot: "Healthy",
	}); err == nil {
		t.Fatal("Correlate() unexpectedly bypassed policy refusal")
	}
	if querier.calls != 0 {
		t.Fatalf("correlator reached fleet query %d times after refusal", querier.calls)
	}

	imageSearcher, err := NewImageSearcher(ImageSearcherConfig{Querier: querier, PEP: refusal.enforcer(t)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := imageSearcher.Search(context.Background(), scope, ImageSearchRequest{Digest: "sha256:" + strings.Repeat("a", 64)}); err == nil {
		t.Fatal("Search() unexpectedly bypassed policy refusal")
	}
	if querier.calls != 0 {
		t.Fatalf("image search reached fleet query %d times after refusal", querier.calls)
	}
	if got, want := refusal.verbs, []pep.Verb{pep.VerbSpokeSnapshotRefresh, pep.VerbFleetRead, pep.VerbFleetCorrelate, pep.VerbFleetImageSearch}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] || got[3] != want[3] {
		t.Fatalf("policy verbs = %q, want %q", got, want)
	}
}

type policyRefusal struct {
	verbs []pep.Verb
}

func (refusal *policyRefusal) enforcer(t *testing.T) *pep.Enforcer {
	t.Helper()
	enforcer, err := pep.NewEnforcer(pep.Config{
		Hook: pep.HookFunc(func(_ context.Context, request pep.Request) (pep.Decision, error) {
			refusal.verbs = append(refusal.verbs, request.Verb)
			if request.ArgumentsDigest == "" {
				return pep.Decision{}, errors.New("policy request omitted argument digest")
			}
			return pep.Decision{Verdict: pep.VerdictDeny, ReasonCode: "test-deny"}, nil
		}),
		Auditor: pep.AuditFunc(func(context.Context, pep.AuditEvent) error { return nil }),
	})
	if err != nil {
		t.Fatal(err)
	}
	return enforcer
}

func TestHubReadConstructorsRequirePolicyEnforcer(t *testing.T) {
	store := &memoryStore{snapshots: make(map[string]Snapshot), failures: make(map[string]FailureKind)}
	if _, err := NewCollector(CollectorConfig{Store: store, Transport: transportFunc(func(context.Context, tenancy.WorkspaceID, Spoke) (Snapshot, error) {
		return Snapshot{}, nil
	})}); err == nil {
		t.Fatal("NewCollector() accepted no policy enforcer")
	}
	if _, err := NewCorrelator(CorrelatorConfig{Querier: fleetQuerierFunc(func(context.Context, tenancy.Scope, fleet.Query, time.Duration, time.Time) (fleet.QueryResult, error) {
		return fleet.QueryResult{}, nil
	})}); err == nil {
		t.Fatal("NewCorrelator() accepted no policy enforcer")
	}
	if _, err := NewSource(SourceConfig{Reader: fleetReaderFunc(func(context.Context, tenancy.Scope, time.Duration, time.Time) (fleet.FleetResult, error) {
		return fleet.FleetResult{}, nil
	}), Scope: readerScope(t, "workspace-a")}); err == nil {
		t.Fatal("NewSource() accepted no policy enforcer")
	}
}
