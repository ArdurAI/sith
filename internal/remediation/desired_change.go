// SPDX-License-Identifier: Apache-2.0

package remediation

import (
	"fmt"
	"sort"
	"strings"

	"github.com/ArdurAI/sith/internal/fleet"
)

const (
	// DesiredChangeVersion is the first immutable snapshot-to-output binding contract.
	DesiredChangeVersion = "desired-change/v1"

	maxDesiredChangeEvidenceRefs = 32
)

// DesiredChange is immutable after construction. It binds exact proposed bytes to one exact
// GitSourceSnapshot, one reviewed transformer version, and cited evidence. Its fields remain
// private, and construction remains package-private until a concrete transformer policy is
// separately reviewed.
type DesiredChange struct {
	version            string
	snapshot           GitSourceSnapshot
	transformerVersion string
	desiredContent     string
	evidenceRefs       []fleet.ResourceRef
}

// Version reports the closed desired-change contract without exposing proposed bytes or source
// state.
func (change DesiredChange) Version() string { return change.version }

// newDesiredChange is deliberately package-private. Only a separately reviewed deterministic
// transformer or declarative renderer in this package may turn output bytes into a DesiredChange;
// request and runtime callers cannot relabel arbitrary bytes as trusted transformer output.
func newDesiredChange(
	snapshot GitSourceSnapshot,
	transformerVersion string,
	desiredContent string,
	evidenceRefs []fleet.ResourceRef,
) (DesiredChange, error) {
	change := DesiredChange{
		version:            DesiredChangeVersion,
		snapshot:           cloneGitSourceSnapshot(snapshot),
		transformerVersion: transformerVersion,
		desiredContent:     desiredContent,
		evidenceRefs:       cloneResourceRefs(evidenceRefs),
	}
	sort.Slice(change.evidenceRefs, func(left, right int) bool {
		return resourceRefLess(change.evidenceRefs[left], change.evidenceRefs[right])
	})
	if err := change.validate(); err != nil {
		return DesiredChange{}, fmt.Errorf("construct desired change: change is invalid")
	}
	return change, nil
}

func (change DesiredChange) validate() error {
	if change.version != DesiredChangeVersion || change.snapshot.validate() != nil ||
		!validTransformerVersion(change.transformerVersion) ||
		!validGitSourceContent(change.desiredContent) ||
		change.desiredContent == change.snapshot.currentContent || len(change.evidenceRefs) < 2 ||
		len(change.evidenceRefs) > maxDesiredChangeEvidenceRefs {
		return fmt.Errorf("desired change is invalid")
	}

	subjectAttached := false
	blobAttached := false
	for index, ref := range change.evidenceRefs {
		if validateStableRef(ref) != nil ||
			(index > 0 && !resourceRefLess(change.evidenceRefs[index-1], ref)) {
			return fmt.Errorf("desired change evidence is invalid")
		}
		subjectAttached = subjectAttached || sameResourceRef(ref, change.snapshot.subject)
		blobAttached = blobAttached || gitSourceBlobRefMatches(ref, change.snapshot)
	}
	if !subjectAttached || !blobAttached {
		return fmt.Errorf("desired change evidence is unattached")
	}
	return nil
}

func cloneGitSourceSnapshot(snapshot GitSourceSnapshot) GitSourceSnapshot {
	cloned := snapshot
	cloned.subject = cloneResourceRef(snapshot.subject)
	cloned.evidenceRefs = cloneResourceRefs(snapshot.evidenceRefs)
	return cloned
}

func validTransformerVersion(value string) bool {
	if validateSafeText(value, maxIdentityBytes, false) != nil || value != strings.ToLower(value) {
		return false
	}
	parts := strings.Split(value, "/")
	if len(parts) != 2 {
		return false
	}
	for _, part := range parts {
		if part == "" || !isLowerAlphaNumeric(part[0]) || !isLowerAlphaNumeric(part[len(part)-1]) {
			return false
		}
		for _, character := range part {
			if (character < 'a' || character > 'z') && (character < '0' || character > '9') &&
				character != '-' && character != '_' && character != '.' {
				return false
			}
		}
	}
	return true
}

func isLowerAlphaNumeric(value byte) bool {
	return (value >= 'a' && value <= 'z') || (value >= '0' && value <= '9')
}
