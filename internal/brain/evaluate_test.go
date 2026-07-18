// SPDX-License-Identifier: Apache-2.0

package brain

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ArdurAI/sith/internal/fleet"
)

func TestEvaluateCanonicalRules(t *testing.T) {
	now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name         string
		rule         RuleID
		observations []Observation
		coverage     map[fleet.Lens]LensCoverage
		status       Status
	}{
		{"R1 bad deploy", RuleBadDeploy, []Observation{
			observe(now, fleet.LensLive, "workload.status", "Degraded"),
			observe(now, fleet.LensTimeline, "change.kind", "deploy"),
			observe(now, fleet.LensDesired, "desired.changed", "true"),
			observe(now, fleet.LensTelemetry, "error.rate", "increased"),
		}, covered(fleet.LensLive, fleet.LensTimeline, fleet.LensDesired, fleet.LensTelemetry), StatusConfirmed},
		{"R2 OOM detected without telemetry", RuleOOMKilled, []Observation{
			observe(now, fleet.LensLive, "pod.reason", "OOMKilled"),
			observe(now, fleet.LensLive, "pod.restarts", "7"),
		}, covered(fleet.LensLive), StatusDetected},
		{"R3 crash loop", RuleCrashLoop, []Observation{
			observe(now, fleet.LensLive, "pod.failure", "CrashLoopBackOff"),
			observe(now, fleet.LensTelemetry, "logs.cause", "panic"),
		}, covered(fleet.LensLive, fleet.LensTelemetry), StatusConfirmed},
		{"R4 drift", RuleConfigDrift, []Observation{
			observe(now, fleet.LensLive, "workload.status", "Healthy"),
			observe(now, fleet.LensDesired, "desired.drift", "OutOfSync"),
			observe(now, fleet.LensTimeline, "change.kind", "kubectl-edit"),
		}, covered(fleet.LensLive, fleet.LensDesired, fleet.LensTimeline), StatusConfirmed},
		{"R5 expiry", RuleCertExpiry, []Observation{
			observe(now, fleet.LensLive, "certificate.expiry", "lt-7d"),
			observe(now, fleet.LensTelemetry, "certificate.renewal", "failing"),
		}, covered(fleet.LensLive, fleet.LensTelemetry), StatusConfirmed},
		{"R6 node pressure", RuleNodePressure, []Observation{
			observe(now, fleet.LensLive, "node.condition", "MemoryPressure"),
		}, covered(fleet.LensLive), StatusDetected},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := Evaluate(Investigation{Workspace: fleet.LocalWorkspace, Observations: test.observations, Coverage: test.coverage})
			if err != nil {
				t.Fatalf("Evaluate() error = %v", err)
			}
			if len(result.Verdicts) == 0 || result.Verdicts[0].Rule != test.rule || result.Verdicts[0].Status != test.status {
				t.Fatalf("top verdict = %#v, want %s/%s", result.Verdicts, test.rule, test.status)
			}
			if len(result.Verdicts[0].Citations) == 0 || (result.Verdicts[0].Advisory.Command == "" && result.Verdicts[0].Advisory.PRDiff == "") {
				t.Fatalf("verdict lacks cited evidence or advisory: %#v", result.Verdicts[0])
			}
		})
	}
}

