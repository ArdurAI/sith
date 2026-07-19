// SPDX-License-Identifier: Apache-2.0

package opencost

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

var (
	testWindowStart = time.Date(2026, 7, 17, 0, 0, 0, 0, time.UTC)
	testWindowEnd   = testWindowStart.Add(24 * time.Hour)
	testCollectedAt = testWindowEnd.Add(time.Minute)
)

func TestProjectNamespaceCostsNormalizesSortedPrivateFacts(t *testing.T) {
	t.Parallel()
	input := testProjection(t, "payments", "infra")

	facts, err := ProjectNamespaceCosts(input)
	if err != nil {
		t.Fatalf("ProjectNamespaceCosts() error = %v", err)
	}
	if len(facts) != 2 {
		t.Fatalf("fact count = %d, want 2", len(facts))
	}
	for index, namespace := range []string{"infra", "payments"} {
		fact := facts[index]
		if fact.Fact.Kind != fleet.FactCost || fact.Lens != fleet.LensTelemetry {
			t.Fatalf("fact %d taxonomy = %s/%s", index, fact.Fact.Kind, fact.Lens)
		}
		if fact.Fact.Workspace != input.Workspace || fact.Fact.Source != input.Scope ||
			!fact.Fact.ObservedAt.Equal(testWindowEnd) || fact.Fact.Stale || fact.Fact.StaleFor != "" {
			t.Fatalf("fact %d envelope = %#v", index, fact.Fact)
		}
		if !reflect.DeepEqual(fact.Fact.Ref, fleet.ResourceRef{
			SourceKind: Kind, Scope: input.Scope, Kind: "NamespaceCost",
			Namespace: namespace, Name: "allocation",
		}) {
			t.Fatalf("fact %d ref = %#v", index, fact.Fact.Ref)
		}
		if fact.Entity == nil || *fact.Entity != (fleet.EntityRef{Cluster: input.Scope, Namespace: namespace}) {
			t.Fatalf("fact %d entity = %#v", index, fact.Entity)
		}
		if fact.Fact.Provenance.Adapter != Kind || fact.Fact.Provenance.ProtocolV != ProtocolVersion ||
			!strings.HasPrefix(fact.Fact.Provenance.NativeID, "sha256:") ||
			fact.Fact.Provenance.DeepLink != "" || fact.Fact.Provenance.Collector != "" ||
			fact.Fact.Display != nil || fact.Fact.Ref.Attributes != nil {
			t.Fatalf("fact %d provenance or metadata = %#v", index, fact.Fact)
		}
		if err := fact.Validate(input.Workspace); err != nil {
			t.Fatalf("fact %d Validate() = %v", index, err)
		}

		var observed namespaceCostObservation
		if err := json.Unmarshal(fact.Fact.Observed, &observed); err != nil {
			t.Fatalf("decode fact %d: %v", index, err)
		}
		want := namespaceCostObservation{
			Namespace: namespace, WindowStart: testWindowStart, WindowEnd: testWindowEnd, Currency: currencyUSD,
			CPUCost: "1.25000", CPUCostAdjustment: "-0.05000",
			GPUCost: "2.00000", GPUCostAdjustment: "0.00000",
			RAMCost: "0.75000", RAMCostAdjustment: "0.01000",
			PVCost: "0.50000", PVCostAdjustment: "0.00000",
			NetworkCost: "0.25000", NetworkCostAdjustment: "-0.01000",
			LoadBalancerCost: "0.10000", LoadBalancerCostAdjustment: "0.00000",
			SharedCost: "0.20000", ExternalCost: "0.30000", TotalCost: "5.30000",
		}
		if !reflect.DeepEqual(observed, want) {
			t.Fatalf("fact %d observed = %#v, want %#v", index, observed, want)
		}
		encoded, err := json.Marshal(fact)
		if err != nil {
			t.Fatalf("marshal fact %d: %v", index, err)
		}
		for _, forbidden := range []string{
			"do-not-retain-provider-id", "do-not-retain-label", "do-not-retain-annotation",
			"do-not-retain-controller", "do-not-retain-endpoint", testCollectedAt.Format(time.RFC3339),
		} {
			if bytes.Contains(encoded, []byte(forbidden)) {
				t.Fatalf("fact %d retained forbidden source data %q", index, forbidden)
			}
		}
	}

	graph, err := fleet.NewGraph(input.Workspace, facts)
	if err != nil {
		t.Fatalf("NewGraph() error = %v", err)
	}
	if len(graph.Nodes) != 2 || len(graph.Unattached) != 0 {
		t.Fatalf("graph = %#v", graph)
	}
}

