// SPDX-License-Identifier: Apache-2.0

// Package fleet defines the source-abstract fleet snapshot consumed by Sith surfaces.
package fleet

import (
	"sort"
	"strings"
	"time"
)

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
	Stale       []string `json:"stale,omitempty"`
}

// CoverageGap explains why coverage is not safe to treat as complete.
type CoverageGap string

// Closed coverage gaps available to future abstention logic.
const (
	CoverageGapInconsistent CoverageGap = "inconsistent"
	CoverageGapUnreachable  CoverageGap = "unreachable"
	CoverageGapStale        CoverageGap = "stale"
	CoverageGapUnaccounted  CoverageGap = "unaccounted"
)

// CoverageAssessment is a defensive, deterministic explanation of coverage completeness.
// It contains no authorization decision; a future policy layer must still validate its target set.
type CoverageAssessment struct {
	Complete     bool          `json:"complete"`
	Gaps         []CoverageGap `json:"gaps,omitempty"`
	Unreachable  []string      `json:"unreachable,omitempty"`
	Stale        []string      `json:"stale,omitempty"`
	Unaccounted  int           `json:"unaccounted,omitempty"`
	Inconsistent bool          `json:"inconsistent"`
}

// Assessment derives a fail-closed completeness result from the existing coverage fields.
// The returned slices are sorted copies and never alias Coverage's input slices.
func (c Coverage) Assessment() CoverageAssessment {
	unreachable, unreachableValid := coverageScopes(c.Unreachable)
	stale, staleValid := coverageScopes(c.Stale)

	assessment := CoverageAssessment{
		Unreachable: unreachable,
		Stale:       stale,
	}
	if c.Requested < 0 || c.Reachable < 0 || c.Reachable > c.Requested || len(stale) > c.Requested ||
		!unreachableValid || !staleValid {
		assessment.Inconsistent = true
	} else {
		remaining := c.Requested - c.Reachable
		switch {
		case len(unreachable) > remaining:
			assessment.Inconsistent = true
		case len(unreachable) < remaining:
			assessment.Unaccounted = remaining - len(unreachable)
		}
	}

	if assessment.Inconsistent {
		assessment.Gaps = append(assessment.Gaps, CoverageGapInconsistent)
	}
	if len(assessment.Unreachable) != 0 {
		assessment.Gaps = append(assessment.Gaps, CoverageGapUnreachable)
	}
	if len(assessment.Stale) != 0 {
		assessment.Gaps = append(assessment.Gaps, CoverageGapStale)
	}
	if assessment.Unaccounted != 0 {
		assessment.Gaps = append(assessment.Gaps, CoverageGapUnaccounted)
	}
	assessment.Complete = len(assessment.Gaps) == 0
	return assessment
}

// Complete reports whether the coverage is internally consistent and every requested scope answered with fresh data.
func (c Coverage) Complete() bool {
	return c.Assessment().Complete
}

func coverageScopes(values []string) ([]string, bool) {
	if len(values) == 0 {
		return nil, true
	}

	cloned := append([]string(nil), values...)
	sort.Strings(cloned)
	valid := true
	for index, value := range cloned {
		if strings.TrimSpace(value) == "" || (index != 0 && value == cloned[index-1]) {
			valid = false
		}
	}
	return cloned, valid
}