func TestEvaluateImagePullFailureRuleIsBounded(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)

	for _, reason := range []string{"ImagePullBackOff", "imagepullbackoff", "ImAgEpUlLbAcKoFf", "ErrImagePull", "errimagepull"} {
		reason := reason
		t.Run(reason, func(t *testing.T) {
			t.Parallel()
			observation := observe(now, fleet.LensLive, "pod.reason", reason)
			observation.Ref.Scope = "dev'; printf injected #"
			observation.Ref.Namespace = "payments'; printf injected #"
			observation.Ref.Name = "api'; printf injected #"

			result, err := Evaluate(Investigation{
				Workspace:    fleet.LocalWorkspace,
				Observations: []Observation{observation},
				Coverage:     covered(fleet.LensLive),
			})
			if err != nil {
				t.Fatalf("Evaluate() error = %v", err)
			}
			if len(result.Verdicts) != 1 {
				t.Fatalf("verdicts = %#v, want one R7 verdict", result.Verdicts)
			}
			verdict := result.Verdicts[0]
			if verdict.Rule != RuleImagePull || verdict.Status != StatusConfirmed || verdict.FleetWide {
				t.Fatalf("verdict = %#v, want confirmed non-fleet R7", verdict)
			}
			if len(verdict.Citations) != 1 {
				t.Fatalf("citations = %#v, want one exact LIVE citation", verdict.Citations)
			}
			citation := verdict.Citations[0]
			if !reflect.DeepEqual(citation.Ref, observation.Ref) || citation.Lens != fleet.LensLive || citation.Predicate != "pod.reason" ||
				citation.Observed != reason || citation.Source != observation.Source || !citation.ObservedAt.Equal(now) {
				t.Fatalf("citation = %#v, want exact source observation", citation)
			}
			wantCommand := "kubectl --context 'dev'\\''; printf injected #' describe pod 'api'\\''; printf injected #' -n 'payments'\\''; printf injected #'"
			if verdict.Advisory.Command != wantCommand || verdict.Advisory.PRDiff != "" || !verdict.Advisory.Sensitive {
				t.Fatalf("advisory = %#v, want quoted sensitive read-only describe", verdict.Advisory)
			}
			if !strings.Contains(verdict.Hypothesis, "does not identify") || strings.Contains(strings.ToLower(verdict.Hypothesis), "authentication failure") {
				t.Fatalf("hypothesis = %q, want explicit cause uncertainty", verdict.Hypothesis)
			}
		})
	}

	for _, reason := range []string{
		"ImagePullBackOff/registry-auth",
		"ErrImagePull: unauthorized",
		" ImagePullBackOff",
		"ImagePullBackOff ",
		"\tErrImagePull",
		"ImagePull",
		"PullBackOff",
	} {
		reason := reason
		t.Run("reject "+reason, func(t *testing.T) {
			t.Parallel()
			result, err := Evaluate(Investigation{
				Workspace:    fleet.LocalWorkspace,
				Observations: []Observation{observe(now, fleet.LensLive, "pod.reason", reason)},
				Coverage:     covered(fleet.LensLive),
			})
			if err != nil {
				t.Fatalf("Evaluate() error = %v", err)
			}
			if len(result.Verdicts) != 0 {
				t.Fatalf("caller-controlled reason %q yielded %#v", reason, result.Verdicts)
			}
		})
	}
}

func TestEvaluateImagePullFailureAbstainsOnUnusableLiveEvidence(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)

	for _, test := range []struct {
		name        string
		observation Observation
		coverage    map[fleet.Lens]LensCoverage
	}{
		{name: "stale observation", observation: func() Observation {
			observation := observe(now, fleet.LensLive, "pod.reason", "ImagePullBackOff")
			observation.Stale = true
			return observation
		}(), coverage: covered(fleet.LensLive)},
		{name: "stale coverage", observation: observe(now, fleet.LensLive, "pod.reason", "ImagePullBackOff"), coverage: map[fleet.Lens]LensCoverage{
			fleet.LensLive: {Available: true, Stale: true, Reason: "watch disconnected"},
		}},
		{name: "unavailable coverage", observation: observe(now, fleet.LensLive, "pod.reason", "ErrImagePull"), coverage: map[fleet.Lens]LensCoverage{
			fleet.LensLive: {Available: false, Reason: "context unreachable"},
		}},
		{name: "missing coverage", observation: observe(now, fleet.LensLive, "pod.reason", "ErrImagePull"), coverage: map[fleet.Lens]LensCoverage{}},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			result, err := Evaluate(Investigation{
				Workspace:    fleet.LocalWorkspace,
				Observations: []Observation{test.observation},
				Coverage:     test.coverage,
			})
			if err != nil {
				t.Fatalf("Evaluate() error = %v", err)
			}
			if len(result.Verdicts) != 1 || result.Verdicts[0].Rule != RuleImagePull ||
				result.Verdicts[0].Status != StatusUnconfirmed ||
				!slices.Equal(result.Verdicts[0].MissingLenses, []fleet.Lens{fleet.LensLive}) {
				t.Fatalf("verdicts = %#v, want unconfirmed R7 with LIVE gap", result.Verdicts)
			}
		})
	}
}

