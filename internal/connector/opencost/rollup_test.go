// SPDX-License-Identifier: Apache-2.0

package opencost

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

func TestProjectNamespaceCostSnapshotPreservesSuccessfulEmptyCoverage(t *testing.T) {
	t.Parallel()
	input := testProjection(t)

	snapshot, err := ProjectNamespaceCostSnapshot(input)
	if err != nil {
		t.Fatalf("ProjectNamespaceCostSnapshot() error = %v", err)
	}
	if snapshot.Workspace != input.Workspace || snapshot.Scope != input.Scope ||
		snapshot.CurrencyCode != input.CurrencyCode ||
		!snapshot.WindowStart.Equal(input.Query.WindowStart) ||
		!snapshot.WindowEnd.Equal(input.Query.WindowEnd) ||
		snapshot.Facts == nil || len(snapshot.Facts) != 0 {
		t.Fatalf("successful empty snapshot = %#v", snapshot)
	}

	facts, err := ProjectNamespaceCosts(input)
	if err != nil {
		t.Fatalf("ProjectNamespaceCosts() error = %v", err)
	}
	if !reflect.DeepEqual(snapshot.Facts, facts) {
		t.Fatalf("snapshot facts = %#v, compatibility facts = %#v", snapshot.Facts, facts)
	}

	invalid := input
	invalid.CurrencyCode = "EUR"
	failed, err := ProjectNamespaceCostSnapshot(invalid)
	if err == nil || !reflect.DeepEqual(failed, NamespaceCostSnapshot{}) {
		t.Fatalf("invalid projection = %#v, %v", failed, err)
	}
}

func TestRollupWorkspaceCostsSurfacesPartialAndEmptyCoverage(t *testing.T) {
	t.Parallel()
	populated := testCostSnapshot(t, "cluster-a", "payments", "infra")
	empty := testCostSnapshot(t, "cluster-b")
	request := WorkspaceRollupRequest{
		Workspace: "workspace-a", WindowStart: testWindowStart, WindowEnd: testWindowEnd,
		CurrencyCode:   currencyUSD,
		ExpectedScopes: []string{"cluster-c", "cluster-b", "cluster-a"},
		Snapshots:      []NamespaceCostSnapshot{empty, populated},
	}

	rollup, err := RollupWorkspaceCosts(request)
	if err != nil {
		t.Fatalf("RollupWorkspaceCosts() error = %v", err)
	}
	if rollup.Workspace != request.Workspace || !rollup.WindowStart.Equal(testWindowStart) ||
		!rollup.WindowEnd.Equal(testWindowEnd) || rollup.CurrencyCode != currencyUSD ||
		rollup.ObservedAt == nil || !rollup.ObservedAt.Equal(testWindowEnd) ||
		rollup.NamespaceFacts != 2 {
		t.Fatalf("rollup envelope = %#v", rollup)
	}
	wantCoverage := WorkspaceCostCoverage{
		ExpectedScopes: []string{"cluster-a", "cluster-b", "cluster-c"},
		ReportedScopes: []string{"cluster-a", "cluster-b"},
		EmptyScopes:    []string{"cluster-b"},
		MissingScopes:  []string{"cluster-c"},
		Complete:       false,
	}
	if !reflect.DeepEqual(rollup.Coverage, wantCoverage) {
		t.Fatalf("coverage = %#v, want %#v", rollup.Coverage, wantCoverage)
	}
	wantAmounts := CostAmounts{
		CPUCost: "2.50000", CPUCostAdjustment: "-0.10000",
		GPUCost: "4.00000", GPUCostAdjustment: "0.00000",
		RAMCost: "1.50000", RAMCostAdjustment: "0.02000",
		PVCost: "1.00000", PVCostAdjustment: "0.00000",
		NetworkCost: "0.50000", NetworkCostAdjustment: "-0.02000",
		LoadBalancerCost: "0.20000", LoadBalancerCostAdjustment: "0.00000",
		SharedCost: "0.40000", ExternalCost: "0.60000", TotalCost: "10.60000",
	}
	if rollup.Amounts != wantAmounts {
		t.Fatalf("amounts = %#v, want %#v", rollup.Amounts, wantAmounts)
	}
	encoded, err := json.Marshal(rollup)
	if err != nil {
		t.Fatalf("marshal rollup: %v", err)
	}
	if len(encoded) > maxRollupPayloadBytes {
		t.Fatalf("rollup bytes = %d", len(encoded))
	}
	for _, forbidden := range []string{
		"payments", "infra", "do-not-retain-provider-id", "do-not-retain-label",
		"do-not-retain-annotation", "do-not-retain-controller", "do-not-retain-endpoint",
	} {
		if bytes.Contains(encoded, []byte(forbidden)) {
			t.Fatalf("rollup retained forbidden detail %q: %s", forbidden, encoded)
		}
	}
}

