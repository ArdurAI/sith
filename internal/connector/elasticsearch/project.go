// SPDX-License-Identifier: Apache-2.0

// Package elasticsearch normalizes bounded Elasticsearch log-search evidence for Sith's
// operational graph.
package elasticsearch

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/ArdurAI/sith/internal/fleet"
)

const (
	// Kind is the stable registry identifier for Elasticsearch read evidence.
	Kind = "elasticsearch"
	// ProtocolVersion identifies the normalized Elasticsearch Search API ECS fact contract.
	ProtocolVersion = "search/ecs-v1"

	maxResponseBytes    = 1 << 20
	maxHits             = 128
	maxMessageBytes     = 16 << 10
	maxIdentityText     = 253
	maxFactPayloadBytes = 4 << 10
	maxJSONDepth        = 64
	maxQueryWindow      = 15 * time.Minute
	maxClockSkew        = 5 * time.Minute
	maxCauseFacts       = 3
)

const (
	timestampField = "@timestamp"
	messageField   = "message"
	clusterField   = "orchestrator.cluster.name"
	namespaceField = "kubernetes.namespace"
	podField       = "kubernetes.pod.name"
	containerField = "kubernetes.container.name"
)

var allowedHitFields = map[string]bool{
	timestampField: true,
	messageField:   true,
	clusterField:   true,
	namespaceField: true,
	podField:       true,
	containerField: true,
}

// Projection supplies one already-authorized Elasticsearch Search API response. The trusted
// caller owns the query target, authorization, and exact query contract. ProjectLogCauses does
// not perform discovery, network access, credential loading, persistence, or mutation.
type Projection struct {
	Workspace   string
	Scope       string
	Namespace   string
	Pod         string
	Container   string
	WindowStart time.Time
	WindowEnd   time.Time
	ObservedAt  time.Time
	Response    []byte
}

type searchResponse struct {
	TimedOut        *bool
	TerminatedEarly *bool
	Shards          *shardSummary
	Hits            *hitEnvelope
}

type shardSummary struct {
	Total      *int64
	Successful *int64
	Skipped    *int64
	Failed     *int64
}

type hitEnvelope struct {
	Hits *[]searchHit
}

type searchHit struct {
	Fields               *map[string]json.RawMessage
	SourcePresent        bool
	IgnoredPresent       bool
	IgnoredValuesPresent bool
	HighlightPresent     bool
	InnerHitsPresent     bool
}

type causeObservation struct {
	Key          string    `json:"key"`
	Value        string    `json:"value"`
	Count        int       `json:"count"`
	FirstEventAt time.Time `json:"first_event_at"`
	LastEventAt  time.Time `json:"last_event_at"`
	Container    string    `json:"container,omitempty"`
}

type causeAggregate struct {
	Count int
	First time.Time
	Last  time.Time
}

func (response *searchResponse) UnmarshalJSON(document []byte) error {
	fields, err := objectFields(document, "search response")
	if err != nil {
		return err
	}
	if err := decodeOptionalField(fields, "timed_out", &response.TimedOut); err != nil {
		return err
	}
	if err := decodeOptionalField(fields, "terminated_early", &response.TerminatedEarly); err != nil {
		return err
	}
	if err := decodeOptionalField(fields, "_shards", &response.Shards); err != nil {
		return err
	}
	return decodeOptionalField(fields, "hits", &response.Hits)
}

func (summary *shardSummary) UnmarshalJSON(document []byte) error {
	fields, err := objectFields(document, "shard summary")
	if err != nil {
		return err
	}
	for _, field := range []struct {
		name   string
		target **int64
	}{
		{name: "total", target: &summary.Total},
		{name: "successful", target: &summary.Successful},
		{name: "skipped", target: &summary.Skipped},
		{name: "failed", target: &summary.Failed},
	} {
		if err := decodeOptionalField(fields, field.name, field.target); err != nil {
			return err
		}
	}
	return nil
}

func (envelope *hitEnvelope) UnmarshalJSON(document []byte) error {
	fields, err := objectFields(document, "hits envelope")
	if err != nil {
		return err
	}
	return decodeOptionalField(fields, "hits", &envelope.Hits)
}

func (hit *searchHit) UnmarshalJSON(document []byte) error {
	fields, err := objectFields(document, "search hit")
	if err != nil {
		return err
	}
	if err := decodeOptionalField(fields, "fields", &hit.Fields); err != nil {
		return err
	}
	_, hit.SourcePresent = fields["_source"]
	_, hit.IgnoredPresent = fields["_ignored"]
	_, hit.IgnoredValuesPresent = fields["ignored_field_values"]
	_, hit.HighlightPresent = fields["highlight"]
	_, hit.InnerHitsPresent = fields["inner_hits"]
	return nil
}

