// SPDX-License-Identifier: Apache-2.0

package brain

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/ArdurAI/sith/internal/fleet"
	"github.com/ArdurAI/sith/internal/intent"
)

func TestRemediationCandidatePublicShapeIsNonAuthorizing(t *testing.T) {
	t.Parallel()
	typeOfCandidate := reflect.TypeFor[RemediationCandidate]()
	if typeOfCandidate.NumField() != 2 || typeOfCandidate.Field(0).Name != "Verb" ||
		typeOfCandidate.Field(1).Name != "RequiredProvenance" {
		t.Fatalf("RemediationCandidate fields = %#v, want only Verb and RequiredProvenance", typeOfCandidate)
	}
	if typeOfCandidate.Field(0).Type != reflect.TypeFor[intent.Verb]() ||
		typeOfCandidate.Field(1).Type != reflect.TypeFor[[]ProvenanceRequirement]() {
		t.Fatalf("RemediationCandidate field types are not the closed contract: %#v", typeOfCandidate)
	}
}

func TestCatalogUsesOnlyReviewedRemediationMappings(t *testing.T) {
	t.Parallel()
	want := map[RuleID]intent.Verb{
		RuleBadDeploy:   intent.VerbArgoCDRollback,
		RuleOOMKilled:   intent.VerbGitOpsOpenPR,
		RuleConfigDrift: intent.VerbGitOpsOpenPR,
	}
	for _, candidateRule := range catalog {
		candidate := remediationCandidateFor(candidateRule.remediation)
		verb, expected := want[candidateRule.id]
		if !expected {
			if candidateRule.remediation != "" || candidate != nil {
				t.Fatalf("rule %s has unreviewed remediation %#v", candidateRule.id, candidate)
			}
			continue
		}
		if candidate == nil || candidate.Verb != verb {
			t.Fatalf("rule %s candidate = %#v, want verb %s", candidateRule.id, candidate, verb)
		}
		if err := candidate.Validate(); err != nil {
			t.Fatalf("rule %s candidate is invalid: %v", candidateRule.id, err)
		}
	}
}

func TestRemediationCandidateWireBoundaryIsCanonical(t *testing.T) {
	t.Parallel()
	want := remediationCandidateFor(intent.VerbGitOpsOpenPR)
	payload, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var decoded RemediationCandidate
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if decoded.Verb != want.Verb || !slices.Equal(decoded.RequiredProvenance, want.RequiredProvenance) {
		t.Fatalf("decoded candidate = %#v, want %#v", decoded, want)
	}

	invalidCandidates := []RemediationCandidate{
		{},
		{Verb: intent.VerbDeploymentRestart, RequiredProvenance: []ProvenanceRequirement{ProvenanceArgoRevision}},
		{Verb: intent.VerbArgoCDRollback, RequiredProvenance: []ProvenanceRequirement{ProvenanceArgoApplicationTarget}},
		{Verb: intent.VerbArgoCDRollback, RequiredProvenance: []ProvenanceRequirement{ProvenanceArgoRevision, ProvenanceArgoApplicationTarget}},
		{Verb: intent.VerbArgoCDRollback, RequiredProvenance: []ProvenanceRequirement{ProvenanceArgoApplicationTarget, ProvenanceArgoRevision, ProvenanceArgoRevision}},
	}
	for index, candidate := range invalidCandidates {
		if _, err := json.Marshal(candidate); err == nil {
			t.Fatalf("json.Marshal(invalid candidate %d) error = nil", index)
		}
	}

	for _, payload := range []string{
		`{"verb":"argocd.rollback","required_provenance":["argocd.application.target"]}`,
		`{"verb":"argocd.rollback","required_provenance":["argocd.revision","argocd.application.target"]}`,
		`{"verb":"argocd.rollback","required_provenance":["argocd.application.target","argocd.revision"],"target":"forged"}`,
		`{"verb":"argocd.rollback","verb":"argocd.rollback","required_provenance":["argocd.application.target","argocd.revision"]}`,
		`{"Verb":"argocd.rollback","required_provenance":["argocd.application.target","argocd.revision"]}`,
		`{"verb":"argocd.rollback","Required_Provenance":["argocd.application.target","argocd.revision"]}`,
		`{"verb":"shell.exec","required_provenance":["argocd.application.target","argocd.revision"]}`,
		`{"verb":"argocd.rollback","required_provenance":["argocd.application.target","argocd.revision"]}{}`,
	} {
		if err := json.Unmarshal([]byte(payload), &decoded); err == nil {
			t.Fatalf("json.Unmarshal(%s) error = nil", payload)
		}
	}
}

