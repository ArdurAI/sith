// SPDX-License-Identifier: Apache-2.0

// Package opencost normalizes bounded OpenCost allocation evidence for Sith's
// operational graph.
package opencost

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/ArdurAI/sith/internal/fleet"
)

const (
	// Kind is the stable registry identifier for OpenCost read evidence.
	Kind = "opencost"
	// ProtocolVersion identifies the normalized namespace-allocation fact contract.
	ProtocolVersion = "allocation/namespace-usd-v1"

	maxResponseBytes    = 2 << 20
	maxAllocations      = 1_024
	maxFactPayloadBytes = 4 << 10
	maxJSONDepth        = 64
	maxIdentityText     = 253
	maxQueryWindow      = 31 * 24 * time.Hour
	maxClockSkew        = 5 * time.Minute
	maxCostUnits        = int64(1_000_000_000_000)
	maxCostLiteralBytes = 32
	costScale           = 5

	currencyUSD        = "USD"
	aggregateNamespace = "namespace"
)

// AllocationQuery records the exact already-authorized OpenCost query contract. The caller must
// use one explicit UTC window, aggregate by namespace, and request one set for the whole window.
type AllocationQuery struct {
	WindowStart                   time.Time
	WindowEnd                     time.Time
	Step                          time.Duration
	Aggregate                     string
	Filter                        string
	Accumulate                    bool
	IncludeIdle                   bool
	ShareIdle                     bool
	IdleByNode                    bool
	ShareLoadBalancer             bool
	IncludeAggregatedMetadata     bool
	IncludeProportionalAssetCosts bool
}

// Projection supplies one already-authorized OpenCost allocation response. The trusted caller
// owns endpoint selection, authorization, request execution, and the source-currency assertion.
// ProjectNamespaceCosts performs no discovery, network access, credential loading, persistence,
// process execution, billing, optimization, or mutation.
type Projection struct {
	Workspace    string
	Scope        string
	CurrencyCode string
	Query        AllocationQuery
	CollectedAt  time.Time
	Response     []byte
}

type allocationResponse struct {
	Code    *int
	Status  *string
	Data    *[]map[string]json.RawMessage
	Message *string
	Warning *string
}

type allocationRecord struct {
	Name       *string
	Properties *allocationProperties
	Window     *allocationWindow
	Start      *string
	End        *string
	Amounts    map[string]json.RawMessage
}

type allocationProperties struct {
	Cluster   *string
	Namespace *string
}

type allocationWindow struct {
	Start *string
	End   *string
}

type costField struct {
	JSONName      string
	AllowNegative bool
	PartOfTotal   bool
}

var costFields = [...]costField{
	{JSONName: "cpuCost", PartOfTotal: true},
	{JSONName: "cpuCostAdjustment", AllowNegative: true, PartOfTotal: true},
	{JSONName: "gpuCost", PartOfTotal: true},
	{JSONName: "gpuCostAdjustment", AllowNegative: true, PartOfTotal: true},
	{JSONName: "ramCost", PartOfTotal: true},
	{JSONName: "ramCostAdjustment", AllowNegative: true, PartOfTotal: true},
	{JSONName: "pvCost", PartOfTotal: true},
	{JSONName: "pvCostAdjustment", AllowNegative: true, PartOfTotal: true},
	{JSONName: "networkCost", PartOfTotal: true},
	{JSONName: "networkCostAdjustment", AllowNegative: true, PartOfTotal: true},
	{JSONName: "loadBalancerCost", PartOfTotal: true},
	{JSONName: "loadBalancerCostAdjustment", AllowNegative: true, PartOfTotal: true},
	{JSONName: "sharedCost", PartOfTotal: true},
	{JSONName: "externalCost", PartOfTotal: true},
	{JSONName: "totalCost"},
}

type namespaceCostObservation struct {
	Namespace                  string    `json:"namespace"`
	WindowStart                time.Time `json:"window_start"`
	WindowEnd                  time.Time `json:"window_end"`
	Currency                   string    `json:"currency"`
	CPUCost                    string    `json:"cpu_cost"`
	CPUCostAdjustment          string    `json:"cpu_cost_adjustment"`
	GPUCost                    string    `json:"gpu_cost"`
	GPUCostAdjustment          string    `json:"gpu_cost_adjustment"`
	RAMCost                    string    `json:"ram_cost"`
	RAMCostAdjustment          string    `json:"ram_cost_adjustment"`
	PVCost                     string    `json:"pv_cost"`
	PVCostAdjustment           string    `json:"pv_cost_adjustment"`
	NetworkCost                string    `json:"network_cost"`
	NetworkCostAdjustment      string    `json:"network_cost_adjustment"`
	LoadBalancerCost           string    `json:"load_balancer_cost"`
	LoadBalancerCostAdjustment string    `json:"load_balancer_cost_adjustment"`
	SharedCost                 string    `json:"shared_cost"`
	ExternalCost               string    `json:"external_cost"`
	TotalCost                  string    `json:"total_cost"`
}

