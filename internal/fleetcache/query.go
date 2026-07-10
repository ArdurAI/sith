// SPDX-License-Identifier: Apache-2.0

package fleetcache

import (
	"fmt"
	"path"
	"strconv"
	"strings"
)

// Query is a cache-only filter over normalized records.
type Query struct {
	Kind        string
	Name        string
	Namespace   string
	Scopes      []string
	Text        []string
	Status      string
	StatusNot   string
	Image       string
	Node        string
	Labels      map[string]string
	MinRestarts *int64
	Limit       int
}

// ParseSearch parses the composable cache-served search grammar.
func ParseSearch(expression string) (Query, error) {
	query := Query{Labels: map[string]string{}}
	for _, token := range strings.Fields(expression) {
		key, value, structured := strings.Cut(token, ":")
		if !structured {
			query.Text = append(query.Text, strings.ToLower(token))
			continue
		}
		if value == "" {
			return Query{}, fmt.Errorf("search token %q has an empty value", token)
		}
		switch strings.ToLower(key) {
		case "ctx", "cluster":
			query.Scopes = append(query.Scopes, value)
		case "ns", "namespace":
			query.Namespace = value
		case "kind":
			query.Kind = canonicalKind(value)
		case "status":
			if strings.HasPrefix(value, "!") {
				query.StatusNot = strings.TrimPrefix(value, "!")
			} else {
				query.Status = value
			}
		case "image":
			query.Image = value
		case "node":
			query.Node = value
		case "label":
			label, labelValue, ok := strings.Cut(value, "=")
			if !ok || label == "" || labelValue == "" {
				return Query{}, fmt.Errorf("label token %q must be label:key=value", token)
			}
			query.Labels[label] = labelValue
		case "restarts":
			minimum := strings.TrimPrefix(value, ">")
			parsed, err := strconv.ParseInt(minimum, 10, 64)
			if err != nil || parsed < 0 {
				return Query{}, fmt.Errorf("restarts token %q must be a non-negative integer comparison", token)
			}
			query.MinRestarts = &parsed
		default:
			return Query{}, fmt.Errorf("unsupported search token %q", key)
		}
	}
	return query, nil
}

// ParseCorrelation parses the initial deployment-health and image correlation forms.
func ParseCorrelation(expression string) (Query, error) {
	fields := strings.Fields(strings.TrimSpace(expression))
	if len(fields) == 0 {
		return Query{}, fmt.Errorf("correlation expression is empty")
	}
	if strings.Contains(fields[0], ":") {
		return ParseSearch(expression)
	}
	kind, name, ok := strings.Cut(fields[0], "/")
	if !ok || kind == "" || name == "" {
		return Query{}, fmt.Errorf("correlation target %q must be kind/name", fields[0])
	}
	query := Query{Kind: canonicalKind(kind), Name: name, Labels: map[string]string{}}
	for _, predicate := range fields[1:] {
		switch {
		case strings.HasPrefix(predicate, "status!="):
			query.StatusNot = strings.TrimPrefix(predicate, "status!=")
		case strings.HasPrefix(predicate, "status="):
			query.Status = strings.TrimPrefix(predicate, "status=")
		case strings.HasPrefix(predicate, "image:"):
			query.Image = strings.TrimPrefix(predicate, "image:")
		default:
			return Query{}, fmt.Errorf("unsupported correlation predicate %q", predicate)
		}
	}
	if query.Status == "" && query.StatusNot == "" && query.Image == "" {
		return Query{}, fmt.Errorf("correlation expression requires a status or image predicate")
	}
	return query, nil
}

func (query Query) matches(record Record) bool {
	if query.Kind != "" && canonicalKind(record.Kind) != canonicalKind(query.Kind) {
		return false
	}
	if query.Name != "" && record.Name != query.Name {
		return false
	}
	if query.Namespace != "" && record.Namespace != query.Namespace {
		return false
	}
	if len(query.Scopes) > 0 && !matchesAnyGlob(record.Cluster, query.Scopes) {
		return false
	}
	if query.Status != "" && !equalFold(record.Status, query.Status) {
		return false
	}
	if query.StatusNot != "" && equalFold(record.Status, query.StatusNot) {
		return false
	}
	if query.Image != "" && !matchesImages(record.Images, query.Image) {
		return false
	}
	if query.Node != "" && !matchesGlob(record.Node, query.Node) {
		return false
	}
	for key, value := range query.Labels {
		if record.Labels[key] != value {
			return false
		}
	}
	if query.MinRestarts != nil && record.Restarts <= *query.MinRestarts {
		return false
	}
	haystack := strings.ToLower(strings.Join([]string{
		record.Cluster,
		record.Namespace,
		record.Kind,
		record.Name,
		record.Status,
		record.Reason,
		strings.Join(record.Images, " "),
		labelsText(record.Labels),
	}, " "))
	for _, text := range query.Text {
		if !fuzzyContains(haystack, text) {
			return false
		}
	}
	return true
}

func matchesImages(images []string, pattern string) bool {
	for _, image := range images {
		if matchesGlob(image, pattern) || strings.Contains(strings.ToLower(image), strings.ToLower(strings.Trim(pattern, "*"))) {
			return true
		}
	}
	return false
}

func matchesAnyGlob(value string, patterns []string) bool {
	for _, pattern := range patterns {
		if matchesGlob(value, pattern) {
			return true
		}
	}
	return false
}

func matchesGlob(value, pattern string) bool {
	matched, err := path.Match(strings.ToLower(pattern), strings.ToLower(value))
	if err == nil && matched {
		return true
	}
	return equalFold(value, pattern)
}

func equalFold(left, right string) bool {
	return strings.EqualFold(strings.TrimSpace(left), strings.TrimSpace(right))
}

func fuzzyContains(haystack, needle string) bool {
	needle = strings.ToLower(strings.TrimSpace(needle))
	if needle == "" || strings.Contains(haystack, needle) {
		return true
	}
	needleRunes := []rune(needle)
	index := 0
	for _, character := range haystack {
		if index < len(needleRunes) && character == needleRunes[index] {
			index++
		}
	}
	return index == len(needleRunes)
}

func labelsText(labels map[string]string) string {
	parts := make([]string, 0, len(labels))
	for key, value := range labels {
		parts = append(parts, key+"="+value)
	}
	return strings.Join(parts, " ")
}