func TestRollupWorkspaceCostsIsDeterministicAcrossSnapshotAndFactOrder(t *testing.T) {
	t.Parallel()
	alpha := testCostSnapshot(t, "cluster-a", "zeta", "alpha")
	beta := testCostSnapshot(t, "cluster-b", "payments")
	left := WorkspaceRollupRequest{
		Workspace: "workspace-a", WindowStart: testWindowStart, WindowEnd: testWindowEnd,
		CurrencyCode: currencyUSD, ExpectedScopes: []string{"cluster-b", "cluster-a"},
		Snapshots: []NamespaceCostSnapshot{alpha, beta},
	}
	right := cloneRollupRequest(t, left)
	slices.Reverse(right.ExpectedScopes)
	slices.Reverse(right.Snapshots)
	slices.Reverse(right.Snapshots[1].Facts)

	leftRollup, err := RollupWorkspaceCosts(left)
	if err != nil {
		t.Fatalf("left rollup: %v", err)
	}
	rightRollup, err := RollupWorkspaceCosts(right)
	if err != nil {
		t.Fatalf("right rollup: %v", err)
	}
	if !reflect.DeepEqual(leftRollup, rightRollup) {
		t.Fatalf("input order changed rollup:\nleft = %#v\nright = %#v", leftRollup, rightRollup)
	}
	if !leftRollup.Coverage.Complete || len(leftRollup.Coverage.MissingScopes) != 0 ||
		leftRollup.NamespaceFacts != 3 || leftRollup.Amounts.TotalCost != "15.90000" {
		t.Fatalf("complete rollup = %#v", leftRollup)
	}
}

func TestRollupWorkspaceCostsDistinguishesNoReportsFromSuccessfulEmpty(t *testing.T) {
	t.Parallel()
	missing, err := RollupWorkspaceCosts(WorkspaceRollupRequest{
		Workspace: "workspace-a", WindowStart: testWindowStart, WindowEnd: testWindowEnd,
		CurrencyCode: currencyUSD, ExpectedScopes: []string{"cluster-b", "cluster-a"},
	})
	if err != nil {
		t.Fatalf("missing rollup: %v", err)
	}
	if missing.ObservedAt != nil || missing.NamespaceFacts != 0 || missing.Coverage.Complete ||
		!reflect.DeepEqual(missing.Coverage.MissingScopes, []string{"cluster-a", "cluster-b"}) ||
		missing.Amounts != zeroCostAmounts() {
		t.Fatalf("all-missing rollup = %#v", missing)
	}

	empty, err := RollupWorkspaceCosts(WorkspaceRollupRequest{
		Workspace: "workspace-a", WindowStart: testWindowStart, WindowEnd: testWindowEnd,
		CurrencyCode: currencyUSD, ExpectedScopes: []string{"cluster-a"},
		Snapshots: []NamespaceCostSnapshot{testCostSnapshot(t, "cluster-a")},
	})
	if err != nil {
		t.Fatalf("empty rollup: %v", err)
	}
	if empty.ObservedAt == nil || !empty.ObservedAt.Equal(testWindowEnd) ||
		!empty.Coverage.Complete ||
		!reflect.DeepEqual(empty.Coverage.ReportedScopes, []string{"cluster-a"}) ||
		!reflect.DeepEqual(empty.Coverage.EmptyScopes, []string{"cluster-a"}) ||
		len(empty.Coverage.MissingScopes) != 0 || empty.Amounts != zeroCostAmounts() {
		t.Fatalf("successful-empty rollup = %#v", empty)
	}

	noScopes, err := RollupWorkspaceCosts(WorkspaceRollupRequest{
		Workspace: "workspace-a", WindowStart: testWindowStart, WindowEnd: testWindowEnd,
		CurrencyCode: currencyUSD,
	})
	if err != nil {
		t.Fatalf("zero-scope rollup: %v", err)
	}
	if noScopes.ObservedAt != nil || !noScopes.Coverage.Complete ||
		noScopes.Coverage.ExpectedScopes == nil || noScopes.Coverage.ReportedScopes == nil ||
		noScopes.Coverage.EmptyScopes == nil || noScopes.Coverage.MissingScopes == nil {
		t.Fatalf("zero-scope rollup = %#v", noScopes)
	}
}

