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

func argoApplication(status map[string]any) unstructured.Unstructured {
	return unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "argoproj.io/v1alpha1",
		"kind":       "Application",
		"metadata":   map[string]any{"name": "payments", "namespace": "argocd"},
		"status":     status,
	}}
}

func quotedJSON(t *testing.T, value string) string {
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
