// SPDX-License-Identifier: Apache-2.0

package fleet

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// LocalWorkspace is the implicit single-user workspace used by local mode.
const LocalWorkspace = "local"

// ResourceRef is a source-abstract address for one fleet resource.
type ResourceRef struct {
	SourceKind string            `json:"source_kind"`
	Scope      string            `json:"scope"`
	Kind       string            `json:"kind"`
	Namespace  string            `json:"namespace,omitempty"`
	Name       string            `json:"name"`
	Attributes map[string]string `json:"attributes,omitempty"`
}

// Equal compares source-abstract identity while ignoring adapter-specific attributes.
func (r ResourceRef) Equal(other ResourceRef) bool {
	return r.SourceKind == other.SourceKind &&
		r.Scope == other.Scope &&
		r.Kind == other.Kind &&
		r.Namespace == other.Namespace &&
		r.Name == other.Name
}

// String returns a stable address suitable for logs and audit records.
func (r ResourceRef) String() string {
	parts := []string{r.SourceKind + ":" + r.Scope, r.Kind}
	if r.Namespace != "" {
		parts = append(parts, r.Namespace)
	}
	parts = append(parts, r.Name)
	return strings.Join(parts, "/")
}

// FactKind is the closed taxonomy of normalized fleet observations.
type FactKind string

// Lens identifies one orthogonal evidence dimension in the operational graph.
type Lens string

// Supported operational evidence lenses.
const (
	LensLive      Lens = "live"
	LensDesired   Lens = "desired"
	LensTimeline  Lens = "timeline"
	LensTelemetry Lens = "telemetry"
)

// Supported fact kinds.
const (
	FactInventory FactKind = "inventory"
	FactHealth    FactKind = "health"
	FactAlert     FactKind = "alert"
	FactDrift     FactKind = "drift"
	FactCVE       FactKind = "cve"
	FactCost      FactKind = "cost"
	FactDesired   FactKind = "desired"
	FactChange    FactKind = "change"
	FactDerived   FactKind = "derived"
)

// Valid reports whether the fact kind belongs to the closed taxonomy.
func (kind FactKind) Valid() bool {
	switch kind {
	case FactInventory, FactHealth, FactAlert, FactDrift, FactCVE, FactCost, FactDesired, FactChange, FactDerived:
		return true
	default:
		return false
	}
}

// Evidence is observed state plus source and collection provenance.
type Evidence struct {
	Ref        ResourceRef     `json:"ref"`
	Kind       FactKind        `json:"kind"`
	Observed   json.RawMessage `json:"observed"`
	Display    []DisplayField  `json:"display,omitempty"`
	ObservedAt time.Time       `json:"observed_at"`
	Source     string          `json:"source"`
	Provenance Provenance      `json:"provenance"`
}

// DisplayField is a source-provided, read-only tabular presentation hint.
type DisplayField struct {
	Name     string `json:"name"`
	Value    string `json:"value"`
	Priority int32  `json:"priority,omitempty"`
}

// Provenance identifies how to trace an observation back to its native source.
type Provenance struct {
	Adapter   string `json:"adapter"`
	ProtocolV string `json:"protocol_version"`
	NativeID  string `json:"native_id,omitempty"`
	DeepLink  string `json:"deep_link,omitempty"`
	Collector string `json:"collector,omitempty"`
}

// Fact is evidence stamped with workspace and derived freshness.
type Fact struct {
	Evidence
	Workspace string `json:"workspace"`
	Stale     bool   `json:"stale"`
	StaleFor  string `json:"stale_for,omitempty"`
}

// Query expresses a typed selection over normalized fleet facts.
type Query struct {
	Kinds    []FactKind `json:"kinds,omitempty"`
	Scopes   []string   `json:"scopes,omitempty"`
	Selector Selector   `json:"selector,omitempty"`
	Limit    int        `json:"limit,omitempty"`
}

// Validate rejects unknown or unsafe query values.
func (query Query) Validate() error {
	if query.Limit < 0 {
		return fmt.Errorf("query limit must not be negative")
	}
	for _, kind := range query.Kinds {
		if !kind.Valid() {
			return fmt.Errorf("invalid fact kind %q", kind)
		}
	}
	for key := range query.Selector.Labels {
		if strings.TrimSpace(key) == "" {
			return fmt.Errorf("label selector key must not be empty")
		}
	}
	if query.Selector.Health != "" && query.Selector.HealthNot != "" {
		return fmt.Errorf("health and health-not selectors are mutually exclusive")
	}
	for _, health := range []string{query.Selector.Health, query.Selector.HealthNot} {
		if health != "" && !validHealth(health) {
			return fmt.Errorf("invalid health selector %q", health)
		}
	}

	return nil
}

// Selector is the fail-safe, typed predicate set supported by fleet queries.
type Selector struct {
	ResourceKind string            `json:"resource_kind,omitempty"`
	Namespace    string            `json:"namespace,omitempty"`
	Name         string            `json:"name,omitempty"`
	NamePrefix   string            `json:"name_prefix,omitempty"`
	Labels       map[string]string `json:"labels,omitempty"`
	Health       string            `json:"health,omitempty"`
	HealthNot    string            `json:"health_not,omitempty"`
	Image        string            `json:"image,omitempty"`
	CVE          string            `json:"cve,omitempty"`
}

func validHealth(health string) bool {
	switch health {
	case "Healthy", "Degraded", "Progressing", "Unknown":
		return true
	default:
		return false
	}
}

// QueryResult contains normalized facts and honest scope coverage.
type QueryResult struct {
	Facts    []Fact   `json:"facts"`
	Coverage Coverage `json:"coverage"`
}

// CVEObservation is the normalized payload for one image vulnerability fact.
type CVEObservation struct {
	Image    string   `json:"image"`
	IDs      []string `json:"ids"`
	Severity string   `json:"severity,omitempty"`
}

// Diff is a structured desired-versus-observed result.
type Diff struct {
	Ref     ResourceRef `json:"ref"`
	Drifted bool        `json:"drifted"`
	Hunks   []DiffHunk  `json:"hunks,omitempty"`
}

// DiffHunk is one field-level desired-versus-observed change.
type DiffHunk struct {
	Path     string `json:"path"`
	Observed string `json:"observed"`
	Desired  string `json:"desired"`
}

// Graph is the source-abstract operational graph assembled from facts.
type Graph struct {
	Workspace  string      `json:"workspace"`
	Nodes      []Node      `json:"nodes"`
	Unattached []GraphFact `json:"unattached,omitempty"`
	Edges      []Edge      `json:"edges"`
}

// Node is one validated entity and its bounded, lens-stamped fact bundle.
type Node struct {
	Entity EntityRef   `json:"entity"`
	Facts  []GraphFact `json:"facts"`
}

// Relation is the closed taxonomy of cross-resource graph edges.
type Relation string

// Supported graph relations.
const (
	RelOwns         Relation = "owns"
	RelRoutesTo     Relation = "routes_to"
	RelBackedBy     Relation = "backed_by"
	RelDeployedFrom Relation = "deployed_from"
	RelRunsImage    Relation = "runs_image"
	RelAlertsOn     Relation = "alerts_on"
	RelCostsFor     Relation = "costs_for"
)

// Edge is one typed relationship between fleet resources.
type Edge struct {
	From EntityRef `json:"from"`
	To   EntityRef `json:"to"`
	Rel  Relation  `json:"rel"`
}
