// SPDX-License-Identifier: Apache-2.0

package elasticsearch

import (
	"bytes"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ArdurAI/sith/internal/fleet"
)

var testObservedAt = time.Date(2026, 7, 17, 6, 0, 0, 0, time.UTC)

func TestProjectLogCausesAggregatesClosedTaxonomyWithoutRawLogs(t *testing.T) {
	t.Parallel()
	secret := "postgres://admin:do-not-retain@example.internal/db"
	input := testProjection(t,
		testHit("panic: worker crashed", testObservedAt.Add(-8*time.Minute)),
		testHit("panic: missing required environment variable DATABASE_URL="+secret, testObservedAt.Add(-7*time.Minute)),
		testHit("dial tcp 10.0.0.7:5432: connection refused", testObservedAt.Add(-6*time.Minute)),
		testHit("request completed successfully", testObservedAt.Add(-5*time.Minute)),
		testHit("connection refused while contacting dependency", testObservedAt.Add(-4*time.Minute)),
	)

	facts, err := ProjectLogCauses(input)
	if err != nil {
		t.Fatalf("ProjectLogCauses() error = %v", err)
	}
	if len(facts) != 3 {
		t.Fatalf("fact count = %d, want 3", len(facts))
	}

	wantCauses := []string{"dependency-failure", "missing-config", "panic"}
	wantCounts := []int{2, 1, 1}
	for index, fact := range facts {
		if fact.Lens != fleet.LensTelemetry || fact.Fact.Kind != fleet.FactDerived {
			t.Fatalf("fact %d taxonomy = %s/%s", index, fact.Lens, fact.Fact.Kind)
		}
		if fact.Fact.Ref.SourceKind != Kind || fact.Fact.Ref.Scope != input.Scope ||
			fact.Fact.Ref.Kind != "LogSignal" || fact.Fact.Ref.Namespace != input.Namespace {
			t.Fatalf("fact %d ref = %#v", index, fact.Fact.Ref)
		}
		if fact.Entity == nil || fact.Entity.Cluster != input.Scope || fact.Entity.Namespace != input.Namespace ||
			fact.Entity.Pod != input.Pod || fact.Entity.Kind != "" || fact.Entity.Name != "" {
			t.Fatalf("fact %d entity = %#v", index, fact.Entity)
		}
		if fact.Fact.Provenance.Adapter != Kind || fact.Fact.Provenance.ProtocolV != ProtocolVersion ||
			!strings.HasPrefix(fact.Fact.Provenance.NativeID, "sha256:") {
			t.Fatalf("fact %d provenance = %#v", index, fact.Fact.Provenance)
		}
		var observed causeObservation
		if err := json.Unmarshal(fact.Fact.Observed, &observed); err != nil {
			t.Fatalf("decode fact %d: %v", index, err)
		}
		if observed.Key != "logs.cause" || observed.Value != wantCauses[index] || observed.Count != wantCounts[index] ||
			observed.Container != input.Container {
			t.Fatalf("fact %d observed = %#v", index, observed)
		}
		encoded, err := json.Marshal(fact)
		if err != nil {
			t.Fatalf("marshal fact %d: %v", index, err)
		}
		for _, forbidden := range []string{secret, "worker crashed", "10.0.0.7", "example.internal", "DATABASE_URL"} {
			if bytes.Contains(encoded, []byte(forbidden)) {
				t.Fatalf("fact %d retained forbidden raw log fragment %q", index, forbidden)
			}
		}
	}
}

func TestProjectLogCausesIsDeterministicAcrossHitOrder(t *testing.T) {
	t.Parallel()
	hits := []map[string]any{
		testHit("panic: one", testObservedAt.Add(-8*time.Minute)),
		testHit("panic: two", testObservedAt.Add(-2*time.Minute)),
		testHit("configuration file not found", testObservedAt.Add(-6*time.Minute)),
	}
	forward := testProjection(t, hits...)
	reverseHits := slices.Clone(hits)
	slices.Reverse(reverseHits)
	reverse := testProjection(t, reverseHits...)

	left, err := ProjectLogCauses(forward)
	if err != nil {
		t.Fatalf("forward projection: %v", err)
	}
	right, err := ProjectLogCauses(reverse)
	if err != nil {
		t.Fatalf("reverse projection: %v", err)
	}
	if !reflect.DeepEqual(left, right) {
		t.Fatalf("hit order changed facts:\nforward = %#v\nreverse = %#v", left, right)
	}
}