func TestRollupWorkspaceCostsRejectsInvalidRequestsAtomically(t *testing.T) {
	t.Parallel()
	base := WorkspaceRollupRequest{
		Workspace: "workspace-a", WindowStart: testWindowStart, WindowEnd: testWindowEnd,
		CurrencyCode: currencyUSD, ExpectedScopes: []string{"cluster-a"},
		Snapshots: []NamespaceCostSnapshot{testCostSnapshot(t, "cluster-a", "payments")},
	}
	fixedOffset := time.FixedZone("offset", -5*60*60)
	tests := map[string]func(*WorkspaceRollupRequest){
		"workspace":    func(value *WorkspaceRollupRequest) { value.Workspace = " workspace-a" },
		"currency":     func(value *WorkspaceRollupRequest) { value.CurrencyCode = "EUR" },
		"window start": func(value *WorkspaceRollupRequest) { value.WindowStart = time.Time{} },
		"window end":   func(value *WorkspaceRollupRequest) { value.WindowEnd = value.WindowStart },
		"window duration": func(value *WorkspaceRollupRequest) {
			value.WindowEnd = value.WindowStart.Add(maxQueryWindow + time.Second)
		},
		"expected duplicate": func(value *WorkspaceRollupRequest) {
			value.ExpectedScopes = append(value.ExpectedScopes, value.ExpectedScopes[0])
		},
		"expected invalid": func(value *WorkspaceRollupRequest) { value.ExpectedScopes[0] = "cluster/a" },
		"expected excessive": func(value *WorkspaceRollupRequest) {
			value.ExpectedScopes = make([]string, maxRollupScopes+1)
			for index := range value.ExpectedScopes {
				value.ExpectedScopes[index] = "cluster-" + strings.Repeat("a", index/26) + string(rune('a'+index%26))
			}
			value.Snapshots = nil
		},
		"snapshot excessive": func(value *WorkspaceRollupRequest) {
			value.Snapshots = append(value.Snapshots, cloneCostSnapshot(t, value.Snapshots[0]))
		},
		"snapshot unexpected": func(value *WorkspaceRollupRequest) { value.Snapshots[0].Scope = "cluster-b" },
		"snapshot workspace":  func(value *WorkspaceRollupRequest) { value.Snapshots[0].Workspace = "workspace-b" },
		"snapshot currency":   func(value *WorkspaceRollupRequest) { value.Snapshots[0].CurrencyCode = "EUR" },
		"snapshot window":     func(value *WorkspaceRollupRequest) { value.Snapshots[0].WindowEnd = value.WindowEnd.Add(time.Second) },
		"snapshot non-UTC": func(value *WorkspaceRollupRequest) {
			value.Snapshots[0].WindowStart = value.WindowStart.In(fixedOffset)
		},
		"snapshot fact count": func(value *WorkspaceRollupRequest) {
			value.Snapshots[0].Facts = make([]fleet.GraphFact, maxAllocations+1)
		},
	}
	for name, mutate := range tests {
		name, mutate := name, mutate
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			candidate := cloneRollupRequest(t, base)
			mutate(&candidate)
			assertRollupErrorIsAtomic(t, candidate)
		})
	}
}

