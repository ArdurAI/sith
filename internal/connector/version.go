// SPDX-License-Identifier: Apache-2.0

package connector

import (
	"errors"
	"fmt"
	"sort"
)

const maxWireVersionsPerOffer = 32

var (
	// ErrInvalidWireVersions reports an empty, malformed, or ambiguous version offer.
	ErrInvalidWireVersions = errors.New("invalid connector wire versions")
	// ErrWireMajorMismatch reports that two valid offers have no major version in common.
	ErrWireMajorMismatch = errors.New("connector wire major version mismatch")
	// ErrWireMinorUnsupported reports a common major without an explicitly shared minor.
	ErrWireMinorUnsupported = errors.New("connector wire minor version is not mutually supported")
)

// WireVersion identifies one framework transport contract. It is deliberately independent from
// an adapter's opaque evidence and behavior contract version.
type WireVersion struct {
	Major uint32 `json:"major"`
	Minor uint32 `json:"minor"`
}

// CurrentWireVersion returns the initial E12 framework wire version.
func CurrentWireVersion() WireVersion {
	return WireVersion{Major: 1, Minor: 0}
}

// String returns the deterministic human-readable major.minor form.
func (version WireVersion) String() string {
	return fmt.Sprintf("%d.%d", version.Major, version.Minor)
}

// NegotiateWireVersion returns the highest exact version explicitly offered by both endpoints.
// Same-major versions are not assumed compatible unless the negotiated minor appears in both
// offers. This keeps protobuf wire compatibility separate from application-level compatibility.
func NegotiateWireVersion(local, remote []WireVersion) (WireVersion, error) {
	local, err := canonicalWireVersions(local)
	if err != nil {
		return WireVersion{}, fmt.Errorf("negotiate connector wire version: local offer: %w", err)
	}
	remote, err = canonicalWireVersions(remote)
	if err != nil {
		return WireVersion{}, fmt.Errorf("negotiate connector wire version: remote offer: %w", err)
	}

	localVersions := make(map[WireVersion]struct{}, len(local))
	localMajors := make(map[uint32]struct{}, len(local))
	for _, version := range local {
		localVersions[version] = struct{}{}
		localMajors[version.Major] = struct{}{}
	}

	var selected WireVersion
	matched := false
	commonMajor := false
	for _, version := range remote {
		if _, ok := localMajors[version.Major]; ok {
			commonMajor = true
		}
		if _, ok := localVersions[version]; !ok {
			continue
		}
		if !matched || wireVersionLess(selected, version) {
			selected = version
			matched = true
		}
	}
	if matched {
		return selected, nil
	}
	if !commonMajor {
		return WireVersion{}, ErrWireMajorMismatch
	}
	return WireVersion{}, ErrWireMinorUnsupported
}

func canonicalWireVersions(versions []WireVersion) ([]WireVersion, error) {
	if len(versions) == 0 {
		return nil, fmt.Errorf("%w: offer must not be empty", ErrInvalidWireVersions)
	}
	if len(versions) > maxWireVersionsPerOffer {
		return nil, fmt.Errorf("%w: offer exceeds %d versions", ErrInvalidWireVersions, maxWireVersionsPerOffer)
	}

	canonical := append([]WireVersion(nil), versions...)
	seen := make(map[WireVersion]struct{}, len(canonical))
	for _, version := range canonical {
		if version.Major == 0 {
			return nil, fmt.Errorf("%w: major must be greater than zero", ErrInvalidWireVersions)
		}
		if _, duplicate := seen[version]; duplicate {
			return nil, fmt.Errorf("%w: duplicate version %s", ErrInvalidWireVersions, version)
		}
		seen[version] = struct{}{}
	}
	sort.Slice(canonical, func(left, right int) bool {
		return wireVersionLess(canonical[left], canonical[right])
	})
	return canonical, nil
}

func wireVersionLess(left, right WireVersion) bool {
	if left.Major != right.Major {
		return left.Major < right.Major
	}
	return left.Minor < right.Minor
}