func (response *allocationResponse) UnmarshalJSON(document []byte) error {
	fields, err := objectFields(document, "allocation response")
	if err != nil {
		return err
	}
	if err := rejectCaseAliases(fields, []string{"code", "status", "data", "meta", "message", "warning"}); err != nil {
		return err
	}
	for _, field := range []struct {
		name   string
		target any
	}{
		{name: "code", target: &response.Code},
		{name: "status", target: &response.Status},
		{name: "data", target: &response.Data},
		{name: "message", target: &response.Message},
		{name: "warning", target: &response.Warning},
	} {
		if err := decodeOptionalField(fields, field.name, field.target); err != nil {
			return err
		}
	}
	return nil
}

func (record *allocationRecord) UnmarshalJSON(document []byte) error {
	fields, err := objectFields(document, "allocation")
	if err != nil {
		return err
	}
	known := []string{"name", "properties", "window", "start", "end"}
	for _, field := range costFields {
		known = append(known, field.JSONName)
	}
	if err := rejectCaseAliases(fields, known); err != nil {
		return err
	}
	for _, field := range []struct {
		name   string
		target any
	}{
		{name: "name", target: &record.Name},
		{name: "properties", target: &record.Properties},
		{name: "window", target: &record.Window},
		{name: "start", target: &record.Start},
		{name: "end", target: &record.End},
	} {
		if err := decodeOptionalField(fields, field.name, field.target); err != nil {
			return err
		}
	}
	record.Amounts = make(map[string]json.RawMessage, len(costFields))
	for _, field := range costFields {
		if raw, exists := fields[field.JSONName]; exists {
			record.Amounts[field.JSONName] = append(json.RawMessage(nil), raw...)
		}
	}
	return nil
}

func (properties *allocationProperties) UnmarshalJSON(document []byte) error {
	fields, err := objectFields(document, "allocation properties")
	if err != nil {
		return err
	}
	if err := rejectCaseAliases(fields, []string{"cluster", "namespace"}); err != nil {
		return err
	}
	if err := decodeOptionalField(fields, "cluster", &properties.Cluster); err != nil {
		return err
	}
	return decodeOptionalField(fields, "namespace", &properties.Namespace)
}

func (window *allocationWindow) UnmarshalJSON(document []byte) error {
	fields, err := objectFields(document, "allocation window")
	if err != nil {
		return err
	}
	if err := rejectCaseAliases(fields, []string{"start", "end"}); err != nil {
		return err
	}
	if err := decodeOptionalField(fields, "start", &window.Start); err != nil {
		return err
	}
	return decodeOptionalField(fields, "end", &window.End)
}

// ProjectNamespaceCosts returns one deterministic TELEMETRY cost fact per valid Kubernetes
// namespace. A successful empty allocation set abstains with zero facts. Any invalid row returns
// an error and no partial facts.
func ProjectNamespaceCosts(input Projection) ([]fleet.GraphFact, error) {
	if err := validateProjection(input); err != nil {
		return nil, err
	}
	if err := rejectDuplicateJSON(input.Response); err != nil {
		return nil, fmt.Errorf("decode OpenCost allocation response: %w", err)
	}

	var response allocationResponse
	if err := json.Unmarshal(input.Response, &response); err != nil {
		return nil, fmt.Errorf("decode OpenCost allocation response")
	}
	allocations, err := validateResponse(response)
	if err != nil {
		return nil, err
	}

	namespaces := make([]string, 0, len(allocations))
	for namespace := range allocations {
		namespaces = append(namespaces, namespace)
	}
	sort.Strings(namespaces)

	facts := make([]fleet.GraphFact, 0, len(namespaces))
	for index, namespace := range namespaces {
		observation, err := validateAllocation(input, namespace, allocations[namespace])
		if err != nil {
			return nil, fmt.Errorf("project OpenCost allocation %d: %w", index, err)
		}
		fact, err := buildFact(input, observation)
		if err != nil {
			return nil, err
		}
		facts = append(facts, fact)
	}
	return facts, nil
}