func TestProjectNamespaceCostsIsDeterministicAcrossSourceOrderAndUnknownMetadata(t *testing.T) {
	t.Parallel()
	left := testProjection(t, "zeta", "alpha")
	right := testProjection(t, "alpha", "zeta")
	left.Response = orderedResponse(t, left.Scope, []string{"zeta", "alpha"}, "first secret")
	right.Response = orderedResponse(t, right.Scope, []string{"alpha", "zeta"}, "second secret")

	leftFacts, err := ProjectNamespaceCosts(left)
	if err != nil {
		t.Fatalf("left projection: %v", err)
	}
	rightFacts, err := ProjectNamespaceCosts(right)
	if err != nil {
		t.Fatalf("right projection: %v", err)
	}
	if !reflect.DeepEqual(leftFacts, rightFacts) {
		t.Fatalf("source order or discarded metadata changed facts:\nleft = %#v\nright = %#v", leftFacts, rightFacts)
	}
}

func TestNamespaceCostIdentityBindsOnlyRetainedEvidence(t *testing.T) {
	t.Parallel()
	base := testProjection(t, "payments")
	baseFact := singleFact(t, base)

	collectionChanged := base
	collectionChanged.CollectedAt = collectionChanged.CollectedAt.Add(time.Hour)
	collectionFact := singleFact(t, collectionChanged)
	if collectionFact.Fact.Provenance.NativeID != baseFact.Fact.Provenance.NativeID {
		t.Fatal("collection time changed retained fact identity")
	}

	unknownChanged := base
	unknownChanged.Response = testResponse(t, base.Scope, []string{"payments"}, func(document map[string]any) {
		document["meta"] = map[string]any{"discarded": "different secret"}
	})
	unknownFact := singleFact(t, unknownChanged)
	if unknownFact.Fact.Provenance.NativeID != baseFact.Fact.Provenance.NativeID {
		t.Fatal("discarded source metadata changed retained fact identity")
	}

	workspaceChanged := base
	workspaceChanged.Workspace = "workspace-b"
	scopeChanged := projectionFor(t, "workspace-a", "cluster-b", testWindowStart, testWindowEnd, "payments")
	namespaceChanged := testProjection(t, "shipping")
	windowChanged := projectionFor(t, "workspace-a", "cluster-a", testWindowStart.Add(time.Hour), testWindowEnd.Add(time.Hour), "payments")
	costChanged := base
	costChanged.Response = testResponse(t, base.Scope, []string{"payments"}, func(document map[string]any) {
		allocation := testAllocationFromDocument(document, "payments")
		allocation["cpuCost"] = json.Number("1.35")
		allocation["totalCost"] = json.Number("5.40")
	})

	for name, input := range map[string]Projection{
		"workspace": workspaceChanged,
		"scope":     scopeChanged,
		"namespace": namespaceChanged,
		"window":    windowChanged,
		"cost":      costChanged,
	} {
		name, input := name, input
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fact := singleFact(t, input)
			if fact.Fact.Provenance.NativeID == baseFact.Fact.Provenance.NativeID {
				t.Fatalf("retained %s did not change native identity", name)
			}
		})
	}
}