func TestEvaluateImagePullFailureDoesNotCorrelateFleetWide(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	const repoDigest = "registry.example/payments@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	observations := make([]Observation, 0, 4)
	for _, cluster := range []string{"alpha", "beta"} {
		ref := testRef()
		ref.Scope = cluster
		observations = append(observations,
			Observation{Ref: ref, Lens: fleet.LensLive, Key: "pod.reason", Value: "ImagePullBackOff", ObservedAt: now, Source: "kubeconfig"},
			Observation{Ref: ref, Lens: fleet.LensLive, Key: fleet.OTelContainerImageRepoDigests, Value: repoDigest, ObservedAt: now, Source: "kubeconfig"},
		)
	}

	result, err := Evaluate(Investigation{Workspace: fleet.LocalWorkspace, Observations: observations, Coverage: covered(fleet.LensLive)})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if len(result.Verdicts) != 2 {
		t.Fatalf("verdicts = %#v, want one R7 verdict per Pod", result.Verdicts)
	}
	for _, verdict := range result.Verdicts {
		if verdict.Rule != RuleImagePull || verdict.FleetWide || len(verdict.Clusters) != 0 {
			t.Fatalf("verdict = %#v, R7 must remain entity-local", verdict)
		}
	}
}

func TestEvaluateArgoSyncFailureRuleIsBounded(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 18, 15, 30, 0, 0, time.UTC)
	observation := Observation{
		Ref: fleet.ResourceRef{
			SourceKind: "argocd",
			Scope:      "dev'; printf injected #",
			Kind:       "Application",
			Namespace:  "argocd'; printf injected #",
			Name:       "payments'; printf injected #",
		},
		Lens: fleet.LensTimeline, Key: "change.kind", Value: "sync-failed",
		ObservedAt: now, Source: "fixture",
	}

	result, err := Evaluate(Investigation{
		Workspace: fleet.LocalWorkspace, Observations: []Observation{observation},
		Coverage: covered(fleet.LensTimeline),
	})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if len(result.Verdicts) != 1 {
		t.Fatalf("verdicts = %#v, want one R8 verdict", result.Verdicts)
	}
	verdict := result.Verdicts[0]
	if verdict.Rule != RuleArgoSyncFail || verdict.Status != StatusConfirmed || verdict.FleetWide {
		t.Fatalf("verdict = %#v, want confirmed entity-local R8", verdict)
	}
	if len(verdict.Citations) != 1 {
		t.Fatalf("citations = %#v, want one exact TIMELINE citation", verdict.Citations)
	}
	citation := verdict.Citations[0]
	if !reflect.DeepEqual(citation.Ref, observation.Ref) || citation.Lens != fleet.LensTimeline ||
		citation.Predicate != "change.kind" || citation.Observed != "sync-failed" ||
		citation.Source != observation.Source || !citation.ObservedAt.Equal(now) {
		t.Fatalf("citation = %#v, want exact normalized source observation", citation)
	}
	wantCommand := "kubectl --context 'dev'\\''; printf injected #' describe application.argoproj.io 'payments'\\''; printf injected #' -n 'argocd'\\''; printf injected #'"
	if verdict.Advisory.Command != wantCommand || verdict.Advisory.PRDiff != "" || !verdict.Advisory.Sensitive {
		t.Fatalf("advisory = %#v, want quoted sensitive read-only Application describe", verdict.Advisory)
	}
	hypothesis := strings.ToLower(verdict.Hypothesis)
	if !strings.Contains(hypothesis, "does not identify") {
		t.Fatalf("hypothesis = %q, want explicit cause uncertainty", verdict.Hypothesis)
	}
	for _, overclaim := range []string{"authentication is", "network is", "rendering is", "hook is"} {
		if strings.Contains(hypothesis, overclaim) {
			t.Fatalf("hypothesis = %q, overclaims %q", verdict.Hypothesis, overclaim)
		}
	}
}

func TestEvaluateArgoSyncFailureRejectsNearMisses(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 18, 15, 30, 0, 0, time.UTC)
	for _, value := range []string{
		"OutOfSync",
		"Unknown",
		"Degraded",
		"argocd-sync",
		"sync_failed",
		"sync-failure",
		" sync-failed",
		"sync-failed ",
		"sync-failed/render-error",
	} {
		value := value
		t.Run(value, func(t *testing.T) {
			t.Parallel()
			result, err := Evaluate(Investigation{
				Workspace: fleet.LocalWorkspace,
				Observations: []Observation{{
					Ref:  fleet.ResourceRef{SourceKind: "argocd", Scope: "alpha", Kind: "Application", Namespace: "argocd", Name: "payments"},
					Lens: fleet.LensTimeline, Key: "change.kind", Value: value, ObservedAt: now, Source: "argocd",
				}},
				Coverage: covered(fleet.LensTimeline),
			})
			if err != nil {
				t.Fatalf("Evaluate() error = %v", err)
			}
			if len(result.Verdicts) != 0 {
				t.Fatalf("near-miss value %q yielded %#v", value, result.Verdicts)
			}
		})
	}
}

