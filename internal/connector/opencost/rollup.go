// SPDX-License-Identifier: Apache-2.0

package opencost

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"time"

	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/ArdurAI/sith/internal/fleet"
)

const (
	maxRollupScopes       = 256
	maxRollupFacts        = 4_096
	maxRollupInputBytes   = 8 << 20
	maxRollupPayloadBytes = 256 << 10
)

const maxRollupCostUnits = maxCostUnits * maxRollupFacts

// NamespaceCostSnapshot records one successful, already-authorized per-scope projection.
// Presence of a snapshot is the reporting signal, so an empty Facts slice is distinct from a
// scope that has no snapshot.
type NamespaceCostSnapshot struct {
	Workspace    string            `json:"workspace"`
	Scope        string            `json:"scope"`
	WindowStart  time.Time         `json:"window_start"`
	WindowEnd    time.Time         `json:"window_end"`
	CurrencyCode string            `json:"currency"`
	Facts        []fleet.GraphFact `json:"facts"`
}

// WorkspaceRollupRequest supplies the complete expected scope set and each successful scope
// snapshot for one exact allocation window.
type WorkspaceRollupRequest struct {
	Workspace      string                  `json:"workspace"`
	WindowStart    time.Time               `json:"window_start"`
	WindowEnd      time.Time               `json:"window_end"`
	CurrencyCode   string                  `json:"currency"`
	ExpectedScopes []string                `json:"expected_scopes"`
	Snapshots      []NamespaceCostSnapshot `json:"snapshots"`
}

// WorkspaceCostCoverage makes partial cost reporting impossible to confuse with complete
// coverage. ReportedScopes includes both populated and successful-empty snapshots.
type WorkspaceCostCoverage struct {
	ExpectedScopes []string `json:"expected_scopes"`
	ReportedScopes []string `json:"reported_scopes"`
	EmptyScopes    []string `json:"empty_scopes"`
	MissingScopes  []string `json:"missing_scopes"`
	Complete       bool     `json:"complete"`
}

// CostAmounts is the exact, canonical component breakdown retained by the workspace rollup.
type CostAmounts struct {
	CPUCost                    string `json:"cpu_cost"`
	CPUCostAdjustment          string `json:"cpu_cost_adjustment"`
	GPUCost                    string `json:"gpu_cost"`
	GPUCostAdjustment          string `json:"gpu_cost_adjustment"`
	RAMCost                    string `json:"ram_cost"`
	RAMCostAdjustment          string `json:"ram_cost_adjustment"`
	PVCost                     string `json:"pv_cost"`
	PVCostAdjustment           string `json:"pv_cost_adjustment"`
	NetworkCost                string `json:"network_cost"`
	NetworkCostAdjustment      string `json:"network_cost_adjustment"`
	LoadBalancerCost           string `json:"load_balancer_cost"`
	LoadBalancerCostAdjustment string `json:"load_balancer_cost_adjustment"`
	SharedCost                 string `json:"shared_cost"`
	ExternalCost               string `json:"external_cost"`
	TotalCost                  string `json:"total_cost"`
}

// WorkspaceCostRollup is one deterministic, read-only workspace total. ObservedAt is nil only
// when no expected scope reported a successful snapshot.
type WorkspaceCostRollup struct {
	Workspace      string                `json:"workspace"`
	WindowStart    time.Time             `json:"window_start"`
	WindowEnd      time.Time             `json:"window_end"`
	CurrencyCode   string                `json:"currency"`
	ObservedAt     *time.Time            `json:"observed_at,omitempty"`
	NamespaceFacts int                   `json:"namespace_facts"`
	Coverage       WorkspaceCostCoverage `json:"coverage"`
	Amounts        CostAmounts           `json:"amounts"`
}

// ProjectNamespaceCostSnapshot preserves the successful per-scope reporting envelope around the
// existing fact projector. It performs no additional I/O and returns a zero snapshot on error.
func ProjectNamespaceCostSnapshot(input Projection) (NamespaceCostSnapshot, error) {
	facts, err := ProjectNamespaceCosts(input)
	if err != nil {
		return NamespaceCostSnapshot{}, err
	}
	return NamespaceCostSnapshot{
		Workspace: input.Workspace, Scope: input.Scope,
		WindowStart: input.Query.WindowStart, WindowEnd: input.Query.WindowEnd,
		CurrencyCode: input.CurrencyCode, Facts: facts,
	}, nil
}

