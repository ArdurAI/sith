// SPDX-License-Identifier: Apache-2.0

package fleet

import (
	"fmt"
	"sort"
	"strings"
)

const maxCVEIdentifiers = 256

// NormalizeCVEIdentifier returns one canonical CVE identifier.
func NormalizeCVEIdentifier(value string) (string, error) {
	if strings.TrimSpace(value) != value {
		return "", fmt.Errorf("CVE identifier must not contain surrounding whitespace")
	}
	value = strings.ToUpper(value)
	if len(value) < len("CVE-0000-0000") || !strings.HasPrefix(value, "CVE-") || value[8] != '-' {
		return "", fmt.Errorf("CVE identifier has an invalid format")
	}
	for _, character := range value[4:8] {
		if character < '0' || character > '9' {
			return "", fmt.Errorf("CVE identifier has an invalid year")
		}
	}
	for _, character := range value[9:] {
		if character < '0' || character > '9' {
			return "", fmt.Errorf("CVE identifier has an invalid sequence")
		}
	}
	return value, nil
}

// NormalizeCVESeverity converts the fixed Kubernetes report severity vocabulary to Sith's
// lower-case response profile.
func NormalizeCVESeverity(value string) (string, error) {
	if strings.TrimSpace(value) != value {
		return "", fmt.Errorf("CVE severity must not contain surrounding whitespace")
	}
	switch strings.ToUpper(value) {
	case "CRITICAL":
		return "critical", nil
	case "HIGH":
		return "high", nil
	case "MEDIUM":
		return "medium", nil
	case "LOW":
		return "low", nil
	case "UNKNOWN":
		return "unknown", nil
	default:
		return "", fmt.Errorf("CVE severity is unsupported")
	}
}

// ValidateCVEObservation validates the privacy-preserving normalized vulnerability profile.
func ValidateCVEObservation(observation CVEObservation) error {
	if err := ValidateImageDigest(observation.Image); err != nil {
		return fmt.Errorf("CVE image digest: %w", err)
	}
	if len(observation.IDs) == 0 || len(observation.IDs) > maxCVEIdentifiers {
		return fmt.Errorf("CVE identifiers must contain between 1 and %d entries", maxCVEIdentifiers)
	}
	previous := ""
	for _, identifier := range observation.IDs {
		canonical, err := NormalizeCVEIdentifier(identifier)
		if err != nil || canonical != identifier {
			return fmt.Errorf("CVE identifier %q is not canonical", identifier)
		}
		if previous != "" && previous >= identifier {
			return fmt.Errorf("CVE identifiers must be unique and sorted")
		}
		previous = identifier
	}
	if _, err := NormalizeCVESeverity(observation.Severity); err != nil || observation.Severity != strings.ToLower(observation.Severity) {
		return fmt.Errorf("CVE severity is not canonical")
	}
	return nil
}

// CanonicalCVEObservation returns the bounded canonical image observation assembled from
// untrusted scanner-reported identifiers and severities.
func CanonicalCVEObservation(image string, identifiers []string, severity string) (CVEObservation, error) {
	if err := ValidateImageDigest(image); err != nil {
		return CVEObservation{}, fmt.Errorf("CVE image digest: %w", err)
	}
	if len(identifiers) == 0 || len(identifiers) > maxCVEIdentifiers {
		return CVEObservation{}, fmt.Errorf("CVE identifiers must contain between 1 and %d entries", maxCVEIdentifiers)
	}
	seen := make(map[string]struct{}, len(identifiers))
	canonical := make([]string, 0, len(identifiers))
	for _, identifier := range identifiers {
		value, err := NormalizeCVEIdentifier(identifier)
		if err != nil {
			return CVEObservation{}, err
		}
		if _, exists := seen[value]; exists {
			return CVEObservation{}, fmt.Errorf("CVE identifier %q is duplicated", value)
		}
		seen[value] = struct{}{}
		canonical = append(canonical, value)
	}
	canonicalSeverity, err := NormalizeCVESeverity(severity)
	if err != nil {
		return CVEObservation{}, err
	}
	sort.Strings(canonical)
	return CVEObservation{Image: image, IDs: canonical, Severity: canonicalSeverity}, nil
}