func TestEvaluateArgoSyncFailureAbstainsOnUnusableTimelineEvidence(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 18, 15, 30, 0, 0, time.UTC)
	base := Observation{
		Ref:  fleet.ResourceRef{SourceKind: "argocd", Scope: "alpha", Kind: "Application", Namespace: "argocd", Name: "payments"},
		Lens: fleet.LensTimeline, Key: "change.kind", Value: "sync-failed", ObservedAt: now, Source: "argocd",
	}
	for _, test := range []struct {
		name        string
		observation Observation
		coverage    map[fleet.Lens]LensCoverage
	}{
		{name: "stale observation", observation: func() Observation {
			observation := base
			observation.Stale = true
			return observation
		}(), coverage: covered(fleet.LensTimeline)},
		{name: "stale coverage", observation: base, coverage: map[fleet.Lens]LensCoverage{
			fleet.LensTimeline: {Available: true, Stale: true, Reason: "collector stale"},
		}},
		{name: "unavailable coverage", observation: base, coverage: map[fleet.Lens]LensCoverage{
			fleet.LensTimeline: {Reason: "Argo evidence unavailable"},
		}},
		{name: "missing coverage", observation: base, coverage: map[fleet.Lens]LensCoverage{}},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			result, err := Evaluate(Investigation{
				Workspace: fleet.LocalWorkspace, Observations: []Observation{test.observation}, Coverage: test.coverage,
			})
			if err != nil {
				t.Fatalf("Evaluate() error = %v", err)
			}
			if len(result.Verdicts) != 1 || result.Verdicts[0].Rule != RuleArgoSyncFail ||
				result.Verdicts[0].Status != StatusUnconfirmed ||
				!slices.Equal(result.Verdicts[0].MissingLenses, []fleet.Lens{fleet.LensTimeline}) {
				t.Fatalf("verdicts = %#v, want unconfirmed R8 with TIMELINE gap", result.Verdicts)
			}
		})
	}
}

func TestEvaluateArgoSyncFailureDoesNotCorrelateFleetWide(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 18, 15, 30, 0, 0, time.UTC)
	const repoDigest = "registry.example/payments@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	observations := make([]Observation, 0, 4)
	for _, cluster := range []string{"alpha", "beta"} {
		ref := fleet.ResourceRef{SourceKind: "argocd", Scope: cluster, Kind: "Application", Namespace: "argocd", Name: "payments"}
		observations = append(observations,
			Observation{Ref: ref, Lens: fleet.LensTimeline, Key: "change.kind", Value: "sync-failed", ObservedAt: now, Source: "argocd"},
			Observation{Ref: ref, Lens: fleet.LensLive, Key: fleet.OTelContainerImageRepoDigests, Value: repoDigest, ObservedAt: now, Source: "fixture"},
		)
	}

	result, err := Evaluate(Investigation{
		Workspace: fleet.LocalWorkspace, Observations: observations,
		Coverage: covered(fleet.LensLive, fleet.LensTimeline),
	})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if len(result.Verdicts) != 2 {
		t.Fatalf("verdicts = %#v, want one R8 verdict per Application", result.Verdicts)
	}
	for _, verdict := range result.Verdicts {
		if verdict.Rule != RuleArgoSyncFail || verdict.FleetWide || len(verdict.Clusters) != 0 {
			t.Fatalf("verdict = %#v, R8 must remain entity-local", verdict)
		}
	}
}