func TestProjectLogCausesAbstainsForUnclassifiedAndEmptySuccess(t *testing.T) {
	t.Parallel()
	for _, hits := range [][]map[string]any{
		{},
		{testHit("service started", testObservedAt.Add(-time.Minute))},
	} {
		input := testProjection(t, hits...)
		facts, err := ProjectLogCauses(input)
		if err != nil {
			t.Fatalf("ProjectLogCauses() error = %v", err)
		}
		if len(facts) != 0 {
			t.Fatalf("facts = %#v, want abstention", facts)
		}
	}
}

func TestClassifyMessageIsConservativeAndSpecific(t *testing.T) {
	t.Parallel()
	tests := []struct {
		message string
		want    string
	}{
		{message: "panic: runtime failure", want: "panic"},
		{message: "INFO\nTraceback (most recent call last):\nValueError", want: "panic"},
		{message: "panic: required config not found", want: "missing-config"},
		{message: "environment variable API_KEY is not set", want: "missing-config"},
		{message: "configmap payments not found", want: "missing-config"},
		{message: "dial tcp: no such host", want: "dependency-failure"},
		{message: "upstream connect error", want: "dependency-failure"},
		{message: "documentation: avoid panic: by validating input", want: ""},
		{message: "optional config not loaded", want: ""},
		{message: "retrying dependency", want: ""},
	}
	for _, test := range tests {
		test := test
		t.Run(test.message, func(t *testing.T) {
			t.Parallel()
			if got := classifyMessage(test.message); got != test.want {
				t.Fatalf("classifyMessage() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestProjectLogCausesRejectsInvalidProjection(t *testing.T) {
	t.Parallel()
	valid := testProjection(t)
	tests := []struct {
		name   string
		mutate func(*Projection)
	}{
		{name: "workspace", mutate: func(input *Projection) { input.Workspace = "" }},
		{name: "scope", mutate: func(input *Projection) { input.Scope = "alpha/beta" }},
		{name: "namespace", mutate: func(input *Projection) { input.Namespace = "" }},
		{name: "pod", mutate: func(input *Projection) { input.Pod = " bad " }},
		{name: "container control", mutate: func(input *Projection) { input.Container = "api\nother" }},
		{name: "missing start", mutate: func(input *Projection) { input.WindowStart = time.Time{} }},
		{name: "reversed window", mutate: func(input *Projection) { input.WindowStart = input.WindowEnd }},
		{name: "wide window", mutate: func(input *Projection) { input.WindowStart = input.WindowEnd.Add(-maxQueryWindow - time.Nanosecond) }},
		{name: "future window", mutate: func(input *Projection) { input.WindowEnd = input.ObservedAt.Add(maxClockSkew + time.Nanosecond) }},
		{name: "missing observed", mutate: func(input *Projection) { input.ObservedAt = time.Time{} }},
		{name: "empty response", mutate: func(input *Projection) { input.Response = nil }},
		{name: "oversized response", mutate: func(input *Projection) { input.Response = bytes.Repeat([]byte{' '}, maxResponseBytes+1) }},
		{name: "invalid UTF-8", mutate: func(input *Projection) { input.Response = []byte{0xff} }},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			input := valid
			input.Response = slices.Clone(valid.Response)
			test.mutate(&input)
			if facts, err := ProjectLogCauses(input); err == nil || facts != nil {
				t.Fatalf("ProjectLogCauses() = %#v, %v; want nil error result", facts, err)
			}
		})
	}
}

func TestProjectLogCausesRejectsIncompleteSearchResponses(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "missing timed out", mutate: func(response map[string]any) { delete(response, "timed_out") }},
		{name: "timed out", mutate: func(response map[string]any) { response["timed_out"] = true }},
		{name: "terminated early", mutate: func(response map[string]any) { response["terminated_early"] = true }},
		{name: "missing shards", mutate: func(response map[string]any) { delete(response, "_shards") }},
		{name: "zero shards", mutate: func(response map[string]any) {
			response["_shards"].(map[string]any)["total"] = 0
			response["_shards"].(map[string]any)["successful"] = 0
		}},
		{name: "failed shard", mutate: func(response map[string]any) { response["_shards"].(map[string]any)["failed"] = 1 }},
		{name: "inconsistent shards", mutate: func(response map[string]any) { response["_shards"].(map[string]any)["total"] = 2 }},
		{name: "negative shard count", mutate: func(response map[string]any) { response["_shards"].(map[string]any)["skipped"] = -1 }},
		{name: "missing hits", mutate: func(response map[string]any) { delete(response, "hits") }},
		{name: "missing hit list", mutate: func(response map[string]any) { response["hits"] = map[string]any{} }},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			response := testResponseMap(nil)
			test.mutate(response)
			input := testProjectionWithResponse(t, response)
			if facts, err := ProjectLogCauses(input); err == nil || facts != nil {
				t.Fatalf("ProjectLogCauses() = %#v, %v; want rejection", facts, err)
			}
		})
	}
}

