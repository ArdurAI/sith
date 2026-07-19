// SPDX-License-Identifier: Apache-2.0

// Package dcgm normalizes bounded NVIDIA dcgm-exporter GPU utilization evidence
// from an already-authorized Prometheus instant query.
package dcgm

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/ArdurAI/sith/internal/fleet"
)

const (
	// Kind is the stable registry identifier for dcgm-exporter evidence.
	Kind = "dcgm"
	// ProtocolVersion identifies the normalized Prometheus GPU-utilization vector contract.
	ProtocolVersion = "prometheus/gpu-utilization-vector-v1"
	// GPUUtilizationMetric is the exact dcgm-exporter metric accepted by this projector.
	GPUUtilizationMetric = "DCGM_FI_DEV_GPU_UTIL"

	maxResponseBytes        = 2 << 20
	maxSeries               = 4_096
	maxLabelsPerSeries      = 64
	maxLabelNameBytes       = 128
	maxLabelValueBytes      = 2 << 10
	maxIdentityText         = 253
	maxModelNameBytes       = 256
	maxMIGProfileBytes      = 64
	maxPercentLiteralBytes  = 32
	maxTimestampLiteralSize = 32
	maxFactPayloadBytes     = 2 << 10
	maxJSONDepth            = 64
	maxClockSkew            = 5 * time.Minute

	attributionPhysicalGPU = "physical_gpu"
	attributionMIGInstance = "mig_instance"
	attributionWorkload    = "workload_best_effort"
)

// InstantQuery records the exact Prometheus query contract used to produce Response.
type InstantQuery struct {
	Expression    string
	EvaluatedAt   time.Time
	Limit         int
	LookbackDelta time.Duration
}

// Projection supplies one already-authorized Prometheus /api/v1/query response. The trusted
// caller owns source selection, authorization, request execution, and response collection.
// ProjectGPUUtilization performs no discovery, network access, credential loading, persistence,
// billing, optimization, process execution, or mutation.
type Projection struct {
	Workspace   string
	Scope       string
	Query       InstantQuery
	CollectedAt time.Time
	Response    []byte
}

type queryEnvelope struct {
	Status    string     `json:"status"`
	Data      *queryData `json:"data"`
	ErrorType string     `json:"errorType"`
	Error     string     `json:"error"`
	Warnings  []string   `json:"warnings"`
	Infos     []string   `json:"infos"`
}

type queryData struct {
	ResultType string          `json:"resultType"`
	Result     *[]sourceSeries `json:"result"`
}

type sourceSeries struct {
	Metric map[string]string `json:"metric"`
	Value  []json.RawMessage `json:"value"`
}

type gpuUtilizationObservation struct {
	UtilizationPercent string `json:"utilization_percent"`
	Attribution        string `json:"attribution"`
	DeviceScope        string `json:"device_scope"`
	ModelName          string `json:"model_name"`
	MIGInstanceID      string `json:"mig_instance_id,omitempty"`
	MIGProfile         string `json:"mig_profile,omitempty"`
	Namespace          string `json:"namespace,omitempty"`
	Pod                string `json:"pod,omitempty"`
	Container          string `json:"container,omitempty"`
}

type seriesIdentity struct {
	Metric        string `json:"metric"`
	GPU           string `json:"gpu"`
	UUID          string `json:"uuid"`
	Device        string `json:"device"`
	Hostname      string `json:"hostname"`
	MIGInstanceID string `json:"mig_instance_id,omitempty"`
	MIGProfile    string `json:"mig_profile,omitempty"`
	Namespace     string `json:"namespace,omitempty"`
	Pod           string `json:"pod,omitempty"`
	Container     string `json:"container,omitempty"`
}

type projectedSeries struct {
	fact     fleet.GraphFact
	nativeID string
}