func TestEvaluateAbstainsWhenRequiredLensIsStale(t *testing.T) {
	now := time.Now().UTC()
	result, err := Evaluate(Investigation{
		Workspace: fleet.LocalWorkspace,
		Observations: []Observation{
			observe(now, fleet.LensLive, "workload.status", "Degraded"),
			observe(now, fleet.LensTimeline, "change.kind", "deploy"),
		},
		Coverage: map[fleet.Lens]LensCoverage{
			fleet.LensLive: {Available: true}, fleet.LensTimeline: {Available: true, Stale: true, Reason: "watch disconnected"},
		},
	})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if len(result.Verdicts) != 1 || result.Verdicts[0].Status != StatusUnconfirmed || !slices.Contains(result.Verdicts[0].MissingLenses, fleet.LensTimeline) {
		t.Fatalf("verdict = %#v, want unconfirmed with timeline gap", result.Verdicts)
	}
}

func TestEvaluateKeepsStaleTriggerAsUnconfirmedEvidence(t *testing.T) {
	observation := observe(time.Now().UTC(), fleet.LensLive, "pod.failure", "CrashLoopBackOff")
	observation.Stale = true
	result, err := Evaluate(Investigation{
		Workspace:    fleet.LocalWorkspace,
		Observations: []Observation{observation},
		Coverage:     covered(fleet.LensLive),
	})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if len(result.Verdicts) != 1 || result.Verdicts[0].Status != StatusUnconfirmed || !result.Verdicts[0].Citations[0].Stale {
		t.Fatalf("verdicts = %#v, want cited stale unconfirmed trigger", result.Verdicts)
	}
}

func TestEvaluatePrefersFreshDuplicateSignal(t *testing.T) {
	stale := observe(time.Now().Add(-time.Hour), fleet.LensLive, "pod.failure", "CrashLoopBackOff")
	stale.Stale = true
	fresh := observe(time.Now(), fleet.LensLive, "pod.failure", "CrashLoopBackOff")
	result, err := Evaluate(Investigation{Workspace: fleet.LocalWorkspace, Observations: []Observation{stale, fresh}, Coverage: covered(fleet.LensLive)})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if len(result.Verdicts) != 1 || result.Verdicts[0].Citations[0].Stale || result.Verdicts[0].Citations[0].ObservedAt != fresh.ObservedAt {
		t.Fatalf("verdicts = %#v, want fresh citation", result.Verdicts)
	}
}

func TestEvaluateDoesNotBorrowLensCoverageFromAnotherEntity(t *testing.T) {
	now := time.Now().UTC()
	degraded := observe(now, fleet.LensLive, "workload.status", "Degraded")
	unrelated := observe(now, fleet.LensTimeline, "change.kind", "deploy")
	unrelated.Ref.Name = "inventory"
	result, err := Evaluate(Investigation{
		Workspace:    fleet.LocalWorkspace,
		Observations: []Observation{degraded, unrelated},
		Coverage:     covered(fleet.LensLive, fleet.LensTimeline),
	})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if len(result.Verdicts) != 1 || result.Verdicts[0].Status != StatusUnconfirmed || !slices.Contains(result.Verdicts[0].MissingLenses, fleet.LensTimeline) {
		t.Fatalf("verdicts = %#v, want entity-local timeline abstention", result.Verdicts)
	}
}

func TestEvaluateRanksFleetDigestCorrelationFirst(t *testing.T) {
	now := time.Now().UTC()
	const repoDigest = "registry.example/payments@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	observations := make([]Observation, 0, 4)
	for _, cluster := range []string{"alpha", "beta"} {
		ref := testRef()
		ref.Scope = cluster
		observations = append(observations,
			Observation{Ref: ref, Lens: fleet.LensLive, Key: "pod.failure", Value: "CrashLoopBackOff", ObservedAt: now, Source: "kubeconfig"},
			Observation{Ref: ref, Lens: fleet.LensLive, Key: fleet.OTelContainerImageRepoDigests, Value: repoDigest, ObservedAt: now, Source: "kubeconfig"},
		)
	}
	result, err := Evaluate(Investigation{Workspace: fleet.LocalWorkspace, Observations: observations, Coverage: covered(fleet.LensLive)})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if len(result.Verdicts) != 3 || !result.Verdicts[0].FleetWide || !slices.Equal(result.Verdicts[0].Clusters, []string{"alpha", "beta"}) {
		t.Fatalf("verdicts = %#v, want fleet-wide correlation first", result.Verdicts)
	}
	digestCitations := citationsForPredicate(result.Verdicts[0].Citations, fleet.OTelContainerImageRepoDigests)
	if len(digestCitations) != 2 || digestCitations[0].Ref.Scope != "alpha" || digestCitations[1].Ref.Scope != "beta" {
		t.Fatalf("digest citations = %#v, want one fresh citation per correlated cluster", digestCitations)
	}
}