func TestProjectNamespaceCostsAbstainsOnlyForCompleteEmptySet(t *testing.T) {
	t.Parallel()
	input := testProjection(t)
	facts, err := ProjectNamespaceCosts(input)
	if err != nil {
		t.Fatalf("ProjectNamespaceCosts() error = %v", err)
	}
	if facts == nil || len(facts) != 0 {
		t.Fatalf("facts = %#v, want non-nil empty abstention", facts)
	}
}

func TestProjectNamespaceCostsAcceptsDocumentedRoundingTolerance(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		total   string
		wantErr bool
	}{
		{name: "exact", total: "5.30000"},
		{name: "positive tolerance", total: "5.30010"},
		{name: "negative tolerance", total: "5.29990"},
		{name: "outside tolerance", total: "5.30011", wantErr: true},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			input := testProjection(t, "payments")
			input.Response = testResponse(t, input.Scope, []string{"payments"}, func(document map[string]any) {
				testAllocationFromDocument(document, "payments")["totalCost"] = json.Number(test.total)
			})
			facts, err := ProjectNamespaceCosts(input)
			if test.wantErr {
				if err == nil || facts != nil {
					t.Fatalf("ProjectNamespaceCosts() = %#v, %v; want atomic error", facts, err)
				}
				return
			}
			if err != nil || len(facts) != 1 {
				t.Fatalf("ProjectNamespaceCosts() = %#v, %v", facts, err)
			}
		})
	}
}

func TestProjectNamespaceCostsRejectsInvalidProjection(t *testing.T) {
	t.Parallel()
	valid := testProjection(t, "payments")
	tests := []struct {
		name   string
		mutate func(*Projection)
	}{
		{name: "workspace", mutate: func(input *Projection) { input.Workspace = "" }},
		{name: "workspace control", mutate: func(input *Projection) { input.Workspace = "workspace\nother" }},
		{name: "scope", mutate: func(input *Projection) { input.Scope = "cluster/other" }},
		{name: "currency", mutate: func(input *Projection) { input.CurrencyCode = "EUR" }},
		{name: "aggregate", mutate: func(input *Projection) { input.Query.Aggregate = "pod" }},
		{name: "filter", mutate: func(input *Projection) { input.Query.Filter = "namespace:payments" }},
		{name: "accumulate", mutate: func(input *Projection) { input.Query.Accumulate = true }},
		{name: "include idle", mutate: func(input *Projection) { input.Query.IncludeIdle = true }},
		{name: "share idle", mutate: func(input *Projection) { input.Query.ShareIdle = true }},
		{name: "idle by node", mutate: func(input *Projection) { input.Query.IdleByNode = true }},
		{name: "share load balancer", mutate: func(input *Projection) { input.Query.ShareLoadBalancer = true }},
		{name: "aggregated metadata", mutate: func(input *Projection) { input.Query.IncludeAggregatedMetadata = true }},
		{name: "asset costs", mutate: func(input *Projection) { input.Query.IncludeProportionalAssetCosts = true }},
		{name: "missing start", mutate: func(input *Projection) { input.Query.WindowStart = time.Time{} }},
		{name: "non UTC start", mutate: func(input *Projection) {
			input.Query.WindowStart = input.Query.WindowStart.In(time.FixedZone("offset", 3600))
		}},
		{name: "fractional end", mutate: func(input *Projection) { input.Query.WindowEnd = input.Query.WindowEnd.Add(time.Nanosecond) }},
		{name: "reversed window", mutate: func(input *Projection) { input.Query.WindowStart = input.Query.WindowEnd }},
		{name: "wide window", mutate: func(input *Projection) {
			input.Query.WindowStart = input.Query.WindowEnd.Add(-maxQueryWindow - time.Second)
			input.Query.Step = input.Query.WindowEnd.Sub(input.Query.WindowStart)
		}},
		{name: "step", mutate: func(input *Projection) { input.Query.Step-- }},
		{name: "future window", mutate: func(input *Projection) {
			input.CollectedAt = input.Query.WindowEnd.Add(-maxClockSkew - time.Second)
		}},
		{name: "missing collection", mutate: func(input *Projection) { input.CollectedAt = time.Time{} }},
		{name: "empty response", mutate: func(input *Projection) { input.Response = nil }},
		{name: "oversized response", mutate: func(input *Projection) {
			input.Response = bytes.Repeat([]byte{' '}, maxResponseBytes+1)
		}},
		{name: "invalid UTF-8", mutate: func(input *Projection) { input.Response = []byte{0xff} }},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			input := valid
			input.Response = slices.Clone(valid.Response)
			test.mutate(&input)
			if facts, err := ProjectNamespaceCosts(input); err == nil || facts != nil {
				t.Fatalf("ProjectNamespaceCosts() = %#v, %v; want atomic error", facts, err)
			}
		})
	}
}

