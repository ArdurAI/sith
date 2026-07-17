// SPDX-License-Identifier: Apache-2.0

package github

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ArdurAI/sith/internal/fleet"
)

var (
	headSHA  = strings.Repeat("a", 40)
	baseSHA  = strings.Repeat("b", 40)
	mergeSHA = strings.Repeat("c", 40)
)

func TestProjectMergedPullRequestEmitsSanitizedUnattachedTimelineFact(t *testing.T) {
	t.Parallel()
	input := validProjection(t)
	facts, err := ProjectMergedPullRequest(input)
	if err != nil {
		t.Fatalf("ProjectMergedPullRequest() error = %v", err)
	}
	if len(facts) != 1 {
		t.Fatalf("fact count = %d, want 1", len(facts))
	}
	fact := facts[0]
	wantMergedAt := time.Date(2026, 7, 16, 20, 30, 0, 123000000, time.UTC)
	if fact.Fact.Kind != fleet.FactChange || fact.Lens != fleet.LensTimeline || fact.Entity != nil {
		t.Fatalf("unexpected graph contract: %#v", fact)
	}
	wantRef := fleet.ResourceRef{
		SourceKind: Kind,
		Scope:      "github.example.com",
		Kind:       "PullRequest",
		Namespace:  "ArdurAI",
		Name:       "sith#42",
	}
	if fact.Fact.Workspace != "workspace-a" || !fact.Fact.Ref.Equal(wantRef) || fact.Fact.Ref.Attributes != nil {
		t.Fatalf("unexpected trusted identity: %#v", fact.Fact)
	}
	if fact.Fact.Source != "github.example.com" || !fact.Fact.ObservedAt.Equal(wantMergedAt) ||
		fact.Fact.Provenance != (fleet.Provenance{
			Adapter: Kind, ProtocolV: ProtocolVersion, NativeID: "ArdurAI/sith#42@" + mergeSHA,
		}) {
		t.Fatalf("unexpected provenance: %#v", fact.Fact)
	}
	var observation changeObservation
	if err := json.Unmarshal(fact.Fact.Observed, &observation); err != nil {
		t.Fatalf("decode observation: %v", err)
	}
	if observation != (changeObservation{
		PullNumber: 42, ChangeKind: "pull-request-merged", HeadSHA: headSHA,
		BaseSHA: baseSHA, MergeCommitSHA: mergeSHA, MergedAt: wantMergedAt,
	}) {
		t.Fatalf("observation = %#v", observation)
	}
	if len(fact.Fact.Observed) > maxFactPayloadBytes {
		t.Fatalf("encoded fact is %d bytes, limit %d", len(fact.Fact.Observed), maxFactPayloadBytes)
	}

	encoded, err := json.Marshal(facts)
	if err != nil {
		t.Fatalf("marshal facts: %v", err)
	}
	for _, forbidden := range []string{
		"body-secret", "title-secret", "token-secret", "user-secret", "label-secret",
		"attacker.example", "attacker-owner", "attacker-repository", "head-secret", "base-secret",
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
	if APIVersion != "2026-03-10" || ProtocolVersion != "pulls/2026-03-10" {
		t.Fatalf("unexpected API/protocol versions %q/%q", APIVersion, ProtocolVersion)
	}
}

func TestProjectMergedPullRequestIsDeterministicAcrossDiscardedSourceData(t *testing.T) {
	t.Parallel()
	first := validProjection(t)
	second := validProjection(t)
	second.Response = mutateResponse(t, second.Response, func(response map[string]any) {
		response["title"] = "different title"
		response["body"] = "different body"
		response["unknown_additive_field"] = map[string]any{"safe_to_drop": true}
	})
	firstFacts, err := ProjectMergedPullRequest(first)
	if err != nil {
		t.Fatalf("first projection error = %v", err)
	}
	secondFacts, err := ProjectMergedPullRequest(second)
	if err != nil {
		t.Fatalf("second projection error = %v", err)
	}
	firstJSON, _ := json.Marshal(firstFacts)
	secondJSON, _ := json.Marshal(secondFacts)
	if !slices.Equal(firstJSON, secondJSON) {
		t.Fatalf("discarded source fields changed projection\nfirst:  %s\nsecond: %s", firstJSON, secondJSON)
	}
}

func TestProjectMergedPullRequestAbstainsForValidUnmergedResponses(t *testing.T) {
	t.Parallel()
	for _, state := range []string{"open", "closed"} {
		state := state
		t.Run(state, func(t *testing.T) {
			t.Parallel()
			input := validProjection(t)
			input.Response = mutateResponse(t, input.Response, func(response map[string]any) {
				response["state"] = state
				response["draft"] = state == "open"
				response["merged"] = false
				response["merged_at"] = nil
				// GitHub documents this as a test-merge SHA before merge. It must not create a fact.
				response["merge_commit_sha"] = strings.Repeat("d", 40)
			})
			facts, err := ProjectMergedPullRequest(input)
			if err != nil {
				t.Fatalf("ProjectMergedPullRequest() error = %v", err)
			}
			if len(facts) != 0 {
				t.Fatalf("facts = %#v, want honest abstention", facts)
			}
		})
	}
}

func TestProjectMergedPullRequestAcceptsSHA256CommitIdentifiers(t *testing.T) {
	t.Parallel()
	input := validProjection(t)
	input.Response = mutateResponse(t, input.Response, func(response map[string]any) {
		response["merge_commit_sha"] = strings.Repeat("1", 64)
		response["head"] = map[string]any{"sha": strings.Repeat("2", 64)}
		response["base"] = map[string]any{"sha": strings.Repeat("3", 64)}
	})
	facts, err := ProjectMergedPullRequest(input)
	if err != nil {
		t.Fatalf("ProjectMergedPullRequest() error = %v", err)
	}
	if len(facts) != 1 {
		t.Fatalf("fact count = %d, want 1", len(facts))
	}
}

func TestProjectMergedPullRequestAllowsBoundedCollectionClockSkew(t *testing.T) {
	t.Parallel()
	input := validProjection(t)
	input.Response = mutateResponse(t, input.Response, func(response map[string]any) {
		response["merged_at"] = input.ObservedAt.UTC().Add(maxClockSkew).Format(time.RFC3339Nano)
	})
	facts, err := ProjectMergedPullRequest(input)
	if err != nil {
		t.Fatalf("ProjectMergedPullRequest() error = %v", err)
	}
	if len(facts) != 1 {
		t.Fatalf("fact count = %d, want 1", len(facts))
	}
}

func TestProjectMergedPullRequestRejectsMalformedOrInconsistentEvidence(t *testing.T) {
	t.Parallel()
	valid := validProjection(t)

	tests := []struct {
		name   string
		mutate func(*Projection)
	}{
		{"missing workspace", func(input *Projection) { input.Workspace = "" }},
		{"padded workspace", func(input *Projection) { input.Workspace = " workspace-a" }},
		{"control workspace", func(input *Projection) { input.Workspace = "workspace-a\nother" }},
		{"oversized workspace", func(input *Projection) { input.Workspace = strings.Repeat("w", maxWorkspaceBytes+1) }},
		{"missing host", func(input *Projection) { input.Host = "" }},
		{"uppercase host", func(input *Projection) { input.Host = "GitHub.com" }},
		{"scheme in host", func(input *Projection) { input.Host = "https://github.com" }},
		{"port in host", func(input *Projection) { input.Host = "github.com:443" }},
		{"empty host label", func(input *Projection) { input.Host = "github..com" }},
		{"hyphen host edge", func(input *Projection) { input.Host = "-github.com" }},
		{"oversized host label", func(input *Projection) { input.Host = strings.Repeat("a", 64) + ".com" }},
		{"invalid owner", func(input *Projection) { input.Owner = "owner/other" }},
		{"dot owner", func(input *Projection) { input.Owner = "." }},
		{"oversized owner", func(input *Projection) { input.Owner = strings.Repeat("o", maxOwnerBytes+1) }},
		{"invalid repository", func(input *Projection) { input.Repository = "repo/other" }},
		{"dot repository", func(input *Projection) { input.Repository = ".." }},
		{"git suffix", func(input *Projection) { input.Repository = "sith.GIT" }},
		{"oversized repository", func(input *Projection) { input.Repository = strings.Repeat("r", maxRepositoryBytes+1) }},
		{"zero pull number", func(input *Projection) { input.PullNumber = 0 }},
		{"negative pull number", func(input *Projection) { input.PullNumber = -1 }},
		{"zero observed time", func(input *Projection) { input.ObservedAt = time.Time{} }},
		{"empty response", func(input *Projection) { input.Response = nil }},
		{"oversized response", func(input *Projection) { input.Response = bytes.Repeat([]byte(" "), maxResponseBytes+1) }},
		{"invalid UTF-8", func(input *Projection) { input.Response = []byte{'{', 0xff, '}'} }},
		{"malformed JSON", func(input *Projection) { input.Response = []byte(`{"number":`) }},
		{"null document", func(input *Projection) { input.Response = []byte(`null`) }},
		{"duplicate root member", func(input *Projection) {
			input.Response = []byte(`{"number":42,"number":42,"state":"closed","draft":false,"merged":false}`)
		}},
		{"duplicate discarded nested member", func(input *Projection) {
			input.Response = []byte(`{"number":42,"state":"open","draft":false,"merged":false,"unknown":{"secret":"a","secret":"b"}}`)
		}},
		{"trailing JSON", func(input *Projection) { input.Response = append(input.Response, []byte(` {}`)...) }},
		{"excessive nesting", func(input *Projection) {
			input.Response = []byte(strings.Repeat("[", maxJSONDepth+1) + strings.Repeat("]", maxJSONDepth+1))
		}},
		{"missing number", mutateProjectionResponse(t, func(response map[string]any) { delete(response, "number") })},
		{"null number", mutateProjectionResponse(t, func(response map[string]any) { response["number"] = nil })},
		{"fractional number", mutateProjectionResponse(t, func(response map[string]any) { response["number"] = 42.5 })},
		{"mismatched number", mutateProjectionResponse(t, func(response map[string]any) { response["number"] = 43 })},
		{"wrong-case number alias", mutateProjectionResponse(t, func(response map[string]any) {
			delete(response, "number")
			response["Number"] = 42
		})},
		{"missing state", mutateProjectionResponse(t, func(response map[string]any) { delete(response, "state") })},
		{"invalid state", mutateProjectionResponse(t, func(response map[string]any) { response["state"] = "merged" })},
		{"non-string state", mutateProjectionResponse(t, func(response map[string]any) { response["state"] = true })},
		{"missing draft", mutateProjectionResponse(t, func(response map[string]any) { delete(response, "draft") })},
		{"non-boolean draft", mutateProjectionResponse(t, func(response map[string]any) { response["draft"] = "false" })},
		{"missing merged", mutateProjectionResponse(t, func(response map[string]any) { delete(response, "merged") })},
		{"non-boolean merged", mutateProjectionResponse(t, func(response map[string]any) { response["merged"] = "true" })},
		{"unmerged with merged_at", mutateProjectionResponse(t, func(response map[string]any) { response["merged"] = false })},
		{"merged open", mutateProjectionResponse(t, func(response map[string]any) { response["state"] = "open" })},
		{"merged draft", mutateProjectionResponse(t, func(response map[string]any) { response["draft"] = true })},
		{"missing merged_at", mutateProjectionResponse(t, func(response map[string]any) { delete(response, "merged_at") })},
		{"null merged_at", mutateProjectionResponse(t, func(response map[string]any) { response["merged_at"] = nil })},
		{"non-string merged_at", mutateProjectionResponse(t, func(response map[string]any) { response["merged_at"] = 1 })},
		{"invalid merged_at", mutateProjectionResponse(t, func(response map[string]any) { response["merged_at"] = "yesterday" })},
		{"zero merged_at", mutateProjectionResponse(t, func(response map[string]any) { response["merged_at"] = "0001-01-01T00:00:00Z" })},
		{"future merged_at", mutateProjectionResponse(t, func(response map[string]any) {
			response["merged_at"] = valid.ObservedAt.UTC().Add(maxClockSkew + time.Nanosecond).Format(time.RFC3339Nano)
		})},
		{"missing head", mutateProjectionResponse(t, func(response map[string]any) { delete(response, "head") })},
		{"null head", mutateProjectionResponse(t, func(response map[string]any) { response["head"] = nil })},
		{"non-object head", mutateProjectionResponse(t, func(response map[string]any) { response["head"] = "secret" })},
		{"missing head SHA", mutateProjectionResponse(t, func(response map[string]any) { response["head"] = map[string]any{} })},
		{"wrong-case head SHA alias", mutateProjectionResponse(t, func(response map[string]any) {
			response["head"] = map[string]any{"SHA": headSHA}
		})},
		{"control head SHA", mutateProjectionResponse(t, func(response map[string]any) { response["head"] = map[string]any{"sha": headSHA + "\n"} })},
		{"missing base", mutateProjectionResponse(t, func(response map[string]any) { delete(response, "base") })},
		{"missing base SHA", mutateProjectionResponse(t, func(response map[string]any) { response["base"] = map[string]any{} })},
		{"missing merge SHA", mutateProjectionResponse(t, func(response map[string]any) { delete(response, "merge_commit_sha") })},
		{"null merge SHA", mutateProjectionResponse(t, func(response map[string]any) { response["merge_commit_sha"] = nil })},
		{"short merge SHA", mutateProjectionResponse(t, func(response map[string]any) { response["merge_commit_sha"] = "abc" })},
		{"uppercase merge SHA", mutateProjectionResponse(t, func(response map[string]any) { response["merge_commit_sha"] = strings.Repeat("A", 40) })},
		{"non-hex merge SHA", mutateProjectionResponse(t, func(response map[string]any) { response["merge_commit_sha"] = strings.Repeat("z", 40) })},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			input := valid
			input.Response = slices.Clone(valid.Response)
			test.mutate(&input)
			if facts, err := ProjectMergedPullRequest(input); err == nil {
				t.Fatalf("ProjectMergedPullRequest() facts = %#v, want rejection", facts)
			}
		})
	}
}