func TestEvaluateRejectsStaleFleetDigestCorrelation(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	const repoDigest = "registry.example/payments@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	observations := make([]Observation, 0, 4)
	for _, cluster := range []string{"alpha", "beta"} {
		ref := testRef()
		ref.Scope = cluster
		digest := Observation{
			Ref: ref, Lens: fleet.LensLive, Key: fleet.OTelContainerImageRepoDigests,
			Value: repoDigest, ObservedAt: now, Source: "kubeconfig",
		}
		if cluster == "beta" {
			digest.Stale = true
		}
		observations = append(observations,
			Observation{Ref: ref, Lens: fleet.LensLive, Key: "pod.failure", Value: "CrashLoopBackOff", ObservedAt: now, Source: "kubeconfig"},
			digest,
		)
	}

	result, err := Evaluate(Investigation{Workspace: fleet.LocalWorkspace, Observations: observations, Coverage: covered(fleet.LensLive)})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	for _, verdict := range result.Verdicts {
		if verdict.FleetWide {
			t.Fatalf("stale digest yielded fleet-wide verdict %#v", verdict)
		}
	}
}

func TestEvaluateFleetDigestCorrelationDeduplicatesDeterministically(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	const repoDigest = "registry.example/payments@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	alpha, beta := testRef(), testRef()
	alpha.Scope, beta.Scope = "alpha", "beta"
	stale := Observation{Ref: alpha, Lens: fleet.LensLive, Key: fleet.OTelContainerImageRepoDigests, Value: repoDigest, ObservedAt: now.Add(-2 * time.Hour), Source: "cache", Stale: true}
	older := Observation{Ref: alpha, Lens: fleet.LensLive, Key: fleet.OTelContainerImageRepoDigests, Value: repoDigest, ObservedAt: now.Add(-time.Hour), Source: "watch"}
	freshest := Observation{Ref: alpha, Lens: fleet.LensLive, Key: fleet.OTelContainerImageRepoDigests, Value: repoDigest, ObservedAt: now, Source: "query"}
	tiedSource := freshest
	tiedSource.Ref.SourceKind = "z-source"
	tiedSource.Ref.Attributes = map[string]string{"container": "sidecar"}
	observations := []Observation{
		{Ref: alpha, Lens: fleet.LensLive, Key: "pod.failure", Value: "CrashLoopBackOff", ObservedAt: now, Source: "query"},
		stale, older, freshest, tiedSource,
		{Ref: beta, Lens: fleet.LensLive, Key: "pod.failure", Value: "CrashLoopBackOff", ObservedAt: now, Source: "query"},
		{Ref: beta, Lens: fleet.LensLive, Key: fleet.OTelContainerImageRepoDigests, Value: repoDigest, ObservedAt: now, Source: "query"},
	}
	reversed := append([]Observation(nil), observations...)
	slices.Reverse(reversed)

	first, err := Evaluate(Investigation{Workspace: fleet.LocalWorkspace, Observations: observations, Coverage: covered(fleet.LensLive)})
	if err != nil {
		t.Fatalf("Evaluate() first error = %v", err)
	}
	second, err := Evaluate(Investigation{Workspace: fleet.LocalWorkspace, Observations: reversed, Coverage: covered(fleet.LensLive)})
	if err != nil {
		t.Fatalf("Evaluate() reversed error = %v", err)
	}
	if len(first.Verdicts) == 0 || len(second.Verdicts) == 0 || !reflect.DeepEqual(first.Verdicts[0], second.Verdicts[0]) {
		t.Fatalf("top verdict changed with duplicate input order:\nfirst:  %#v\nsecond: %#v", first.Verdicts, second.Verdicts)
	}
	digestCitations := citationsForPredicate(first.Verdicts[0].Citations, fleet.OTelContainerImageRepoDigests)
	if len(digestCitations) != 2 || digestCitations[0].Ref.Scope != "alpha" || digestCitations[1].Ref.Scope != "beta" ||
		digestCitations[0].ObservedAt != freshest.ObservedAt || digestCitations[0].Stale {
		t.Fatalf("digest citations = %#v, want deduplicated freshest evidence", digestCitations)
	}
}