func TestProjectNamespaceCostsRejectsInvalidResponsesAtomically(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "missing code", mutate: func(document map[string]any) { delete(document, "code") }},
		{name: "wrong code", mutate: func(document map[string]any) { document["code"] = 206 }},
		{name: "failed status", mutate: func(document map[string]any) { document["status"] = "failed" }},
		{name: "message", mutate: func(document map[string]any) { document["message"] = "do-not-echo" }},
		{name: "warning", mutate: func(document map[string]any) { document["warning"] = "do-not-echo" }},
		{name: "missing data", mutate: func(document map[string]any) { delete(document, "data") }},
		{name: "null data", mutate: func(document map[string]any) { document["data"] = nil }},
		{name: "multiple sets", mutate: func(document map[string]any) {
			document["data"] = append(document["data"].([]any), map[string]any{})
		}},
		{name: "null set", mutate: func(document map[string]any) { document["data"] = []any{nil} }},
		{name: "too many allocations", mutate: func(document map[string]any) {
			set := make(map[string]any, maxAllocations+1)
			for index := 0; index <= maxAllocations; index++ {
				namespace := fmt.Sprintf("ns-%04d", index)
				set[namespace] = testAllocation("cluster-a", namespace)
			}
			document["data"] = []any{set}
		}},
		{name: "null allocation", mutate: func(document map[string]any) {
			testAllocationSet(document)["payments"] = nil
		}},
		{name: "missing name", mutate: func(document map[string]any) {
			delete(testAllocationFromDocument(document, "payments"), "name")
		}},
		{name: "missing properties", mutate: func(document map[string]any) {
			delete(testAllocationFromDocument(document, "payments"), "properties")
		}},
		{name: "missing window", mutate: func(document map[string]any) {
			delete(testAllocationFromDocument(document, "payments"), "window")
		}},
		{name: "name mismatch", mutate: func(document map[string]any) {
			testAllocationFromDocument(document, "payments")["name"] = "shipping"
		}},
		{name: "namespace mismatch", mutate: func(document map[string]any) {
			testAllocationProperties(document, "payments")["namespace"] = "shipping"
		}},
		{name: "cluster mismatch", mutate: func(document map[string]any) {
			testAllocationProperties(document, "payments")["cluster"] = "cluster-b"
		}},
		{name: "invalid namespace", mutate: func(document map[string]any) {
			allocation := testAllocationFromDocument(document, "payments")
			delete(testAllocationSet(document), "payments")
			allocation["name"] = "Invalid_Namespace"
			allocation["properties"].(map[string]any)["namespace"] = "Invalid_Namespace"
			testAllocationSet(document)["Invalid_Namespace"] = allocation
		}},
		{name: "synthetic namespace", mutate: func(document map[string]any) {
			allocation := testAllocationFromDocument(document, "payments")
			delete(testAllocationSet(document), "payments")
			allocation["name"] = "__idle__"
			allocation["properties"].(map[string]any)["namespace"] = "__idle__"
			testAllocationSet(document)["__idle__"] = allocation
		}},
		{name: "window mismatch", mutate: func(document map[string]any) {
			testAllocationFromDocument(document, "payments")["window"].(map[string]any)["end"] = testWindowEnd.Add(-time.Second).Format(time.RFC3339)
		}},
		{name: "noncanonical window", mutate: func(document map[string]any) {
			testAllocationFromDocument(document, "payments")["start"] = testWindowStart.Format("2006-01-02T15:04:05+00:00")
		}},
		{name: "missing cost", mutate: func(document map[string]any) {
			delete(testAllocationFromDocument(document, "payments"), "cpuCost")
		}},
		{name: "null cost", mutate: func(document map[string]any) {
			testAllocationFromDocument(document, "payments")["cpuCost"] = nil
		}},
		{name: "string cost", mutate: func(document map[string]any) {
			testAllocationFromDocument(document, "payments")["cpuCost"] = "1.25"
		}},
		{name: "exponent cost", mutate: func(document map[string]any) {
			testAllocationFromDocument(document, "payments")["cpuCost"] = json.RawMessage("1e0")
		}},
		{name: "over-precision cost", mutate: func(document map[string]any) {
			testAllocationFromDocument(document, "payments")["cpuCost"] = json.RawMessage("1.000001")
		}},
		{name: "negative base cost", mutate: func(document map[string]any) {
			testAllocationFromDocument(document, "payments")["cpuCost"] = json.Number("-1")
		}},
		{name: "excessive cost", mutate: func(document map[string]any) {
			testAllocationFromDocument(document, "payments")["cpuCost"] = json.Number("1000000000000.00001")
		}},
		{name: "wrong total", mutate: func(document map[string]any) {
			testAllocationFromDocument(document, "payments")["totalCost"] = json.Number("6")
		}},
		{name: "negative total", mutate: func(document map[string]any) {
			testAllocationFromDocument(document, "payments")["totalCost"] = json.Number("-1")
		}},
		{name: "mixed-case code alias", mutate: func(document map[string]any) { document["Code"] = 200 }},
		{name: "mixed-case cost alias", mutate: func(document map[string]any) {
			testAllocationFromDocument(document, "payments")["CPUCost"] = 1.25
		}},
		{name: "deep unknown field", mutate: func(document map[string]any) {
			testAllocationFromDocument(document, "payments")["unknown"] = json.RawMessage(
				strings.Repeat("[", maxJSONDepth+1) + "0" + strings.Repeat("]", maxJSONDepth+1),
			)
		}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			input := testProjection(t, "payments")
			input.Response = testResponse(t, input.Scope, []string{"payments"}, test.mutate)
			facts, err := ProjectNamespaceCosts(input)
			if err == nil || facts != nil {
				t.Fatalf("ProjectNamespaceCosts() = %#v, %v; want atomic error", facts, err)
			}
			if strings.Contains(err.Error(), "do-not-echo") {
				t.Fatalf("error echoed untrusted source content: %v", err)
			}
		})
	}
}