func validateProjection(input Projection) error {
	if err := validateText("workspace", input.Workspace, maxIdentityText); err != nil {
		return err
	}
	if err := validateText("scope", input.Scope, maxIdentityText); err != nil || strings.Contains(input.Scope, "/") {
		return fmt.Errorf("scope is invalid")
	}
	if input.CurrencyCode != currencyUSD {
		return fmt.Errorf("OpenCost source currency must be USD")
	}
	if input.Query.Aggregate != aggregateNamespace || input.Query.Filter != "" || input.Query.Accumulate ||
		input.Query.IncludeIdle || input.Query.ShareIdle || input.Query.IdleByNode ||
		input.Query.ShareLoadBalancer || input.Query.IncludeAggregatedMetadata ||
		input.Query.IncludeProportionalAssetCosts {
		return fmt.Errorf("OpenCost allocation query contract is invalid")
	}
	if err := validateCanonicalTime("window start", input.Query.WindowStart); err != nil {
		return err
	}
	if err := validateCanonicalTime("window end", input.Query.WindowEnd); err != nil {
		return err
	}
	if err := validateCanonicalTime("collection time", input.CollectedAt); err != nil {
		return err
	}
	window := input.Query.WindowEnd.Sub(input.Query.WindowStart)
	if window <= 0 || window > maxQueryWindow {
		return fmt.Errorf("OpenCost allocation window is invalid")
	}
	if input.Query.Step != window {
		return fmt.Errorf("OpenCost allocation step must equal the query window")
	}
	if input.Query.WindowEnd.After(input.CollectedAt.Add(maxClockSkew)) {
		return fmt.Errorf("OpenCost allocation window ends after collection time")
	}
	if len(input.Response) == 0 {
		return fmt.Errorf("OpenCost allocation response is required")
	}
	if len(input.Response) > maxResponseBytes {
		return fmt.Errorf("OpenCost allocation response exceeds %d bytes", maxResponseBytes)
	}
	if !utf8.Valid(input.Response) {
		return fmt.Errorf("OpenCost allocation response must be valid UTF-8")
	}
	return nil
}

func validateResponse(response allocationResponse) (map[string]json.RawMessage, error) {
	if response.Code == nil || *response.Code != 200 || response.Data == nil {
		return nil, fmt.Errorf("OpenCost allocation response is not a complete success")
	}
	if response.Status != nil && *response.Status != "success" {
		return nil, fmt.Errorf("OpenCost allocation response status is invalid")
	}
	if response.Message != nil && *response.Message != "" {
		return nil, fmt.Errorf("OpenCost allocation response contains a message")
	}
	if response.Warning != nil && *response.Warning != "" {
		return nil, fmt.Errorf("OpenCost allocation response contains a warning")
	}
	if len(*response.Data) != 1 || (*response.Data)[0] == nil {
		return nil, fmt.Errorf("OpenCost allocation response must contain exactly one allocation set")
	}
	allocations := (*response.Data)[0]
	if len(allocations) > maxAllocations {
		return nil, fmt.Errorf("OpenCost allocation count exceeds %d", maxAllocations)
	}
	return allocations, nil
}

