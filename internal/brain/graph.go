// SPDX-License-Identifier: Apache-2.0

package brain

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/ArdurAI/sith/internal/fleet"
)

const (
	argoGraphSource          = "argocd"
	argoGraphProtocolVersion = "1.0.0"
	maxArgoChangePayload     = 16 << 10

	githubGraphSource                  = "github"
	githubWorkflowGraphProtocolVersion = "workflow-runs/2026-03-10"
	maxGitHubWorkflowChangePayload     = 4 << 10

	elasticsearchGraphSource          = "elasticsearch"
	elasticsearchGraphProtocolVersion = "search/ecs-v1"
	maxElasticsearchLogCausePayload   = 4 << 10
	maxElasticsearchCauseCount        = 128
	maxElasticsearchCauseWindow       = 15 * time.Minute
	maxElasticsearchClockSkew         = 5 * time.Minute

	maxGraphJSONDepth = 64
)

type argoChangePayload struct {
	ChangeKind       string    `json:"change_kind"`
	Revision         string    `json:"revision,omitempty"`
	Phase            string    `json:"phase,omitempty"`
	EventAt          time.Time `json:"event_at"`
	HistoryID        string    `json:"history_id,omitempty"`
	HistoryTruncated bool      `json:"history_truncated,omitempty"`
}

type githubWorkflowChangePayload struct {
	RunID      int64     `json:"run_id"`
	WorkflowID int64     `json:"workflow_id"`
	RunAttempt int64     `json:"run_attempt"`
	ChangeKind string    `json:"change_kind"`
	Conclusion string    `json:"conclusion"`
	EventAt    time.Time `json:"event_at"`
}

type elasticsearchLogCausePayload struct {
	Key          string    `json:"key"`
	Value        string    `json:"value"`
	Count        int       `json:"count"`
	FirstEventAt time.Time `json:"first_event_at"`
	LastEventAt  time.Time `json:"last_event_at"`
	Container    string    `json:"container,omitempty"`
}

// FromGraphFacts converts reviewed graph facts into normalized brain observations while preserving
// caller-declared coverage. It performs no discovery, I/O, coverage inference, or mutation.
func FromGraphFacts(
	workspace string,
	facts []fleet.GraphFact,
	coverage map[fleet.Lens]LensCoverage,
) (Investigation, error) {
	if strings.TrimSpace(workspace) == "" {
		return Investigation{}, fmt.Errorf("project graph facts: workspace is required")
	}
	graph, err := fleet.NewGraph(workspace, facts)
	if err != nil {
		return Investigation{}, fmt.Errorf("project graph facts: %w", err)
	}

	input := Investigation{
		Workspace: workspace,
		Coverage:  make(map[fleet.Lens]LensCoverage, len(coverage)),
	}
	for lens, state := range coverage {
		if !lens.Valid() {
			return Investigation{}, fmt.Errorf("project graph facts: coverage lens %q is invalid", lens)
		}
		input.Coverage[lens] = state
	}
	for _, node := range graph.Nodes {
		for _, fact := range node.Facts {
			observation, present, err := observationFromGraphFact(fact)
			if err != nil {
				return Investigation{}, err
			}
			if present {
				input.Observations = append(input.Observations, observation)
			}
		}
	}
	for _, fact := range graph.Unattached {
		observation, present, err := observationFromGraphFact(fact)
		if err != nil {
			return Investigation{}, err
		}
		if present {
			input.Observations = append(input.Observations, observation)
		}
	}
	return input, nil
}

func observationFromGraphFact(fact fleet.GraphFact) (Observation, bool, error) {
	provenance := fact.Fact.Provenance
	ref := fact.Fact.Ref
	if provenance.ProtocolV == elasticsearchGraphProtocolVersion {
		return observationFromElasticsearchLogCauseGraphFact(fact)
	}
	if provenance.ProtocolV == githubWorkflowGraphProtocolVersion {
		return observationFromGitHubWorkflowGraphFact(fact)
	}
	if (ref.SourceKind == elasticsearchGraphSource || provenance.Adapter == elasticsearchGraphSource) &&
		ref.Kind == "LogSignal" {
		return observationFromElasticsearchLogCauseGraphFact(fact)
	}
	if fact.Fact.Kind != fleet.FactChange || fact.Lens != fleet.LensTimeline {
		return Observation{}, false, nil
	}
	if ref.SourceKind == argoGraphSource || provenance.Adapter == argoGraphSource {
		return observationFromArgoGraphFact(fact)
	}
	if (ref.SourceKind == githubGraphSource || provenance.Adapter == githubGraphSource) &&
		ref.Kind == "WorkflowRun" {
		return observationFromGitHubWorkflowGraphFact(fact)
	}
	return Observation{}, false, nil
}