// RollupWorkspaceCosts validates and aggregates successful per-scope snapshots. Missing expected
// scopes remain explicit coverage gaps; they never contribute a synthetic zero-cost fact.
func RollupWorkspaceCosts(input WorkspaceRollupRequest) (WorkspaceCostRollup, error) {
	expected, err := validateWorkspaceRollupRequest(input)
	if err != nil {
		return WorkspaceCostRollup{}, err
	}

	byScope := make(map[string]NamespaceCostSnapshot, len(input.Snapshots))
	reported := make([]string, 0, len(input.Snapshots))
	empty := make([]string, 0, len(input.Snapshots))
	accumulator := newCostAccumulator()
	factCount := 0
	inputBytes := 0
	for index, snapshot := range input.Snapshots {
		if _, exists := byScope[snapshot.Scope]; exists {
			return WorkspaceCostRollup{}, fmt.Errorf("OpenCost workspace rollup snapshot %d duplicates scope", index)
		}
		if _, exists := expected[snapshot.Scope]; !exists {
			return WorkspaceCostRollup{}, fmt.Errorf("OpenCost workspace rollup snapshot %d has unexpected scope", index)
		}
		if err := validateNamespaceCostSnapshot(input, snapshot, accumulator, &factCount, &inputBytes); err != nil {
			return WorkspaceCostRollup{}, fmt.Errorf("OpenCost workspace rollup snapshot %d: %w", index, err)
		}
		byScope[snapshot.Scope] = snapshot
		reported = append(reported, snapshot.Scope)
		if len(snapshot.Facts) == 0 {
			empty = append(empty, snapshot.Scope)
		}
	}

	sort.Strings(reported)
	sort.Strings(empty)
	missing := make([]string, 0, len(expected)-len(reported))
	for scope := range expected {
		if _, exists := byScope[scope]; !exists {
			missing = append(missing, scope)
		}
	}
	sort.Strings(missing)

	amounts, err := accumulator.amounts()
	if err != nil {
		return WorkspaceCostRollup{}, fmt.Errorf("OpenCost workspace rollup amounts: %w", err)
	}
	rollup := WorkspaceCostRollup{
		Workspace: input.Workspace, WindowStart: input.WindowStart, WindowEnd: input.WindowEnd,
		CurrencyCode: input.CurrencyCode, NamespaceFacts: factCount,
		Coverage: WorkspaceCostCoverage{
			ExpectedScopes: sortedScopeKeys(expected), ReportedScopes: reported,
			EmptyScopes: empty, MissingScopes: missing, Complete: len(missing) == 0,
		},
		Amounts: amounts,
	}
	if len(reported) != 0 {
		observedAt := input.WindowEnd
		rollup.ObservedAt = &observedAt
	}
	encoded, err := json.Marshal(rollup)
	if err != nil {
		return WorkspaceCostRollup{}, fmt.Errorf("encode OpenCost workspace rollup: %w", err)
	}
	if len(encoded) > maxRollupPayloadBytes {
		return WorkspaceCostRollup{}, fmt.Errorf("OpenCost workspace rollup exceeds %d bytes", maxRollupPayloadBytes)
	}
	return rollup, nil
}