func TestProjectLogCausesRejectsMalformedResponseFieldTypes(t *testing.T) {
	t.Parallel()
	valid := testProjection(t)
	cases := [][]byte{
		[]byte(`[]`),
		[]byte(`{"timed_out":{},"_shards":{"total":1,"successful":1,"skipped":0,"failed":0},"hits":{"hits":[]}}`),
		[]byte(`{"timed_out":false,"terminated_early":{},"_shards":{"total":1,"successful":1,"skipped":0,"failed":0},"hits":{"hits":[]}}`),
		[]byte(`{"timed_out":false,"_shards":[],"hits":{"hits":[]}}`),
		[]byte(`{"timed_out":false,"_shards":{"total":"one","successful":1,"skipped":0,"failed":0},"hits":{"hits":[]}}`),
		[]byte(`{"timed_out":false,"_shards":{"total":1,"successful":1,"skipped":0,"failed":0},"hits":[]}`),
		[]byte(`{"timed_out":false,"_shards":{"total":1,"successful":1,"skipped":0,"failed":0},"hits":{"hits":{}}}`),
		[]byte(`{"timed_out":false,"_shards":{"total":1,"successful":1,"skipped":0,"failed":0},"hits":{"hits":[[]]}}`),
		[]byte(`{"timed_out":false,"_shards":{"total":1,"successful":1,"skipped":0,"failed":0},"hits":{"hits":[{"fields":[]}]}}`),
	}
	for index, document := range cases {
		input := valid
		input.Response = document
		facts, err := ProjectLogCauses(input)
		if err == nil || facts != nil {
			t.Fatalf("case %d = %#v, %v; want rejection", index, facts, err)
		}
		if len(err.Error()) > 512 {
			t.Fatalf("case %d produced attacker-sized error", index)
		}
	}
}