func TestProjectNamespaceCostsRejectsMalformedDuplicateTrailingAndMixedCaseJSON(t *testing.T) {
	t.Parallel()
	valid := testProjection(t, "payments")
	tests := [][]byte{
		[]byte(`{"code":200,"code":200,"data":[{}]}`),
		append(slices.Clone(valid.Response), []byte(` {}`)...),
		[]byte(`{"code":200,"data":[{"payments":{"name":"payments"}}]`),
		[]byte(`{"Code":200,"data":[{}]}`),
	}
	for index, response := range tests {
		input := valid
		input.Response = response
		if facts, err := ProjectNamespaceCosts(input); err == nil || facts != nil {
			t.Fatalf("case %d = %#v, %v; want atomic error", index, facts, err)
		}
	}
}

func TestProjectNamespaceCostsNeverReturnsValidatedPrefix(t *testing.T) {
	t.Parallel()
	input := testProjection(t, "alpha", "zeta")
	input.Response = testResponse(t, input.Scope, []string{"alpha", "zeta"}, func(document map[string]any) {
		testAllocationFromDocument(document, "zeta")["totalCost"] = json.Number("999")
	})
	if facts, err := ProjectNamespaceCosts(input); err == nil || facts != nil {
		t.Fatalf("ProjectNamespaceCosts() = %#v, %v; want no partial facts", facts, err)
	}
}