func validateWorkspaceRollupRequest(input WorkspaceRollupRequest) (map[string]struct{}, error) {
	if err := validateText("workspace", input.Workspace, maxIdentityText); err != nil {
		return nil, err
	}
	if input.CurrencyCode != currencyUSD {
		return nil, fmt.Errorf("OpenCost workspace rollup currency must be USD")
	}
	if err := validateCanonicalTime("window start", input.WindowStart); err != nil {
		return nil, err
	}
	if err := validateCanonicalTime("window end", input.WindowEnd); err != nil {
		return nil, err
	}
	window := input.WindowEnd.Sub(input.WindowStart)
	if window <= 0 || window > maxQueryWindow {
		return nil, fmt.Errorf("OpenCost workspace rollup window is invalid")
	}
	if len(input.ExpectedScopes) > maxRollupScopes {
		return nil, fmt.Errorf("OpenCost workspace rollup expected scope count exceeds %d", maxRollupScopes)
	}
	if len(input.Snapshots) > len(input.ExpectedScopes) || len(input.Snapshots) > maxRollupScopes {
		return nil, fmt.Errorf("OpenCost workspace rollup snapshot count is invalid")
	}
	expected := make(map[string]struct{}, len(input.ExpectedScopes))
	for index, scope := range input.ExpectedScopes {
		if err := validateText("scope", scope, maxIdentityText); err != nil || bytes.ContainsRune([]byte(scope), '/') {
			return nil, fmt.Errorf("OpenCost workspace rollup expected scope %d is invalid", index)
		}
		if _, exists := expected[scope]; exists {
			return nil, fmt.Errorf("OpenCost workspace rollup expected scope %d is duplicated", index)
		}
		expected[scope] = struct{}{}
	}
	return expected, nil
}

func validateNamespaceCostSnapshot(
	request WorkspaceRollupRequest,
	snapshot NamespaceCostSnapshot,
	accumulator *costAccumulator,
	factCount *int,
	inputBytes *int,
) error {
	if snapshot.Workspace != request.Workspace || snapshot.CurrencyCode != request.CurrencyCode ||
		!snapshot.WindowStart.Equal(request.WindowStart) || !snapshot.WindowEnd.Equal(request.WindowEnd) {
		return fmt.Errorf("snapshot envelope does not match requested workspace, window, and currency")
	}
	if err := validateCanonicalTime("snapshot window start", snapshot.WindowStart); err != nil {
		return err
	}
	if err := validateCanonicalTime("snapshot window end", snapshot.WindowEnd); err != nil {
		return err
	}
	if err := validateText("scope", snapshot.Scope, maxIdentityText); err != nil ||
		bytes.ContainsRune([]byte(snapshot.Scope), '/') {
		return fmt.Errorf("snapshot scope is invalid")
	}
	if len(snapshot.Facts) > maxAllocations {
		return fmt.Errorf("snapshot fact count exceeds %d", maxAllocations)
	}
	if *factCount+len(snapshot.Facts) > maxRollupFacts {
		return fmt.Errorf("workspace fact count exceeds %d", maxRollupFacts)
	}

	namespaces := make(map[string]struct{}, len(snapshot.Facts))
	for index, fact := range snapshot.Facts {
		observation, observedBytes, err := validateNamespaceCostFact(request, snapshot.Scope, fact)
		if err != nil {
			return fmt.Errorf("fact %d: %w", index, err)
		}
		if _, exists := namespaces[observation.Namespace]; exists {
			return fmt.Errorf("fact %d duplicates namespace", index)
		}
		namespaces[observation.Namespace] = struct{}{}
		*inputBytes += observedBytes
		if *inputBytes > maxRollupInputBytes {
			return fmt.Errorf("workspace fact payload exceeds %d bytes", maxRollupInputBytes)
		}
		if err := accumulator.add(observation); err != nil {
			return fmt.Errorf("fact %d amounts: %w", index, err)
		}
	}
	*factCount += len(snapshot.Facts)
	return nil
}

