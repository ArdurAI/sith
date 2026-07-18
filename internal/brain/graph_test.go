// SPDX-License-Identifier: Apache-2.0

package brain

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/ArdurAI/sith/internal/connector/argocd"
	connectorgithub "github.com/ArdurAI/sith/internal/connector/github"
	"github.com/ArdurAI/sith/internal/fleet"
)

func TestFromGraphFactsProjectsReviewedArgoSyncFailures(t *testing.T) {
	t.Parallel()
	eventAt := time.Date(2026, 7, 18, 15, 45, 0, 0, time.UTC)
	for _, phase := range []string{"Failed", "Error"} {
		phase := phase
		t.Run(phase, func(t *testing.T) {
			t.Parallel()
			facts := projectedArgoOperation(t, phase, eventAt)
			coverage := covered(fleet.LensTimeline)
			input, err := FromGraphFacts(fleet.LocalWorkspace, facts, coverage)
			if err != nil {
				t.Fatalf("FromGraphFacts() error = %v", err)
			}
			coverage[fleet.LensTimeline] = LensCoverage{}
			if !input.Coverage[fleet.LensTimeline].Available {
				t.Fatalf("coverage alias mutated projected input: %#v", input.Coverage)
			}
			if len(input.Observations) != 1 {
				t.Fatalf("observations = %#v, want one canonical sync failure", input.Observations)
			}
			observation := input.Observations[0]
			wantRef := fleet.ResourceRef{
				SourceKind: "argocd", Scope: "cluster-a", Kind: "Application", Namespace: "argocd", Name: "payments",
			}
			if !reflect.DeepEqual(observation.Ref, wantRef) || observation.Lens != fleet.LensTimeline ||
				observation.Key != "change.kind" || observation.Value != "sync-failed" ||
				!observation.ObservedAt.Equal(eventAt) || observation.Source != "cluster-a" || observation.Stale {
				t.Fatalf("observation = %#v, want exact sanitized Argo citation metadata", observation)
			}

			result, err := Evaluate(input)
			if err != nil {
				t.Fatalf("Evaluate() error = %v", err)
			}
			if len(result.Verdicts) != 1 || result.Verdicts[0].Rule != RuleArgoSyncFail ||
				result.Verdicts[0].Status != StatusConfirmed {
				t.Fatalf("verdicts = %#v, want confirmed R8", result.Verdicts)
			}
			encoded, err := json.Marshal(struct {
				Input  Investigation `json:"input"`
				Result Result        `json:"result"`
			}{Input: input, Result: result})
			if err != nil {
				t.Fatalf("marshal projected result: %v", err)
			}
			for _, discarded := range []string{"discard-this-revision", phase} {
				if strings.Contains(string(encoded), discarded) {
					t.Fatalf("brain output retained discarded Argo payload %q: %s", discarded, encoded)
				}
			}
		})
	}
}

func TestFromGraphFactsDoesNotTreatDriftOrNonFailedOperationsAsR8(t *testing.T) {
	t.Parallel()
	eventAt := time.Date(2026, 7, 18, 15, 45, 0, 0, time.UTC)
	for _, phase := range []string{"Succeeded", "Running", "Terminating"} {
		phase := phase
		t.Run(phase, func(t *testing.T) {
			t.Parallel()
			input, err := FromGraphFacts(
				fleet.LocalWorkspace,
				projectedArgoOperation(t, phase, eventAt),
				covered(fleet.LensTimeline),
			)
			if err != nil {
				t.Fatalf("FromGraphFacts() error = %v", err)
			}
			if len(input.Observations) != 0 {
				t.Fatalf("phase %q observations = %#v, want no R8 signal", phase, input.Observations)
			}
		})
	}

	for _, syncStatus := range []string{"OutOfSync", "Unknown", "Synced"} {
		syncStatus := syncStatus
		t.Run(syncStatus, func(t *testing.T) {
			t.Parallel()
			facts, err := argocd.ProjectApplication(argocd.Projection{
				Workspace:  fleet.LocalWorkspace,
				Scope:      "cluster-a",
				ObservedAt: eventAt.Add(time.Minute),
				Application: argoApplication(map[string]any{
					"sync": map[string]any{"status": syncStatus, "revision": "discard-this-revision"},
				}),
			})
			if err != nil {
				t.Fatalf("ProjectApplication() error = %v", err)
			}
			input, err := FromGraphFacts(fleet.LocalWorkspace, facts, covered(fleet.LensDesired))
			if err != nil {
				t.Fatalf("FromGraphFacts() error = %v", err)
			}
			if len(input.Observations) != 0 {
				t.Fatalf("sync status %q observations = %#v, want no R8 signal", syncStatus, input.Observations)
			}
		})
	}
}

