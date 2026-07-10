// SPDX-License-Identifier: Apache-2.0

package fleet

import (
	"context"
	"encoding/json"
	"testing"
)

var _ Source = StubSource{}
var _ Source = memorySource{}

type memorySource struct {
	result FleetResult
}

func (memorySource) Kind() string {
	return "memory"
}

func (source memorySource) Fleet(_ context.Context) (FleetResult, error) {
	return source.result, nil
}

func TestStubSourceKind(t *testing.T) {
	t.Parallel()

	if got := (StubSource{}).Kind(); got != "stub" {
		t.Fatalf("Kind() = %q, want stub", got)
	}
}

func TestStubSourceEmpty(t *testing.T) {
	t.Parallel()

	got, err := (StubSource{}).Fleet(context.Background())
	if err != nil {
		t.Fatalf("Fleet() error = %v", err)
	}
	if got.Clusters == nil || len(got.Clusters) != 0 {
		t.Fatalf("Clusters = %#v, want allocated empty slice", got.Clusters)
	}
	if got.Coverage.Requested != 0 || got.Coverage.Reachable != 0 || got.Coverage.Unreachable != nil {
		t.Fatalf("Coverage = %#v, want zero value", got.Coverage)
	}
}

func TestSourceInterfaceSatisfied(t *testing.T) {
	t.Parallel()

	sources := []Source{
		StubSource{},
		memorySource{result: FleetResult{Clusters: []Cluster{{Name: "lab"}}}},
	}

	for _, source := range sources {
		if _, err := source.Fleet(context.Background()); err != nil {
			t.Fatalf("%s Fleet() error = %v", source.Kind(), err)
		}
	}
}

func TestFleetResultJSONShape(t *testing.T) {
	t.Parallel()

	result := FleetResult{Clusters: []Cluster{}, Coverage: Coverage{}}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal fleet result: %v", err)
	}

	const want = `{"clusters":[],"coverage":{"requested":0,"reachable":0}}`
	if string(encoded) != want {
		t.Fatalf("JSON = %s, want %s", encoded, want)
	}
}
