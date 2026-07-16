// SPDX-License-Identifier: Apache-2.0

package brain

import (
	"testing"
	"time"

	"github.com/ArdurAI/sith/internal/fleet"
	"github.com/ArdurAI/sith/internal/fleetcache"
)

func TestFromCacheProjectsLiveFactsAndHonestCoverage(t *testing.T) {
	now := time.Now().UTC()
	ref := fleet.ResourceRef{SourceKind: "kubeconfig", Scope: "alpha", Kind: "Pod", Namespace: "prod", Name: "payments-0"}
	input := FromCache(fleet.LocalWorkspace, fleetcache.Snapshot{
		Coverage: fleet.Coverage{Requested: 1, Reachable: 1},
		Records: []fleetcache.Record{{
			Fact:      fleet.Fact{Evidence: fleet.Evidence{Ref: ref, Source: "alpha"}, Workspace: fleet.LocalWorkspace},
			Workspace: fleet.LocalWorkspace, Kind: "Pod", Cluster: "alpha", Namespace: "prod", Name: "payments-0",
			Status: "CrashLoopBackOff", Reasons: []string{"CrashLoopBackOff", "OOMKilled"}, Restarts: 4, ImageDigests: []string{"sha256:abc"}, ObservedAt: now,
		}},
	})
	if !input.Coverage[fleet.LensLive].Available || input.Coverage[fleet.LensTelemetry].Available {
		t.Fatalf("coverage = %#v", input.Coverage)
	}
	result, err := Evaluate(input)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if len(result.Verdicts) != 2 || result.Verdicts[0].Rule != RuleOOMKilled || result.Verdicts[1].Rule != RuleCrashLoop {
		t.Fatalf("verdicts = %#v", result.Verdicts)
	}
}

func TestFromCacheExplainsTruncatedLiveCoverage(t *testing.T) {
	t.Parallel()
	input := FromCache(fleet.LocalWorkspace, fleetcache.Snapshot{
		Coverage: fleet.Coverage{Requested: 2, Reachable: 2, Truncated: []string{"beta"}},
	})
	if got := input.Coverage[fleet.LensLive]; !got.Available || got.Reason != "one or more kubeconfig contexts returned truncated evidence" {
		t.Fatalf("live coverage = %#v, want available but explicitly truncated", got)
	}
}

func TestFromCacheProjectsOnlyOneProvenRepoDigest(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	valid := "registry.example/payments@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	for _, test := range []struct {
		name        string
		repoDigests []string
		want        bool
	}{
		{name: "one proven digest", repoDigests: []string{valid}, want: true},
		{name: "runtime only", repoDigests: []string{"containerd://sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}},
		{name: "mutable tag", repoDigests: []string{"registry.example/payments:latest@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}},
		{name: "ambiguous images", repoDigests: []string{valid, "registry.example/sidecar@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			input := FromCache(fleet.LocalWorkspace, fleetcache.Snapshot{Records: []fleetcache.Record{{
				Fact:      fleet.Fact{Evidence: fleet.Evidence{Ref: fleet.ResourceRef{SourceKind: "kubeconfig", Scope: "alpha", Kind: "Pod", Namespace: "prod", Name: "payments-0"}, Source: "alpha"}, Workspace: fleet.LocalWorkspace},
				Workspace: fleet.LocalWorkspace, Kind: "Pod", Cluster: "alpha", Namespace: "prod", Name: "payments-0", ImageRepoDigests: test.repoDigests, ObservedAt: now,
			}}})
			found := false
			for _, observation := range input.Observations {
				if observation.Key == fleet.OTelContainerImageRepoDigests {
					found = true
				}
			}
			if found != test.want {
				t.Fatalf("repo digest observation = %t, want %t: %#v", found, test.want, input.Observations)
			}
		})
	}
}

func TestFromCacheAttachesEventTimelineToInvolvedObject(t *testing.T) {
	now := time.Now().UTC()
	input := FromCache(fleet.LocalWorkspace, fleetcache.Snapshot{
		Coverage: fleet.Coverage{Requested: 1, Reachable: 1},
		Records: []fleetcache.Record{{
			Fact:      fleet.Fact{Evidence: fleet.Evidence{Ref: fleet.ResourceRef{SourceKind: "kubeconfig", Scope: "alpha", Kind: "Event", Namespace: "prod", Name: "rollout-event"}, Source: "alpha"}, Workspace: fleet.LocalWorkspace},
			Workspace: fleet.LocalWorkspace, Kind: "Event", Cluster: "alpha", Namespace: "prod", Name: "rollout-event",
			Reason: "ScalingReplicaSet", Ready: "Deployment/payments", ObservedAt: now,
		}},
	})
	if len(input.Observations) != 2 || input.Observations[1].Ref.Kind != "Deployment" || input.Observations[1].Ref.Name != "payments" || input.Observations[1].Key != "change.kind" {
		t.Fatalf("observations = %#v", input.Observations)
	}
}
