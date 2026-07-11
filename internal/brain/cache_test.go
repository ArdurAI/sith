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
