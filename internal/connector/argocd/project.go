// SPDX-License-Identifier: Apache-2.0

// Package argocd normalizes read-only Argo CD evidence for Sith's operational graph.
package argocd

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/ArdurAI/sith/internal/fleet"
)

const (
	// Kind is the stable registry identifier for Argo CD read evidence.
	Kind = "argocd"
	// ProtocolVersion identifies the normalized fact contract emitted by this projector.
	ProtocolVersion = "1.0.0"

	maxApplicationSources = 16
	maxApplicationHistory = 32
	maxApplicationFacts   = 3 + maxApplicationHistory + 1
	maxFactPayloadBytes   = 16 << 10
	maxTextBytes          = 2 << 10
	maxRevisionBytes      = 512
)

// Projection supplies one already-authorized Application observation. ProjectApplication does
// not perform discovery, network access, credential loading, or mutation.
type Projection struct {
	Workspace   string
	Scope       string
	ObservedAt  time.Time
	Application unstructured.Unstructured
}

type desiredObservation struct {
	Project     string              `json:"project,omitempty"`
	Sources     []applicationSource `json:"sources,omitempty"`
	Destination applicationTarget   `json:"destination,omitempty"`
}

type applicationSource struct {
	Repository     string `json:"repository"`
	Path           string `json:"path,omitempty"`
	Chart          string `json:"chart,omitempty"`
	TargetRevision string `json:"target_revision,omitempty"`
}

type applicationTarget struct {
	Name      string `json:"name,omitempty"`
	Server    string `json:"server,omitempty"`
	Namespace string `json:"namespace,omitempty"`
}

type healthObservation struct {
	Health string `json:"health"`
}

type driftObservation struct {
	SyncStatus string `json:"sync_status"`
	Revision   string `json:"revision,omitempty"`
	Drifted    *bool  `json:"drifted,omitempty"`
}

type changeObservation struct {
	ChangeKind       string    `json:"change_kind"`
	Revision         string    `json:"revision,omitempty"`
	Phase            string    `json:"phase,omitempty"`
	EventAt          time.Time `json:"event_at"`
	HistoryID        string    `json:"history_id,omitempty"`
	HistoryTruncated bool      `json:"history_truncated,omitempty"`
}

type projectedChange struct {
	observation changeObservation
	nativeID    string
}

// ProjectApplication returns deterministic, bounded, allowlisted graph facts for one Argo CD
// Application. Missing status fields produce no corresponding fact; the projector never invents
// healthy, synced, or change evidence.
func ProjectApplication(input Projection) ([]fleet.GraphFact, error) {
	application := input.Application
	if err := validateProjection(input); err != nil {
		return nil, err
	}
	observedAt := input.ObservedAt.UTC()
	entity := fleet.EntityRef{
		Cluster: input.Scope, Namespace: application.GetNamespace(), Kind: "Application", Name: application.GetName(),
	}
	baseNativeID := input.Scope + "/" + application.GetNamespace() + "/" + application.GetName()

	facts := make([]fleet.GraphFact, 0, 3+maxApplicationHistory+1)
	desired, present, err := projectDesired(application.Object)
	if err != nil {
		return nil, fmt.Errorf("project Argo CD Application desired state: %w", err)
	}
	if present {
		fact, err := graphFact(input, entity, fleet.FactDesired, fleet.LensDesired, observedAt, baseNativeID+"#desired", desired)
		if err != nil {
			return nil, err
		}
		facts = append(facts, fact)
	}

	status, err := optionalObjectMap(application.Object, "status")
	if err != nil {
		return nil, fmt.Errorf("project Argo CD Application status: %w", err)
	}
	if status == nil {
		return facts, nil
	}
	if health, present, err := projectHealth(status); err != nil {
		return nil, err
	} else if present {
		fact, factErr := graphFact(input, entity, fleet.FactHealth, fleet.LensLive, observedAt, baseNativeID+"#health", health)
		if factErr != nil {
			return nil, factErr
		}
		facts = append(facts, fact)
	}
	if drift, present, err := projectDrift(status); err != nil {
		return nil, err
	} else if present {
		fact, factErr := graphFact(input, entity, fleet.FactDrift, fleet.LensDesired, observedAt, baseNativeID+"#drift", drift)
		if factErr != nil {
			return nil, factErr
		}
		facts = append(facts, fact)
	}

	changes, err := projectChanges(status)
	if err != nil {
		return nil, err
	}
	for _, change := range changes {
		fact, factErr := graphFact(
			input, entity, fleet.FactChange, fleet.LensTimeline, change.observation.EventAt,
			baseNativeID+"#"+change.nativeID, change.observation,
		)
		if factErr != nil {
			return nil, factErr
		}
		facts = append(facts, fact)
	}
	if len(facts) > maxApplicationFacts {
		return nil, fmt.Errorf("argo CD application fact count exceeds %d", maxApplicationFacts)
	}
	return facts, nil
}

