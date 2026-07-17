// SPDX-License-Identifier: Apache-2.0

// Package prometheus normalizes read-only Prometheus alert evidence for Sith's operational graph.
package prometheus

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/ArdurAI/sith/internal/fleet"
)

const (
	// Kind is the stable registry identifier for Prometheus read evidence.
	Kind = "prometheus"
	// ProtocolVersion identifies the normalized /api/v1/alerts fact contract.
	ProtocolVersion = "alerts/v1"

	maxResponseBytes    = 512 << 10
	maxAlerts           = 256
	maxLabelsPerAlert   = 64
	maxLabelNameBytes   = 256
	maxLabelValueBytes  = 2 << 10
	maxIdentityText     = 253
	maxFactPayloadBytes = 8 << 10
	maxJSONDepth        = 64
)

var projectedLabels = map[string]bool{
	"container":   true,
	"daemonset":   true,
	"deployment":  true,
	"job":         true,
	"namespace":   true,
	"node":        true,
	"pod":         true,
	"service":     true,
	"severity":    true,
	"statefulset": true,
}

// Projection supplies one already-authorized Prometheus /api/v1/alerts response. ProjectAlerts
// does not perform discovery, network access, credential loading, persistence, or mutation.
type Projection struct {
	Workspace  string
	Scope      string
	ObservedAt time.Time
	Response   []byte
}

type alertsEnvelope struct {
	Status string      `json:"status"`
	Data   *alertsData `json:"data"`
}

type alertsData struct {
	Alerts *[]sourceAlert `json:"alerts"`
}

type sourceAlert struct {
	ActiveAt    string            `json:"activeAt"`
	Annotations map[string]string `json:"annotations"`
	Labels      map[string]string `json:"labels"`
	State       string            `json:"state"`
	Value       string            `json:"value"`
}

type alertObservation struct {
	AlertName string            `json:"alert_name"`
	State     string            `json:"state"`
	ActiveAt  time.Time         `json:"active_at"`
	Value     string            `json:"value"`
	Labels    map[string]string `json:"labels,omitempty"`
}

type projectedAlert struct {
	fact      fleet.GraphFact
	nativeID  string
	canonical []byte
}

