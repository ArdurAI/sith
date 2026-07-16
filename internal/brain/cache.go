// SPDX-License-Identifier: Apache-2.0

package brain

import (
	"strconv"
	"strings"

	"github.com/ArdurAI/sith/internal/fleet"
	"github.com/ArdurAI/sith/internal/fleetcache"
)

// FromCache converts the read-only local cache projection into normalized brain evidence.
func FromCache(workspace string, snapshot fleetcache.Snapshot) Investigation {
	input := Investigation{
		Workspace: workspace,
		Coverage: map[fleet.Lens]LensCoverage{
			fleet.LensLive: {
				Available: snapshot.Coverage.Reachable > 0,
				Reason:    coverageReason(snapshot),
			},
			fleet.LensDesired:   {Reason: "no desired-state connector is available"},
			fleet.LensTimeline:  {Reason: "no fresh timeline events are available"},
			fleet.LensTelemetry: {Reason: "no reachable Prometheus or Loki connector is available"},
		},
	}
	for _, record := range snapshot.Records {
		if record.Workspace != workspace {
			continue
		}
		input.Observations = append(input.Observations, recordObservations(record)...)
		if record.Kind == "Event" && !record.Stale {
			input.Coverage[fleet.LensTimeline] = LensCoverage{Available: true}
		}
	}
	return input
}

func recordObservations(record fleetcache.Record) []Observation {
	ref := record.Fact.Ref
	if ref.Scope == "" {
		ref = fleet.ResourceRef{SourceKind: "kubeconfig", Scope: record.Cluster, Kind: record.Kind, Namespace: record.Namespace, Name: record.Name}
	}
	add := func(lens fleet.Lens, key, value string) Observation {
		return Observation{Ref: ref, Lens: lens, Key: key, Value: value, ObservedAt: record.ObservedAt,
			Source: record.Fact.Source, Stale: record.Stale}
	}
	result := make([]Observation, 0, 8)
	switch record.Kind {
	case "Pod":
		reasons := record.Reasons
		if len(reasons) == 0 && record.Status != "" {
			reasons = []string{record.Status}
		}
		crashFailure := false
		for _, reason := range reasons {
			if strings.EqualFold(reason, "CrashLoopBackOff") {
				result = append(result, add(fleet.LensLive, "pod.failure", reason))
				crashFailure = true
			} else {
				result = append(result, add(fleet.LensLive, "pod.reason", reason))
			}
			if strings.EqualFold(reason, "Error") && record.Restarts >= 2 {
				crashFailure = true
			}
		}
		if crashFailure && !hasObservation(result, "pod.failure") {
			result = append(result, add(fleet.LensLive, "pod.failure", "repeated-error"))
		}
		if record.Restarts > 0 {
			result = append(result, add(fleet.LensLive, "pod.restarts", strconv.FormatInt(record.Restarts, 10)))
		}
		if len(record.ImageRepoDigests) == 1 {
			repoDigest := record.ImageRepoDigests[0]
			if _, err := fleet.ImageDigestFromRepoDigest(repoDigest); err == nil {
				result = append(result, add(fleet.LensLive, fleet.OTelContainerImageRepoDigests, repoDigest))
			}
		}
	case "Deployment", "StatefulSet", "DaemonSet", "Rollout":
		if record.Status != "" {
			result = append(result, add(fleet.LensLive, "workload.status", record.Status))
		}
	case "Node":
		if strings.EqualFold(record.Status, "NotReady") {
			result = append(result, add(fleet.LensLive, "node.condition", "NotReady"))
		}
		for _, condition := range record.Conditions {
			result = append(result, add(fleet.LensLive, "node.condition", condition))
		}
	case "Event":
		if involvedKind, involvedName, ok := strings.Cut(record.Ready, "/"); ok && involvedKind != "" && involvedName != "" {
			ref.Kind = involvedKind
			ref.Name = involvedName
		}
		if record.Reason != "" {
			result = append(result, add(fleet.LensLive, "event.reason", record.Reason))
			if change := timelineChange(record.Reason); change != "" {
				result = append(result, add(fleet.LensTimeline, "change.kind", change))
			}
		}
	}
	return result
}

func hasObservation(observations []Observation, key string) bool {
	for _, observation := range observations {
		if observation.Key == key {
			return true
		}
	}
	return false
}

func timelineChange(reason string) string {
	switch strings.ToLower(reason) {
	case "scalingreplicaset", "successfulcreate", "successfuldelete":
		return "rollout"
	case "syncsucceeded", "operationcompleted":
		return "argocd-sync"
	default:
		return ""
	}
}

func coverageReason(snapshot fleetcache.Snapshot) string {
	if snapshot.Coverage.Reachable == 0 {
		return "no reachable kubeconfig context"
	}
	if len(snapshot.Coverage.Stale) > 0 {
		return "one or more kubeconfig contexts are stale"
	}
	if len(snapshot.Coverage.Unreachable) > 0 {
		return "one or more kubeconfig contexts are unreachable"
	}
	if len(snapshot.Coverage.Truncated) > 0 {
		return "one or more kubeconfig contexts returned truncated evidence"
	}
	return ""
}
