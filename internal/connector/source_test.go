// SPDX-License-Identifier: Apache-2.0

package connector

import (
	"context"
	"testing"
	"time"
)

func TestAsSourcePreservesCoverageAndScopes(t *testing.T) {
	t.Parallel()

	observed := time.Date(2026, 7, 10, 19, 0, 0, 0, time.UTC)
	reader := newTestReader("memory")
	reader.discovery = Discovery{
		Scopes: []Scope{
			{Name: "prod", Reachable: true, ObservedAt: observed},
			{Name: "lab", Reachable: false},
		},
		Unreachable: []string{"lab"},
	}
	reader.query.Coverage.Requested = 2
	reader.query.Coverage.Reachable = 1
	reader.query.Coverage.Unreachable = []string{"lab", "lab"}

	source := AsSource(reader)
	result, err := source.Fleet(context.Background())
	if err != nil {
		t.Fatalf("Fleet() error = %v", err)
	}
	if source.Kind() != "memory" {
		t.Fatalf("Kind() = %q", source.Kind())
	}
	if len(result.Clusters) != 2 || result.Clusters[0].Name != "lab" || result.Clusters[1].Name != "prod" {
		t.Fatalf("Clusters = %#v", result.Clusters)
	}
	if result.Clusters[1].ObservedAt != observed || !result.Clusters[1].Reachable {
		t.Fatalf("prod cluster = %#v", result.Clusters[1])
	}
	if len(result.Coverage.Unreachable) != 1 || result.Coverage.Unreachable[0] != "lab" {
		t.Fatalf("Coverage = %#v", result.Coverage)
	}
}

func TestAsSourceFallsBackToDiscoveryCoverage(t *testing.T) {
	t.Parallel()

	reader := newTestReader("memory")
	reader.discovery = Discovery{
		Scopes:      []Scope{{Name: "prod", Reachable: true}},
		Unreachable: []string{"missing"},
	}

	result, err := AsSource(reader).Fleet(context.Background())
	if err != nil {
		t.Fatalf("Fleet() error = %v", err)
	}
	if result.Coverage.Requested != 2 || result.Coverage.Reachable != 1 {
		t.Fatalf("Coverage = %#v", result.Coverage)
	}
}