func validateProjection(input Projection) error {
	if err := validateText("workspace", input.Workspace, 253); err != nil {
		return err
	}
	if err := validateText("scope", input.Scope, 253); err != nil {
		return err
	}
	if input.ObservedAt.IsZero() {
		return fmt.Errorf("projection observation time is required")
	}
	application := input.Application
	groupVersion, err := schema.ParseGroupVersion(application.GetAPIVersion())
	if err != nil || groupVersion.Group != "argoproj.io" || groupVersion.Version == "" || application.GetKind() != "Application" {
		return fmt.Errorf("projection object must be an argoproj.io Application")
	}
	if err := validateText("Application name", application.GetName(), 253); err != nil {
		return err
	}
	if err := validateText("Application namespace", application.GetNamespace(), 253); err != nil {
		return err
	}
	return nil
}

func projectDesired(object map[string]any) (desiredObservation, bool, error) {
	spec, err := optionalObjectMap(object, "spec")
	if err != nil {
		return desiredObservation{}, false, err
	}
	if spec == nil {
		return desiredObservation{}, false, nil
	}
	project, err := optionalText(spec, "project", 253)
	if err != nil {
		return desiredObservation{}, false, err
	}
	destination, err := projectDestination(spec)
	if err != nil {
		return desiredObservation{}, false, err
	}
	sources, err := projectSources(spec)
	if err != nil {
		return desiredObservation{}, false, err
	}
	present := project != "" || len(sources) != 0 || destination != (applicationTarget{})
	return desiredObservation{Project: project, Sources: sources, Destination: destination}, present, nil
}

func projectSources(spec map[string]any) ([]applicationSource, error) {
	single, singlePresent, err := optionalMap(spec, "source")
	if err != nil {
		return nil, err
	}
	multiple, multiplePresent, err := optionalSlice(spec, "sources")
	if err != nil {
		return nil, err
	}
	if singlePresent && multiplePresent && len(multiple) != 0 {
		return nil, fmt.Errorf("application source and sources are mutually exclusive")
	}
	if singlePresent {
		multiple = []any{single}
	}
	if len(multiple) > maxApplicationSources {
		return nil, fmt.Errorf("application source count exceeds %d", maxApplicationSources)
	}
	result := make([]applicationSource, 0, len(multiple))
	for index, raw := range multiple {
		source, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("application source %d must be an object", index)
		}
		repository, err := requiredSanitizedURL(source, "repoURL")
		if err != nil {
			return nil, fmt.Errorf("application source %d: %w", index, err)
		}
		path, err := optionalText(source, "path", maxTextBytes)
		if err != nil {
			return nil, fmt.Errorf("application source %d: %w", index, err)
		}
		chart, err := optionalText(source, "chart", maxTextBytes)
		if err != nil {
			return nil, fmt.Errorf("application source %d: %w", index, err)
		}
		revision, err := optionalText(source, "targetRevision", maxRevisionBytes)
		if err != nil {
			return nil, fmt.Errorf("application source %d: %w", index, err)
		}
		result = append(result, applicationSource{
			Repository: repository, Path: path, Chart: chart, TargetRevision: revision,
		})
	}
	return result, nil
}

