// SPDX-License-Identifier: Apache-2.0

package fleet

import (
	"slices"
	"testing"
	"time"
)

const testGraphDigest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestNewGraphScopesNamesAndCorrelatesOnlyExactDigests(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 12, 12, 0, 0, 0, time.UTC)
	entity := func(cluster, namespace string) *EntityRef {
		return &EntityRef{Cluster: cluster, Namespace: namespace, Kind: "Deployment", Name: "payments", ImageDigest: testGraphDigest}
	}
	graph, err := NewGraph("workspace-a", []GraphFact{
		graphTestFact(now, "workspace-a", "cluster-a", "payments", FactInventory, LensLive, entity("cluster-a", "payments")),
		graphTestFact(now, "workspace-a", "cluster-b", "payments", FactInventory, LensLive, entity("cluster-b", "payments")),
		graphTestFact(now, "workspace-a", "cluster-a", "payments", FactChange, LensTimeline, entity("cluster-a", "payments")),
		graphTestFact(now, "workspace-a", "cluster-a", "payments", FactAlert, LensTelemetry, nil),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Nodes) != 2 || len(graph.Nodes[0].Facts) != 2 || len(graph.Unattached) != 1 {
		t.Fatalf("graph = %#v", graph)
	}
	if graph.Nodes[0].Entity.Cluster != "cluster-a" || graph.Nodes[1].Entity.Cluster != "cluster-b" {
		t.Fatalf("nodes = %#v", graph.Nodes)
	}
	correlations := graph.ImageCorrelations()
	if len(correlations) != 1 || correlations[0].Digest != testGraphDigest || len(correlations[0].Entities) != 2 ||
		!slices.Equal([]string{correlations[0].Entities[0].Cluster, correlations[0].Entities[1].Cluster}, []string{"cluster-a", "cluster-b"}) {
		t.Fatalf("image correlations = %#v", correlations)
	}
}

func TestNewGraphRejectsUnsafeJoinClaims(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 12, 12, 0, 0, 0, time.UTC)
	valid := graphTestFact(now, "workspace-a", "cluster-a", "payments", FactInventory, LensLive, &EntityRef{Cluster: "cluster-a", Namespace: "payments", Kind: "Deployment", Name: "payments"})
	for _, test := range []struct {
		name string
		fact GraphFact
	}{
		{
			name: "cross cluster entity", fact: graphTestFact(now, "workspace-a", "cluster-a", "payments", FactInventory, LensLive,
				&EntityRef{Cluster: "cluster-b", Namespace: "payments", Kind: "Deployment", Name: "payments"}),
		},
		{
			name: "cross namespace entity", fact: graphTestFact(now, "workspace-a", "cluster-a", "payments", FactInventory, LensLive,
				&EntityRef{Cluster: "cluster-a", Namespace: "other", Kind: "Deployment", Name: "payments"}),
		},
		{
			name: "mutable image tag", fact: graphTestFact(now, "workspace-a", "cluster-a", "payments", FactInventory, LensLive,
				&EntityRef{Cluster: "cluster-a", Namespace: "payments", Kind: "Deployment", Name: "payments", ImageDigest: "payments:latest"}),
		},
		{
			name: "wrong workspace", fact: graphTestFact(now, "workspace-b", "cluster-a", "payments", FactInventory, LensLive,
				&EntityRef{Cluster: "cluster-a", Namespace: "payments", Kind: "Deployment", Name: "payments"}),
		},
		{
			name: "unknown lens", fact: graphTestFact(now, "workspace-a", "cluster-a", "payments", FactInventory, Lens("guess"),
				&EntityRef{Cluster: "cluster-a", Namespace: "payments", Kind: "Deployment", Name: "payments"}),
		},
		{
			name: "incompatible fact lens", fact: graphTestFact(now, "workspace-a", "cluster-a", "payments", FactInventory, LensTelemetry,
				&EntityRef{Cluster: "cluster-a", Namespace: "payments", Kind: "Deployment", Name: "payments"}),
		},
		{
			name: "separator injection", fact: graphTestFact(now, "workspace-a", "cluster-a/other", "payments", FactInventory, LensLive,
				&EntityRef{Cluster: "cluster-a/other", Namespace: "payments", Kind: "Deployment", Name: "payments"}),
		},
		{
			name: "missing source kind", fact: GraphFact{Fact: Fact{Evidence: Evidence{
				Ref:  ResourceRef{Scope: "cluster-a", Kind: "Deployment", Namespace: "payments", Name: "payments"},
				Kind: FactInventory, ObservedAt: now, Source: "cluster-a",
			}, Workspace: "workspace-a"}, Lens: LensLive, Entity: &EntityRef{Cluster: "cluster-a", Namespace: "payments", Kind: "Deployment", Name: "payments"}},
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := NewGraph("workspace-a", []GraphFact{test.fact}); err == nil {
				t.Fatal("unsafe graph fact unexpectedly accepted")
			}
		})
	}
	if _, err := NewGraph("workspace-a", []GraphFact{valid}); err != nil {
		t.Fatalf("valid graph fact rejected: %v", err)
	}
}

func TestNewGraphBoundsFacts(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 12, 12, 0, 0, 0, time.UTC)
	facts := make([]GraphFact, maxGraphFacts+1)
	for index := range facts {
		facts[index] = graphTestFact(now, "workspace-a", "cluster-a", "payments", FactInventory, LensLive,
			&EntityRef{Cluster: "cluster-a", Namespace: "payments", Kind: "Deployment", Name: "payments"})
	}
	if _, err := NewGraph("workspace-a", facts); err == nil {
		t.Fatal("oversized graph unexpectedly accepted")
	}
}

func TestImageDigestFromRepoDigest(t *testing.T) {
	t.Parallel()

	got, err := ImageDigestFromRepoDigest("registry.example/payments@" + testGraphDigest)
	if err != nil || got != testGraphDigest {
		t.Fatalf("ImageDigestFromRepoDigest() = %q, %v", got, err)
	}
	for _, value := range []string{
		"registry.example/payments:latest",
		"registry.example/payments:latest@" + testGraphDigest,
		" registry.example/payments@" + testGraphDigest,
		"sha256:short",
		"registry.example/payments@sha256:ABC",
	} {
		if _, err := ImageDigestFromRepoDigest(value); err == nil {
			t.Fatalf("mutable or malformed repo digest %q unexpectedly accepted", value)
		}
	}
}

func TestEntityRefKeyIncludesEveryLocalDimension(t *testing.T) {
	t.Parallel()

	base := EntityRef{Cluster: "cluster-a", Namespace: "payments", Kind: "Deployment", Name: "payments"}
	withPod := base
	withPod.Pod = "payments-0"
	withDigest := base
	withDigest.ImageDigest = testGraphDigest
	if base.Key() == withPod.Key() || base.Key() == withDigest.Key() || withPod.Key() == withDigest.Key() {
		t.Fatalf("ambiguous entity keys: base=%q pod=%q digest=%q", base.Key(), withPod.Key(), withDigest.Key())
	}
}

func graphTestFact(at time.Time, workspace, cluster, namespace string, kind FactKind, lens Lens, entity *EntityRef) GraphFact {
	return GraphFact{
		Fact: Fact{Evidence: Evidence{
			Ref:  ResourceRef{SourceKind: "fixture", Scope: cluster, Kind: "Deployment", Namespace: namespace, Name: "payments"},
			Kind: kind, ObservedAt: at, Source: cluster,
		}, Workspace: workspace},
		Lens: lens, Entity: entity,
	}
}