func validateAllocation(input Projection, mapKey string, document json.RawMessage) (namespaceCostObservation, error) {
	var record allocationRecord
	if err := json.Unmarshal(document, &record); err != nil {
		return namespaceCostObservation{}, fmt.Errorf("decode namespace allocation")
	}
	if record.Name == nil || record.Properties == nil || record.Properties.Cluster == nil ||
		record.Properties.Namespace == nil || record.Window == nil || record.Window.Start == nil ||
		record.Window.End == nil || record.Start == nil || record.End == nil {
		return namespaceCostObservation{}, fmt.Errorf("namespace allocation identity and window are required")
	}
	if mapKey != *record.Name || mapKey != *record.Properties.Namespace ||
		len(validation.IsDNS1123Label(mapKey)) != 0 {
		return namespaceCostObservation{}, fmt.Errorf("namespace allocation identity is invalid")
	}
	if *record.Properties.Cluster != input.Scope {
		return namespaceCostObservation{}, fmt.Errorf("namespace allocation cluster does not match trusted scope")
	}
	if err := validateAllocationWindow(input.Query, *record.Window.Start, *record.Window.End, *record.Start, *record.End); err != nil {
		return namespaceCostObservation{}, err
	}

	parsed := make(map[string]*big.Rat, len(costFields))
	canonical := make(map[string]string, len(costFields))
	total := new(big.Rat)
	for _, field := range costFields {
		raw, exists := record.Amounts[field.JSONName]
		if !exists {
			return namespaceCostObservation{}, fmt.Errorf("namespace allocation cost components are incomplete")
		}
		amount, normalized, err := parseCostAmount(raw, field.AllowNegative)
		if err != nil {
			return namespaceCostObservation{}, fmt.Errorf("namespace allocation cost component is invalid")
		}
		parsed[field.JSONName] = amount
		canonical[field.JSONName] = normalized
		if field.PartOfTotal {
			total.Add(total, amount)
		}
	}
	difference := new(big.Rat).Sub(parsed["totalCost"], total)
	difference.Abs(difference)
	if difference.Cmp(new(big.Rat).SetFrac64(1, 10_000)) > 0 {
		return namespaceCostObservation{}, fmt.Errorf("namespace allocation total does not match cost components")
	}

	return namespaceCostObservation{
		Namespace: mapKey, WindowStart: input.Query.WindowStart, WindowEnd: input.Query.WindowEnd,
		Currency: currencyUSD,
		CPUCost:  canonical["cpuCost"], CPUCostAdjustment: canonical["cpuCostAdjustment"],
		GPUCost: canonical["gpuCost"], GPUCostAdjustment: canonical["gpuCostAdjustment"],
		RAMCost: canonical["ramCost"], RAMCostAdjustment: canonical["ramCostAdjustment"],
		PVCost: canonical["pvCost"], PVCostAdjustment: canonical["pvCostAdjustment"],
		NetworkCost: canonical["networkCost"], NetworkCostAdjustment: canonical["networkCostAdjustment"],
		LoadBalancerCost:           canonical["loadBalancerCost"],
		LoadBalancerCostAdjustment: canonical["loadBalancerCostAdjustment"],
		SharedCost:                 canonical["sharedCost"], ExternalCost: canonical["externalCost"],
		TotalCost: canonical["totalCost"],
	}, nil
}

func validateAllocationWindow(query AllocationQuery, values ...string) error {
	if len(values) != 4 {
		return fmt.Errorf("namespace allocation window is invalid")
	}
	want := []time.Time{query.WindowStart, query.WindowEnd, query.WindowStart, query.WindowEnd}
	for index, value := range values {
		parsed, err := parseCanonicalTime(value)
		if err != nil || !parsed.Equal(want[index]) {
			return fmt.Errorf("namespace allocation window does not match trusted query")
		}
	}
	return nil
}

func parseCostAmount(raw json.RawMessage, allowNegative bool) (*big.Rat, string, error) {
	value := string(bytes.TrimSpace(raw))
	if len(value) == 0 || len(value) > maxCostLiteralBytes || !validCostLiteral(value) {
		return nil, "", fmt.Errorf("invalid cost literal")
	}
	amount, ok := new(big.Rat).SetString(value)
	if !ok || (!allowNegative && amount.Sign() < 0) {
		return nil, "", fmt.Errorf("invalid cost amount")
	}
	absolute := new(big.Rat).Abs(new(big.Rat).Set(amount))
	if absolute.Cmp(new(big.Rat).SetInt64(maxCostUnits)) > 0 {
		return nil, "", fmt.Errorf("cost amount exceeds limit")
	}
	return amount, amount.FloatString(costScale), nil
}

func validCostLiteral(value string) bool {
	if value == "" {
		return false
	}
	index := 0
	if value[index] == '-' {
		index++
		if index == len(value) {
			return false
		}
	}
	if value[index] == '0' {
		index++
		if index < len(value) && value[index] >= '0' && value[index] <= '9' {
			return false
		}
	} else {
		if value[index] < '1' || value[index] > '9' {
			return false
		}
		for index < len(value) && value[index] >= '0' && value[index] <= '9' {
			index++
		}
	}
	if index == len(value) {
		return true
	}
	if value[index] != '.' {
		return false
	}
	index++
	fractionStart := index
	for index < len(value) && value[index] >= '0' && value[index] <= '9' {
		index++
	}
	return index == len(value) && index > fractionStart && index-fractionStart <= costScale
}