func TestEvaluateReturnsMutationIsolatedRemediationCandidate(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	input := Investigation{
		Workspace: fleet.LocalWorkspace,
		Observations: []Observation{
			observe(now, fleet.LensLive, "pod.reason", "OOMKilled"),
			observe(now, fleet.LensTelemetry, "memory.variant", "near-limit"),
		},
		Coverage: covered(fleet.LensLive, fleet.LensTelemetry),
	}
	first, err := Evaluate(input)
	if err != nil {
		t.Fatalf("first Evaluate() error = %v", err)
	}
	if len(first.Verdicts) != 1 || first.Verdicts[0].RemediationCandidate == nil {
		t.Fatalf("first verdicts = %#v, want R2 candidate", first.Verdicts)
	}
	first.Verdicts[0].RemediationCandidate.RequiredProvenance[0] = ProvenanceArgoRevision

	second, err := Evaluate(input)
	if err != nil {
		t.Fatalf("second Evaluate() error = %v", err)
	}
	want := remediationCandidateFor(intent.VerbGitOpsOpenPR)
	if len(second.Verdicts) != 1 || second.Verdicts[0].RemediationCandidate == nil ||
		second.Verdicts[0].RemediationCandidate.Verb != want.Verb ||
		!slices.Equal(second.Verdicts[0].RemediationCandidate.RequiredProvenance, want.RequiredProvenance) {
		t.Fatalf("second candidate = %#v, want mutation-isolated %#v", second.Verdicts[0].RemediationCandidate, want)
	}
}

func TestFleetCandidateDoesNotAliasEntityCandidate(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	const repoDigest = "registry.example/payments@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	observations := make([]Observation, 0, 6)
	for _, cluster := range []string{"alpha", "beta"} {
		ref := testRef()
		ref.Scope = cluster
		observations = append(observations,
			Observation{Ref: ref, Lens: fleet.LensLive, Key: "pod.reason", Value: "OOMKilled", ObservedAt: now, Source: "kubeconfig"},
			Observation{Ref: ref, Lens: fleet.LensTelemetry, Key: "memory.variant", Value: "near-limit", ObservedAt: now, Source: "prometheus"},
			Observation{Ref: ref, Lens: fleet.LensLive, Key: fleet.OTelContainerImageRepoDigests, Value: repoDigest, ObservedAt: now, Source: "kubeconfig"},
		)
	}
	result, err := Evaluate(Investigation{
		Workspace:    fleet.LocalWorkspace,
		Observations: observations,
		Coverage:     covered(fleet.LensLive, fleet.LensTelemetry),
	})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if len(result.Verdicts) != 3 || !result.Verdicts[0].FleetWide || result.Verdicts[0].RemediationCandidate == nil {
		t.Fatalf("verdicts = %#v, want fleet candidate followed by two entity candidates", result.Verdicts)
	}
	result.Verdicts[0].RemediationCandidate.RequiredProvenance[0] = ProvenanceArgoRevision
	want := remediationCandidateFor(intent.VerbGitOpsOpenPR)
	for index, verdict := range result.Verdicts[1:] {
		if verdict.RemediationCandidate == nil ||
			!slices.Equal(verdict.RemediationCandidate.RequiredProvenance, want.RequiredProvenance) {
			t.Fatalf("entity candidate %d = %#v, want independent %#v", index, verdict.RemediationCandidate, want)
		}
	}
}

func TestCoverageUnconfirmedVerdictKeepsAdvisoryAndInertCandidate(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	result, err := Evaluate(Investigation{
		Workspace: fleet.LocalWorkspace,
		Observations: []Observation{
			observe(now, fleet.LensLive, "workload.status", "Degraded"),
			observe(now, fleet.LensTimeline, "change.kind", "deploy"),
		},
		Coverage: covered(fleet.LensLive),
	})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if len(result.Verdicts) != 1 {
		t.Fatalf("verdicts = %#v, want one R1 verdict", result.Verdicts)
	}
	verdict := result.Verdicts[0]
	if verdict.Rule != RuleBadDeploy || verdict.Status != StatusUnconfirmed || verdict.Advisory.Command == "" ||
		verdict.RemediationCandidate == nil || verdict.RemediationCandidate.Verb != intent.VerbArgoCDRollback {
		t.Fatalf("verdict = %#v, want unconfirmed advisory plus inert R1 candidate", verdict)
	}
}

func TestAdvisoryOnlyRuleHasNoRemediationCandidate(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	result, err := Evaluate(Investigation{
		Workspace:    fleet.LocalWorkspace,
		Observations: []Observation{observe(now, fleet.LensLive, "pod.failure", "CrashLoopBackOff")},
		Coverage:     covered(fleet.LensLive),
	})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if len(result.Verdicts) != 1 || result.Verdicts[0].Rule != RuleCrashLoop ||
		result.Verdicts[0].Advisory.Command == "" || result.Verdicts[0].RemediationCandidate != nil {
		t.Fatalf("verdicts = %#v, want advisory-only R3", result.Verdicts)
	}
}