// ProjectGPUUtilization returns deterministic, bounded TELEMETRY facts from one successful
// Prometheus instant vector. Unknown labels are validated but not retained. Whole-input failure is
// atomic, and complete workload labels are always marked best-effort rather than exact accounting.
func ProjectGPUUtilization(input Projection) ([]fleet.GraphFact, error) {
	if err := validateProjection(input); err != nil {
		return nil, err
	}
	if err := rejectDuplicateJSON(input.Response); err != nil {
		return nil, fmt.Errorf("decode DCGM Prometheus response: %w", err)
	}

	var envelope queryEnvelope
	if err := json.Unmarshal(input.Response, &envelope); err != nil {
		return nil, fmt.Errorf("decode DCGM Prometheus response")
	}
	series, err := validateResponse(envelope)
	if err != nil {
		return nil, err
	}

	projected := make([]projectedSeries, 0, len(series))
	seen := make(map[string]bool, len(series))
	for index, source := range series {
		fact, nativeID, err := projectSeries(input, source)
		if err != nil {
			return nil, fmt.Errorf("project DCGM GPU utilization series %d: %w", index, err)
		}
		if seen[nativeID] {
			return nil, fmt.Errorf("project DCGM GPU utilization series %d: duplicate projected identity", index)
		}
		seen[nativeID] = true
		projected = append(projected, projectedSeries{fact: fact, nativeID: nativeID})
	}

	sort.Slice(projected, func(left, right int) bool {
		return projected[left].nativeID < projected[right].nativeID
	})
	facts := make([]fleet.GraphFact, len(projected))
	for index := range projected {
		facts[index] = projected[index].fact
	}
	return facts, nil
}

func validateProjection(input Projection) error {
	if err := validateText("workspace", input.Workspace, maxIdentityText, false); err != nil {
		return err
	}
	if err := validateText("scope", input.Scope, maxIdentityText, false); err != nil {
		return err
	}
	if strings.Contains(input.Scope, "/") {
		return fmt.Errorf("scope is invalid")
	}
	if input.Query.Expression != GPUUtilizationMetric {
		return fmt.Errorf("DCGM Prometheus expression must equal %s", GPUUtilizationMetric)
	}
	if input.Query.Limit != 0 {
		return fmt.Errorf("DCGM Prometheus query limit must be disabled")
	}
	if input.Query.LookbackDelta != 0 {
		return fmt.Errorf("DCGM Prometheus query lookback override must be disabled")
	}
	if err := validateCanonicalTime("query evaluation time", input.Query.EvaluatedAt); err != nil {
		return err
	}
	if err := validateCanonicalTime("collection time", input.CollectedAt); err != nil {
		return err
	}
	if input.Query.EvaluatedAt.After(input.CollectedAt.Add(maxClockSkew)) {
		return fmt.Errorf("DCGM Prometheus query evaluation time is after collection time")
	}
	if len(input.Response) == 0 {
		return fmt.Errorf("DCGM Prometheus response is required")
	}
	if len(input.Response) > maxResponseBytes {
		return fmt.Errorf("DCGM Prometheus response exceeds %d bytes", maxResponseBytes)
	}
	if !utf8.Valid(input.Response) {
		return fmt.Errorf("DCGM Prometheus response must be valid UTF-8")
	}
	return nil
}

func validateResponse(envelope queryEnvelope) ([]sourceSeries, error) {
	if envelope.Status != "success" {
		return nil, fmt.Errorf("DCGM Prometheus response status must be success")
	}
	if envelope.ErrorType != "" || envelope.Error != "" {
		return nil, fmt.Errorf("successful DCGM Prometheus response must not contain an error")
	}
	if len(envelope.Warnings) != 0 || len(envelope.Infos) != 0 {
		return nil, fmt.Errorf("DCGM Prometheus response must not contain warnings or infos")
	}
	if envelope.Data == nil || envelope.Data.Result == nil {
		return nil, fmt.Errorf("DCGM Prometheus response data.result is required")
	}
	if envelope.Data.ResultType != "vector" {
		return nil, fmt.Errorf("DCGM Prometheus response resultType must be vector")
	}
	series := *envelope.Data.Result
	if len(series) > maxSeries {
		return nil, fmt.Errorf("DCGM Prometheus series count exceeds %d", maxSeries)
	}
	return series, nil
}

