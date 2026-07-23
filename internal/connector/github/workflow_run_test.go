// SPDX-License-Identifier: Apache-2.0

package github

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ArdurAI/sith/internal/fleet"
)

const testWorkflowRunID int64 = 30433642

func TestProjectFailedWorkflowRunEmitsSanitizedUnattachedTimelineFact(t *testing.T) {
	t.Parallel()
	for _, conclusion := range []string{"failure", "timed_out", "startup_failure"} {
		conclusion := conclusion
		t.Run(conclusion, func(t *testing.T) {
			t.Parallel()
			input := validWorkflowRunProjection(t)
			input.Response = mutateResponse(t, input.Response, func(response map[string]any) {
				response["conclusion"] = conclusion
			})
			facts, err := ProjectFailedWorkflowRun(input)
			if err != nil {
				t.Fatalf("ProjectFailedWorkflowRun() error = %v", err)
			}
			if len(facts) != 1 {
				t.Fatalf("fact count = %d, want 1", len(facts))
			}
			fact := facts[0]
			wantEventAt := time.Date(2026, 7, 18, 19, 55, 0, 123000000, time.UTC)
			if fact.Entity != nil || fact.Fact.Kind != fleet.FactChange || fact.Lens != fleet.LensTimeline {
				t.Fatalf("unexpected graph contract: %#v", fact)
			}
			wantRef := fleet.ResourceRef{
				SourceKind: Kind,
				Scope:      "github.example.com",
				Kind:       "WorkflowRun",
				Namespace:  "ArdurAI",
				Name:       "sith#30433642-attempt-2",
			}
			if fact.Fact.Workspace != "workspace-a" || !fact.Fact.Ref.Equal(wantRef) || fact.Fact.Ref.Attributes != nil {
				t.Fatalf("unexpected trusted identity: %#v", fact.Fact)
			}
			wantProvenance := fleet.Provenance{
				Adapter: Kind, ProtocolV: WorkflowRunProtocolVersion,
				NativeID: "ArdurAI/sith#30433642-attempt-2",
			}
			if fact.Fact.Source != "github.example.com" || !fact.Fact.ObservedAt.Equal(wantEventAt) ||
				fact.Fact.Provenance != wantProvenance {
				t.Fatalf("unexpected evidence metadata: %#v", fact.Fact)
			}
			var observation workflowRunObservation
			if err := json.Unmarshal(fact.Fact.Observed, &observation); err != nil {
				t.Fatalf("decode observation: %v", err)
			}
			wantObservation := workflowRunObservation{
				RunID: testWorkflowRunID, WorkflowID: 159038, RunAttempt: 2,
				ChangeKind: workflowRunFailureKind, Conclusion: conclusion, EventAt: wantEventAt,
			}
			if observation != wantObservation {
				t.Fatalf("observation = %#v, want %#v", observation, wantObservation)
			}
			if len(fact.Fact.Observed) > maxFactPayloadBytes {
				t.Fatalf("encoded fact is %d bytes, limit %d", len(fact.Fact.Observed), maxFactPayloadBytes)
			}

			encoded, err := json.Marshal(facts)
			if err != nil {
				t.Fatalf("marshal facts: %v", err)
			}
			for _, forbidden := range []string{
				"workflow-title-secret", "branch-secret", "commit-message-secret", "actor-secret",
				"token-secret", "logs-secret", "jobs-secret", "artifact-secret", "attacker.example",
			} {
				if bytes.Contains(encoded, []byte(forbidden)) {
					t.Fatalf("projected fact retained forbidden response value %q: %s", forbidden, encoded)
				}
			}
			graph, err := fleet.NewGraph(input.Workspace, facts)
			if err != nil {
				t.Fatalf("NewGraph() error = %v", err)
			}
			if len(graph.Nodes) != 0 || len(graph.Unattached) != 1 {
				t.Fatalf("graph nodes/unattached = %d/%d, want 0/1", len(graph.Nodes), len(graph.Unattached))
			}
		})
	}
	if WorkflowRunProtocolVersion != "workflow-runs/2026-03-10" {
		t.Fatalf("workflow-run protocol version = %q", WorkflowRunProtocolVersion)
	}
}