func projectDestination(spec map[string]any) (applicationTarget, error) {
	destination, present, err := optionalMap(spec, "destination")
	if err != nil || !present {
		return applicationTarget{}, err
	}
	name, err := optionalText(destination, "name", 253)
	if err != nil {
		return applicationTarget{}, err
	}
	server, err := optionalText(destination, "server", maxTextBytes)
	if err != nil {
		return applicationTarget{}, err
	}
	if name != "" && server != "" {
		return applicationTarget{}, fmt.Errorf("application destination name and server are mutually exclusive")
	}
	if server != "" {
		server, err = sanitizeAbsoluteURL(server, map[string]bool{"http": true, "https": true})
		if err != nil {
			return applicationTarget{}, fmt.Errorf("application destination server: %w", err)
		}
	}
	namespace, err := optionalText(destination, "namespace", 253)
	if err != nil {
		return applicationTarget{}, err
	}
	return applicationTarget{Name: name, Server: server, Namespace: namespace}, nil
}

func projectHealth(status map[string]any) (healthObservation, bool, error) {
	health, present, err := optionalMap(status, "health")
	if err != nil || !present {
		return healthObservation{}, false, err
	}
	value, err := optionalText(health, "status", 64)
	if err != nil || value == "" {
		return healthObservation{}, false, err
	}
	switch value {
	case "Healthy", "Progressing", "Degraded", "Suspended", "Missing", "Unknown":
		return healthObservation{Health: value}, true, nil
	default:
		return healthObservation{}, false, fmt.Errorf("application health status %q is unsupported", value)
	}
}

func projectDrift(status map[string]any) (driftObservation, bool, error) {
	syncStatus, present, err := optionalMap(status, "sync")
	if err != nil || !present {
		return driftObservation{}, false, err
	}
	value, err := optionalText(syncStatus, "status", 64)
	if err != nil || value == "" {
		return driftObservation{}, false, err
	}
	revision, err := optionalText(syncStatus, "revision", maxRevisionBytes)
	if err != nil {
		return driftObservation{}, false, err
	}
	result := driftObservation{SyncStatus: value, Revision: revision}
	switch value {
	case "Synced":
		value := false
		result.Drifted = &value
	case "OutOfSync":
		value := true
		result.Drifted = &value
	case "Unknown":
	default:
		return driftObservation{}, false, fmt.Errorf("application sync status %q is unsupported", value)
	}
	return result, true, nil
}

func projectChanges(status map[string]any) ([]projectedChange, error) {
	changes := make([]projectedChange, 0, maxApplicationHistory+1)
	history, present, err := optionalSlice(status, "history")
	if err != nil {
		return nil, fmt.Errorf("application history: %w", err)
	}
	truncated := false
	if present && len(history) > maxApplicationHistory {
		history = history[len(history)-maxApplicationHistory:]
		truncated = true
	}
	for index, raw := range history {
		entry, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("application history entry %d must be an object", index)
		}
		at, present, err := optionalTime(entry, "deployedAt")
		if err != nil {
			return nil, fmt.Errorf("application history entry %d: %w", index, err)
		}
		if !present {
			continue
		}
		revision, err := optionalText(entry, "revision", maxRevisionBytes)
		if err != nil {
			return nil, fmt.Errorf("application history entry %d: %w", index, err)
		}
		id, err := optionalScalarID(entry, "id")
		if err != nil {
			return nil, fmt.Errorf("application history entry %d: %w", index, err)
		}
		changes = append(changes, projectedChange{
			observation: changeObservation{
				ChangeKind: "argocd-sync", Revision: revision, EventAt: at, HistoryID: id,
				HistoryTruncated: truncated && index == 0,
			},
			nativeID: "history/" + stableChangeID(id, revision, at),
		})
	}

	operation, operationPresent, err := optionalMap(status, "operationState")
	if err != nil {
		return nil, fmt.Errorf("application operation state: %w", err)
	}
	if operationPresent {
		change, present, err := projectOperation(operation)
		if err != nil {
			return nil, err
		}
		if present {
			changes = append(changes, change)
		}
	}

	deduplicated := make(map[string]projectedChange, len(changes))
	for _, change := range changes {
		key := change.observation.ChangeKind + "\x00" + change.observation.Revision + "\x00" + change.observation.EventAt.Format(time.RFC3339Nano)
		if previous, exists := deduplicated[key]; exists {
			previous.observation.HistoryTruncated = previous.observation.HistoryTruncated || change.observation.HistoryTruncated
			if previous.observation.Phase == "" {
				previous.observation.Phase = change.observation.Phase
			}
			deduplicated[key] = previous
			continue
		}
		deduplicated[key] = change
	}
	result := make([]projectedChange, 0, len(deduplicated))
	for _, change := range deduplicated {
		result = append(result, change)
	}
	sort.Slice(result, func(left, right int) bool {
		leftAt, rightAt := result[left].observation.EventAt, result[right].observation.EventAt
		if !leftAt.Equal(rightAt) {
			return leftAt.Before(rightAt)
		}
		return result[left].nativeID < result[right].nativeID
	})
	return result, nil
}