func observationFromArgoGraphFact(fact fleet.GraphFact) (Observation, bool, error) {
	provenance := fact.Fact.Provenance
	ref := fact.Fact.Ref
	if ref.SourceKind != argoGraphSource && provenance.Adapter != argoGraphSource {
		return Observation{}, false, nil
	}
	if ref.SourceKind != argoGraphSource || provenance.Adapter != argoGraphSource {
		return Observation{}, false, fmt.Errorf("project Argo change fact: source and provenance must both be %q", argoGraphSource)
	}
	if provenance.ProtocolV != argoGraphProtocolVersion {
		return Observation{}, false, fmt.Errorf("project Argo change fact: unsupported protocol version %q", provenance.ProtocolV)
	}
	if fact.Entity == nil {
		return Observation{}, false, fmt.Errorf("project Argo change fact: Application entity is required")
	}
	entity := *fact.Entity
	if ref.Kind != "Application" || ref.Namespace == "" ||
		entity.Cluster != ref.Scope || entity.Namespace != ref.Namespace ||
		entity.Kind != ref.Kind || entity.Name != ref.Name ||
		entity.Pod != "" || entity.Node != "" || entity.ImageDigest != "" {
		return Observation{}, false, fmt.Errorf("project Argo change fact: source and entity must identify the same Application")
	}
	if fact.Fact.Source != ref.Scope {
		return Observation{}, false, fmt.Errorf("project Argo change fact: evidence source must match the Application scope")
	}
	if len(ref.Attributes) != 0 || len(fact.Fact.Display) != 0 {
		return Observation{}, false, fmt.Errorf("project Argo change fact: unexpected attributes or display fields")
	}
	if len(fact.Fact.Observed) == 0 || len(fact.Fact.Observed) > maxArgoChangePayload {
		return Observation{}, false, fmt.Errorf("project Argo change fact: payload must be between 1 and %d bytes", maxArgoChangePayload)
	}

	payload, err := decodeArgoChangePayload(fact.Fact.Observed)
	if err != nil {
		return Observation{}, false, err
	}
	if payload.EventAt.IsZero() || !payload.EventAt.Equal(fact.Fact.ObservedAt) {
		return Observation{}, false, fmt.Errorf("project Argo change fact: payload event time must equal the fact observation time")
	}
	switch payload.ChangeKind {
	case "argocd-sync":
		switch payload.Phase {
		case "", "Succeeded", "Running", "Terminating":
			return Observation{}, false, nil
		default:
			return Observation{}, false, fmt.Errorf("project Argo change fact: phase %q is inconsistent with an Argo sync change", payload.Phase)
		}
	case "sync-failed":
		if payload.Phase != "Failed" && payload.Phase != "Error" {
			return Observation{}, false, fmt.Errorf("project Argo change fact: phase %q does not prove a failed sync operation", payload.Phase)
		}
		if payload.HistoryID != "" || payload.HistoryTruncated {
			return Observation{}, false, fmt.Errorf("project Argo change fact: failed operation must not carry history metadata")
		}
		nativePrefix := ref.Scope + "/" + ref.Namespace + "/" + ref.Name + "#operation/"
		operationID, found := strings.CutPrefix(provenance.NativeID, nativePrefix)
		if !found || strings.TrimSpace(operationID) == "" {
			return Observation{}, false, fmt.Errorf("project Argo change fact: operation provenance is inconsistent with the Application")
		}
	default:
		return Observation{}, false, fmt.Errorf("project Argo change fact: unsupported change kind %q", payload.ChangeKind)
	}

	return Observation{
		Ref:        ref,
		Lens:       fleet.LensTimeline,
		Key:        "change.kind",
		Value:      "sync-failed",
		ObservedAt: fact.Fact.ObservedAt,
		Source:     fact.Fact.Source,
		Stale:      fact.Fact.Stale,
	}, true, nil
}