func TestProjectLogCausesRejectsUnsafeOrAmbiguousHits(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "source", mutate: func(hit map[string]any) { hit["_source"] = map[string]any{"secret": "raw"} }},
		{name: "ignored", mutate: func(hit map[string]any) { hit["_ignored"] = []string{"message"} }},
		{name: "ignored values", mutate: func(hit map[string]any) { hit["ignored_field_values"] = map[string]any{"message": []string{"raw"}} }},
		{name: "highlight", mutate: func(hit map[string]any) { hit["highlight"] = map[string]any{"message": []string{"raw"}} }},
		{name: "inner hits", mutate: func(hit map[string]any) { hit["inner_hits"] = map[string]any{} }},
		{name: "missing fields", mutate: func(hit map[string]any) { delete(hit, "fields") }},
		{name: "unknown returned field", mutate: func(hit map[string]any) {
			hit["fields"].(map[string]any)["user.email"] = []string{"private@example.com"}
		}},
		{name: "missing timestamp", mutate: func(hit map[string]any) { delete(hit["fields"].(map[string]any), timestampField) }},
		{name: "missing message", mutate: func(hit map[string]any) { delete(hit["fields"].(map[string]any), messageField) }},
		{name: "ambiguous message", mutate: func(hit map[string]any) {
			hit["fields"].(map[string]any)[messageField] = []string{"panic: one", "panic: two"}
		}},
		{name: "non-string timestamp", mutate: func(hit map[string]any) { hit["fields"].(map[string]any)[timestampField] = []int{42} }},
		{name: "bad timestamp", mutate: func(hit map[string]any) { hit["fields"].(map[string]any)[timestampField] = []string{"yesterday"} }},
		{name: "out of window", mutate: func(hit map[string]any) {
			hit["fields"].(map[string]any)[timestampField] = []string{testObservedAt.Add(time.Nanosecond).Format(time.RFC3339Nano)}
		}},
		{name: "missing cluster", mutate: func(hit map[string]any) { delete(hit["fields"].(map[string]any), clusterField) }},
		{name: "invalid cluster", mutate: func(hit map[string]any) { hit["fields"].(map[string]any)[clusterField] = []string{" alpha "} }},
		{name: "cluster mismatch", mutate: func(hit map[string]any) { hit["fields"].(map[string]any)[clusterField] = []string{"beta"} }},
		{name: "missing namespace", mutate: func(hit map[string]any) { delete(hit["fields"].(map[string]any), namespaceField) }},
		{name: "namespace mismatch", mutate: func(hit map[string]any) { hit["fields"].(map[string]any)[namespaceField] = []string{"other"} }},
		{name: "missing pod", mutate: func(hit map[string]any) { delete(hit["fields"].(map[string]any), podField) }},
		{name: "pod mismatch", mutate: func(hit map[string]any) { hit["fields"].(map[string]any)[podField] = []string{"other"} }},
		{name: "container missing", mutate: func(hit map[string]any) { delete(hit["fields"].(map[string]any), containerField) }},
		{name: "container mismatch", mutate: func(hit map[string]any) { hit["fields"].(map[string]any)[containerField] = []string{"sidecar"} }},
		{name: "oversized message", mutate: func(hit map[string]any) {
			hit["fields"].(map[string]any)[messageField] = []string{strings.Repeat("x", maxMessageBytes+1)}
		}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			hit := testHit("panic: boom", testObservedAt.Add(-time.Minute))
			test.mutate(hit)
			input := testProjection(t, hit)
			if facts, err := ProjectLogCauses(input); err == nil || facts != nil {
				t.Fatalf("ProjectLogCauses() = %#v, %v; want rejection", facts, err)
			}
		})
	}
}

func TestProjectLogCausesAcceptsInclusiveWindowAndOptionalContainer(t *testing.T) {
	t.Parallel()
	startHit := testHit("panic: start", testObservedAt.Add(-10*time.Minute))
	endHit := testHit("panic: end", testObservedAt)
	delete(startHit["fields"].(map[string]any), containerField)
	delete(endHit["fields"].(map[string]any), containerField)
	input := testProjection(t, startHit, endHit)
	input.Container = ""
	facts, err := ProjectLogCauses(input)
	if err != nil {
		t.Fatalf("ProjectLogCauses() error = %v", err)
	}
	if len(facts) != 1 {
		t.Fatalf("facts = %#v", facts)
	}
	var observed causeObservation
	if err := json.Unmarshal(facts[0].Fact.Observed, &observed); err != nil {
		t.Fatal(err)
	}
	if observed.Count != 2 || !observed.FirstEventAt.Equal(input.WindowStart) || !observed.LastEventAt.Equal(input.WindowEnd) || observed.Container != "" {
		t.Fatalf("observed = %#v", observed)
	}
}