func TestProjectMergedPullRequestErrorDoesNotEchoUntrustedValues(t *testing.T) {
	t.Parallel()
	input := validProjection(t)
	input.Response = mutateResponse(t, input.Response, func(response map[string]any) {
		response["merge_commit_sha"] = "do-not-echo-this-secret"
		response["body"] = "also-secret"
	})
	_, err := ProjectMergedPullRequest(input)
	if err == nil {
		t.Fatal("ProjectMergedPullRequest() accepted invalid merge SHA")
	}
	for _, secret := range []string{"do-not-echo-this-secret", "also-secret"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("error echoed untrusted value %q: %v", secret, err)
		}
	}
}

func validProjection(t *testing.T) Projection {
	t.Helper()
	return Projection{
		Workspace: "workspace-a", Host: "github.example.com", Owner: "ArdurAI", Repository: "sith", PullNumber: 42,
		ObservedAt: time.Date(2026, 7, 16, 17, 0, 0, 0, time.FixedZone("CDT", -5*60*60)),
		Response:   validMergedResponse(t),
	}
}

func validMergedResponse(t testing.TB) []byte {
	t.Helper()
	return encodeResponse(t, map[string]any{
		"number": 42, "state": "closed", "draft": false, "merged": true,
		"merged_at": "2026-07-16T15:30:00.123-05:00", "merge_commit_sha": mergeSHA,
		"head": map[string]any{
			"sha": headSHA, "label": "attacker-owner:head-secret",
			"repo": map[string]any{"name": "attacker-repository", "temp_clone_token": "token-secret"},
		},
		"base": map[string]any{
			"sha": baseSHA, "label": "attacker-owner:base-secret",
			"repo": map[string]any{"name": "attacker-repository", "url": "https://attacker.example/base"},
		},
		"url":   "https://attacker.example/repos/attacker-owner/attacker-repository/pulls/42",
		"title": "title-secret", "body": "body-secret\nwith-control", "user": map[string]any{"login": "user-secret"},
		"labels": []any{map[string]any{"name": "label-secret"}},
	})
}

