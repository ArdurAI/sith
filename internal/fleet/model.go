// SPDX-License-Identifier: Apache-2.0

// Package fleet defines the source-abstract fleet snapshot consumed by Sith surfaces.
package fleet

import "time"

// FleetResult is the normalized snapshot returned by a Source.
//
//nolint:revive // FleetResult is the locked cross-slice contract name from issue #47.
type FleetResult struct {
	Clusters []Cluster `json:"clusters"`
	Coverage Coverage  `json:"coverage"`
}

// Cluster is one cluster or context in the fleet, stamped with source and freshness.
type Cluster struct {
	Name       string    `json:"name"`
	Context    string    `json:"context,omitempty"`
	SourceKind string    `json:"source_kind"`
	Reachable  bool      `json:"reachable"`
	ObservedAt time.Time `json:"observed_at,omitempty"`
}

// Coverage summarizes which requested scopes answered and which were unreachable.
type Coverage struct {
	Requested   int      `json:"requested"`
	Reachable   int      `json:"reachable"`
	Unreachable []string `json:"unreachable,omitempty"`
}
