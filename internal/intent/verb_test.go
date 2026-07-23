// SPDX-License-Identifier: Apache-2.0

package intent

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestVocabularyIsExactAndClassified(t *testing.T) {
	t.Parallel()

	want := []Definition{
		{Verb: VerbArgoCDRollback, Class: ClassLiveMutation},
		{Verb: VerbArgoCDSync, Class: ClassLiveMutation},
		{Verb: VerbDeploymentRestart, Class: ClassLiveMutation},
		{Verb: VerbDeploymentScale, Class: ClassLiveMutation},
		{Verb: VerbGitOpsOpenPR, Class: ClassProposal},
		{Verb: VerbRolloutAbort, Class: ClassLiveMutation},
		{Verb: VerbRolloutPromote, Class: ClassLiveMutation},
	}
	if got := Vocabulary(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Vocabulary() = %#v, want %#v", got, want)
	}

	got := Vocabulary()
	got[0] = Definition{Verb: "shell.exec", Class: ClassProposal}
	if second := Vocabulary(); !reflect.DeepEqual(second, want) {
		t.Fatalf("Vocabulary() exposed mutable state: %#v", second)
	}
}

func TestVerbParsingAndJSONFailClosed(t *testing.T) {
	t.Parallel()

	for _, definition := range Vocabulary() {
		parsed, err := ParseVerb(string(definition.Verb))
		if err != nil || parsed != definition.Verb {
			t.Fatalf("ParseVerb(%q) = %q, %v", definition.Verb, parsed, err)
		}
		payload, err := json.Marshal(definition.Verb)
		if err != nil {
			t.Fatalf("Marshal(%q) error = %v", definition.Verb, err)
		}
		var decoded Verb
		if err := json.Unmarshal(payload, &decoded); err != nil || decoded != definition.Verb {
			t.Fatalf("Unmarshal(%q) = %q, %v", payload, decoded, err)
		}
	}

	for _, value := range []string{"", " shell.exec", "shell.exec", "kubectl.apply", "secret.read", "rbac.mutate", "GITOPS.OPEN-PR"} {
		if parsed, err := ParseVerb(value); err == nil || parsed.Valid() {
			t.Fatalf("ParseVerb(%q) = %q, %v; want refusal", value, parsed, err)
		}
		if _, err := json.Marshal(Verb(value)); err == nil {
			t.Fatalf("Marshal(%q) accepted unknown verb", value)
		}
		payload, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("Marshal(%q) error = %v", value, err)
		}
		var decoded Verb
		if err := json.Unmarshal(payload, &decoded); err == nil {
			t.Fatalf("Unmarshal(%q) accepted unknown verb", payload)
		}
	}

	for _, payload := range [][]byte{[]byte("null"), []byte("1"), []byte("{}"), []byte(`"argocd.sync" "rollout.abort"`)} {
		var decoded Verb
		if err := json.Unmarshal(payload, &decoded); err == nil {
			t.Fatalf("Unmarshal(%q) accepted malformed verb", payload)
		}
	}
}

func TestDefinitionsAreInternallyConsistent(t *testing.T) {
	t.Parallel()

	for _, definition := range Vocabulary() {
		got, ok := definition.Verb.Definition()
		if !ok || got != definition {
			t.Fatalf("Definition(%q) = %#v/%t", definition.Verb, got, ok)
		}
		if definition.Verb == VerbGitOpsOpenPR {
			if definition.Class != ClassProposal {
				t.Fatalf("first write class = %q, want proposal", definition.Class)
			}
			continue
		}
		if definition.Class != ClassLiveMutation {
			t.Fatalf("live verb %q class = %q", definition.Verb, definition.Class)
		}
	}
}

func TestDefinitionJSONRejectsForgedClassification(t *testing.T) {
	t.Parallel()

	for _, definition := range Vocabulary() {
		payload, err := json.Marshal(definition)
		if err != nil {
			t.Fatalf("Marshal(%#v) error = %v", definition, err)
		}
		var decoded Definition
		if err := json.Unmarshal(payload, &decoded); err != nil || decoded != definition {
			t.Fatalf("Unmarshal(%q) = %#v, %v", payload, decoded, err)
		}
	}

	for _, definition := range []Definition{
		{Verb: VerbArgoCDSync, Class: ClassProposal},
		{Verb: VerbGitOpsOpenPR, Class: ClassLiveMutation},
		{Verb: "shell.exec", Class: ClassProposal},
		{},
	} {
		if definition.Valid() {
			t.Fatalf("inconsistent definition is valid: %#v", definition)
		}
		if _, err := json.Marshal(definition); err == nil {
			t.Fatalf("Marshal(%#v) accepted inconsistent definition", definition)
		}
	}

	for _, payload := range [][]byte{
		[]byte(`{"verb":"argocd.sync","class":"proposal"}`),
		[]byte(`{"verb":"gitops.open-pr","class":"live-mutation"}`),
		[]byte(`{"verb":"shell.exec","class":"proposal"}`),
		[]byte(`{"verb":"argocd.sync"}`),
	} {
		var decoded Definition
		if err := json.Unmarshal(payload, &decoded); err == nil {
			t.Fatalf("Unmarshal(%q) accepted forged definition", payload)
		}
	}
}
