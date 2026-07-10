// SPDX-License-Identifier: Apache-2.0

// Package fleetcache provides the interaction-safe local fleet store.
package fleetcache

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/ArdurAI/sith/internal/fleet"
)

// Record is a render-ready projection of one cached fleet fact.
type Record struct {
	Fact       fleet.Fact        `json:"fact"`
	Kind       string            `json:"kind"`
	Cluster    string            `json:"cluster"`
	Namespace  string            `json:"namespace,omitempty"`
	Name       string            `json:"name"`
	Ready      string            `json:"ready,omitempty"`
	Status     string            `json:"status,omitempty"`
	Reason     string            `json:"reason,omitempty"`
	Message    string            `json:"message,omitempty"`
	Node       string            `json:"node,omitempty"`
	Version    string            `json:"version,omitempty"`
	Restarts   int64             `json:"restarts,omitempty"`
	Images     []string          `json:"images,omitempty"`
	Labels     map[string]string `json:"labels,omitempty"`
	CreatedAt  time.Time         `json:"created_at,omitempty"`
	ObservedAt time.Time         `json:"observed_at"`
	Stale      bool              `json:"stale"`
	StaleFor   time.Duration     `json:"stale_for,omitempty"`
}

func normalize(fact fleet.Fact) (Record, error) {
	object := &unstructured.Unstructured{}
	if err := json.Unmarshal(fact.Observed, &object.Object); err != nil {
		return Record{}, fmt.Errorf("decode %s: %w", fact.Ref.String(), err)
	}
	record := Record{
		Fact:       cloneFact(fact),
		Kind:       canonicalKind(fact.Ref.Kind),
		Cluster:    fact.Ref.Scope,
		Namespace:  fact.Ref.Namespace,
		Name:       fact.Ref.Name,
		Labels:     object.GetLabels(),
		CreatedAt:  object.GetCreationTimestamp().Time,
		ObservedAt: fact.ObservedAt,
		Images:     objectImages(*object),
		Stale:      fact.Stale,
	}
	if record.Labels == nil {
		record.Labels = map[string]string{}
	}

	switch record.Kind {
	case "Pod":
		normalizePod(&record, *object)
	case "Deployment":
		normalizeDeployment(&record, *object)
	case "Event":
		normalizeEvent(&record, *object)
	case "Node":
		normalizeNode(&record, *object)
	default:
		record.Status, _, _ = unstructured.NestedString(object.Object, "status", "phase")
	}
	return record, nil
}

func normalizePod(record *Record, object unstructured.Unstructured) {
	record.Status, _, _ = unstructured.NestedString(object.Object, "status", "phase")
	record.Node, _, _ = unstructured.NestedString(object.Object, "spec", "nodeName")
	statuses, _, _ := unstructured.NestedSlice(object.Object, "status", "containerStatuses")
	ready := 0
	for _, value := range statuses {
		status, ok := value.(map[string]any)
		if !ok {
			continue
		}
		if isTrue(status["ready"]) {
			ready++
		}
		record.Restarts += number(status["restartCount"])
		if reason := nestedString(status, "state", "waiting", "reason"); reason != "" {
			record.Status = reason
		}
		if reason := nestedString(status, "state", "terminated", "reason"); reason != "" {
			record.Status = reason
		}
	}
	desired, _, _ := unstructured.NestedSlice(object.Object, "spec", "containers")
	record.Ready = fmt.Sprintf("%d/%d", ready, len(desired))
}

func normalizeDeployment(record *Record, object unstructured.Unstructured) {
	desired := nestedNumber(object.Object, "spec", "replicas")
	if desired == 0 {
		if _, found, _ := unstructured.NestedFieldNoCopy(object.Object, "spec", "replicas"); !found {
			desired = 1
		}
	}
	available := nestedNumber(object.Object, "status", "availableReplicas")
	updated := nestedNumber(object.Object, "status", "updatedReplicas")
	record.Ready = fmt.Sprintf("%d/%d", available, desired)
	switch {
	case available >= desired && updated >= desired:
		record.Status = "Healthy"
	case available > 0 || updated > 0:
		record.Status = "Progressing"
	default:
		record.Status = "Degraded"
	}
}