func validateNamespaceCostFact(
	request WorkspaceRollupRequest,
	scope string,
	fact fleet.GraphFact,
) (namespaceCostObservation, int, error) {
	if err := fact.Validate(request.Workspace); err != nil {
		return namespaceCostObservation{}, 0, fmt.Errorf("graph fact is invalid")
	}
	if fact.Fact.Kind != fleet.FactCost || fact.Lens != fleet.LensTelemetry ||
		fact.Fact.Workspace != request.Workspace || fact.Fact.Source != scope ||
		!fact.Fact.ObservedAt.Equal(request.WindowEnd) || fact.Fact.Stale || fact.Fact.StaleFor != "" {
		return namespaceCostObservation{}, 0, fmt.Errorf("fact envelope is invalid")
	}
	if fact.Fact.Ref.SourceKind != Kind || fact.Fact.Ref.Scope != scope ||
		fact.Fact.Ref.Kind != "NamespaceCost" || fact.Fact.Ref.Name != "allocation" ||
		fact.Fact.Ref.Namespace == "" || fact.Fact.Ref.Attributes != nil || fact.Fact.Display != nil {
		return namespaceCostObservation{}, 0, fmt.Errorf("fact resource identity or display metadata is invalid")
	}
	if fact.Fact.Provenance.Adapter != Kind || fact.Fact.Provenance.ProtocolV != ProtocolVersion ||
		fact.Fact.Provenance.DeepLink != "" || fact.Fact.Provenance.Collector != "" {
		return namespaceCostObservation{}, 0, fmt.Errorf("fact provenance is invalid")
	}
	if fact.Entity == nil || *fact.Entity != (fleet.EntityRef{
		Cluster: scope, Namespace: fact.Fact.Ref.Namespace,
	}) {
		return namespaceCostObservation{}, 0, fmt.Errorf("fact entity is invalid")
	}
	if len(fact.Fact.Observed) == 0 || len(fact.Fact.Observed) > maxFactPayloadBytes ||
		!json.Valid(fact.Fact.Observed) {
		return namespaceCostObservation{}, 0, fmt.Errorf("fact payload is invalid")
	}
	if err := rejectDuplicateJSON(fact.Fact.Observed); err != nil {
		return namespaceCostObservation{}, 0, fmt.Errorf("fact payload is not canonical")
	}
	var observation namespaceCostObservation
	if err := json.Unmarshal(fact.Fact.Observed, &observation); err != nil {
		return namespaceCostObservation{}, 0, fmt.Errorf("fact payload is invalid")
	}
	canonical, err := json.Marshal(observation)
	if err != nil || !bytes.Equal(canonical, fact.Fact.Observed) {
		return namespaceCostObservation{}, 0, fmt.Errorf("fact payload is not canonical")
	}
	if observation.Namespace != fact.Fact.Ref.Namespace || observation.Currency != request.CurrencyCode ||
		!observation.WindowStart.Equal(request.WindowStart) || !observation.WindowEnd.Equal(request.WindowEnd) {
		return namespaceCostObservation{}, 0, fmt.Errorf("fact payload identity, window, or currency is invalid")
	}
	if len(validation.IsDNS1123Label(observation.Namespace)) != 0 {
		return namespaceCostObservation{}, 0, fmt.Errorf("fact payload namespace is invalid")
	}
	if err := validateCanonicalTime("fact window start", observation.WindowStart); err != nil {
		return namespaceCostObservation{}, 0, err
	}
	if err := validateCanonicalTime("fact window end", observation.WindowEnd); err != nil {
		return namespaceCostObservation{}, 0, err
	}
	nativeID, err := namespaceCostNativeID(request.Workspace, scope, fact.Fact.Observed)
	if err != nil || fact.Fact.Provenance.NativeID != nativeID {
		return namespaceCostObservation{}, 0, fmt.Errorf("fact native identity is invalid")
	}
	return observation, len(fact.Fact.Observed), nil
}

type costAccumulator struct {
	values map[string]*big.Rat
}

func newCostAccumulator() *costAccumulator {
	values := make(map[string]*big.Rat, len(costFields))
	for _, field := range costFields {
		values[field.JSONName] = new(big.Rat)
	}
	return &costAccumulator{values: values}
}

func (accumulator *costAccumulator) add(observation namespaceCostObservation) error {
	if accumulator == nil || len(accumulator.values) != len(costFields) {
		return fmt.Errorf("cost accumulator is invalid")
	}
	total := new(big.Rat)
	for _, field := range costFields {
		value := observationCostValue(observation, field.JSONName)
		amount, err := parseCanonicalCost(value, field.AllowNegative)
		if err != nil {
			return fmt.Errorf("%s is invalid", field.JSONName)
		}
		if field.PartOfTotal {
			total.Add(total, amount)
		}
		accumulator.values[field.JSONName].Add(accumulator.values[field.JSONName], amount)
		if new(big.Rat).Abs(new(big.Rat).Set(accumulator.values[field.JSONName])).Cmp(
			new(big.Rat).SetInt64(maxRollupCostUnits),
		) > 0 {
			return fmt.Errorf("%s aggregate exceeds limit", field.JSONName)
		}
	}
	reportedTotal, err := parseCanonicalCost(observation.TotalCost, false)
	if err != nil {
		return fmt.Errorf("totalCost is invalid")
	}
	difference := new(big.Rat).Sub(reportedTotal, total)
	difference.Abs(difference)
	if difference.Cmp(new(big.Rat).SetFrac64(1, 10_000)) > 0 {
		return fmt.Errorf("totalCost does not match components")
	}
	return nil
}