func TestFromGraphFactsFailsClosedOnAmbiguousArgoChangeFacts(t *testing.T) {
	t.Parallel()
	eventAt := time.Date(2026, 7, 18, 15, 45, 0, 0, time.UTC)
	baseFacts := projectedArgoOperation(t, "Failed", eventAt)
	if len(baseFacts) != 1 {
		t.Fatalf("base facts = %#v, want one operation change", baseFacts)
	}
	base := baseFacts[0]
	payload := func(changeKind, phase string, at time.Time, extra string) json.RawMessage {
		t.Helper()
		raw := `{"change_kind":` + quotedJSON(t, changeKind) +
			`,"revision":"discard-this-revision","phase":` + quotedJSON(t, phase) +
			`,"event_at":` + quotedJSON(t, at.Format(time.RFC3339Nano)) + extra + `}`
		return json.RawMessage(raw)
	}

	tests := []struct {
		name   string
		mutate func(*fleet.GraphFact)
	}{
		{name: "source kind mismatch", mutate: func(fact *fleet.GraphFact) { fact.Fact.Ref.SourceKind = "kubeconfig" }},
		{name: "provenance adapter mismatch", mutate: func(fact *fleet.GraphFact) { fact.Fact.Provenance.Adapter = "other" }},
		{name: "protocol mismatch", mutate: func(fact *fleet.GraphFact) { fact.Fact.Provenance.ProtocolV = "2.0.0" }},
		{name: "evidence source mismatch", mutate: func(fact *fleet.GraphFact) { fact.Fact.Source = "cluster-b" }},
		{name: "resource kind mismatch", mutate: func(fact *fleet.GraphFact) { fact.Fact.Ref.Kind = "Deployment" }},
		{name: "entity name mismatch", mutate: func(fact *fleet.GraphFact) { fact.Entity.Name = "other" }},
		{name: "entity carries Pod identity", mutate: func(fact *fleet.GraphFact) { fact.Entity.Pod = "payments-0" }},
		{name: "unexpected reference attributes", mutate: func(fact *fleet.GraphFact) {
			fact.Fact.Ref.Attributes = map[string]string{"untrusted": "value"}
		}},
		{name: "unexpected display field", mutate: func(fact *fleet.GraphFact) {
			fact.Fact.Display = []fleet.DisplayField{{Name: "message", Value: "raw failure"}}
		}},
		{name: "unknown payload field", mutate: func(fact *fleet.GraphFact) {
			fact.Fact.Observed = payload("sync-failed", "Failed", eventAt, `,"message":"do not retain"`)
		}},
		{name: "successful phase with failure kind", mutate: func(fact *fleet.GraphFact) {
			fact.Fact.Observed = payload("sync-failed", "Succeeded", eventAt, "")
		}},
		{name: "failed phase with successful kind", mutate: func(fact *fleet.GraphFact) {
			fact.Fact.Observed = payload("argocd-sync", "Failed", eventAt, "")
		}},
		{name: "unsupported change kind", mutate: func(fact *fleet.GraphFact) {
			fact.Fact.Observed = payload("pipeline-failed", "Failed", eventAt, "")
		}},
		{name: "event time mismatch", mutate: func(fact *fleet.GraphFact) {
			fact.Fact.Observed = payload("sync-failed", "Failed", eventAt.Add(time.Second), "")
		}},
		{name: "history metadata on operation", mutate: func(fact *fleet.GraphFact) {
			fact.Fact.Observed = payload("sync-failed", "Failed", eventAt, `,"history_id":"7"`)
		}},
		{name: "native operation identity mismatch", mutate: func(fact *fleet.GraphFact) {
			fact.Fact.Provenance.NativeID = "cluster-a/argocd/other#operation/id"
		}},
		{name: "native operation identity missing", mutate: func(fact *fleet.GraphFact) {
			fact.Fact.Provenance.NativeID = "cluster-a/argocd/payments#operation/"
		}},
		{name: "unattached fact", mutate: func(fact *fleet.GraphFact) { fact.Entity = nil }},
		{name: "oversized payload", mutate: func(fact *fleet.GraphFact) {
			fact.Fact.Observed = json.RawMessage(`{"change_kind":"sync-failed","phase":"Failed","event_at":"` +
				eventAt.Format(time.RFC3339Nano) + `","revision":"` + strings.Repeat("x", maxArgoChangePayload) + `"}`)
		}},
		{name: "multiple JSON values", mutate: func(fact *fleet.GraphFact) {
			fact.Fact.Observed = append(append(json.RawMessage(nil), fact.Fact.Observed...), []byte(` {}`)...)
		}},
		{name: "malformed JSON", mutate: func(fact *fleet.GraphFact) { fact.Fact.Observed = json.RawMessage(`{"change_kind":`) }},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fact := cloneBrainGraphFact(base)
			test.mutate(&fact)
			if _, err := FromGraphFacts(fleet.LocalWorkspace, []fleet.GraphFact{fact}, covered(fleet.LensTimeline)); err == nil {
				t.Fatalf("FromGraphFacts() error = nil for %#v", fact)
			}
		})
	}

	if _, err := FromGraphFacts("other-workspace", baseFacts, covered(fleet.LensTimeline)); err == nil {
		t.Fatal("FromGraphFacts() workspace mismatch error = nil")
	}
	if _, err := FromGraphFacts(fleet.LocalWorkspace, baseFacts, map[fleet.Lens]LensCoverage{"unknown": {Available: true}}); err == nil {
		t.Fatal("FromGraphFacts() unknown coverage lens error = nil")
	}
}