func TestProjectFailedWorkflowRunIsDeterministicAcrossDiscardedSourceData(t *testing.T) {
	t.Parallel()
	first := validWorkflowRunProjection(t)
	second := validWorkflowRunProjection(t)
	second.Response = mutateResponse(t, second.Response, func(response map[string]any) {
		response["name"] = "different discarded name"
		response["display_title"] = "different discarded title"
		response["head_branch"] = "different-discarded-branch"
		response["ID"] = 7
		response["STATUS"] = "queued"
		response["repository"].(map[string]any)["FULL_NAME"] = "attacker/other"
		response["unknown_additive_field"] = map[string]any{"safe_to_drop": true}
	})
	firstFacts, err := ProjectFailedWorkflowRun(first)
	if err != nil {
		t.Fatalf("first projection error = %v", err)
	}
	secondFacts, err := ProjectFailedWorkflowRun(second)
	if err != nil {
		t.Fatalf("second projection error = %v", err)
	}
	firstJSON, _ := json.Marshal(firstFacts)
	secondJSON, _ := json.Marshal(secondFacts)
	if !slices.Equal(firstJSON, secondJSON) {
		t.Fatalf("discarded source fields changed projection\nfirst:  %s\nsecond: %s", firstJSON, secondJSON)
	}
}

func TestProjectFailedWorkflowRunAbstainsForNonFailureRuns(t *testing.T) {
	t.Parallel()
	tests := make(map[string]func(map[string]any))
	for _, status := range []string{"queued", "in_progress", "requested", "waiting", "pending"} {
		status := status
		tests[status] = func(response map[string]any) {
			response["status"] = status
			response["conclusion"] = nil
		}
	}
	for _, conclusion := range []string{
		"action_required", "cancelled", "neutral", "skipped", "stale", "success", //nolint:misspell // GitHub's wire value uses British spelling.
	} {
		conclusion := conclusion
		tests["completed-"+conclusion] = func(response map[string]any) {
			response["status"] = "completed"
			response["conclusion"] = conclusion
		}
	}
	for name, mutate := range tests {
		name, mutate := name, mutate
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			input := validWorkflowRunProjection(t)
			input.Response = mutateResponse(t, input.Response, mutate)
			facts, err := ProjectFailedWorkflowRun(input)
			if err != nil {
				t.Fatalf("ProjectFailedWorkflowRun() error = %v", err)
			}
			if len(facts) != 0 {
				t.Fatalf("facts = %#v, want honest abstention", facts)
			}
		})
	}
}

func TestProjectFailedWorkflowRunAcceptsGitHubCaseInsensitiveRepositoryIdentity(t *testing.T) {
	t.Parallel()
	input := validWorkflowRunProjection(t)
	input.Response = mutateResponse(t, input.Response, func(response map[string]any) {
		response["repository"].(map[string]any)["full_name"] = "ardurai/SITH"
	})
	facts, err := ProjectFailedWorkflowRun(input)
	if err != nil || len(facts) != 1 {
		t.Fatalf("ProjectFailedWorkflowRun() = %#v, %v, want one fact", facts, err)
	}
}

func TestProjectFailedWorkflowRunRejectsNonCanonicalResponseRepositoryIdentity(t *testing.T) {
	t.Parallel()
	for _, fullName := range []string{
		"ArdurAI/sith/other", "ArdurAI/sith.git", "ArdurAI/.", "ArdurAI/..", "ArdurAI/sith secret",
		"ArdurA\u212a/sith",
	} {
		fullName := fullName
		t.Run(fullName, func(t *testing.T) {
			t.Parallel()
			input := validWorkflowRunProjection(t)
			input.Response = mutateResponse(t, input.Response, func(response map[string]any) {
				response["repository"].(map[string]any)["full_name"] = fullName
			})
			if _, err := ProjectFailedWorkflowRun(input); err == nil {
				t.Fatal("ProjectFailedWorkflowRun() error = nil")
			}
		})
	}
}