func TestCostLiteralContract(t *testing.T) {
	t.Parallel()
	for value, valid := range map[string]bool{
		"0": true, "-0": true, "1": true, "1.2": true, "1.23456": true,
		"-1.23456": true, "1000000000000": true,
		"": false, "-": false, "+1": false, ".1": false, "1.": false,
		"01": false, "-01": false, "1.234567": false, "1e2": false,
		"NaN": false, "Inf": false, "1/2": false, " 1": false,
	} {
		value, valid := value, valid
		t.Run(value, func(t *testing.T) {
			t.Parallel()
			if got := validCostLiteral(value); got != valid {
				t.Fatalf("validCostLiteral(%q) = %t, want %t", value, got, valid)
			}
		})
	}
}

func FuzzProjectNamespaceCostsNeverPanicsOrReturnsPartial(f *testing.F) {
	seed := testResponse(f, "cluster-a", []string{"payments"}, nil)
	f.Add(seed)
	f.Add([]byte(`{"code":200,"data":[{}]}`))
	f.Add([]byte(`{"code":200,"code":500,"data":[{}]}`))
	f.Fuzz(func(t *testing.T, response []byte) {
		input := baseProjection(response)
		facts, err := ProjectNamespaceCosts(input)
		if err != nil {
			if facts != nil {
				t.Fatalf("error returned partial facts: %#v, %v", facts, err)
			}
			return
		}
		if len(facts) > maxAllocations {
			t.Fatalf("fact count = %d", len(facts))
		}
		seen := make(map[string]bool, len(facts))
		for index, fact := range facts {
			if err := fact.Validate(input.Workspace); err != nil {
				t.Fatalf("fact %d failed validation: %v", index, err)
			}
			if fact.Fact.Kind != fleet.FactCost || fact.Lens != fleet.LensTelemetry ||
				fact.Fact.Provenance.Adapter != Kind || fact.Fact.Provenance.ProtocolV != ProtocolVersion {
				t.Fatalf("fact %d escaped closed taxonomy: %#v", index, fact)
			}
			if seen[fact.Fact.Provenance.NativeID] {
				t.Fatalf("duplicate fact identity %q", fact.Fact.Provenance.NativeID)
			}
			seen[fact.Fact.Provenance.NativeID] = true
		}
	})
}

func singleFact(t testing.TB, input Projection) fleet.GraphFact {
	t.Helper()
	facts, err := ProjectNamespaceCosts(input)
	if err != nil {
		t.Fatalf("ProjectNamespaceCosts() error = %v", err)
	}
	if len(facts) != 1 {
		t.Fatalf("fact count = %d, want 1", len(facts))
	}
	return facts[0]
}

func testProjection(t testing.TB, namespaces ...string) Projection {
	t.Helper()
	return projectionFor(t, "workspace-a", "cluster-a", testWindowStart, testWindowEnd, namespaces...)
}

func projectionFor(t testing.TB, workspace, scope string, start, end time.Time, namespaces ...string) Projection {
	t.Helper()
	input := baseProjection(testResponse(t, scope, namespaces, nil))
	input.Workspace = workspace
	input.Scope = scope
	input.Query.WindowStart = start
	input.Query.WindowEnd = end
	input.Query.Step = end.Sub(start)
	input.CollectedAt = end.Add(time.Minute)
	input.Response = testResponseForWindow(t, scope, namespaces, start, end, nil)
	return input
}

func baseProjection(response []byte) Projection {
	return Projection{
		Workspace: "workspace-a", Scope: "cluster-a", CurrencyCode: currencyUSD,
		Query: AllocationQuery{
			WindowStart: testWindowStart, WindowEnd: testWindowEnd,
			Step: testWindowEnd.Sub(testWindowStart), Aggregate: aggregateNamespace,
		},
		CollectedAt: testCollectedAt,
		Response:    response,
	}
}