func projectOperation(operation map[string]any) (projectedChange, bool, error) {
	phase, err := optionalText(operation, "phase", 64)
	if err != nil || phase == "" {
		return projectedChange{}, false, err
	}
	var changeKind string
	switch phase {
	case "Succeeded":
		changeKind = "argocd-sync"
	case "Failed", "Error":
		changeKind = "sync-failed"
	case "Running", "Terminating":
		changeKind = "argocd-sync"
	default:
		return projectedChange{}, false, fmt.Errorf("application operation phase %q is unsupported", phase)
	}
	at, present, err := optionalTime(operation, "finishedAt")
	if err != nil {
		return projectedChange{}, false, err
	}
	if !present {
		at, present, err = optionalTime(operation, "startedAt")
		if err != nil || !present {
			return projectedChange{}, false, err
		}
	}
	revision := ""
	if syncResult, ok, err := optionalMap(operation, "syncResult"); err != nil {
		return projectedChange{}, false, err
	} else if ok {
		revision, err = optionalText(syncResult, "revision", maxRevisionBytes)
		if err != nil {
			return projectedChange{}, false, err
		}
	}
	return projectedChange{
		observation: changeObservation{ChangeKind: changeKind, Revision: revision, Phase: phase, EventAt: at},
		nativeID:    "operation/" + stableChangeID(phase, revision, at),
	}, true, nil
}

func graphFact(
	input Projection,
	entity fleet.EntityRef,
	kind fleet.FactKind,
	lens fleet.Lens,
	observedAt time.Time,
	nativeID string,
	payload any,
) (fleet.GraphFact, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return fleet.GraphFact{}, fmt.Errorf("encode Argo CD Application %s fact: %w", kind, err)
	}
	if len(encoded) > maxFactPayloadBytes {
		return fleet.GraphFact{}, fmt.Errorf("argo CD application %s fact exceeds %d encoded bytes", kind, maxFactPayloadBytes)
	}
	fact := fleet.GraphFact{
		Fact: fleet.Fact{
			Evidence: fleet.Evidence{
				Ref: fleet.ResourceRef{
					SourceKind: Kind, Scope: input.Scope, Kind: "Application",
					Namespace: input.Application.GetNamespace(), Name: input.Application.GetName(),
				},
				Kind: kind, Observed: encoded, ObservedAt: observedAt.UTC(), Source: input.Scope,
				Provenance: fleet.Provenance{Adapter: Kind, ProtocolV: ProtocolVersion, NativeID: nativeID},
			},
			Workspace: input.Workspace,
		},
		Lens: lens, Entity: &entity,
	}
	if err := fact.Validate(input.Workspace); err != nil {
		return fleet.GraphFact{}, fmt.Errorf("validate Argo CD Application %s fact: %w", kind, err)
	}
	return fact, nil
}

func requiredSanitizedURL(object map[string]any, key string) (string, error) {
	value, err := optionalText(object, key, maxTextBytes)
	if err != nil {
		return "", err
	}
	if value == "" {
		return "", fmt.Errorf("%s is required", key)
	}
	result, err := sanitizeRepositoryURL(value)
	if err != nil {
		return "", fmt.Errorf("%s: %w", key, err)
	}
	return result, nil
}