func (accumulator *costAccumulator) amounts() (CostAmounts, error) {
	if accumulator == nil || len(accumulator.values) != len(costFields) {
		return CostAmounts{}, fmt.Errorf("cost accumulator is invalid")
	}
	value := func(name string) string {
		return accumulator.values[name].FloatString(costScale)
	}
	return CostAmounts{
		CPUCost: value("cpuCost"), CPUCostAdjustment: value("cpuCostAdjustment"),
		GPUCost: value("gpuCost"), GPUCostAdjustment: value("gpuCostAdjustment"),
		RAMCost: value("ramCost"), RAMCostAdjustment: value("ramCostAdjustment"),
		PVCost: value("pvCost"), PVCostAdjustment: value("pvCostAdjustment"),
		NetworkCost: value("networkCost"), NetworkCostAdjustment: value("networkCostAdjustment"),
		LoadBalancerCost:           value("loadBalancerCost"),
		LoadBalancerCostAdjustment: value("loadBalancerCostAdjustment"),
		SharedCost:                 value("sharedCost"), ExternalCost: value("externalCost"),
		TotalCost: value("totalCost"),
	}, nil
}

func parseCanonicalCost(value string, allowNegative bool) (*big.Rat, error) {
	if !validCostLiteral(value) || len(value) < costScale+2 ||
		value[len(value)-costScale-1] != '.' {
		return nil, fmt.Errorf("cost value is not canonical")
	}
	amount, ok := new(big.Rat).SetString(value)
	if !ok || (!allowNegative && amount.Sign() < 0) || amount.FloatString(costScale) != value {
		return nil, fmt.Errorf("cost value is not canonical")
	}
	if new(big.Rat).Abs(new(big.Rat).Set(amount)).Cmp(new(big.Rat).SetInt64(maxCostUnits)) > 0 {
		return nil, fmt.Errorf("cost value exceeds limit")
	}
	return amount, nil
}

func observationCostValue(observation namespaceCostObservation, name string) string {
	switch name {
	case "cpuCost":
		return observation.CPUCost
	case "cpuCostAdjustment":
		return observation.CPUCostAdjustment
	case "gpuCost":
		return observation.GPUCost
	case "gpuCostAdjustment":
		return observation.GPUCostAdjustment
	case "ramCost":
		return observation.RAMCost
	case "ramCostAdjustment":
		return observation.RAMCostAdjustment
	case "pvCost":
		return observation.PVCost
	case "pvCostAdjustment":
		return observation.PVCostAdjustment
	case "networkCost":
		return observation.NetworkCost
	case "networkCostAdjustment":
		return observation.NetworkCostAdjustment
	case "loadBalancerCost":
		return observation.LoadBalancerCost
	case "loadBalancerCostAdjustment":
		return observation.LoadBalancerCostAdjustment
	case "sharedCost":
		return observation.SharedCost
	case "externalCost":
		return observation.ExternalCost
	case "totalCost":
		return observation.TotalCost
	default:
		return ""
	}
}

func namespaceCostNativeID(workspace, scope string, observed json.RawMessage) (string, error) {
	identity, err := json.Marshal(struct {
		Workspace string          `json:"workspace"`
		Scope     string          `json:"scope"`
		Observed  json.RawMessage `json:"observed"`
	}{Workspace: workspace, Scope: scope, Observed: observed})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(identity)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func sortedScopeKeys(scopes map[string]struct{}) []string {
	result := make([]string, 0, len(scopes))
	for scope := range scopes {
		result = append(result, scope)
	}
	sort.Strings(result)
	return result
}
