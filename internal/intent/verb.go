// SPDX-License-Identifier: Apache-2.0

// Package intent defines the fail-closed vocabulary for governed Sith actions.
package intent

import (
	"encoding/json"
	"fmt"
	"sort"
)

// Verb names one reviewed governed action. Unknown strings are never verbs.
type Verb string

// The initial action vocabulary is locked by ADR-0004. Adding a verb is an ADR-level change.
const (
	VerbArgoCDSync        Verb = "argocd.sync"
	VerbArgoCDRollback    Verb = "argocd.rollback"
	VerbRolloutPromote    Verb = "rollout.promote"
	VerbRolloutAbort      Verb = "rollout.abort"
	VerbDeploymentScale   Verb = "deployment.scale"
	VerbDeploymentRestart Verb = "deployment.restart"
	VerbGitOpsOpenPR      Verb = "gitops.open-pr"
)

// Class separates the proposal-only first write from live cluster mutations.
type Class string

const (
	// ClassProposal names an action whose output still requires a separate human merge gate.
	ClassProposal Class = "proposal"
	// ClassLiveMutation names a P3 action that can directly change managed state.
	ClassLiveMutation Class = "live-mutation"
)

// Definition is immutable classification metadata for one reviewed verb.
type Definition struct {
	Verb  Verb  `json:"verb"`
	Class Class `json:"class"`
}

type definitionWire struct {
	Verb  Verb  `json:"verb"`
	Class Class `json:"class"`
}

var definitions = map[Verb]Definition{
	VerbArgoCDSync:        {Verb: VerbArgoCDSync, Class: ClassLiveMutation},
	VerbArgoCDRollback:    {Verb: VerbArgoCDRollback, Class: ClassLiveMutation},
	VerbRolloutPromote:    {Verb: VerbRolloutPromote, Class: ClassLiveMutation},
	VerbRolloutAbort:      {Verb: VerbRolloutAbort, Class: ClassLiveMutation},
	VerbDeploymentScale:   {Verb: VerbDeploymentScale, Class: ClassLiveMutation},
	VerbDeploymentRestart: {Verb: VerbDeploymentRestart, Class: ClassLiveMutation},
	VerbGitOpsOpenPR:      {Verb: VerbGitOpsOpenPR, Class: ClassProposal},
}

// ParseVerb accepts only an exact member of the reviewed vocabulary.
func ParseVerb(value string) (Verb, error) {
	verb := Verb(value)
	if !verb.Valid() {
		return "", fmt.Errorf("unknown action verb")
	}
	return verb, nil
}

// Valid reports whether verb belongs to the reviewed vocabulary.
func (verb Verb) Valid() bool {
	_, exists := definitions[verb]
	return exists
}

// Definition returns the immutable classification for verb.
func (verb Verb) Definition() (Definition, bool) {
	definition, exists := definitions[verb]
	return definition, exists
}

// Valid reports whether definition exactly matches the canonical classification table.
func (definition Definition) Valid() bool {
	canonical, exists := definitions[definition.Verb]
	return exists && definition == canonical
}

// MarshalJSON prevents forged or inconsistent classification metadata from crossing a wire boundary.
func (definition Definition) MarshalJSON() ([]byte, error) {
	if !definition.Valid() {
		return nil, fmt.Errorf("encode action definition: classification mismatch")
	}
	return json.Marshal(definitionWire(definition))
}

// UnmarshalJSON replaces accepted input with the canonical definition and rejects mismatches.
func (definition *Definition) UnmarshalJSON(payload []byte) error {
	if definition == nil {
		return fmt.Errorf("decode action definition: destination is nil")
	}
	var decoded definitionWire
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return fmt.Errorf("decode action definition: %w", err)
	}
	canonical, exists := definitions[decoded.Verb]
	if !exists || decoded.Class != canonical.Class {
		return fmt.Errorf("decode action definition: classification mismatch")
	}
	*definition = canonical
	return nil
}

// Vocabulary returns deterministic copies of all reviewed definitions.
func Vocabulary() []Definition {
	result := make([]Definition, 0, len(definitions))
	for _, definition := range definitions {
		result = append(result, definition)
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].Verb < result[right].Verb
	})
	return result
}

// MarshalJSON prevents an invalid programmatic value from crossing a wire boundary.
func (verb Verb) MarshalJSON() ([]byte, error) {
	if !verb.Valid() {
		return nil, fmt.Errorf("encode action verb: unknown action verb")
	}
	return json.Marshal(string(verb))
}

// UnmarshalJSON rejects unknown wire strings at the type boundary.
func (verb *Verb) UnmarshalJSON(payload []byte) error {
	if verb == nil {
		return fmt.Errorf("decode action verb: destination is nil")
	}
	var value string
	if err := json.Unmarshal(payload, &value); err != nil {
		return fmt.Errorf("decode action verb: %w", err)
	}
	parsed, err := ParseVerb(value)
	if err != nil {
		return fmt.Errorf("decode action verb: %w", err)
	}
	*verb = parsed
	return nil
}