func TestRollupWorkspaceCostsRevalidatesFactsAtomically(t *testing.T) {
	t.Parallel()
	base := WorkspaceRollupRequest{
		Workspace: "workspace-a", WindowStart: testWindowStart, WindowEnd: testWindowEnd,
		CurrencyCode: currencyUSD, ExpectedScopes: []string{"cluster-a"},
		Snapshots: []NamespaceCostSnapshot{testCostSnapshot(t, "cluster-a", "payments")},
	}
	tests := map[string]func(*fleet.GraphFact){
		"workspace": func(fact *fleet.GraphFact) { fact.Fact.Workspace = "workspace-b" },
		"kind":      func(fact *fleet.GraphFact) { fact.Fact.Kind = fleet.FactAlert },
		"lens":      func(fact *fleet.GraphFact) { fact.Lens = fleet.LensLive },
		"source":    func(fact *fleet.GraphFact) { fact.Fact.Source = "cluster-b" },
		"observed at": func(fact *fleet.GraphFact) {
			fact.Fact.ObservedAt = fact.Fact.ObservedAt.Add(time.Second)
		},
		"stale":        func(fact *fleet.GraphFact) { fact.Fact.Stale = true },
		"stale detail": func(fact *fleet.GraphFact) { fact.Fact.StaleFor = "1m" },
		"source kind":  func(fact *fleet.GraphFact) { fact.Fact.Ref.SourceKind = "other" },
		"scope":        func(fact *fleet.GraphFact) { fact.Fact.Ref.Scope = "cluster-b" },
		"resource kind": func(fact *fleet.GraphFact) {
			fact.Fact.Ref.Kind = "Namespace"
		},
		"resource name": func(fact *fleet.GraphFact) { fact.Fact.Ref.Name = "other" },
		"attributes":    func(fact *fleet.GraphFact) { fact.Fact.Ref.Attributes = map[string]string{} },
		"display":       func(fact *fleet.GraphFact) { fact.Fact.Display = []fleet.DisplayField{} },
		"adapter":       func(fact *fleet.GraphFact) { fact.Fact.Provenance.Adapter = "other" },
		"protocol":      func(fact *fleet.GraphFact) { fact.Fact.Provenance.ProtocolV = "other" },
		"native identity": func(fact *fleet.GraphFact) {
			fact.Fact.Provenance.NativeID = "sha256:" + strings.Repeat("0", 64)
		},
		"deep link":  func(fact *fleet.GraphFact) { fact.Fact.Provenance.DeepLink = "https://secret.invalid" },
		"collector":  func(fact *fleet.GraphFact) { fact.Fact.Provenance.Collector = "secret" },
		"entity nil": func(fact *fleet.GraphFact) { fact.Entity = nil },
		"entity mismatch": func(fact *fleet.GraphFact) {
			fact.Entity.Namespace = "other"
		},
		"payload whitespace": func(fact *fleet.GraphFact) {
			fact.Fact.Observed = append([]byte(" "), fact.Fact.Observed...)
			rebindFactNativeID(t, fact)
		},
		"payload unknown": func(fact *fleet.GraphFact) {
			var document map[string]any
			mustUnmarshal(t, fact.Fact.Observed, &document)
			document["provider_id"] = "secret"
			fact.Fact.Observed = mustMarshal(t, document)
			rebindFactNativeID(t, fact)
		},
		"payload duplicate": func(fact *fleet.GraphFact) {
			fact.Fact.Observed = bytes.Replace(
				fact.Fact.Observed,
				[]byte(`{"namespace":"payments",`),
				[]byte(`{"namespace":"payments","namespace":"payments",`),
				1,
			)
			rebindFactNativeID(t, fact)
		},
		"payload oversized": func(fact *fleet.GraphFact) {
			fact.Fact.Observed = append(fact.Fact.Observed, bytes.Repeat([]byte(" "), maxFactPayloadBytes)...)
			rebindFactNativeID(t, fact)
		},
		"payload namespace": func(fact *fleet.GraphFact) {
			mutateFactObservation(t, fact, func(value *namespaceCostObservation) { value.Namespace = "Invalid" })
			fact.Fact.Ref.Namespace = "Invalid"
			fact.Entity.Namespace = "Invalid"
			rebindFactNativeID(t, fact)
		},
		"payload window": func(fact *fleet.GraphFact) {
			mutateFactObservation(t, fact, func(value *namespaceCostObservation) {
				value.WindowEnd = value.WindowEnd.Add(time.Second)
			})
			rebindFactNativeID(t, fact)
		},
		"payload currency": func(fact *fleet.GraphFact) {
			mutateFactObservation(t, fact, func(value *namespaceCostObservation) { value.Currency = "EUR" })
			rebindFactNativeID(t, fact)
		},
		"payload noncanonical amount": func(fact *fleet.GraphFact) {
			mutateFactObservation(t, fact, func(value *namespaceCostObservation) { value.CPUCost = "1.25" })
			rebindFactNativeID(t, fact)
		},
		"payload component total": func(fact *fleet.GraphFact) {
			mutateFactObservation(t, fact, func(value *namespaceCostObservation) { value.TotalCost = "5.40000" })
			rebindFactNativeID(t, fact)
		},
	}
	for name, mutate := range tests {
		name, mutate := name, mutate
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			candidate := cloneRollupRequest(t, base)
			mutate(&candidate.Snapshots[0].Facts[0])
			assertRollupErrorIsAtomic(t, candidate)
		})
	}

	duplicate := cloneRollupRequest(t, base)
	duplicate.Snapshots[0].Facts = append(
		duplicate.Snapshots[0].Facts,
		duplicate.Snapshots[0].Facts[0],
	)
	assertRollupErrorIsAtomic(t, duplicate)
}