// ProjectAlerts returns deterministic, bounded, allowlisted telemetry facts from a Prometheus
// /api/v1/alerts response. It deliberately drops annotations and unknown labels, and leaves alerts
// unattached when their Kubernetes identity cannot be resolved without guessing.
func ProjectAlerts(input Projection) ([]fleet.GraphFact, error) {
	if err := validateProjection(input); err != nil {
		return nil, err
	}
	if err := rejectDuplicateJSON(input.Response); err != nil {
		return nil, fmt.Errorf("decode Prometheus alerts response: %w", err)
	}
	var envelope alertsEnvelope
	decoder := json.NewDecoder(bytes.NewReader(input.Response))
	if err := decoder.Decode(&envelope); err != nil {
		return nil, fmt.Errorf("decode Prometheus alerts response: %w", err)
	}
	if envelope.Status != "success" {
		return nil, fmt.Errorf("prometheus alerts response status must be success")
	}
	if envelope.Data == nil || envelope.Data.Alerts == nil {
		return nil, fmt.Errorf("prometheus alerts response data.alerts is required")
	}
	alerts := *envelope.Data.Alerts
	if len(alerts) > maxAlerts {
		return nil, fmt.Errorf("prometheus alert count exceeds %d", maxAlerts)
	}

	projected := make([]projectedAlert, 0, len(alerts))
	seen := make(map[string]bool, len(alerts))
	for index, alert := range alerts {
		fact, nativeID, canonical, err := projectAlert(input, alert)
		if err != nil {
			return nil, fmt.Errorf("project Prometheus alert %d: %w", index, err)
		}
		if seen[nativeID] {
			return nil, fmt.Errorf("project Prometheus alert %d: duplicate alert identity", index)
		}
		seen[nativeID] = true
		projected = append(projected, projectedAlert{fact: fact, nativeID: nativeID, canonical: canonical})
	}
	sort.Slice(projected, func(left, right int) bool {
		if projected[left].nativeID != projected[right].nativeID {
			return projected[left].nativeID < projected[right].nativeID
		}
		return bytes.Compare(projected[left].canonical, projected[right].canonical) < 0
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
	if input.ObservedAt.IsZero() {
		return fmt.Errorf("projection observation time is required")
	}
	if len(input.Response) == 0 {
		return fmt.Errorf("prometheus alerts response is required")
	}
	if len(input.Response) > maxResponseBytes {
		return fmt.Errorf("prometheus alerts response exceeds %d bytes", maxResponseBytes)
	}
	if !utf8.Valid(input.Response) {
		return fmt.Errorf("prometheus alerts response must be valid UTF-8")
	}
	return nil
}

func projectAlert(input Projection, alert sourceAlert) (fleet.GraphFact, string, []byte, error) {
	if len(alert.Labels) == 0 {
		return fleet.GraphFact{}, "", nil, fmt.Errorf("labels are required")
	}
	if len(alert.Labels) > maxLabelsPerAlert {
		return fleet.GraphFact{}, "", nil, fmt.Errorf("label count exceeds %d", maxLabelsPerAlert)
	}
	for name, value := range alert.Labels {
		if err := validateText("label name", name, maxLabelNameBytes, false); err != nil {
			return fleet.GraphFact{}, "", nil, err
		}
		if err := validateLabelValue(value); err != nil {
			return fleet.GraphFact{}, "", nil, err
		}
	}
	alertName, ok := alert.Labels["alertname"]
	if !ok {
		return fleet.GraphFact{}, "", nil, fmt.Errorf("alertname label is required")
	}
	if err := validateText("alertname", alertName, maxIdentityText, false); err != nil {
		return fleet.GraphFact{}, "", nil, err
	}
	if alert.State != "pending" && alert.State != "firing" {
		return fleet.GraphFact{}, "", nil, fmt.Errorf("alert state must be pending or firing")
	}
	activeAt, err := time.Parse(time.RFC3339Nano, alert.ActiveAt)
	if err != nil {
		return fleet.GraphFact{}, "", nil, fmt.Errorf("activeAt must be RFC3339: %w", err)
	}
	if activeAt.IsZero() {
		return fleet.GraphFact{}, "", nil, fmt.Errorf("activeAt must not be zero")
	}
	value, err := strconv.ParseFloat(alert.Value, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return fleet.GraphFact{}, "", nil, fmt.Errorf("alert value must be finite")
	}

	labels := make(map[string]string)
	for name, value := range alert.Labels {
		if projectedLabels[name] {
			labels[name] = value
		}
	}
	entity, err := resolveEntity(input.Scope, labels)
	if err != nil {
		return fleet.GraphFact{}, "", nil, err
	}
	observation := alertObservation{
		AlertName: alertName,
		State:     alert.State,
		ActiveAt:  activeAt.UTC(),
		Value:     strconv.FormatFloat(value, 'g', -1, 64),
		Labels:    labels,
	}
	canonical, err := json.Marshal(observation)
	if err != nil {
		return fleet.GraphFact{}, "", nil, fmt.Errorf("encode alert fact: %w", err)
	}
	if len(canonical) > maxFactPayloadBytes {
		return fleet.GraphFact{}, "", nil, fmt.Errorf("alert fact exceeds %d encoded bytes", maxFactPayloadBytes)
	}
	fingerprintPayload, err := json.Marshal(struct {
		AlertName string            `json:"alert_name"`
		ActiveAt  time.Time         `json:"active_at"`
		Labels    map[string]string `json:"labels,omitempty"`
	}{AlertName: alertName, ActiveAt: activeAt.UTC(), Labels: labels})
	if err != nil {
		return fleet.GraphFact{}, "", nil, fmt.Errorf("encode alert identity: %w", err)
	}
	digest := sha256.Sum256(fingerprintPayload)
	nativeID := "sha256:" + hex.EncodeToString(digest[:])
	resourceName := "alert-" + hex.EncodeToString(digest[:16])

	namespace := alert.Labels["namespace"]
	fact := fleet.GraphFact{
		Fact: fleet.Fact{
			Evidence: fleet.Evidence{
				Ref: fleet.ResourceRef{
					SourceKind: Kind, Scope: input.Scope, Kind: "Alert", Namespace: namespace, Name: resourceName,
				},
				Kind: fleet.FactAlert, Observed: canonical, ObservedAt: input.ObservedAt.UTC(), Source: input.Scope,
				Provenance: fleet.Provenance{Adapter: Kind, ProtocolV: ProtocolVersion, NativeID: nativeID},
			},
			Workspace: input.Workspace,
		},
		Lens: fleet.LensTelemetry, Entity: entity,
	}
	if err := fact.Validate(input.Workspace); err != nil {
		return fleet.GraphFact{}, "", nil, fmt.Errorf("validate Prometheus alert fact: %w", err)
	}
	return fact, nativeID, canonical, nil
}

func resolveEntity(scope string, labels map[string]string) (*fleet.EntityRef, error) {
	type candidate struct {
		kind  string
		value string
	}
	candidates := make([]candidate, 0, 5)
	for label, kind := range map[string]string{
		"pod": "Pod", "node": "Node", "deployment": "Deployment", "statefulset": "StatefulSet", "daemonset": "DaemonSet",
	} {
		if value := labels[label]; value != "" {
			if err := validateText(label, value, maxIdentityText, false); err != nil {
				return nil, err
			}
			candidates = append(candidates, candidate{kind: kind, value: value})
		}
	}
	if len(candidates) == 0 {
		return nil, nil
	}
	if len(candidates) != 1 {
		return nil, fmt.Errorf("alert has ambiguous Kubernetes identity")
	}
	namespace := labels["namespace"]
	if candidates[0].kind == "Node" {
		if namespace != "" {
			return nil, fmt.Errorf("node alert must not include a namespace identity")
		}
		return &fleet.EntityRef{Cluster: scope, Node: candidates[0].value}, nil
	}
	if err := validateText("namespace", namespace, maxIdentityText, false); err != nil {
		return nil, fmt.Errorf("namespaced alert identity: %w", err)
	}
	if candidates[0].kind == "Pod" {
		return &fleet.EntityRef{Cluster: scope, Namespace: namespace, Pod: candidates[0].value}, nil
	}
	return &fleet.EntityRef{
		Cluster: scope, Namespace: namespace, Kind: candidates[0].kind, Name: candidates[0].value,
	}, nil
}

func validateText(label, value string, maximum int, allowEmpty bool) error {
	if (!allowEmpty && value == "") || len(value) > maximum || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return fmt.Errorf("%s is invalid", label)
	}
	for _, runeValue := range value {
		if unicode.IsControl(runeValue) {
			return fmt.Errorf("%s is invalid", label)
		}
	}
	return nil
}

func validateLabelValue(value string) error {
	if len(value) > maxLabelValueBytes || !utf8.ValidString(value) {
		return fmt.Errorf("label value is invalid")
	}
	for _, runeValue := range value {
		if unicode.IsControl(runeValue) {
			return fmt.Errorf("label value is invalid")
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