func observationFromElasticsearchLogCauseGraphFact(fact fleet.GraphFact) (Observation, bool, error) {
	provenance := fact.Fact.Provenance
	ref := fact.Fact.Ref
	if fact.Fact.Kind != fleet.FactDerived || fact.Lens != fleet.LensTelemetry {
		return Observation{}, false, fmt.Errorf("project Elasticsearch log-cause fact: fact must be derived TELEMETRY")
	}
	if ref.SourceKind != elasticsearchGraphSource || provenance.Adapter != elasticsearchGraphSource {
		return Observation{}, false, fmt.Errorf(
			"project Elasticsearch log-cause fact: source and provenance must both be %q",
			elasticsearchGraphSource,
		)
	}
	if provenance.ProtocolV != elasticsearchGraphProtocolVersion {
		return Observation{}, false, fmt.Errorf(
			"project Elasticsearch log-cause fact: unsupported protocol version %q",
			provenance.ProtocolV,
		)
	}
	if provenance.DeepLink != "" || provenance.Collector != "" {
		return Observation{}, false, fmt.Errorf(
			"project Elasticsearch log-cause fact: unexpected provenance metadata",
		)
	}
	if ref.Kind != "LogSignal" || ref.Scope == "" || ref.Namespace == "" {
		return Observation{}, false, fmt.Errorf("project Elasticsearch log-cause fact: resource identity is invalid")
	}
	if fact.Entity == nil {
		return Observation{}, false, fmt.Errorf("project Elasticsearch log-cause fact: Pod entity is required")
	}
	entity := *fact.Entity
	if entity.Cluster != ref.Scope || entity.Namespace != ref.Namespace || entity.Pod == "" ||
		entity.Kind != "" || entity.Name != "" || entity.Node != "" || entity.ImageDigest != "" {
		return Observation{}, false, fmt.Errorf(
			"project Elasticsearch log-cause fact: source and entity must identify one exact Pod",
		)
	}
	if fact.Fact.Source != ref.Scope {
		return Observation{}, false, fmt.Errorf(
			"project Elasticsearch log-cause fact: evidence source must match the Elasticsearch scope",
		)
	}
	if len(ref.Attributes) != 0 || len(fact.Fact.Display) != 0 {
		return Observation{}, false, fmt.Errorf(
			"project Elasticsearch log-cause fact: unexpected attributes or display fields",
		)
	}
	if len(fact.Fact.Observed) == 0 || len(fact.Fact.Observed) > maxElasticsearchLogCausePayload {
		return Observation{}, false, fmt.Errorf(
			"project Elasticsearch log-cause fact: payload must be between 1 and %d bytes",
			maxElasticsearchLogCausePayload,
		)
	}

	payload, err := decodeElasticsearchLogCausePayload(fact.Fact.Observed)
	if err != nil {
		return Observation{}, false, err
	}
	if payload.Key != "logs.cause" {
		return Observation{}, false, fmt.Errorf(
			"project Elasticsearch log-cause fact: unsupported key %q",
			payload.Key,
		)
	}
	switch payload.Value {
	case "panic", "missing-config", "dependency-failure":
	default:
		return Observation{}, false, fmt.Errorf(
			"project Elasticsearch log-cause fact: unsupported cause %q",
			payload.Value,
		)
	}
	if payload.Count <= 0 || payload.Count > maxElasticsearchCauseCount {
		return Observation{}, false, fmt.Errorf(
			"project Elasticsearch log-cause fact: count must be between 1 and %d",
			maxElasticsearchCauseCount,
		)
	}
	if payload.FirstEventAt.IsZero() || payload.LastEventAt.IsZero() ||
		payload.LastEventAt.Before(payload.FirstEventAt) ||
		payload.LastEventAt.Sub(payload.FirstEventAt) > maxElasticsearchCauseWindow {
		return Observation{}, false, fmt.Errorf(
			"project Elasticsearch log-cause fact: event interval is invalid",
		)
	}
	if payload.LastEventAt.After(fact.Fact.ObservedAt.Add(maxElasticsearchClockSkew)) {
		return Observation{}, false, fmt.Errorf(
			"project Elasticsearch log-cause fact: last event exceeds collection clock skew",
		)
	}
	if payload.Container != "" && !validElasticsearchGraphText(payload.Container) {
		return Observation{}, false, fmt.Errorf(
			"project Elasticsearch log-cause fact: container identity is invalid",
		)
	}
	digest, valid := validElasticsearchNativeID(provenance.NativeID)
	if !valid {
		return Observation{}, false, fmt.Errorf(
			"project Elasticsearch log-cause fact: native identity is inconsistent with the log signal",
		)
	}
	wantDigest, err := elasticsearchLogCauseDigest(fact, payload)
	if err != nil || digest != wantDigest || ref.Name != "log-"+wantDigest[:32] {
		return Observation{}, false, fmt.Errorf(
			"project Elasticsearch log-cause fact: native identity is inconsistent with the log signal",
		)
	}

	return Observation{
		Ref: fleet.ResourceRef{
			SourceKind: elasticsearchGraphSource,
			Scope:      entity.Cluster,
			Kind:       "Pod",
			Namespace:  entity.Namespace,
			Name:       entity.Pod,
		},
		Lens:       fleet.LensTelemetry,
		Key:        "logs.cause",
		Value:      payload.Value,
		ObservedAt: payload.LastEventAt,
		Source:     fact.Fact.Source,
		Stale:      fact.Fact.Stale,
	}, true, nil
}