func TestProjectLogCausesRejectsHitCountAndJSONAttacks(t *testing.T) {
	t.Parallel()
	hits := make([]map[string]any, maxHits+1)
	for index := range hits {
		hits[index] = testHit("service started", testObservedAt.Add(-time.Minute))
	}
	overCount := testProjection(t, hits...)
	if facts, err := ProjectLogCauses(overCount); err == nil || facts != nil {
		t.Fatalf("over-count projection = %#v, %v", facts, err)
	}

	valid := testProjection(t)
	attacks := [][]byte{
		[]byte(`{"timed_out":false,"timed_out":false}`),
		append(slices.Clone(valid.Response), []byte(` {}`)...),
		[]byte(`{"timed_out":false,"deep":` + strings.Repeat("[", maxJSONDepth+1) + `0` + strings.Repeat("]", maxJSONDepth+1) + `}`),
		[]byte(`{"timed_out":` + strings.Repeat("9", 64<<10) + `}`),
	}
	for index, attack := range attacks {
		input := valid
		input.Response = attack
		facts, err := ProjectLogCauses(input)
		if err == nil || facts != nil {
			t.Fatalf("attack %d = %#v, %v; want rejection", index, facts, err)
		}
		if len(err.Error()) > 512 {
			t.Fatalf("attack %d produced attacker-sized error (%d bytes)", index, len(err.Error()))
		}
	}
}

func FuzzProjectLogCauses(f *testing.F) {
	valid := testProjection(f,
		testHit("panic: boom", testObservedAt.Add(-time.Minute)),
	)
	f.Add(valid.Response)
	f.Add([]byte(`{"timed_out":false}`))
	f.Add([]byte(`{"timed_out":false,"timed_out":true}`))
	f.Fuzz(func(t *testing.T, response []byte) {
		input := valid
		input.Response = response
		facts, err := ProjectLogCauses(input)
		if err != nil {
			if facts != nil {
				t.Fatalf("error returned non-nil facts: %#v", facts)
			}
			return
		}
		if len(facts) > maxCauseFacts {
			t.Fatalf("fact count = %d", len(facts))
		}
		for _, fact := range facts {
			if fact.Lens != fleet.LensTelemetry || fact.Fact.Kind != fleet.FactDerived {
				t.Fatalf("invalid fuzz fact taxonomy: %#v", fact)
			}
		}
	})
}

type testingHelper interface {
	Helper()
	Fatalf(string, ...any)
}

func testProjection(t testingHelper, hits ...map[string]any) Projection {
	t.Helper()
	return testProjectionWithResponse(t, testResponseMap(hits))
}

func testProjectionWithResponse(t testingHelper, response map[string]any) Projection {
	t.Helper()
	document, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("marshal test response: %v", err)
	}
	return Projection{
		Workspace:   "workspace-a",
		Scope:       "alpha",
		Namespace:   "payments",
		Pod:         "api-7d9f",
		Container:   "api",
		WindowStart: testObservedAt.Add(-10 * time.Minute),
		WindowEnd:   testObservedAt,
		ObservedAt:  testObservedAt,
		Response:    document,
	}
}

func testResponseMap(hits []map[string]any) map[string]any {
	items := make([]any, len(hits))
	for index := range hits {
		items[index] = hits[index]
	}
	return map[string]any{
		"took":      2,
		"timed_out": false,
		"_shards": map[string]any{
			"total": 1, "successful": 1, "skipped": 0, "failed": 0,
		},
		"hits": map[string]any{
			"total": map[string]any{"value": len(hits), "relation": "eq"},
			"hits":  items,
		},
	}
}

func testHit(message string, eventAt time.Time) map[string]any {
	return map[string]any{
		"_index": "logs-kubernetes.container_logs-default",
		"_id":    "discarded-document-id",
		"_score": nil,
		"fields": map[string]any{
			timestampField: []string{eventAt.Format(time.RFC3339Nano)},
			messageField:   []string{message},
			clusterField:   []string{"alpha"},
			namespaceField: []string{"payments"},
			podField:       []string{"api-7d9f"},
			containerField: []string{"api"},
		},
	}
}