func objectFields(document []byte, label string) (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(document, &fields); err != nil || fields == nil {
		return nil, fmt.Errorf("%s must be a JSON object", label)
	}
	return fields, nil
}

func decodeOptionalField(fields map[string]json.RawMessage, name string, target any) error {
	value, exists := fields[name]
	if !exists {
		return nil
	}
	if err := json.Unmarshal(value, target); err != nil {
		return fmt.Errorf("search response field %s is invalid", name)
	}
	return nil
}

// ProjectLogCauses returns deterministic, bounded TELEMETRY facts for conservative R3 cause
// signatures. Raw log messages are classified in memory and discarded before fact construction.
// A successful response with no classified messages abstains with zero facts.
func ProjectLogCauses(input Projection) ([]fleet.GraphFact, error) {
	if err := validateProjection(input); err != nil {
		return nil, err
	}
	if err := rejectDuplicateJSON(input.Response); err != nil {
		return nil, fmt.Errorf("decode Elasticsearch search response: %w", err)
	}

	var response searchResponse
	decoder := json.NewDecoder(bytes.NewReader(input.Response))
	if err := decoder.Decode(&response); err != nil {
		return nil, fmt.Errorf("decode Elasticsearch search response")
	}
	if err := validateSearchResponse(response); err != nil {
		return nil, err
	}

	aggregates := make(map[string]*causeAggregate, maxCauseFacts)
	for index, hit := range *response.Hits.Hits {
		message, eventAt, err := validateHit(input, hit)
		if err != nil {
			return nil, fmt.Errorf("project Elasticsearch search hit %d: %w", index, err)
		}
		cause := classifyMessage(message)
		if cause == "" {
			continue
		}
		aggregate := aggregates[cause]
		if aggregate == nil {
			aggregate = &causeAggregate{First: eventAt, Last: eventAt}
			aggregates[cause] = aggregate
		}
		aggregate.Count++
		if eventAt.Before(aggregate.First) {
			aggregate.First = eventAt
		}
		if eventAt.After(aggregate.Last) {
			aggregate.Last = eventAt
		}
	}

	causes := make([]string, 0, len(aggregates))
	for cause := range aggregates {
		causes = append(causes, cause)
	}
	sort.Strings(causes)
	if len(causes) > maxCauseFacts {
		return nil, fmt.Errorf("elasticsearch cause fact count exceeds %d", maxCauseFacts)
	}

	facts := make([]fleet.GraphFact, 0, len(causes))
	for _, cause := range causes {
		fact, err := buildFact(input, cause, *aggregates[cause])
		if err != nil {
			return nil, err
		}
		facts = append(facts, fact)
	}
	return facts, nil
}

func validateProjection(input Projection) error {
	for _, field := range []struct {
		label    string
		value    string
		optional bool
	}{
		{label: "workspace", value: input.Workspace},
		{label: "scope", value: input.Scope},
		{label: "namespace", value: input.Namespace},
		{label: "pod", value: input.Pod},
		{label: "container", value: input.Container, optional: true},
	} {
		if err := validateText(field.label, field.value, maxIdentityText, field.optional); err != nil {
			return err
		}
	}
	if strings.Contains(input.Scope, "/") {
		return fmt.Errorf("scope is invalid")
	}
	if input.WindowStart.IsZero() || input.WindowEnd.IsZero() || input.ObservedAt.IsZero() {
		return fmt.Errorf("query window and observation time are required")
	}
	windowStart := input.WindowStart.UTC()
	windowEnd := input.WindowEnd.UTC()
	if !windowStart.Before(windowEnd) {
		return fmt.Errorf("query window start must be before end")
	}
	if windowEnd.Sub(windowStart) > maxQueryWindow {
		return fmt.Errorf("query window exceeds %s", maxQueryWindow)
	}
	if windowEnd.After(input.ObservedAt.UTC().Add(maxClockSkew)) {
		return fmt.Errorf("query window exceeds allowed collection clock skew")
	}
	if len(input.Response) == 0 {
		return fmt.Errorf("elasticsearch search response is required")
	}
	if len(input.Response) > maxResponseBytes {
		return fmt.Errorf("elasticsearch search response exceeds %d bytes", maxResponseBytes)
	}
	if !utf8.Valid(input.Response) {
		return fmt.Errorf("elasticsearch search response must be valid UTF-8")
	}
	return nil
}