func TestCanonicalRollupCostContract(t *testing.T) {
	t.Parallel()
	for value, valid := range map[string]bool{
		"0.00000": true, "1.25000": true, "-0.05000": true,
		"0": false, "1.25": false, "01.00000": false, "-0.00000": false,
		"1.250000": false, "1e2": false, " 1.00000": false,
	} {
		value, valid := value, valid
		t.Run(value, func(t *testing.T) {
			t.Parallel()
			_, err := parseCanonicalCost(value, true)
			if (err == nil) != valid {
				t.Fatalf("parseCanonicalCost(%q) error = %v, valid = %t", value, err, valid)
			}
		})
	}
}

func FuzzRollupWorkspaceCostsNeverPanicsOrReturnsPartial(f *testing.F) {
	seed := testCostSnapshot(f, "cluster-a", "payments")
	f.Add([]byte(seed.Facts[0].Fact.Observed), uint8(0), "cluster-a")
	f.Add([]byte(`{"namespace":"payments"}`), uint8(1), "cluster-b")
	f.Add([]byte(`{"namespace":"payments","namespace":"payments"}`), uint8(2), "cluster/a")
	f.Fuzz(func(t *testing.T, payload []byte, mode uint8, scope string) {
		request := WorkspaceRollupRequest{
			Workspace: "workspace-a", WindowStart: testWindowStart, WindowEnd: testWindowEnd,
			CurrencyCode: currencyUSD, ExpectedScopes: []string{"cluster-a"},
			Snapshots: []NamespaceCostSnapshot{testCostSnapshot(t, "cluster-a", "payments")},
		}
		switch mode % 4 {
		case 0:
			request.Snapshots[0].Facts[0].Fact.Observed = append([]byte(nil), payload...)
			nativeID, err := namespaceCostNativeID(
				request.Workspace,
				request.Snapshots[0].Scope,
				request.Snapshots[0].Facts[0].Fact.Observed,
			)
			if err == nil {
				request.Snapshots[0].Facts[0].Fact.Provenance.NativeID = nativeID
			}
		case 1:
			request.ExpectedScopes = []string{scope}
			request.Snapshots[0].Scope = scope
		case 2:
			request.Snapshots[0].Facts = append(
				request.Snapshots[0].Facts,
				request.Snapshots[0].Facts[0],
			)
		case 3:
			request.ExpectedScopes = append(request.ExpectedScopes, scope)
		}
		rollup, err := RollupWorkspaceCosts(request)
		if err != nil {
			if !reflect.DeepEqual(rollup, WorkspaceCostRollup{}) {
				t.Fatalf("error returned partial rollup: %#v, %v", rollup, err)
			}
			return
		}
		if rollup.NamespaceFacts > maxRollupFacts ||
			rollup.Coverage.Complete != (len(rollup.Coverage.MissingScopes) == 0) ||
			!slices.IsSorted(rollup.Coverage.ExpectedScopes) ||
			!slices.IsSorted(rollup.Coverage.ReportedScopes) ||
			!slices.IsSorted(rollup.Coverage.EmptyScopes) ||
			!slices.IsSorted(rollup.Coverage.MissingScopes) {
			t.Fatalf("invalid successful rollup: %#v", rollup)
		}
		for _, value := range costAmountValues(rollup.Amounts) {
			if _, err := parseCanonicalCost(value, true); err != nil {
				t.Fatalf("noncanonical successful amount %q: %v", value, err)
			}
		}
		encoded, err := json.Marshal(rollup)
		if err != nil || len(encoded) > maxRollupPayloadBytes {
			t.Fatalf("successful rollup encoding = %d bytes, %v", len(encoded), err)
		}
	})
}