func observationFromGitHubWorkflowGraphFact(fact fleet.GraphFact) (Observation, bool, error) {
	provenance := fact.Fact.Provenance
	ref := fact.Fact.Ref
	if fact.Fact.Kind != fleet.FactChange || fact.Lens != fleet.LensTimeline {
		return Observation{}, false, fmt.Errorf("project GitHub workflow-run fact: fact must be a TIMELINE change")
	}
	if ref.SourceKind != githubGraphSource || provenance.Adapter != githubGraphSource {
		return Observation{}, false, fmt.Errorf("project GitHub workflow-run fact: source and provenance must both be %q", githubGraphSource)
	}
	if provenance.ProtocolV != githubWorkflowGraphProtocolVersion {
		return Observation{}, false, fmt.Errorf("project GitHub workflow-run fact: unsupported protocol version %q", provenance.ProtocolV)
	}
	if fact.Entity != nil {
		return Observation{}, false, fmt.Errorf("project GitHub workflow-run fact: source-native fact must be unattached")
	}
	if ref.Kind != "WorkflowRun" || !validGitHubGraphHost(ref.Scope) ||
		!validGitHubGraphPathComponent(ref.Namespace) {
		return Observation{}, false, fmt.Errorf("project GitHub workflow-run fact: resource identity is invalid")
	}
	if fact.Fact.Source != ref.Scope {
		return Observation{}, false, fmt.Errorf("project GitHub workflow-run fact: evidence source must match the GitHub host")
	}
	if len(ref.Attributes) != 0 || len(fact.Fact.Display) != 0 {
		return Observation{}, false, fmt.Errorf("project GitHub workflow-run fact: unexpected attributes or display fields")
	}
	if len(fact.Fact.Observed) == 0 || len(fact.Fact.Observed) > maxGitHubWorkflowChangePayload {
		return Observation{}, false, fmt.Errorf(
			"project GitHub workflow-run fact: payload must be between 1 and %d bytes",
			maxGitHubWorkflowChangePayload,
		)
	}

	payload, err := decodeGitHubWorkflowChangePayload(fact.Fact.Observed)
	if err != nil {
		return Observation{}, false, err
	}
	if payload.RunID <= 0 || payload.WorkflowID <= 0 || payload.RunAttempt <= 0 {
		return Observation{}, false, fmt.Errorf("project GitHub workflow-run fact: run, workflow, and attempt IDs must be positive")
	}
	if payload.ChangeKind != "workflow-run-failed" {
		return Observation{}, false, fmt.Errorf("project GitHub workflow-run fact: unsupported change kind %q", payload.ChangeKind)
	}
	switch payload.Conclusion {
	case "failure", "timed_out", "startup_failure":
	default:
		return Observation{}, false, fmt.Errorf("project GitHub workflow-run fact: conclusion %q does not prove failure", payload.Conclusion)
	}
	if payload.EventAt.IsZero() || !payload.EventAt.Equal(fact.Fact.ObservedAt) {
		return Observation{}, false, fmt.Errorf("project GitHub workflow-run fact: payload event time must equal the fact observation time")
	}

	repository, runIdentity, found := strings.Cut(ref.Name, "#")
	if !found || strings.Contains(runIdentity, "#") || !validGitHubGraphPathComponent(repository) ||
		strings.HasSuffix(strings.ToLower(repository), ".git") {
		return Observation{}, false, fmt.Errorf("project GitHub workflow-run fact: resource name is invalid")
	}
	wantRunIdentity := fmt.Sprintf("%d-attempt-%d", payload.RunID, payload.RunAttempt)
	if runIdentity != wantRunIdentity || provenance.NativeID != ref.Namespace+"/"+ref.Name {
		return Observation{}, false, fmt.Errorf("project GitHub workflow-run fact: native identity is inconsistent with the workflow run")
	}

	return Observation{
		Ref:        ref,
		Lens:       fleet.LensTimeline,
		Key:        "change.kind",
		Value:      "workflow-run-failed",
		ObservedAt: fact.Fact.ObservedAt,
		Source:     fact.Fact.Source,
		Stale:      fact.Fact.Stale,
	}, true, nil
}