func testResponse(t testing.TB, scope string, namespaces []string, mutate func(map[string]any)) []byte {
	t.Helper()
	return testResponseForWindow(t, scope, namespaces, testWindowStart, testWindowEnd, mutate)
}

func testResponseForWindow(
	t testing.TB,
	scope string,
	namespaces []string,
	start, end time.Time,
	mutate func(map[string]any),
) []byte {
	t.Helper()
	set := make(map[string]any, len(namespaces))
	for _, namespace := range namespaces {
		set[namespace] = testAllocationForWindow(scope, namespace, start, end)
	}
	document := map[string]any{
		"code": 200, "status": "success", "data": []any{set},
		"meta": map[string]any{"endpoint": "do-not-retain-endpoint"},
	}
	if mutate != nil {
		mutate(document)
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("marshal test response: %v", err)
	}
	return encoded
}

func orderedResponse(t testing.TB, scope string, namespaces []string, discarded string) []byte {
	t.Helper()
	var entries []string
	for _, namespace := range namespaces {
		allocation, err := json.Marshal(testAllocation(scope, namespace))
		if err != nil {
			t.Fatalf("marshal allocation: %v", err)
		}
		key, err := json.Marshal(namespace)
		if err != nil {
			t.Fatalf("marshal namespace: %v", err)
		}
		entries = append(entries, string(key)+":"+string(allocation))
	}
	secret, err := json.Marshal(discarded)
	if err != nil {
		t.Fatalf("marshal discarded metadata: %v", err)
	}
	return []byte(`{"code":200,"data":[{` + strings.Join(entries, ",") + `}],"meta":{"discarded":` + string(secret) + `}}`)
}

func testAllocation(scope, namespace string) map[string]any {
	return testAllocationForWindow(scope, namespace, testWindowStart, testWindowEnd)
}

func testAllocationForWindow(scope, namespace string, start, end time.Time) map[string]any {
	return map[string]any{
		"name": namespace,
		"properties": map[string]any{
			"cluster": scope, "namespace": namespace,
			"providerID":  "do-not-retain-provider-id",
			"labels":      map[string]any{"secret": "do-not-retain-label"},
			"annotations": map[string]any{"secret": "do-not-retain-annotation"},
			"controller":  "do-not-retain-controller",
		},
		"window": map[string]any{"start": start.Format(time.RFC3339), "end": end.Format(time.RFC3339)},
		"start":  start.Format(time.RFC3339), "end": end.Format(time.RFC3339),
		"cpuCost": json.Number("1.25"), "cpuCostAdjustment": json.Number("-0.05"),
		"gpuCost": json.Number("2"), "gpuCostAdjustment": json.Number("0"),
		"ramCost": json.Number("0.75"), "ramCostAdjustment": json.Number("0.01"),
		"pvCost": json.Number("0.5"), "pvCostAdjustment": json.Number("0"),
		"networkCost": json.Number("0.25"), "networkCostAdjustment": json.Number("-0.01"),
		"loadBalancerCost": json.Number("0.1"), "loadBalancerCostAdjustment": json.Number("0"),
		"sharedCost": json.Number("0.2"), "externalCost": json.Number("0.3"),
		"totalCost":         json.Number("5.3"),
		"rawAllocationOnly": map[string]any{"secret": "do-not-retain-provider-id"},
	}
}

func testAllocationSet(document map[string]any) map[string]any {
	return document["data"].([]any)[0].(map[string]any)
}

func testAllocationFromDocument(document map[string]any, namespace string) map[string]any {
	return testAllocationSet(document)[namespace].(map[string]any)
}

func testAllocationProperties(document map[string]any, namespace string) map[string]any {
	return testAllocationFromDocument(document, namespace)["properties"].(map[string]any)
}