func TestProjectFailedWorkflowRunRejectsMalformedOrInconsistentEvidence(t *testing.T) {
	t.Parallel()
	valid := validWorkflowRunProjection(t)
	future := valid.ObservedAt.Add(maxClockSkew + time.Nanosecond).Format(time.RFC3339Nano)
	tests := []struct {
		name   string
		mutate func(*WorkflowRunProjection)
	}{
		{name: "missing workspace", mutate: func(input *WorkflowRunProjection) { input.Workspace = "" }},
		{name: "invalid host", mutate: func(input *WorkflowRunProjection) { input.Host = "https://github.com" }},
		{name: "invalid owner", mutate: func(input *WorkflowRunProjection) { input.Owner = "owner/other" }},
		{name: "invalid repository", mutate: func(input *WorkflowRunProjection) { input.Repository = "sith.git" }},
		{name: "invalid run ID", mutate: func(input *WorkflowRunProjection) { input.RunID = 0 }},
		{name: "missing observation time", mutate: func(input *WorkflowRunProjection) { input.ObservedAt = time.Time{} }},
		{name: "missing response", mutate: func(input *WorkflowRunProjection) { input.Response = nil }},
		{name: "oversized response", mutate: func(input *WorkflowRunProjection) { input.Response = bytes.Repeat([]byte("x"), maxResponseBytes+1) }},
		{name: "invalid UTF-8", mutate: func(input *WorkflowRunProjection) { input.Response = []byte{0xff} }},
		{name: "null response", mutate: func(input *WorkflowRunProjection) { input.Response = []byte("null") }},
		{name: "duplicate member", mutate: func(input *WorkflowRunProjection) { input.Response = []byte(`{"id":30433642,"id":30433643}`) }},
		{name: "mismatched run ID", mutate: mutateWorkflowRunResponse(t, func(response map[string]any) { response["id"] = 7 })},
		{name: "mixed-case run ID alias", mutate: mutateWorkflowRunResponse(t, func(response map[string]any) {
			delete(response, "id")
			response["ID"] = testWorkflowRunID
		})},
		{name: "missing workflow ID", mutate: mutateWorkflowRunResponse(t, func(response map[string]any) { delete(response, "workflow_id") })},
		{name: "zero workflow ID", mutate: mutateWorkflowRunResponse(t, func(response map[string]any) { response["workflow_id"] = 0 })},
		{name: "missing attempt", mutate: mutateWorkflowRunResponse(t, func(response map[string]any) { delete(response, "run_attempt") })},
		{name: "zero attempt", mutate: mutateWorkflowRunResponse(t, func(response map[string]any) { response["run_attempt"] = 0 })},
		{name: "missing repository", mutate: mutateWorkflowRunResponse(t, func(response map[string]any) { delete(response, "repository") })},
		{name: "mixed-case repository full-name alias", mutate: mutateWorkflowRunResponse(t, func(response map[string]any) {
			repository := response["repository"].(map[string]any)
			delete(repository, "full_name")
			repository["FULL_NAME"] = "ArdurAI/sith"
		})},
		{name: "mismatched repository", mutate: mutateWorkflowRunResponse(t, func(response map[string]any) {
			response["repository"].(map[string]any)["full_name"] = "ArdurAI/other"
		})},
		{name: "missing status", mutate: mutateWorkflowRunResponse(t, func(response map[string]any) { delete(response, "status") })},
		{name: "mixed-case status alias", mutate: mutateWorkflowRunResponse(t, func(response map[string]any) {
			delete(response, "status")
			response["STATUS"] = "completed"
		})},
		{name: "unknown status", mutate: mutateWorkflowRunResponse(t, func(response map[string]any) { response["status"] = "running" })},
		{name: "incomplete with conclusion", mutate: mutateWorkflowRunResponse(t, func(response map[string]any) { response["status"] = "queued" })},
		{name: "completed without conclusion", mutate: mutateWorkflowRunResponse(t, func(response map[string]any) { response["conclusion"] = nil })},
		{name: "unknown conclusion", mutate: mutateWorkflowRunResponse(t, func(response map[string]any) { response["conclusion"] = "soft_failure" })},
		{name: "missing update time", mutate: mutateWorkflowRunResponse(t, func(response map[string]any) { delete(response, "updated_at") })},
		{name: "malformed update time", mutate: mutateWorkflowRunResponse(t, func(response map[string]any) { response["updated_at"] = "not-a-time" })},
		{name: "future update time", mutate: mutateWorkflowRunResponse(t, func(response map[string]any) { response["updated_at"] = future })},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			input := valid
			input.Response = append([]byte(nil), valid.Response...)
			test.mutate(&input)
			if _, err := ProjectFailedWorkflowRun(input); err == nil {
				t.Fatal("ProjectFailedWorkflowRun() error = nil")
			}
		})
	}
}

func TestProjectFailedWorkflowRunAllowsBoundedCollectionClockSkew(t *testing.T) {
	t.Parallel()
	input := validWorkflowRunProjection(t)
	input.Response = mutateResponse(t, input.Response, func(response map[string]any) {
		response["updated_at"] = input.ObservedAt.Add(maxClockSkew).Format(time.RFC3339Nano)
	})
	facts, err := ProjectFailedWorkflowRun(input)
	if err != nil || len(facts) != 1 {
		t.Fatalf("ProjectFailedWorkflowRun() = %#v, %v, want one fact", facts, err)
	}
}