func normalizeEvent(record *Record, object unstructured.Unstructured) {
	eventType, _, _ := unstructured.NestedString(object.Object, "type")
	record.Reason, _, _ = unstructured.NestedString(object.Object, "reason")
	record.Message, _, _ = unstructured.NestedString(object.Object, "message")
	involvedKind, _, _ := unstructured.NestedString(object.Object, "involvedObject", "kind")
	involvedName, _, _ := unstructured.NestedString(object.Object, "involvedObject", "name")
	record.Status = eventType
	if involvedKind != "" || involvedName != "" {
		record.Ready = strings.Trim(involvedKind+"/"+involvedName, "/")
	}
}

func normalizeNode(record *Record, object unstructured.Unstructured) {
	record.Status = "Unknown"
	conditions, _, _ := unstructured.NestedSlice(object.Object, "status", "conditions")
	for _, value := range conditions {
		condition, ok := value.(map[string]any)
		if !ok || condition["type"] != "Ready" {
			continue
		}
		if condition["status"] == "True" {
			record.Status = "Ready"
		} else {
			record.Status = "NotReady"
		}
		record.Reason, _ = condition["reason"].(string)
		break
	}
	record.Version, _, _ = unstructured.NestedString(object.Object, "status", "nodeInfo", "kubeletVersion")
}

func objectImages(object unstructured.Unstructured) []string {
	paths := [][]string{
		{"spec", "containers"},
		{"spec", "initContainers"},
		{"spec", "template", "spec", "containers"},
		{"spec", "template", "spec", "initContainers"},
	}
	set := make(map[string]struct{})
	for _, path := range paths {
		containers, found, err := unstructured.NestedSlice(object.Object, path...)
		if err != nil || !found {
			continue
		}
		for _, raw := range containers {
			container, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if image, ok := container["image"].(string); ok && image != "" {
				set[image] = struct{}{}
			}
		}
	}
	result := make([]string, 0, len(set))
	for image := range set {
		result = append(result, image)
	}
	sort.Strings(result)
	return result
}

func nestedNumber(object map[string]any, fields ...string) int64 {
	value, found, _ := unstructured.NestedFieldNoCopy(object, fields...)
	if !found {
		return 0
	}
	return number(value)
}

func number(value any) int64 {
	switch typed := value.(type) {
	case int:
		return int64(typed)
	case int32:
		return int64(typed)
	case int64:
		return typed
	case float64:
		return int64(typed)
	case json.Number:
		parsed, _ := typed.Int64()
		return parsed
	case string:
		parsed, _ := strconv.ParseInt(typed, 10, 64)
		return parsed
	default:
		return 0
	}
}

func nestedString(object map[string]any, fields ...string) string {
	value := any(object)
	for _, field := range fields {
		mapping, ok := value.(map[string]any)
		if !ok {
			return ""
		}
		value = mapping[field]
	}
	result, _ := value.(string)
	return result
}

func isTrue(value any) bool {
	result, _ := value.(bool)
	return result
}

func canonicalKind(kind string) string {
	trimmed := strings.TrimSpace(kind)
	switch strings.ToLower(trimmed) {
	case "pod", "pods", "po":
		return "Pod"
	case "deployment", "deployments", "deploy":
		return "Deployment"
	case "event", "events", "ev":
		return "Event"
	case "node", "nodes", "no":
		return "Node"
	default:
		if trimmed == "" {
			return ""
		}
		return strings.ToUpper(trimmed[:1]) + trimmed[1:]
	}
}

func cloneFact(fact fleet.Fact) fleet.Fact {
	fact.Observed = append(json.RawMessage(nil), fact.Observed...)
	if fact.Ref.Attributes != nil {
		fact.Ref.Attributes = cloneMap(fact.Ref.Attributes)
	}
	return fact
}

func cloneMap(values map[string]string) map[string]string {
	result := make(map[string]string, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}