func TestEvaluateRejectsUnprovenFleetImageCorrelation(t *testing.T) {
	t.Parallel()

	const fullDigest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	for _, value := range []string{
		fullDigest,
		"containerd://" + fullDigest,
		"registry.example/payments:latest@" + fullDigest,
		"registry.example/payments@sha256:abc123",
	} {
		value := value
		t.Run(value, func(t *testing.T) {
			t.Parallel()
			now := time.Now().UTC()
			observations := make([]Observation, 0, 4)
			for _, cluster := range []string{"alpha", "beta"} {
				ref := testRef()
				ref.Scope = cluster
				observations = append(observations,
					Observation{Ref: ref, Lens: fleet.LensLive, Key: "pod.failure", Value: "CrashLoopBackOff", ObservedAt: now, Source: "kubeconfig"},
					Observation{Ref: ref, Lens: fleet.LensLive, Key: fleet.OTelContainerImageRepoDigests, Value: value, ObservedAt: now, Source: "kubeconfig"},
				)
			}
			result, err := Evaluate(Investigation{Workspace: fleet.LocalWorkspace, Observations: observations, Coverage: covered(fleet.LensLive)})
			if err != nil {
				t.Fatalf("Evaluate() error = %v", err)
			}
			for _, verdict := range result.Verdicts {
				if verdict.FleetWide {
					t.Fatalf("unproven image value %q yielded fleet-wide verdict %#v", value, verdict)
				}
			}
		})
	}
}

func TestEvaluateChainsBadDeployAsCrashLoopRootCause(t *testing.T) {
	now := time.Now().UTC()
	result, err := Evaluate(Investigation{
		Workspace: fleet.LocalWorkspace,
		Observations: []Observation{
			observe(now, fleet.LensLive, "workload.status", "Degraded"),
			observe(now, fleet.LensLive, "pod.failure", "CrashLoopBackOff"),
			observe(now, fleet.LensTimeline, "change.kind", "deploy"),
			observe(now, fleet.LensTelemetry, "logs.cause", "panic"),
		},
		Coverage: covered(fleet.LensLive, fleet.LensTimeline, fleet.LensTelemetry),
	})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if len(result.Verdicts) < 2 || result.Verdicts[0].Rule != RuleBadDeploy || !slices.Equal(result.Verdicts[0].CauseOf, []RuleID{RuleCrashLoop}) {
		t.Fatalf("verdicts = %#v, want R1 cause-of R3 first", result.Verdicts)
	}
}

func TestInvestigationRejectsIncompleteEvidence(t *testing.T) {
	_, err := Evaluate(Investigation{Workspace: fleet.LocalWorkspace, Observations: []Observation{{Lens: fleet.LensLive, Key: "pod.reason", Value: "OOMKilled"}}})
	if err == nil {
		t.Fatal("Evaluate() error = nil, want incomplete reference rejection")
	}
}

func TestAdvisoryBindsAndQuotesKubeconfigContext(t *testing.T) {
	ref := testRef()
	ref.Scope = "dev'; rm -rf / #"
	advisory := renderAdvisory(Advisory{Command: "kubectl --context {context} logs {name} -n {namespace}"}, ref)
	if advisory.Command != "kubectl --context 'dev'\\''; rm -rf / #' logs 'payments-0' -n 'prod'" {
		t.Fatalf("command = %q", advisory.Command)
	}
}

func observe(at time.Time, lens fleet.Lens, key, value string) Observation {
	return Observation{Ref: testRef(), Lens: lens, Key: key, Value: value, ObservedAt: at, Source: "fixture"}
}

func citationsForPredicate(citations []Citation, predicate string) []Citation {
	result := make([]Citation, 0)
	for _, citation := range citations {
		if citation.Predicate == predicate {
			result = append(result, citation)
		}
	}
	return result
}

func testRef() fleet.ResourceRef {
	return fleet.ResourceRef{SourceKind: "kubeconfig", Scope: "alpha", Kind: "Pod", Namespace: "prod", Name: "payments-0"}
}

func covered(lenses ...fleet.Lens) map[fleet.Lens]LensCoverage {
	result := make(map[fleet.Lens]LensCoverage, len(lenses))
	for _, lens := range lenses {
		result[lens] = LensCoverage{Available: true}
	}
	return result
}