func TestFromGraphFactsPreservesStalenessWithoutInferringCoverage(t *testing.T) {
	t.Parallel()
	eventAt := time.Date(2026, 7, 18, 15, 45, 0, 0, time.UTC)
	facts := projectedArgoOperation(t, "Failed", eventAt)
	facts[0].Fact.Stale = true

	input, err := FromGraphFacts(fleet.LocalWorkspace, facts, nil)
	if err != nil {
		t.Fatalf("FromGraphFacts() error = %v", err)
	}
	if len(input.Observations) != 1 || !input.Observations[0].Stale {
		t.Fatalf("observations = %#v, want one stale R8 signal", input.Observations)
	}
	if len(input.Coverage) != 0 {
		t.Fatalf("coverage = %#v, projection must not infer coverage", input.Coverage)
	}
	result, err := Evaluate(input)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if len(result.Verdicts) != 1 || result.Verdicts[0].Rule != RuleArgoSyncFail ||
		result.Verdicts[0].Status != StatusUnconfirmed ||
		!result.Verdicts[0].Citations[0].Stale {
		t.Fatalf("verdicts = %#v, want cited stale unconfirmed R8", result.Verdicts)
	}
}

func TestFromGraphFactsProjectsReviewedGitHubWorkflowFailures(t *testing.T) {
	t.Parallel()
	eventAt := time.Date(2026, 7, 18, 19, 55, 0, 123000000, time.UTC)
	for _, conclusion := range []string{"failure", "timed_out", "startup_failure"} {
		conclusion := conclusion
		t.Run(conclusion, func(t *testing.T) {
			t.Parallel()
			facts := projectedGitHubWorkflowRun(t, conclusion, eventAt)
			input, err := FromGraphFacts(fleet.LocalWorkspace, facts, covered(fleet.LensTimeline))
			if err != nil {
				t.Fatalf("FromGraphFacts() error = %v", err)
			}
			if len(input.Observations) != 1 {
				t.Fatalf("observations = %#v, want one canonical workflow-run failure", input.Observations)
			}
			observation := input.Observations[0]
			wantRef := fleet.ResourceRef{
				SourceKind: "github", Scope: "github.com", Kind: "WorkflowRun",
				Namespace: "ArdurAI", Name: "sith#30433642-attempt-2",
			}
			if !reflect.DeepEqual(observation.Ref, wantRef) || observation.Lens != fleet.LensTimeline ||
				observation.Key != "change.kind" || observation.Value != "workflow-run-failed" ||
				!observation.ObservedAt.Equal(eventAt) || observation.Source != "github.com" || observation.Stale {
				t.Fatalf("observation = %#v, want exact sanitized GitHub citation metadata", observation)
			}

			result, err := Evaluate(input)
			if err != nil {
				t.Fatalf("Evaluate() error = %v", err)
			}
			if len(result.Verdicts) != 1 || result.Verdicts[0].Rule != RuleWorkflowFail ||
				result.Verdicts[0].Status != StatusConfirmed || result.Verdicts[0].FleetWide {
				t.Fatalf("verdicts = %#v, want confirmed entity-local R9", result.Verdicts)
			}
			encoded, err := json.Marshal(struct {
				Input  Investigation `json:"input"`
				Result Result        `json:"result"`
			}{Input: input, Result: result})
			if err != nil {
				t.Fatalf("marshal projected result: %v", err)
			}
			for _, discarded := range []string{`"workflow_id":`, `"run_attempt":`, `"conclusion":`, "raw-workflow-secret"} {
				if strings.Contains(string(encoded), discarded) {
					t.Fatalf("brain output retained discarded workflow-run payload %q: %s", discarded, encoded)
				}
			}
		})
	}
}