func validateSearchResponse(response searchResponse) error {
	if response.TimedOut == nil {
		return fmt.Errorf("elasticsearch search response timed_out is required")
	}
	if *response.TimedOut {
		return fmt.Errorf("elasticsearch search response must not be timed out")
	}
	if response.TerminatedEarly != nil && *response.TerminatedEarly {
		return fmt.Errorf("elasticsearch search response must not be terminated early")
	}
	if response.Shards == nil {
		return fmt.Errorf("elasticsearch search response _shards is required")
	}
	counts := []*int64{response.Shards.Total, response.Shards.Successful, response.Shards.Skipped, response.Shards.Failed}
	for _, count := range counts {
		if count == nil || *count < 0 {
			return fmt.Errorf("elasticsearch search response shard counts must be present and non-negative")
		}
	}
	if *response.Shards.Total == 0 {
		return fmt.Errorf("elasticsearch search response must cover at least one shard")
	}
	if *response.Shards.Failed != 0 || *response.Shards.Successful+*response.Shards.Skipped != *response.Shards.Total {
		return fmt.Errorf("elasticsearch search response must be complete with no failed shards")
	}
	if response.Hits == nil || response.Hits.Hits == nil {
		return fmt.Errorf("elasticsearch search response hits.hits is required")
	}
	if len(*response.Hits.Hits) > maxHits {
		return fmt.Errorf("elasticsearch search hit count exceeds %d", maxHits)
	}
	return nil
}

func validateHit(input Projection, hit searchHit) (string, time.Time, error) {
	if hit.SourcePresent {
		return "", time.Time{}, fmt.Errorf("_source must not be returned")
	}
	if hit.IgnoredPresent || hit.IgnoredValuesPresent {
		return "", time.Time{}, fmt.Errorf("ignored raw field values must not be returned")
	}
	if hit.HighlightPresent || hit.InnerHitsPresent {
		return "", time.Time{}, fmt.Errorf("expanded or highlighted log content must not be returned")
	}
	if hit.Fields == nil {
		return "", time.Time{}, fmt.Errorf("fields are required")
	}
	fields := *hit.Fields
	for name := range fields {
		if !allowedHitFields[name] {
			return "", time.Time{}, fmt.Errorf("returned field is not allowlisted")
		}
	}

	timestamp, err := requiredSingleString(fields, timestampField, 64)
	if err != nil {
		return "", time.Time{}, err
	}
	message, err := requiredSingleMessage(fields)
	if err != nil {
		return "", time.Time{}, err
	}
	cluster, err := requiredSingleString(fields, clusterField, maxIdentityText)
	if err != nil {
		return "", time.Time{}, err
	}
	namespace, err := requiredSingleString(fields, namespaceField, maxIdentityText)
	if err != nil {
		return "", time.Time{}, err
	}
	pod, err := requiredSingleString(fields, podField, maxIdentityText)
	if err != nil {
		return "", time.Time{}, err
	}
	container, containerPresent, err := optionalSingleString(fields, containerField, maxIdentityText)
	if err != nil {
		return "", time.Time{}, err
	}
	if cluster != input.Scope || namespace != input.Namespace || pod != input.Pod {
		return "", time.Time{}, fmt.Errorf("kubernetes identity does not match trusted caller identity")
	}
	if input.Container != "" && (!containerPresent || container != input.Container) {
		return "", time.Time{}, fmt.Errorf("container identity does not match trusted caller identity")
	}

	eventAt, err := time.Parse(time.RFC3339Nano, timestamp)
	if err != nil || eventAt.IsZero() {
		return "", time.Time{}, fmt.Errorf("@timestamp must be a non-zero RFC3339 timestamp")
	}
	eventAt = eventAt.UTC()
	if eventAt.Before(input.WindowStart.UTC()) || eventAt.After(input.WindowEnd.UTC()) {
		return "", time.Time{}, fmt.Errorf("log event timestamp is outside the trusted query window")
	}
	return message, eventAt, nil
}

func requiredSingleString(fields map[string]json.RawMessage, name string, maximum int) (string, error) {
	value, present, err := optionalSingleString(fields, name, maximum)
	if err != nil {
		return "", err
	}
	if !present {
		return "", fmt.Errorf("field %s is required", name)
	}
	return value, nil
}

func optionalSingleString(fields map[string]json.RawMessage, name string, maximum int) (string, bool, error) {
	raw, present := fields[name]
	if !present {
		return "", false, nil
	}
	var values []string
	if err := json.Unmarshal(raw, &values); err != nil || len(values) != 1 {
		return "", false, fmt.Errorf("field %s must contain exactly one string", name)
	}
	if err := validateText(name, values[0], maximum, false); err != nil {
		return "", false, err
	}
	return values[0], true, nil
}

func requiredSingleMessage(fields map[string]json.RawMessage) (string, error) {
	raw, present := fields[messageField]
	if !present {
		return "", fmt.Errorf("field %s is required", messageField)
	}
	var values []string
	if err := json.Unmarshal(raw, &values); err != nil || len(values) != 1 {
		return "", fmt.Errorf("field %s must contain exactly one string", messageField)
	}
	if len(values[0]) > maxMessageBytes || !utf8.ValidString(values[0]) {
		return "", fmt.Errorf("field %s exceeds the safe message contract", messageField)
	}
	return values[0], nil
}

