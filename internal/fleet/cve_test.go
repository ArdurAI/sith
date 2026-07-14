// SPDX-License-Identifier: Apache-2.0

package fleet

import (
	"strings"
	"testing"
)

func TestCanonicalCVEObservationNormalizesBoundedInput(t *testing.T) {
	t.Parallel()

	digest := "sha256:" + strings.Repeat("a", 64)
	observation, err := CanonicalCVEObservation(digest, []string{"cve-2026-9999", "CVE-2020-0001"}, "HIGH")
	if err != nil || observation.Image != digest || observation.Severity != "high" ||
		!equalStrings(observation.IDs, []string{"CVE-2020-0001", "CVE-2026-9999"}) {
		t.Fatalf("CanonicalCVEObservation() = %#v, %v", observation, err)
	}
	if err := ValidateCVEObservation(observation); err != nil {
		t.Fatalf("ValidateCVEObservation() error = %v", err)
	}
}

func TestCanonicalCVEObservationRejectsAmbiguousInput(t *testing.T) {
	t.Parallel()

	digest := "sha256:" + strings.Repeat("a", 64)
	for _, input := range []struct {
		identifiers []string
		severity    string
	}{
		{identifiers: []string{"CVE-2026-1"}, severity: "HIGH"},
		{identifiers: []string{"CVE-2026-0001", "cve-2026-0001"}, severity: "HIGH"},
		{identifiers: []string{"CVE-2026-0001"}, severity: "urgent"},
	} {
		if _, err := CanonicalCVEObservation(digest, input.identifiers, input.severity); err == nil {
			t.Fatalf("CanonicalCVEObservation(%#v) unexpectedly succeeded", input)
		}
	}
}

func TestValidateCVEObservationRejectsNonCanonicalInput(t *testing.T) {
	t.Parallel()

	digest := "sha256:" + strings.Repeat("a", 64)
	for _, observation := range []CVEObservation{
		{Image: "registry.example/api:latest", IDs: []string{"CVE-2026-0001"}, Severity: "high"},
		{Image: digest, IDs: []string{"CVE-2026-0001", "CVE-2026-0001"}, Severity: "high"},
		{Image: digest, IDs: []string{"CVE-2026-0001"}, Severity: "HIGH"},
	} {
		if err := ValidateCVEObservation(observation); err == nil {
			t.Fatalf("ValidateCVEObservation(%#v) unexpectedly succeeded", observation)
		}
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