func TestFromGraphFactsIgnoresUnrelatedGitHubTimelineFacts(t *testing.T) {
	t.Parallel()
	mergedAt := time.Date(2026, 7, 18, 18, 30, 0, 0, time.UTC)
	response, err := json.Marshal(map[string]any{
		"number": 42, "state": "closed", "draft": false, "merged": true,
		"merged_at":        mergedAt.Format(time.RFC3339Nano),
		"merge_commit_sha": strings.Repeat("c", 40),
		"head":             map[string]any{"sha": strings.Repeat("a", 40)},
		"base":             map[string]any{"sha": strings.Repeat("b", 40)},
	})
	if err != nil {
		t.Fatalf("marshal pull response: %v", err)
	}
	facts, err := connectorgithub.ProjectMergedPullRequest(connectorgithub.Projection{
		Workspace: fleet.LocalWorkspace, Host: "github.com", Owner: "ArdurAI", Repository: "sith",
		PullNumber: 42, ObservedAt: mergedAt.Add(time.Minute), Response: response,
	})
	if err != nil {
		t.Fatalf("ProjectMergedPullRequest() error = %v", err)
	}
	input, err := FromGraphFacts(fleet.LocalWorkspace, facts, covered(fleet.LensTimeline))
	if err != nil {
		t.Fatalf("FromGraphFacts() error = %v", err)
	}
	if len(input.Observations) != 0 {
		t.Fatalf("observations = %#v, merged pull request must not prove R9", input.Observations)
	}
}