func projectSeries(input Projection, source sourceSeries) (fleet.GraphFact, string, error) {
	if len(source.Metric) == 0 {
		return fleet.GraphFact{}, "", fmt.Errorf("metric labels are required")
	}
	if len(source.Metric) > maxLabelsPerSeries {
		return fleet.GraphFact{}, "", fmt.Errorf("metric label count exceeds %d", maxLabelsPerSeries)
	}
	for name, value := range source.Metric {
		if !validLabelName(name) {
			return fleet.GraphFact{}, "", fmt.Errorf("metric label name is invalid")
		}
		if err := validateLabelValue(value); err != nil {
			return fleet.GraphFact{}, "", err
		}
	}

	metric, err := requiredLabel(source.Metric, "__name__", maxLabelValueBytes)
	if err != nil || metric != GPUUtilizationMetric {
		return fleet.GraphFact{}, "", fmt.Errorf("metric name must equal %s", GPUUtilizationMetric)
	}
	gpu, err := requiredLabel(source.Metric, "gpu", 10)
	if err != nil || !validIndex(gpu) {
		return fleet.GraphFact{}, "", fmt.Errorf("gpu label is invalid")
	}
	uuid, err := requiredLabel(source.Metric, "UUID", 128)
	if err != nil || !validGPUUUID(uuid) {
		return fleet.GraphFact{}, "", fmt.Errorf("UUID label is invalid")
	}
	device, err := requiredLabel(source.Metric, "device", 32)
	if err != nil || device != "nvidia"+gpu {
		return fleet.GraphFact{}, "", fmt.Errorf("device label is invalid")
	}
	modelName, err := requiredLabel(source.Metric, "modelName", maxModelNameBytes)
	if err != nil {
		return fleet.GraphFact{}, "", fmt.Errorf("modelName label is invalid")
	}
	hostname, err := requiredLabel(source.Metric, "hostname", maxIdentityText)
	if err != nil || !validSafeToken(hostname, ".-_") {
		return fleet.GraphFact{}, "", fmt.Errorf("hostname label is invalid")
	}

	migInstanceID, hasMIGID := source.Metric["GPU_I_ID"]
	migProfile, hasMIGProfile := source.Metric["GPU_I_PROFILE"]
	if hasMIGID != hasMIGProfile {
		return fleet.GraphFact{}, "", fmt.Errorf("MIG identity labels must be supplied together")
	}
	deviceScope := attributionPhysicalGPU
	if hasMIGID {
		if !validIndex(migInstanceID) {
			return fleet.GraphFact{}, "", fmt.Errorf("GPU_I_ID label is invalid")
		}
		if err := validateText("GPU_I_PROFILE label", migProfile, maxMIGProfileBytes, false); err != nil ||
			!validSafeToken(migProfile, ".-+_") {
			return fleet.GraphFact{}, "", fmt.Errorf("GPU_I_PROFILE label is invalid")
		}
		deviceScope = attributionMIGInstance
	}

	namespace, pod, container, hasWorkload, err := workloadIdentity(source.Metric)
	if err != nil {
		return fleet.GraphFact{}, "", err
	}
	attribution := deviceScope
	if hasWorkload {
		attribution = attributionWorkload
	}

	if len(source.Value) != 2 {
		return fleet.GraphFact{}, "", fmt.Errorf("instant-vector value must contain timestamp and sample")
	}
	sampleTime, err := parseUnixTimestamp(source.Value[0])
	if err != nil {
		return fleet.GraphFact{}, "", err
	}
	if !sampleTime.Equal(input.Query.EvaluatedAt) {
		return fleet.GraphFact{}, "", fmt.Errorf("sample timestamp must equal query evaluation time")
	}
	var sourcePercent string
	if err := json.Unmarshal(source.Value[1], &sourcePercent); err != nil {
		return fleet.GraphFact{}, "", fmt.Errorf("GPU utilization sample must be a string")
	}
	percent, err := canonicalPercent(sourcePercent)
	if err != nil {
		return fleet.GraphFact{}, "", err
	}

	identity := seriesIdentity{
		Metric: metric, GPU: gpu, UUID: uuid, Device: device, Hostname: hostname,
		MIGInstanceID: migInstanceID, MIGProfile: migProfile,
		Namespace: namespace, Pod: pod, Container: container,
	}
	identityJSON, err := json.Marshal(identity)
	if err != nil {
		return fleet.GraphFact{}, "", fmt.Errorf("encode DCGM series identity: %w", err)
	}
	digest := sha256.Sum256(identityJSON)
	hexDigest := hex.EncodeToString(digest[:])
	nativeID := "sha256:" + hexDigest

	observation := gpuUtilizationObservation{
		UtilizationPercent: percent,
		Attribution:        attribution,
		DeviceScope:        deviceScope,
		ModelName:          modelName,
		MIGInstanceID:      migInstanceID,
		MIGProfile:         migProfile,
		Namespace:          namespace,
		Pod:                pod,
		Container:          container,
	}
	canonical, err := json.Marshal(observation)
	if err != nil {
		return fleet.GraphFact{}, "", fmt.Errorf("encode DCGM GPU utilization fact: %w", err)
	}
	if len(canonical) > maxFactPayloadBytes {
		return fleet.GraphFact{}, "", fmt.Errorf("DCGM GPU utilization fact exceeds %d bytes", maxFactPayloadBytes)
	}

	entity := &fleet.EntityRef{Cluster: input.Scope}
	if hasWorkload {
		entity.Namespace = namespace
		entity.Pod = pod
	}
	fact := fleet.GraphFact{
		Fact: fleet.Fact{
			Evidence: fleet.Evidence{
				Ref: fleet.ResourceRef{
					SourceKind: Kind, Scope: input.Scope, Kind: "GPUUtilization",
					Namespace: namespace, Name: "gpu-utilization-" + hexDigest,
				},
				Kind: fleet.FactDerived, Observed: canonical, ObservedAt: sampleTime,
				Source: input.Scope,
				Provenance: fleet.Provenance{
					Adapter: Kind, ProtocolV: ProtocolVersion, NativeID: nativeID,
				},
			},
			Workspace: input.Workspace,
		},
		Lens: fleet.LensTelemetry, Entity: entity,
	}
	if err := fact.Validate(input.Workspace); err != nil {
		return fleet.GraphFact{}, "", fmt.Errorf("validate DCGM GPU utilization fact: %w", err)
	}
	return fact, nativeID, nil
}