func testCostSnapshot(t testing.TB, scope string, namespaces ...string) NamespaceCostSnapshot {
	t.Helper()
	input := projectionFor(t, "workspace-a", scope, testWindowStart, testWindowEnd, namespaces...)
	snapshot, err := ProjectNamespaceCostSnapshot(input)
	if err != nil {
		t.Fatalf("ProjectNamespaceCostSnapshot() error = %v", err)
	}
	return snapshot
}

func cloneCostSnapshot(t testing.TB, value NamespaceCostSnapshot) NamespaceCostSnapshot {
	t.Helper()
	encoded := mustMarshal(t, value)
	var cloned NamespaceCostSnapshot
	mustUnmarshal(t, encoded, &cloned)
	return cloned
}

func cloneRollupRequest(t testing.TB, value WorkspaceRollupRequest) WorkspaceRollupRequest {
	t.Helper()
	encoded := mustMarshal(t, value)
	var cloned WorkspaceRollupRequest
	mustUnmarshal(t, encoded, &cloned)
	return cloned
}

func mutateFactObservation(
	t testing.TB,
	fact *fleet.GraphFact,
	mutate func(*namespaceCostObservation),
) {
	t.Helper()
	var observation namespaceCostObservation
	mustUnmarshal(t, fact.Fact.Observed, &observation)
	mutate(&observation)
	fact.Fact.Observed = mustMarshal(t, observation)
}

func rebindFactNativeID(t testing.TB, fact *fleet.GraphFact) {
	t.Helper()
	nativeID, err := namespaceCostNativeID(fact.Fact.Workspace, fact.Fact.Ref.Scope, fact.Fact.Observed)
	if err != nil {
		t.Fatalf("namespaceCostNativeID() error = %v", err)
	}
	fact.Fact.Provenance.NativeID = nativeID
}

func assertRollupErrorIsAtomic(t testing.TB, request WorkspaceRollupRequest) {
	t.Helper()
	rollup, err := RollupWorkspaceCosts(request)
	if err == nil || !reflect.DeepEqual(rollup, WorkspaceCostRollup{}) {
		t.Fatalf("RollupWorkspaceCosts() = %#v, %v; want zero rollup and error", rollup, err)
	}
}

func zeroCostAmounts() CostAmounts {
	return CostAmounts{
		CPUCost: "0.00000", CPUCostAdjustment: "0.00000",
		GPUCost: "0.00000", GPUCostAdjustment: "0.00000",
		RAMCost: "0.00000", RAMCostAdjustment: "0.00000",
		PVCost: "0.00000", PVCostAdjustment: "0.00000",
		NetworkCost: "0.00000", NetworkCostAdjustment: "0.00000",
		LoadBalancerCost: "0.00000", LoadBalancerCostAdjustment: "0.00000",
		SharedCost: "0.00000", ExternalCost: "0.00000", TotalCost: "0.00000",
	}
}

func costAmountValues(amounts CostAmounts) []string {
	return []string{
		amounts.CPUCost, amounts.CPUCostAdjustment,
		amounts.GPUCost, amounts.GPUCostAdjustment,
		amounts.RAMCost, amounts.RAMCostAdjustment,
		amounts.PVCost, amounts.PVCostAdjustment,
		amounts.NetworkCost, amounts.NetworkCostAdjustment,
		amounts.LoadBalancerCost, amounts.LoadBalancerCostAdjustment,
		amounts.SharedCost, amounts.ExternalCost, amounts.TotalCost,
	}
}

func mustMarshal(t testing.TB, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal test value: %v", err)
	}
	return encoded
}

func mustUnmarshal(t testing.TB, document []byte, target any) {
	t.Helper()
	if err := json.Unmarshal(document, target); err != nil {
		t.Fatalf("unmarshal test value: %v", err)
	}
}