func TestFromGraphFactsFailsClosedOnAmbiguousGitHubWorkflowFacts(t *testing.T) {
	t.Parallel()
	eventAt := time.Date(2026, 7, 18, 19, 55, 0, 123000000, time.UTC)
	baseFacts := projectedGitHubWorkflowRun(t, "failure", eventAt)
	if len(baseFacts) != 1 {
		t.Fatalf("base facts = %#v, want one workflow-run change", baseFacts)
	}
	base := baseFacts[0]
	payload := func(runID, workflowID, attempt int64, changeKind, conclusion string, at time.Time, extra string) json.RawMessage {
		t.Helper()
		raw := `{"run_id":` + quotedJSON(t, runID) +
			`,"workflow_id":` + quotedJSON(t, workflowID) +
			`,"run_attempt":` + quotedJSON(t, attempt) +
			`,"change_kind":` + quotedJSON(t, changeKind) +
			`,"conclusion":` + quotedJSON(t, conclusion) +
			`,"event_at":` + quotedJSON(t, at.Format(time.RFC3339Nano)) + extra + `}`
		return json.RawMessage(raw)
	}

	tests := []struct {
		name   string
		mutate func(*fleet.GraphFact)
	}{
		{name: "source kind mismatch", mutate: func(fact *fleet.GraphFact) { fact.Fact.Ref.SourceKind = "kubeconfig" }},
		{name: "provenance adapter mismatch", mutate: func(fact *fleet.GraphFact) { fact.Fact.Provenance.Adapter = "other" }},
		{name: "both source fields mismatch with workflow protocol", mutate: func(fact *fleet.GraphFact) {
			fact.Fact.Ref.SourceKind = "kubeconfig"
			fact.Fact.Provenance.Adapter = "other"
		}},
		{name: "workflow protocol on non-timeline fact", mutate: func(fact *fleet.GraphFact) {
			fact.Fact.Kind = fleet.FactHealth
			fact.Lens = fleet.LensLive
		}},
		{name: "protocol mismatch", mutate: func(fact *fleet.GraphFact) { fact.Fact.Provenance.ProtocolV = connectorgithub.ProtocolVersion }},
		{name: "attached fact", mutate: func(fact *fleet.GraphFact) { fact.Entity = &fleet.EntityRef{Cluster: "alpha"} }},
		{name: "resource kind mismatch", mutate: func(fact *fleet.GraphFact) { fact.Fact.Ref.Kind = "PullRequest" }},
		{name: "invalid host", mutate: func(fact *fleet.GraphFact) { fact.Fact.Ref.Scope = "https://github.com" }},
		{name: "invalid owner", mutate: func(fact *fleet.GraphFact) { fact.Fact.Ref.Namespace = "ArdurAI/other" }},
		{name: "evidence source mismatch", mutate: func(fact *fleet.GraphFact) { fact.Fact.Source = "github.example.com" }},
		{name: "unexpected reference attributes", mutate: func(fact *fleet.GraphFact) {
			fact.Fact.Ref.Attributes = map[string]string{"untrusted": "value"}
		}},
		{name: "unexpected display field", mutate: func(fact *fleet.GraphFact) {
			fact.Fact.Display = []fleet.DisplayField{{Name: "message", Value: "raw failure"}}
		}},
		{name: "unknown payload field", mutate: func(fact *fleet.GraphFact) {
			fact.Fact.Observed = payload(30433642, 159038, 2, "workflow-run-failed", "failure", eventAt, `,"message":"do not retain"`)
		}},
		{name: "duplicate payload field", mutate: func(fact *fleet.GraphFact) {
			fact.Fact.Observed = payload(30433642, 159038, 2, "workflow-run-failed", "failure", eventAt, `,"run_id":30433642`)
		}},
		{name: "mixed-case payload alias", mutate: func(fact *fleet.GraphFact) {
			fact.Fact.Observed = payload(30433642, 159038, 2, "workflow-run-failed", "failure", eventAt, `,"RUN_ID":30433642`)
		}},
		{name: "zero run ID", mutate: func(fact *fleet.GraphFact) {
			fact.Fact.Observed = payload(0, 159038, 2, "workflow-run-failed", "failure", eventAt, "")
		}},
		{name: "zero workflow ID", mutate: func(fact *fleet.GraphFact) {
			fact.Fact.Observed = payload(30433642, 0, 2, "workflow-run-failed", "failure", eventAt, "")
		}},
		{name: "zero attempt", mutate: func(fact *fleet.GraphFact) {
			fact.Fact.Observed = payload(30433642, 159038, 0, "workflow-run-failed", "failure", eventAt, "")
		}},
		{name: "unsupported change kind", mutate: func(fact *fleet.GraphFact) {
			fact.Fact.Observed = payload(30433642, 159038, 2, "pipeline-failed", "failure", eventAt, "")
		}},
		{name: "nonfailure conclusion", mutate: func(fact *fleet.GraphFact) {
			fact.Fact.Observed = payload(30433642, 159038, 2, "workflow-run-failed", "success", eventAt, "")
		}},
		{name: "event time mismatch", mutate: func(fact *fleet.GraphFact) {
			fact.Fact.Observed = payload(30433642, 159038, 2, "workflow-run-failed", "failure", eventAt.Add(time.Second), "")
		}},
		{name: "resource run ID mismatch", mutate: func(fact *fleet.GraphFact) { fact.Fact.Ref.Name = "sith#7-attempt-2" }},
		{name: "resource attempt mismatch", mutate: func(fact *fleet.GraphFact) { fact.Fact.Ref.Name = "sith#30433642-attempt-3" }},
		{name: "invalid repository identity", mutate: func(fact *fleet.GraphFact) { fact.Fact.Ref.Name = "other/repo#30433642-attempt-2" }},
		{name: "git-suffixed repository identity", mutate: func(fact *fleet.GraphFact) { fact.Fact.Ref.Name = "sith.git#30433642-attempt-2" }},
		{name: "native identity mismatch", mutate: func(fact *fleet.GraphFact) { fact.Fact.Provenance.NativeID = "ArdurAI/other#30433642-attempt-2" }},
		{name: "oversized payload", mutate: func(fact *fleet.GraphFact) {
			fact.Fact.Observed = json.RawMessage(strings.Repeat(" ", maxGitHubWorkflowChangePayload+1))
		}},
		{name: "multiple JSON values", mutate: func(fact *fleet.GraphFact) {
			fact.Fact.Observed = append(append(json.RawMessage(nil), fact.Fact.Observed...), []byte(` {}`)...)
		}},
		{name: "malformed JSON", mutate: func(fact *fleet.GraphFact) { fact.Fact.Observed = json.RawMessage(`{"run_id":`) }},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fact := cloneBrainGraphFact(base)
			test.mutate(&fact)
			if _, err := FromGraphFacts(fleet.LocalWorkspace, []fleet.GraphFact{fact}, covered(fleet.LensTimeline)); err == nil {
				t.Fatalf("FromGraphFacts() error = nil for %#v", fact)
			}
		})
	}
}

