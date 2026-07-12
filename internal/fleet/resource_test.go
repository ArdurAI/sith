// SPDX-License-Identifier: Apache-2.0

package fleet

import "testing"

func TestResourceRefEqualIgnoresAttributes(t *testing.T) {
	t.Parallel()

	left := ResourceRef{
		SourceKind: "local-kubeconfig",
		Scope:      "prod",
		Kind:       "Pod",
		Namespace:  "payments",
		Name:       "api-123",
		Attributes: map[string]string{"uid": "one"},
	}
	right := left
	right.Attributes = map[string]string{"uid": "two"}
	if !left.Equal(right) {
		t.Fatal("Equal() = false for identical source-abstract identity")
	}

	right.Name = "api-456"
	if left.Equal(right) {
		t.Fatal("Equal() = true for different resource names")
	}
}

func TestResourceRefString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ref  ResourceRef
		want string
	}{
		{
			name: "namespaced",
			ref:  ResourceRef{SourceKind: "local-kubeconfig", Scope: "prod", Kind: "Pod", Namespace: "payments", Name: "api"},
			want: "local-kubeconfig:prod/Pod/payments/api",
		},
		{
			name: "cluster scoped",
			ref:  ResourceRef{SourceKind: "local-kubeconfig", Scope: "prod", Kind: "Node", Name: "worker-1"},
			want: "local-kubeconfig:prod/Node/worker-1",
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := test.ref.String(); got != test.want {
				t.Fatalf("String() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestFactKindValid(t *testing.T) {
	t.Parallel()

	for _, kind := range []FactKind{FactInventory, FactHealth, FactAlert, FactDrift, FactCVE, FactCost} {
		if !kind.Valid() {
			t.Errorf("Valid() = false for %q", kind)
		}
	}
	if FactKind("unknown").Valid() {
		t.Fatal("Valid() = true for unknown fact kind")
	}
}

func TestQueryValidateHealthNegation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		query Query
		want  bool
	}{
		{name: "exact unhealthy health", query: Query{Kinds: []FactKind{FactHealth}, Selector: Selector{Name: "payments", HealthNot: "Healthy"}}, want: true},
		{name: "both health predicates", query: Query{Kinds: []FactKind{FactHealth}, Selector: Selector{Health: "Degraded", HealthNot: "Healthy"}}},
		{name: "unknown health negation", query: Query{Kinds: []FactKind{FactHealth}, Selector: Selector{HealthNot: "Broken"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.query.Validate()
			if (err == nil) != test.want {
				t.Fatalf("Validate() error = %v, want valid = %t", err, test.want)
			}
		})
	}
}

func TestQueryValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		query   Query
		wantErr bool
	}{
		{name: "valid", query: Query{Kinds: []FactKind{FactInventory}, Selector: Selector{Health: "Healthy"}, Limit: 10}},
		{name: "negative limit", query: Query{Limit: -1}, wantErr: true},
		{name: "unknown fact", query: Query{Kinds: []FactKind{"mystery"}}, wantErr: true},
		{name: "empty label", query: Query{Selector: Selector{Labels: map[string]string{"": "x"}}}, wantErr: true},
		{name: "unknown health", query: Query{Selector: Selector{Health: "Fine"}}, wantErr: true},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := test.query.Validate()
			if (err != nil) != test.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %t", err, test.wantErr)
			}
		})
	}
}

func TestCoverageComplete(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		coverage Coverage
		want     bool
	}{
		{name: "complete", coverage: Coverage{Requested: 2, Reachable: 2}, want: true},
		{name: "unreachable", coverage: Coverage{Requested: 2, Reachable: 1}},
		{name: "stale", coverage: Coverage{Requested: 2, Reachable: 2, Stale: []string{"prod"}}},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := test.coverage.Complete(); got != test.want {
				t.Fatalf("Complete() = %t, want %t", got, test.want)
			}
		})
	}
}