func buildFact(input Projection, observation namespaceCostObservation) (fleet.GraphFact, error) {
	encoded, err := json.Marshal(observation)
	if err != nil {
		return fleet.GraphFact{}, fmt.Errorf("encode OpenCost namespace allocation fact: %w", err)
	}
	if len(encoded) > maxFactPayloadBytes {
		return fleet.GraphFact{}, fmt.Errorf("OpenCost namespace allocation fact exceeds %d bytes", maxFactPayloadBytes)
	}
	identity, err := json.Marshal(struct {
		Workspace string          `json:"workspace"`
		Scope     string          `json:"scope"`
		Observed  json.RawMessage `json:"observed"`
	}{Workspace: input.Workspace, Scope: input.Scope, Observed: encoded})
	if err != nil {
		return fleet.GraphFact{}, fmt.Errorf("encode OpenCost namespace allocation identity: %w", err)
	}
	digest := sha256.Sum256(identity)
	nativeID := "sha256:" + hex.EncodeToString(digest[:])
	entity := fleet.EntityRef{Cluster: input.Scope, Namespace: observation.Namespace}
	fact := fleet.GraphFact{
		Fact: fleet.Fact{
			Evidence: fleet.Evidence{
				Ref: fleet.ResourceRef{
					SourceKind: Kind, Scope: input.Scope, Kind: "NamespaceCost",
					Namespace: observation.Namespace, Name: "allocation",
				},
				Kind: fleet.FactCost, Observed: encoded, ObservedAt: input.Query.WindowEnd,
				Source:     input.Scope,
				Provenance: fleet.Provenance{Adapter: Kind, ProtocolV: ProtocolVersion, NativeID: nativeID},
			},
			Workspace: input.Workspace,
		},
		Lens: fleet.LensTelemetry, Entity: &entity,
	}
	if err := fact.Validate(input.Workspace); err != nil {
		return fleet.GraphFact{}, fmt.Errorf("validate OpenCost namespace allocation fact: %w", err)
	}
	return fact, nil
}

func validateCanonicalTime(label string, value time.Time) error {
	if value.IsZero() || value.Year() < 2000 || value.Year() > 9999 || value.Location() != time.UTC ||
		value.Nanosecond() != 0 {
		return fmt.Errorf("%s is invalid", label)
	}
	return nil
}

func parseCanonicalTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil || parsed.Year() < 2000 || parsed.Year() > 9999 ||
		value != parsed.UTC().Format(time.RFC3339) {
		return time.Time{}, fmt.Errorf("time is not canonical UTC RFC3339")
	}
	return parsed, nil
}

func validateText(label, value string, maximum int) error {
	if value == "" || len(value) > maximum || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return fmt.Errorf("%s is invalid", label)
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return fmt.Errorf("%s is invalid", label)
		}
	}
	return nil
}

func objectFields(document []byte, label string) (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(document, &fields); err != nil || fields == nil {
		return nil, fmt.Errorf("%s must be a JSON object", label)
	}
	return fields, nil
}

func rejectCaseAliases(fields map[string]json.RawMessage, exact []string) error {
	known := make(map[string]string, len(exact))
	for _, name := range exact {
		known[strings.ToLower(name)] = name
	}
	for name := range fields {
		canonical, recognized := known[strings.ToLower(name)]
		if recognized && name != canonical {
			return fmt.Errorf("JSON field %s must use exact case", canonical)
		}
	}
	return nil
}

func decodeOptionalField(fields map[string]json.RawMessage, name string, target any) error {
	value, exists := fields[name]
	if !exists {
		return nil
	}
	if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		return fmt.Errorf("JSON field %s is invalid", name)
	}
	if err := json.Unmarshal(value, target); err != nil {
		return fmt.Errorf("JSON field %s is invalid", name)
	}
	return nil
}

func rejectDuplicateJSON(document []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.UseNumber()
	if err := consumeUniqueJSON(decoder, 0); err != nil {
		return err
	}
	if token, err := decoder.Token(); err != io.EOF || token != nil {
		return fmt.Errorf("JSON contains trailing data")
	}
	return nil
}

func consumeUniqueJSON(decoder *json.Decoder, depth int) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, isDelimiter := token.(json.Delim)
	if !isDelimiter {
		return nil
	}
	if depth >= maxJSONDepth {
		return fmt.Errorf("JSON nesting exceeds %d levels", maxJSONDepth)
	}
	switch delimiter {
	case '{':
		seen := make(map[string]bool)
		for decoder.More() {
			nameToken, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := nameToken.(string)
			if !ok || seen[name] {
				return fmt.Errorf("JSON contains a duplicate or invalid object member")
			}
			seen[name] = true
			if err := consumeUniqueJSON(decoder, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for decoder.More() {
			if err := consumeUniqueJSON(decoder, depth+1); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("JSON contains an invalid delimiter")
	}
	closing, err := decoder.Token()
	if err != nil || closing != matchingDelimiter(delimiter) {
		return fmt.Errorf("JSON contains an invalid closing delimiter")
	}
	return nil
}

func matchingDelimiter(open json.Delim) json.Delim {
	if open == '{' {
		return '}'
	}
	return ']'
}