func requiredLabel(labels map[string]string, name string, maximum int) (string, error) {
	value, exists := labels[name]
	if !exists {
		return "", fmt.Errorf("required metric label is missing")
	}
	if err := validateText("metric label", value, maximum, false); err != nil {
		return "", err
	}
	return value, nil
}

func workloadIdentity(labels map[string]string) (string, string, string, bool, error) {
	namespace, hasNamespace := labels["namespace"]
	pod, hasPod := labels["pod"]
	container, hasContainer := labels["container"]
	count := 0
	for _, present := range []bool{hasNamespace, hasPod, hasContainer} {
		if present {
			count++
		}
	}
	if count == 0 {
		return "", "", "", false, nil
	}
	if count != 3 {
		return "", "", "", false, fmt.Errorf("workload labels must be supplied together")
	}
	if len(validation.IsDNS1123Label(namespace)) != 0 {
		return "", "", "", false, fmt.Errorf("namespace label is invalid")
	}
	if len(validation.IsDNS1123Subdomain(pod)) != 0 {
		return "", "", "", false, fmt.Errorf("pod label is invalid")
	}
	if len(validation.IsDNS1123Label(container)) != 0 {
		return "", "", "", false, fmt.Errorf("container label is invalid")
	}
	return namespace, pod, container, true, nil
}

func validLabelName(value string) bool {
	if value == "" || len(value) > maxLabelNameBytes {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || character == '_' ||
			(index > 0 && character >= '0' && character <= '9') {
			continue
		}
		return false
	}
	return true
}

func validateLabelValue(value string) error {
	if len(value) > maxLabelValueBytes || !utf8.ValidString(value) {
		return fmt.Errorf("metric label value is invalid")
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return fmt.Errorf("metric label value is invalid")
		}
	}
	return nil
}

func validIndex(value string) bool {
	if value == "" || len(value) > 10 || (len(value) > 1 && value[0] == '0') {
		return false
	}
	for index := range value {
		if value[index] < '0' || value[index] > '9' {
			return false
		}
	}
	return true
}

