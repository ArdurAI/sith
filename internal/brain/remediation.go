// SPDX-License-Identifier: Apache-2.0

package brain

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"slices"

	"github.com/ArdurAI/sith/internal/intent"
)

// ProvenanceRequirement names one authoritative fact that a later governed-plan renderer must
// resolve before it may construct handler-owned arguments. These values are requirement
// identifiers, not graph predicates or caller-provided data.
type ProvenanceRequirement string

// Reviewed provenance requirements for the first structured remediation candidates.
const (
	ProvenanceArgoApplicationTarget ProvenanceRequirement = "argocd.application.target"
	ProvenanceArgoRevision          ProvenanceRequirement = "argocd.revision"
	ProvenanceGitRepository         ProvenanceRequirement = "git.repository"
	ProvenanceGitBaseRef            ProvenanceRequirement = "git.base-ref"
	ProvenanceGitBaseCommit         ProvenanceRequirement = "git.base-commit"
	ProvenanceGitFilePath           ProvenanceRequirement = "git.file-path"
	ProvenanceGitObservedBlob       ProvenanceRequirement = "git.observed-blob"
	ProvenanceGitDesiredContent     ProvenanceRequirement = "git.desired-content"
)

// RemediationCandidate is an inert rule-owned requirement template. It is not a resolved target,
// handler argument document, policy proposal, approval, or execution capability.
type RemediationCandidate struct {
	Verb               intent.Verb             `json:"verb"`
	RequiredProvenance []ProvenanceRequirement `json:"required_provenance"`
}

type remediationCandidateWire struct {
	Verb               intent.Verb             `json:"verb"`
	RequiredProvenance []ProvenanceRequirement `json:"required_provenance"`
}

// Validate accepts only an exact reviewed verb-to-requirements mapping. Requirement order is part
// of the deterministic contract.
func (candidate RemediationCandidate) Validate() error {
	expected, reviewed := canonicalRemediationRequirementsFor(candidate.Verb)
	if !reviewed || !candidate.Verb.Valid() || !slices.Equal(candidate.RequiredProvenance, expected) {
		return fmt.Errorf("remediation candidate is not canonical")
	}
	return nil
}

// MarshalJSON prevents a mutated or forged candidate from crossing an output boundary.
func (candidate RemediationCandidate) MarshalJSON() ([]byte, error) {
	if err := candidate.Validate(); err != nil {
		return nil, fmt.Errorf("encode remediation candidate: %w", err)
	}
	return json.Marshal(remediationCandidateWire{
		Verb: candidate.Verb, RequiredProvenance: slices.Clone(candidate.RequiredProvenance),
	})
}

// UnmarshalJSON accepts only the exact closed candidate vocabulary and rejects shape drift.
func (candidate *RemediationCandidate) UnmarshalJSON(payload []byte) error {
	if candidate == nil {
		return fmt.Errorf("decode remediation candidate: destination is nil")
	}
	if err := rejectDuplicateRemediationCandidateMembers(payload); err != nil {
		return fmt.Errorf("decode remediation candidate: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var wire remediationCandidateWire
	if err := decoder.Decode(&wire); err != nil {
		return fmt.Errorf("decode remediation candidate: %w", err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("decode remediation candidate: expected one JSON value")
		}
		return fmt.Errorf("decode remediation candidate: %w", err)
	}
	decoded := RemediationCandidate{
		Verb: wire.Verb, RequiredProvenance: slices.Clone(wire.RequiredProvenance),
	}
	if err := decoded.Validate(); err != nil {
		return fmt.Errorf("decode remediation candidate: %w", err)
	}
	*candidate = decoded
	return nil
}

func rejectDuplicateRemediationCandidateMembers(payload []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	opening, err := decoder.Token()
	if err != nil {
		return err
	}
	if opening != json.Delim('{') {
		return nil
	}
	seen := make(map[string]struct{}, 2)
	for decoder.More() {
		member, err := decoder.Token()
		if err != nil {
			return err
		}
		name, ok := member.(string)
		if !ok {
			return fmt.Errorf("JSON object contains an invalid member")
		}
		if name != "verb" && name != "required_provenance" {
			return fmt.Errorf("JSON object contains an unknown member")
		}
		if _, duplicate := seen[name]; duplicate {
			return fmt.Errorf("JSON object contains a duplicate member")
		}
		seen[name] = struct{}{}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return err
		}
	}
	closing, err := decoder.Token()
	if err != nil {
		return err
	}
	if closing != json.Delim('}') {
		return fmt.Errorf("JSON object has an invalid closing delimiter")
	}
	return nil
}

func remediationCandidateFor(verb intent.Verb) *RemediationCandidate {
	requirements, reviewed := canonicalRemediationRequirementsFor(verb)
	if !reviewed {
		return nil
	}
	return &RemediationCandidate{Verb: verb, RequiredProvenance: slices.Clone(requirements)}
}

func cloneRemediationCandidate(candidate *RemediationCandidate) *RemediationCandidate {
	if candidate == nil {
		return nil
	}
	return &RemediationCandidate{
		Verb:               candidate.Verb,
		RequiredProvenance: slices.Clone(candidate.RequiredProvenance),
	}
}

func canonicalRemediationRequirementsFor(verb intent.Verb) ([]ProvenanceRequirement, bool) {
	switch verb {
	case intent.VerbArgoCDRollback:
		return []ProvenanceRequirement{
			ProvenanceArgoApplicationTarget,
			ProvenanceArgoRevision,
		}, true
	case intent.VerbGitOpsOpenPR:
		return []ProvenanceRequirement{
			ProvenanceGitRepository,
			ProvenanceGitBaseRef,
			ProvenanceGitBaseCommit,
			ProvenanceGitFilePath,
			ProvenanceGitObservedBlob,
			ProvenanceGitDesiredContent,
		}, true
	default:
		return nil, false
	}
}