func sanitizeRepositoryURL(value string) (string, error) {
	if err := validateText("URL", value, maxTextBytes); err != nil {
		return "", err
	}
	if strings.Contains(value, "://") {
		return sanitizeAbsoluteURL(value, map[string]bool{
			"git": true, "http": true, "https": true, "oci": true, "ssh": true,
		})
	}
	if strings.ContainsAny(value, "?#") {
		return "", fmt.Errorf("scheme-less URL must not contain query or fragment data")
	}
	if at := strings.LastIndex(value, "@"); at >= 0 {
		remainder := value[at+1:]
		if !strings.Contains(remainder, ":") {
			return "", fmt.Errorf("scheme-less URL user information requires host:path form")
		}
		value = remainder
	}
	host := value
	if boundary := strings.IndexAny(host, ":/"); boundary >= 0 {
		host = host[:boundary]
	}
	if host == "" || host == "." || host == ".." || strings.ContainsAny(value, "\\ ") {
		return "", fmt.Errorf("scheme-less URL must include a repository host")
	}
	return value, nil
}

func sanitizeAbsoluteURL(value string, allowedSchemes map[string]bool) (string, error) {
	if err := validateText("URL", value, maxTextBytes); err != nil {
		return "", err
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("url must include a scheme and host")
	}
	if !allowedSchemes[strings.ToLower(parsed.Scheme)] {
		return "", fmt.Errorf("url scheme %q is unsupported", parsed.Scheme)
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	return parsed.String(), nil
}

func optionalObjectMap(object map[string]any, key string) (map[string]any, error) {
	value, exists := object[key]
	if !exists || value == nil {
		return nil, nil
	}
	result, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s must be an object", key)
	}
	return result, nil
}

func optionalMap(object map[string]any, key string) (map[string]any, bool, error) {
	value, exists := object[key]
	if !exists || value == nil {
		return nil, false, nil
	}
	result, ok := value.(map[string]any)
	if !ok {
		return nil, false, fmt.Errorf("%s must be an object", key)
	}
	return result, true, nil
}

func optionalSlice(object map[string]any, key string) ([]any, bool, error) {
	value, exists := object[key]
	if !exists || value == nil {
		return nil, false, nil
	}
	result, ok := value.([]any)
	if !ok {
		return nil, false, fmt.Errorf("%s must be an array", key)
	}
	return result, true, nil
}

func optionalText(object map[string]any, key string, maximum int) (string, error) {
	value, exists := object[key]
	if !exists || value == nil {
		return "", nil
	}
	result, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string", key)
	}
	if result == "" {
		return "", nil
	}
	if err := validateText(key, result, maximum); err != nil {
		return "", err
	}
	return result, nil
}

func optionalTime(object map[string]any, key string) (time.Time, bool, error) {
	value, err := optionalText(object, key, 64)
	if err != nil || value == "" {
		return time.Time{}, false, err
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("%s must be an RFC3339 timestamp", key)
	}
	return parsed.UTC(), true, nil
}

func optionalScalarID(object map[string]any, key string) (string, error) {
	value, exists := object[key]
	if !exists || value == nil {
		return "", nil
	}
	var result string
	switch typed := value.(type) {
	case string:
		result = typed
	case int64:
		result = strconv.FormatInt(typed, 10)
	case int32:
		result = strconv.FormatInt(int64(typed), 10)
	case int:
		result = strconv.Itoa(typed)
	case float64:
		if typed != float64(int64(typed)) {
			return "", fmt.Errorf("%s must be an integer or string", key)
		}
		result = strconv.FormatInt(int64(typed), 10)
	case json.Number:
		if _, err := typed.Int64(); err != nil {
			return "", fmt.Errorf("%s must be an integer or string", key)
		}
		result = typed.String()
	default:
		return "", fmt.Errorf("%s must be an integer or string", key)
	}
	if result == "" {
		return "", nil
	}
	if err := validateText(key, result, 128); err != nil {
		return "", err
	}
	return result, nil
}

func validateText(label, value string, maximum int) error {
	if value == "" || strings.TrimSpace(value) != value || len(value) > maximum || strings.ContainsAny(value, "\x00\r\n") {
		return fmt.Errorf("%s is invalid", label)
	}
	return nil
}

func stableChangeID(parts ...any) string {
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		switch typed := part.(type) {
		case time.Time:
			values = append(values, typed.UTC().Format(time.RFC3339Nano))
		default:
			values = append(values, fmt.Sprint(part))
		}
	}
	return strings.Join(values, "/")
}
