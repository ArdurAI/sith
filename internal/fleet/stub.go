// SPDX-License-Identifier: Apache-2.0

package fleet

import "context"

var _ Source = StubSource{}

// StubSource returns a well-formed empty fleet until Slice 1 adds kubeconfig discovery.
type StubSource struct{}

// Kind identifies this source as the Slice 0 stub.
func (StubSource) Kind() string {
	return "stub"
}

// Fleet returns an allocated empty cluster slice and zero coverage.
func (StubSource) Fleet(_ context.Context) (FleetResult, error) {
	return FleetResult{
		Clusters: []Cluster{},
		Coverage: Coverage{},
	}, nil
}