func classifyMessage(message string) string {
	normalized := strings.ToLower(message)
	if indicatesMissingConfig(normalized) {
		return "missing-config"
	}
	if indicatesDependencyFailure(normalized) {
		return "dependency-failure"
	}
	if indicatesPanic(normalized) {
		return "panic"
	}
	return ""
}

func indicatesMissingConfig(message string) bool {
	missing := containsAny(message, "missing required", "not set", "is required", "not found", "does not exist", "no such file")
	if !missing {
		return false
	}
	return containsAny(message, "environment variable", "env var", "configuration", "config", "configmap", "secret")
}

func indicatesDependencyFailure(message string) bool {
	return containsAny(message,
		"connection refused",
		"connection reset by peer",
		"connection timed out",
		"context deadline exceeded",
		"dial tcp",
		"failed to connect",
		"i/o timeout",
		"no such host",
		"tls handshake timeout",
		"upstream connect error",
	)
}

func indicatesPanic(message string) bool {
	for _, line := range strings.Split(message, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "panic:") ||
			strings.HasPrefix(trimmed, "fatal error:") ||
			strings.HasPrefix(trimmed, "uncaught exception") ||
			strings.HasPrefix(trimmed, "unhandled exception") ||
			strings.HasPrefix(trimmed, "exception in thread") ||
			strings.HasPrefix(trimmed, "traceback (most recent call last):") {
			return true
		}
	}
	return false
}

func containsAny(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}

func buildFact(input Projection, cause string, aggregate causeAggregate) (fleet.GraphFact, error) {
	observation := causeObservation{
		Key:          "logs.cause",
		Value:        cause,
		Count:        aggregate.Count,
		FirstEventAt: aggregate.First.UTC(),
		LastEventAt:  aggregate.Last.UTC(),
		Container:    input.Container,
	}
	encoded, err := json.Marshal(observation)
	if err != nil {
		return fleet.GraphFact{}, fmt.Errorf("encode Elasticsearch log-cause fact: %w", err)
	}
	if len(encoded) > maxFactPayloadBytes {
		return fleet.GraphFact{}, fmt.Errorf("elasticsearch log-cause fact exceeds %d encoded bytes", maxFactPayloadBytes)
	}

	identity, err := json.Marshal(struct {
		Scope       string    `json:"scope"`
		Namespace   string    `json:"namespace"`
		Pod         string    `json:"pod"`
		Container   string    `json:"container,omitempty"`
		Cause       string    `json:"cause"`
		WindowStart time.Time `json:"window_start"`
		WindowEnd   time.Time `json:"window_end"`
	}{
		Scope: input.Scope, Namespace: input.Namespace, Pod: input.Pod, Container: input.Container,
		Cause: cause, WindowStart: input.WindowStart.UTC(), WindowEnd: input.WindowEnd.UTC(),
	})
	if err != nil {
		return fleet.GraphFact{}, fmt.Errorf("encode Elasticsearch log-cause identity: %w", err)
	}
	digest := sha256.Sum256(identity)
	nativeID := "sha256:" + hex.EncodeToString(digest[:])
	resourceName := "log-" + hex.EncodeToString(digest[:16])
	entity := fleet.EntityRef{Cluster: input.Scope, Namespace: input.Namespace, Pod: input.Pod}
	fact := fleet.GraphFact{
		Fact: fleet.Fact{
			Evidence: fleet.Evidence{
				Ref: fleet.ResourceRef{
					SourceKind: Kind,
					Scope:      input.Scope,
					Kind:       "LogSignal",
					Namespace:  input.Namespace,
					Name:       resourceName,
				},
				Kind:       fleet.FactDerived,
				Observed:   encoded,
				ObservedAt: input.ObservedAt.UTC(),
				Source:     input.Scope,
				Provenance: fleet.Provenance{Adapter: Kind, ProtocolV: ProtocolVersion, NativeID: nativeID},
			},
			Workspace: input.Workspace,
		},
		Lens:   fleet.LensTelemetry,
		Entity: &entity,
	}
	if err := fact.Validate(input.Workspace); err != nil {
		return fleet.GraphFact{}, fmt.Errorf("validate Elasticsearch log-cause fact: %w", err)
	}
	return fact, nil
}

func validateText(label, value string, maximum int, optional bool) error {
	if value == "" {
		if optional {
			return nil
		}
		return fmt.Errorf("%s is invalid", label)
	}
	if len(value) > maximum || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
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