func validWorkflowRunProjection(t testing.TB) WorkflowRunProjection {
	t.Helper()
	return WorkflowRunProjection{
		Workspace: "workspace-a", Host: "github.example.com", Owner: "ArdurAI", Repository: "sith",
		RunID: testWorkflowRunID, ObservedAt: time.Date(2026, 7, 18, 20, 0, 0, 0, time.UTC),
		Response: validFailedWorkflowRunResponse(t),
	}
}

func validFailedWorkflowRunResponse(t testing.TB) []byte {
	t.Helper()
	return encodeResponse(t, map[string]any{
		"id": testWorkflowRunID, "workflow_id": 159038, "run_attempt": 2,
		"status": "completed", "conclusion": "failure", "updated_at": "2026-07-18T14:55:00.123-05:00",
		"repository": map[string]any{
			"full_name": "ArdurAI/sith", "description": "repository-metadata-secret",
			"url": "https://attacker.example/repository",
		},
		"name": "workflow-title-secret", "display_title": "commit-message-secret",
		"head_branch": "branch-secret", "head_sha": strings.Repeat("a", 40),
		"actor":         map[string]any{"login": "actor-secret", "token": "token-secret"},
		"logs_url":      "https://attacker.example/logs-secret",
		"jobs_url":      "https://attacker.example/jobs-secret",
		"artifacts_url": "https://attacker.example/artifact-secret",
	})
}

func mutateWorkflowRunResponse(t *testing.T, mutate func(map[string]any)) func(*WorkflowRunProjection) {
	t.Helper()
	return func(input *WorkflowRunProjection) {
		input.Response = mutateResponse(t, input.Response, mutate)
	}
}

func FuzzProjectFailedWorkflowRunNeverEmitsInvalidOrUnboundedFacts(f *testing.F) {
	f.Add(validFailedWorkflowRunResponse(f))
	f.Add([]byte(`{"id":30433642,"workflow_id":1,"run_attempt":1,"status":"queued","conclusion":null,"updated_at":"2026-07-18T19:55:00Z","repository":{"full_name":"ArdurAI/sith"}}`))
	f.Add([]byte(`{"id":30433642,"id":7}`))
	f.Add([]byte(`null`))
	f.Fuzz(func(t *testing.T, response []byte) {
		input := WorkflowRunProjection{
			Workspace: "workspace-a", Host: "github.com", Owner: "ArdurAI", Repository: "sith",
			RunID: testWorkflowRunID, ObservedAt: time.Date(2026, 7, 18, 20, 0, 0, 0, time.UTC),
			Response: response,
		}
		facts, err := ProjectFailedWorkflowRun(input)
		if err != nil {
			return
		}
		if len(facts) > 1 {
			t.Fatalf("fact count = %d, want at most 1", len(facts))
		}
		for _, fact := range facts {
			if err := fact.Validate(input.Workspace); err != nil {
				t.Fatalf("emitted invalid fact: %v", err)
			}
			if fact.Entity != nil || fact.Lens != fleet.LensTimeline || fact.Fact.Kind != fleet.FactChange {
				t.Fatalf("emitted fact outside contract: %#v", fact)
			}
			if len(fact.Fact.Observed) > maxFactPayloadBytes {
				t.Fatalf("emitted %d-byte payload, limit %d", len(fact.Fact.Observed), maxFactPayloadBytes)
			}
		}
	})
}

func ExampleProjectFailedWorkflowRun() {
	response := []byte(`{"id":30433642,"workflow_id":159038,"run_attempt":1,"status":"completed","conclusion":"failure","updated_at":"2026-07-18T19:55:00Z","repository":{"full_name":"ArdurAI/sith"}}`)
	facts, err := ProjectFailedWorkflowRun(WorkflowRunProjection{
		Workspace: "workspace-a", Host: "github.com", Owner: "ArdurAI", Repository: "sith",
		RunID: testWorkflowRunID, ObservedAt: time.Date(2026, 7, 18, 20, 0, 0, 0, time.UTC),
		Response: response,
	})
	fmt.Println(len(facts), err)
	// Output: 1 <nil>
}

func TestWorkflowRunObservationShapeIsClosed(t *testing.T) {
	t.Parallel()
	typeOf := reflect.TypeOf(workflowRunObservation{})
	want := []string{"run_id", "workflow_id", "run_attempt", "change_kind", "conclusion", "event_at"}
	got := make([]string, 0, typeOf.NumField())
	for index := 0; index < typeOf.NumField(); index++ {
		got = append(got, strings.Split(typeOf.Field(index).Tag.Get("json"), ",")[0])
	}
	if !slices.Equal(got, want) {
		t.Fatalf("workflow-run observation fields = %q, want %q", got, want)
	}
}