func mutateProjectionResponse(t *testing.T, mutate func(map[string]any)) func(*Projection) {
	t.Helper()
	return func(input *Projection) {
		input.Response = mutateResponse(t, input.Response, mutate)
	}
}

func mutateResponse(t *testing.T, document []byte, mutate func(map[string]any)) []byte {
	t.Helper()
	var response map[string]any
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.UseNumber()
	if err := decoder.Decode(&response); err != nil {
		t.Fatalf("decode test response: %v", err)
	}
	mutate(response)
	return encodeResponse(t, response)
}

func encodeResponse(t testing.TB, response map[string]any) []byte {
	t.Helper()
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("encode test response: %v", err)
	}
	return encoded
}

func TestValidCommitSHARejectsAllOtherLengths(t *testing.T) {
	t.Parallel()
	for _, length := range []int{0, 1, 39, 41, 63, 65, 128} {
		if validCommitSHA(strings.Repeat("a", length)) {
			t.Fatalf("validCommitSHA() accepted %d characters", length)
		}
	}
	for _, length := range []int{40, 64} {
		if !validCommitSHA(strings.Repeat("0", length)) {
			t.Fatalf("validCommitSHA() rejected %d lowercase hexadecimal characters", length)
		}
	}
}

func FuzzProjectMergedPullRequestNeverEmitsInvalidOrUnboundedFacts(f *testing.F) {
	f.Add(validMergedResponse(f))
	f.Add([]byte(`{"number":42,"state":"open","draft":false,"merged":false,"merged_at":null}`))
	f.Add([]byte(`{"number":42,"number":43}`))
	f.Add([]byte(`null`))
	f.Fuzz(func(t *testing.T, response []byte) {
		input := Projection{
			Workspace: "workspace-a", Host: "github.com", Owner: "ArdurAI", Repository: "sith", PullNumber: 42,
			ObservedAt: time.Date(2026, 7, 16, 21, 0, 0, 0, time.UTC), Response: response,
		}
		facts, err := ProjectMergedPullRequest(input)
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

func ExampleProjectMergedPullRequest() {
	response := []byte(fmt.Sprintf(
		`{"number":42,"state":"closed","draft":false,"merged":true,"merged_at":"2026-07-16T20:30:00Z","merge_commit_sha":%q,"head":{"sha":%q},"base":{"sha":%q}}`,
		mergeSHA, headSHA, baseSHA,
	))
	facts, err := ProjectMergedPullRequest(Projection{
		Workspace: "workspace-a", Host: "github.com", Owner: "ArdurAI", Repository: "sith", PullNumber: 42,
		ObservedAt: time.Date(2026, 7, 16, 21, 0, 0, 0, time.UTC), Response: response,
	})
	fmt.Println(len(facts), err)
	// Output: 1 <nil>
}
