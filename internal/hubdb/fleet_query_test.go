// SPDX-License-Identifier: Apache-2.0

package hubdb

import (
	"strings"
	"testing"

	"github.com/ArdurAI/sith/internal/fleet"
)

func TestNormalizeFleetQueryAllowsOnlyExactPodImageInventory(t *testing.T) {
	t.Parallel()

	digest := "sha256:" + strings.Repeat("a", 64)
	query, scopes, err := normalizeFleetQuery(fleet.Query{
		Kinds:  []fleet.FactKind{fleet.FactInventory},
		Scopes: []string{"spoke-b", "spoke-a", "spoke-a"},
		Selector: fleet.Selector{
			ResourceKind: "Pod",
			Image:        digest,
		},
	})
	if err != nil {
		t.Fatalf("normalize exact image query: %v", err)
	}
	if query.Selector.Image != digest || len(scopes) != 2 || scopes[0] != "spoke-a" || scopes[1] != "spoke-b" {
		t.Fatalf("normalized image query = %#v, scopes = %#v", query, scopes)
	}
}

func TestNormalizeFleetQueryAllowsOnlyExactImageCVEFacts(t *testing.T) {
	t.Parallel()

	digest := "sha256:" + strings.Repeat("a", 64)
	query, scopes, err := normalizeFleetQuery(fleet.Query{
		Kinds:  []fleet.FactKind{fleet.FactCVE},
		Scopes: []string{"spoke-b", "spoke-a", "spoke-a"},
		Selector: fleet.Selector{
			ResourceKind: "Image",
			Image:        digest,
		},
	})
	if err != nil {
		t.Fatalf("normalize exact CVE image query: %v", err)
	}
	if query.Selector.Image != digest || len(scopes) != 2 || scopes[0] != "spoke-a" || scopes[1] != "spoke-b" {
		t.Fatalf("normalized CVE image query = %#v, scopes = %#v", query, scopes)
	}
}

func TestNormalizeFleetQueryRejectsBroadOrUnsafeImageSelectors(t *testing.T) {
	t.Parallel()

	digest := "sha256:" + strings.Repeat("a", 64)
	for _, query := range []fleet.Query{
		{Kinds: []fleet.FactKind{fleet.FactInventory}, Selector: fleet.Selector{ResourceKind: "Pod", Image: "registry.example/payments:latest"}},
		{Kinds: []fleet.FactKind{fleet.FactHealth}, Selector: fleet.Selector{ResourceKind: "Pod", Image: digest}},
		{Kinds: []fleet.FactKind{fleet.FactInventory}, Selector: fleet.Selector{ResourceKind: "Deployment", Image: digest}},
		{Kinds: []fleet.FactKind{fleet.FactInventory}, Selector: fleet.Selector{ResourceKind: "Pod", Image: digest, NamePrefix: "payments"}},
		{Kinds: []fleet.FactKind{fleet.FactInventory}, Selector: fleet.Selector{ResourceKind: "Pod", Image: digest, Labels: map[string]string{"app": "payments"}}},
		{Kinds: []fleet.FactKind{fleet.FactInventory}, Selector: fleet.Selector{ResourceKind: "Pod", Image: digest, CVE: "CVE-2026-0001"}},
		{Kinds: []fleet.FactKind{fleet.FactCVE}, Selector: fleet.Selector{ResourceKind: "Pod", Image: digest}},
		{Kinds: []fleet.FactKind{fleet.FactCVE}, Selector: fleet.Selector{ResourceKind: "Image", Image: digest, Name: "payments"}},
		{Kinds: []fleet.FactKind{fleet.FactHealth}, Selector: fleet.Selector{ResourceKind: "Image", Image: digest}},
	} {
		if _, _, err := normalizeFleetQuery(query); err == nil {
			t.Fatalf("normalizeFleetQuery(%#v) unexpectedly succeeded", query)
		}
	}
}