func TestFromGraphFactsPreservesGitHubWorkflowStalenessWithoutInferringCoverage(t *testing.T) {
	t.Parallel()
	eventAt := time.Date(2026, 7, 18, 19, 55, 0, 123000000, time.UTC)
	facts := projectedGitHubWorkflowRun(t, "failure", eventAt)
	facts[0].Fact.Stale = true

	input, err := FromGraphFacts(fleet.LocalWorkspace, facts, nil)
	if err != nil {
		t.Fatalf("FromGraphFacts() error = %v", err)
	}
	if len(input.Observations) != 1 || !input.Observations[0].Stale || len(input.Coverage) != 0 {
		t.Fatalf("input = %#v, want one stale R9 signal and no inferred coverage", input)
	}
	result, err := Evaluate(input)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if len(result.Verdicts) != 1 || result.Verdicts[0].Rule != RuleWorkflowFail ||
		result.Verdicts[0].Status != StatusUnconfirmed || !result.Verdicts[0].Citations[0].Stale {
		t.Fatalf("verdicts = %#v, want cited stale unconfirmed R9", result.Verdicts)
	}
}

func projectedArgoOperation(t *testing.T, phase string, eventAt time.Time) []fleet.GraphFact {
	t.Helper()
	facts, err := argocd.ProjectApplication(argocd.Projection{
		Workspace:  fleet.LocalWorkspace,
		Scope:      "cluster-a",
		ObservedAt: eventAt.Add(time.Minute),
		Application: argoApplication(map[string]any{
			"operationState": map[string]any{
				"phase": phase, "finishedAt": eventAt.Format(time.RFC3339Nano),
				"syncResult": map[string]any{"revision": "discard-this-revision"},
			},
		}),
	})
	if err != nil {
		t.Fatalf("ProjectApplication() error = %v", err)
	}
	return facts
}

func projectedGitHubWorkflowRun(t *testing.T, conclusion string, eventAt time.Time) []fleet.GraphFact {
	t.Helper()
	response, err := json.Marshal(map[string]any{
		"id": 30433642, "workflow_id": 159038, "run_attempt": 2,
		"status": "completed", "conclusion": conclusion, "updated_at": eventAt.Format(time.RFC3339Nano),
		"repository":    map[string]any{"full_name": "ArdurAI/sith"},
		"display_title": "raw-workflow-secret", "jobs_url": "https://attacker.example/jobs",
	})
	if err != nil {
		t.Fatalf("marshal workflow-run response: %v", err)
	}
	facts, err := connectorgithub.ProjectFailedWorkflowRun(connectorgithub.WorkflowRunProjection{
		Workspace: fleet.LocalWorkspace, Host: "github.com", Owner: "ArdurAI", Repository: "sith",
		RunID: 30433642, ObservedAt: eventAt.Add(time.Minute), Response: response,
	})
	if err != nil {
		t.Fatalf("ProjectFailedWorkflowRun() error = %v", err)
	}
	return facts
}

func argoApplication(status map[string]any) unstructured.Unstructured {
	return unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "argoproj.io/v1alpha1",
		"kind":       "Application",
		"metadata":   map[string]any{"name": "payments", "namespace": "argocd"},
		"status":     status,
	}}
}

func quotedJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal JSON string: %v", err)
	}
	return string(encoded)
}

func cloneBrainGraphFact(fact fleet.GraphFact) fleet.GraphFact {
	cloned := fact
	cloned.Fact.Observed = append(json.RawMessage(nil), fact.Fact.Observed...)
	cloned.Fact.Display = append([]fleet.DisplayField(nil), fact.Fact.Display...)
	if fact.Fact.Ref.Attributes != nil {
		cloned.Fact.Ref.Attributes = make(map[string]string, len(fact.Fact.Ref.Attributes))
		for key, value := range fact.Fact.Ref.Attributes {
			cloned.Fact.Ref.Attributes[key] = value
		}
	}
	if fact.Entity != nil {
		entity := *fact.Entity
		cloned.Entity = &entity
	}
	return cloned
}