func validGPUUUID(value string) bool {
	if !strings.HasPrefix(value, "GPU-") {
		return false
	}
	identifier := value[len("GPU-"):]
	if len(identifier) != 36 {
		return false
	}
	for index := range identifier {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			if identifier[index] != '-' {
				return false
			}
			continue
		}
		character := identifier[index]
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') &&
			(character < 'A' || character > 'F') {
			return false
		}
	}
	return true
}

func validSafeToken(value, extra string) bool {
	if value == "" {
		return false
	}
	for index := range value {
		character := value[index]
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || strings.ContainsRune(extra, rune(character)) {
			continue
		}
		return false
	}
	return true
}

func canonicalPercent(value string) (string, error) {
	if value == "" || len(value) > maxPercentLiteralBytes {
		return "", fmt.Errorf("GPU utilization sample is invalid")
	}
	integer, fraction, hasFraction := strings.Cut(value, ".")
	if integer == "" || (len(integer) > 1 && integer[0] == '0') || len(integer) > 3 {
		return "", fmt.Errorf("GPU utilization sample is invalid")
	}
	for index := range integer {
		if integer[index] < '0' || integer[index] > '9' {
			return "", fmt.Errorf("GPU utilization sample is invalid")
		}
	}
	if hasFraction {
		if fraction == "" || len(fraction) > 18 {
			return "", fmt.Errorf("GPU utilization sample is invalid")
		}
		for index := range fraction {
			if fraction[index] < '0' || fraction[index] > '9' {
				return "", fmt.Errorf("GPU utilization sample is invalid")
			}
		}
	}
	integerValue, err := strconv.Atoi(integer)
	if err != nil || integerValue > 100 || (integerValue == 100 && strings.Trim(fraction, "0") != "") {
		return "", fmt.Errorf("GPU utilization sample must be between 0 and 100 percent")
	}
	fraction = strings.TrimRight(fraction, "0")
	if fraction == "" {
		return strconv.Itoa(integerValue), nil
	}
	return strconv.Itoa(integerValue) + "." + fraction, nil
}

func parseUnixTimestamp(document json.RawMessage) (time.Time, error) {
	value := string(document)
	if value == "" || len(value) > maxTimestampLiteralSize {
		return time.Time{}, fmt.Errorf("sample timestamp is invalid")
	}
	secondsLiteral, fraction, hasFraction := strings.Cut(value, ".")
	if secondsLiteral == "" || (len(secondsLiteral) > 1 && secondsLiteral[0] == '0') {
		return time.Time{}, fmt.Errorf("sample timestamp is invalid")
	}
	for index := range secondsLiteral {
		if secondsLiteral[index] < '0' || secondsLiteral[index] > '9' {
			return time.Time{}, fmt.Errorf("sample timestamp is invalid")
		}
	}
	if hasFraction {
		if fraction == "" || len(fraction) > 9 {
			return time.Time{}, fmt.Errorf("sample timestamp is invalid")
		}
		for index := range fraction {
			if fraction[index] < '0' || fraction[index] > '9' {
				return time.Time{}, fmt.Errorf("sample timestamp is invalid")
			}
		}
	}
	seconds, err := strconv.ParseInt(secondsLiteral, 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("sample timestamp is invalid")
	}
	for len(fraction) < 9 {
		fraction += "0"
	}
	nanoseconds := int64(0)
	if fraction != "" {
		nanoseconds, err = strconv.ParseInt(fraction, 10, 32)
		if err != nil {
			return time.Time{}, fmt.Errorf("sample timestamp is invalid")
		}
	}
	parsed := time.Unix(seconds, nanoseconds).UTC()
	if parsed.Year() < 2000 || parsed.Year() > 9999 {
		return time.Time{}, fmt.Errorf("sample timestamp is invalid")
	}
	return parsed, nil
}

func validateCanonicalTime(label string, value time.Time) error {
	if value.IsZero() || value.Year() < 2000 || value.Year() > 9999 || value.Location() != time.UTC {
		return fmt.Errorf("%s is invalid", label)
	}
	return nil
}

func validateText(label, value string, maximum int, allowEmpty bool) error {
	if (!allowEmpty && value == "") || len(value) > maximum || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return fmt.Errorf("%s is invalid", label)
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return fmt.Errorf("%s is invalid", label)
		}
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