func decodeArgoChangePayload(raw json.RawMessage) (argoChangePayload, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var payload argoChangePayload
	if err := decoder.Decode(&payload); err != nil {
		return argoChangePayload{}, fmt.Errorf("project Argo change fact: decode payload: %w", err)
	}
	var trailer json.RawMessage
	if err := decoder.Decode(&trailer); err != io.EOF {
		if err == nil {
			return argoChangePayload{}, fmt.Errorf("project Argo change fact: payload must contain one JSON value")
		}
		return argoChangePayload{}, fmt.Errorf("project Argo change fact: decode payload trailer: %w", err)
	}
	return payload, nil
}

func decodeGitHubWorkflowChangePayload(raw json.RawMessage) (githubWorkflowChangePayload, error) {
	if err := rejectDuplicateGraphJSON(raw); err != nil {
		return githubWorkflowChangePayload{}, fmt.Errorf("project GitHub workflow-run fact: decode payload: %w", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return githubWorkflowChangePayload{}, fmt.Errorf("project GitHub workflow-run fact: decode payload fields: %w", err)
	}
	for field := range fields {
		switch field {
		case "run_id", "workflow_id", "run_attempt", "change_kind", "conclusion", "event_at":
		default:
			return githubWorkflowChangePayload{}, fmt.Errorf("project GitHub workflow-run fact: payload field %q is unsupported", field)
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var payload githubWorkflowChangePayload
	if err := decoder.Decode(&payload); err != nil {
		return githubWorkflowChangePayload{}, fmt.Errorf("project GitHub workflow-run fact: decode payload: %w", err)
	}
	var trailer json.RawMessage
	if err := decoder.Decode(&trailer); err != io.EOF {
		if err == nil {
			return githubWorkflowChangePayload{}, fmt.Errorf("project GitHub workflow-run fact: payload must contain one JSON value")
		}
		return githubWorkflowChangePayload{}, fmt.Errorf("project GitHub workflow-run fact: decode payload trailer: %w", err)
	}
	return payload, nil
}

func decodeElasticsearchLogCausePayload(raw json.RawMessage) (elasticsearchLogCausePayload, error) {
	if !utf8.Valid(raw) {
		return elasticsearchLogCausePayload{}, fmt.Errorf(
			"project Elasticsearch log-cause fact: decode payload: invalid UTF-8",
		)
	}
	if err := rejectDuplicateGraphJSON(raw); err != nil {
		return elasticsearchLogCausePayload{}, fmt.Errorf(
			"project Elasticsearch log-cause fact: decode payload: %w",
			err,
		)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return elasticsearchLogCausePayload{}, fmt.Errorf(
			"project Elasticsearch log-cause fact: decode payload fields: %w",
			err,
		)
	}
	for field := range fields {
		switch field {
		case "key", "value", "count", "first_event_at", "last_event_at", "container":
		default:
			return elasticsearchLogCausePayload{}, fmt.Errorf(
				"project Elasticsearch log-cause fact: payload field %q is unsupported",
				field,
			)
		}
	}
	if container, present := fields["container"]; present && bytes.Equal(bytes.TrimSpace(container), []byte("null")) {
		return elasticsearchLogCausePayload{}, fmt.Errorf(
			"project Elasticsearch log-cause fact: container must be a non-null string",
		)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var payload elasticsearchLogCausePayload
	if err := decoder.Decode(&payload); err != nil {
		return elasticsearchLogCausePayload{}, fmt.Errorf(
			"project Elasticsearch log-cause fact: decode payload: %w",
			err,
		)
	}
	var trailer json.RawMessage
	if err := decoder.Decode(&trailer); err != io.EOF {
		if err == nil {
			return elasticsearchLogCausePayload{}, fmt.Errorf(
				"project Elasticsearch log-cause fact: payload must contain one JSON value",
			)
		}
		return elasticsearchLogCausePayload{}, fmt.Errorf(
			"project Elasticsearch log-cause fact: decode payload trailer: %w",
			err,
		)
	}
	if _, present := fields["container"]; present && payload.Container == "" {
		return elasticsearchLogCausePayload{}, fmt.Errorf(
			"project Elasticsearch log-cause fact: container must be a non-empty string when present",
		)
	}
	return payload, nil
}

func validElasticsearchNativeID(nativeID string) (string, bool) {
	digest, found := strings.CutPrefix(nativeID, "sha256:")
	if !found || len(digest) != 64 {
		return "", false
	}
	for _, character := range digest {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return "", false
		}
	}
	return digest, true
}

func elasticsearchLogCauseDigest(fact fleet.GraphFact, payload elasticsearchLogCausePayload) (string, error) {
	identity, err := json.Marshal(struct {
		Workspace    string    `json:"workspace"`
		Scope        string    `json:"scope"`
		Namespace    string    `json:"namespace"`
		Pod          string    `json:"pod"`
		Container    string    `json:"container,omitempty"`
		Cause        string    `json:"cause"`
		Count        int       `json:"count"`
		FirstEventAt time.Time `json:"first_event_at"`
		LastEventAt  time.Time `json:"last_event_at"`
		ObservedAt   time.Time `json:"observed_at"`
	}{
		Workspace: fact.Fact.Workspace, Scope: fact.Fact.Ref.Scope, Namespace: fact.Fact.Ref.Namespace,
		Pod: fact.Entity.Pod, Container: payload.Container, Cause: payload.Value, Count: payload.Count,
		FirstEventAt: payload.FirstEventAt, LastEventAt: payload.LastEventAt,
		ObservedAt: fact.Fact.ObservedAt,
	})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(identity)
	return hex.EncodeToString(digest[:]), nil
}

func validElasticsearchGraphText(value string) bool {
	if value == "" || len(value) > 253 || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func rejectDuplicateGraphJSON(document []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.UseNumber()
	if err := consumeUniqueGraphJSON(decoder, 0); err != nil {
		return err
	}
	if token, err := decoder.Token(); err != io.EOF || token != nil {
		return fmt.Errorf("JSON contains trailing data")
	}
	return nil
}

func consumeUniqueGraphJSON(decoder *json.Decoder, depth int) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, isDelimiter := token.(json.Delim)
	if !isDelimiter {
		return nil
	}
	if depth >= maxGraphJSONDepth {
		return fmt.Errorf("JSON nesting exceeds %d levels", maxGraphJSONDepth)
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
			if err := consumeUniqueGraphJSON(decoder, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for decoder.More() {
			if err := consumeUniqueGraphJSON(decoder, depth+1); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("JSON contains an invalid delimiter")
	}
	closing, err := decoder.Token()
	if err != nil || (delimiter == '{' && closing != json.Delim('}')) ||
		(delimiter == '[' && closing != json.Delim(']')) {
		return fmt.Errorf("JSON contains an invalid closing delimiter")
	}
	return nil
}

func validGitHubGraphHost(value string) bool {
	if value == "" || len(value) > 253 || strings.TrimSpace(value) != value || strings.ToLower(value) != value ||
		strings.HasPrefix(value, ".") || strings.HasSuffix(value, ".") {
		return false
	}
	for _, label := range strings.Split(value, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, character := range label {
			if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
				return false
			}
		}
	}
	return true
}

func validGitHubGraphPathComponent(value string) bool {
	if value == "" || len(value) > 100 || strings.TrimSpace(value) != value || value == "." || value == ".." {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') && character != '-' && character != '_' && character != '.' {
			return false
		}
	}
	return true
}
