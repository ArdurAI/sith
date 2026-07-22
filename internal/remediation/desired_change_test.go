// SPDX-License-Identifier: Apache-2.0

package remediation

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/ArdurAI/sith/internal/fleet"
)

const testTransformerVersion = "fixture-renderer/v1"

func TestDesiredChangePreservesExactSnapshotAndOutput(t *testing.T) {
	t.Parallel()
	snapshot := mustGitSourceSnapshot(t)
	evidence := validDesiredChangeEvidence(snapshot)
	slices.Reverse(evidence)
	desiredContent := "apiVersion: v1\r\nmetadata:\n  name: café\t\nspec:\n  replicas: 4\n"

	change, err := newDesiredChange(snapshot, testTransformerVersion, desiredContent, evidence)
	if err != nil {
		t.Fatalf("newDesiredChange() error = %v", err)
	}
	if change.Version() != DesiredChangeVersion || change.transformerVersion != testTransformerVersion ||
		change.desiredContent != desiredContent || !sameGitSourceSnapshot(change.snapshot, snapshot) {
		t.Fatalf("change did not preserve the exact binding: %#v", change)
	}
	if len(change.evidenceRefs) != 2 || !sameResourceRef(change.evidenceRefs[0], testBlobRef()) ||
		!sameResourceRef(change.evidenceRefs[1], testSubjectRef()) {
		t.Fatalf("evidence = %#v, want canonical blob and subject order", change.evidenceRefs)
	}

	reordered, err := newDesiredChange(
		snapshot,
		testTransformerVersion,
		desiredContent,
		validDesiredChangeEvidence(snapshot),
	)
	if err != nil {
		t.Fatalf("newDesiredChange(reordered) error = %v", err)
	}
	if !slices.EqualFunc(change.evidenceRefs, reordered.evidenceRefs, sameResourceRef) {
		t.Fatalf("evidence ordering changed with caller order: %#v != %#v", change.evidenceRefs, reordered.evidenceRefs)
	}
}

func TestDesiredChangeConstructionIsMutationIsolated(t *testing.T) {
	t.Parallel()
	snapshot := mustGitSourceSnapshot(t)
	evidence := validDesiredChangeEvidence(snapshot)
	desiredContent := "replicas: 4\n"
	change, err := newDesiredChange(snapshot, testTransformerVersion, desiredContent, evidence)
	if err != nil {
		t.Fatal(err)
	}

	snapshot.subject.Name = "mutated-subject"
	snapshot.repository.Repository = "other"
	snapshot.baseCommit = strings.Repeat("c", 40)
	snapshot.currentContent = "mutated source"
	snapshot.evidenceRefs[0].Name = "mutated-snapshot-evidence"
	evidence[0].Name = "mutated-change-evidence"

	if err := change.validate(); err != nil {
		t.Fatalf("change retained caller mutation: %v", err)
	}
	if change.snapshot.subject.Name != "payments" || change.snapshot.repository.Repository != "sith" ||
		change.snapshot.baseCommit != testBaseSHA || change.snapshot.currentContent == "mutated source" ||
		change.snapshot.evidenceRefs[0].Name == "mutated-snapshot-evidence" ||
		change.evidenceRefs[1].Name == "mutated-change-evidence" || change.desiredContent != desiredContent {
		t.Fatalf("change retained caller-owned state: %#v", change)
	}
}

func TestNewDesiredChangeRejectsInvalidClaims(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*desiredChangeFixture)
	}{
		{"zero snapshot", func(input *desiredChangeFixture) { input.snapshot = GitSourceSnapshot{} }},
		{"forged snapshot", func(input *desiredChangeFixture) { input.snapshot.version = "forged/v9" }},
		{"forged snapshot subject", func(input *desiredChangeFixture) { input.snapshot.subject.Name = "other" }},
		{"empty transformer", func(input *desiredChangeFixture) { input.transformerVersion = "" }},
		{"uppercase transformer", func(input *desiredChangeFixture) { input.transformerVersion = "Fixture/v1" }},
		{"unversioned transformer", func(input *desiredChangeFixture) { input.transformerVersion = "fixture-renderer" }},
		{"ambiguous transformer", func(input *desiredChangeFixture) { input.transformerVersion = "fixture/renderer/v1" }},
		{"option-shaped transformer", func(input *desiredChangeFixture) { input.transformerVersion = "-fixture/v1" }},
		{"spaced transformer", func(input *desiredChangeFixture) { input.transformerVersion = "fixture renderer/v1" }},
		{"invalid UTF-8 output", func(input *desiredChangeFixture) { input.desiredContent = string([]byte{0xff}) }},
		{"NUL output", func(input *desiredChangeFixture) { input.desiredContent = "secret\x00value" }},
		{"oversized output", func(input *desiredChangeFixture) {
			input.desiredContent = strings.Repeat("x", maxGitSourceSnapshotContentBytes+1)
		}},
		{"no-op output", func(input *desiredChangeFixture) {
			input.desiredContent = input.snapshot.currentContent
		}},
		{"no evidence", func(input *desiredChangeFixture) { input.evidenceRefs = nil }},
		{"subject-only evidence", func(input *desiredChangeFixture) {
			input.evidenceRefs = []fleet.ResourceRef{input.snapshot.subject}
		}},
		{"blob-only evidence", func(input *desiredChangeFixture) {
			input.evidenceRefs = []fleet.ResourceRef{testBlobRef()}
		}},
		{"foreign subject evidence", func(input *desiredChangeFixture) { input.evidenceRefs[0].Name = "other" }},
		{"foreign blob evidence", func(input *desiredChangeFixture) {
			input.evidenceRefs[1].Name = strings.Repeat("e", 40)
		}},
		{"duplicate evidence", func(input *desiredChangeFixture) {
			input.evidenceRefs = append(input.evidenceRefs, input.evidenceRefs[0])
		}},
		{"unsafe evidence", func(input *desiredChangeFixture) { input.evidenceRefs[0].Name = "secret\nforged" }},
		{"evidence attributes", func(input *desiredChangeFixture) {
			input.evidenceRefs[0].Attributes = map[string]string{"uid": "private"}
		}},
		{"too much evidence", func(input *desiredChangeFixture) {
			for index := len(input.evidenceRefs); index <= maxDesiredChangeEvidenceRefs; index++ {
				input.evidenceRefs = append(input.evidenceRefs, fleet.ResourceRef{
					SourceKind: "github", Scope: "github.com", Kind: "Observation",
					Namespace: "ArdurAI/sith", Name: fmt.Sprintf("evidence-%02d", index),
				})
			}
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := validDesiredChangeFixture(t)
			test.mutate(&input)
			change, err := newDesiredChange(
				input.snapshot,
				input.transformerVersion,
				input.desiredContent,
				input.evidenceRefs,
			)
			if err == nil || change.Version() != "" {
				t.Fatalf("newDesiredChange() = %#v, %v, want rejection", change, err)
			}
			if strings.Contains(err.Error(), "secret") || len(err.Error()) > 160 {
				t.Fatalf("constructor leaked or returned unbounded error: %q", err)
			}
		})
	}
}

func TestDesiredChangeRejectsForgedState(t *testing.T) {
	t.Parallel()
	fixture := validDesiredChangeFixture(t)
	change, err := newDesiredChange(
		fixture.snapshot,
		fixture.transformerVersion,
		fixture.desiredContent,
		fixture.evidenceRefs,
	)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*DesiredChange)
	}{
		{"version", func(value *DesiredChange) { value.version = "forged/v9" }},
		{"snapshot", func(value *DesiredChange) { value.snapshot.observedBlobSHA = strings.Repeat("e", 40) }},
		{"transformer", func(value *DesiredChange) { value.transformerVersion = "forged" }},
		{"no-op output", func(value *DesiredChange) { value.desiredContent = value.snapshot.currentContent }},
		{"evidence order", func(value *DesiredChange) { slices.Reverse(value.evidenceRefs) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			forged := change
			forged.snapshot = cloneGitSourceSnapshot(change.snapshot)
			forged.evidenceRefs = cloneResourceRefs(change.evidenceRefs)
			test.mutate(&forged)
			if err := forged.validate(); err == nil {
				t.Fatalf("forged change validated: %#v", forged)
			}
		})
	}
}

func TestDesiredChangeValidationIsConcurrentAndReadOnly(t *testing.T) {
	t.Parallel()
	fixture := validDesiredChangeFixture(t)
	change, err := newDesiredChange(
		fixture.snapshot,
		fixture.transformerVersion,
		fixture.desiredContent,
		fixture.evidenceRefs,
	)
	if err != nil {
		t.Fatal(err)
	}
	before := change
	before.snapshot = cloneGitSourceSnapshot(change.snapshot)
	before.evidenceRefs = cloneResourceRefs(change.evidenceRefs)

	const readers = 64
	errors := make(chan error, readers)
	var group sync.WaitGroup
	for range readers {
		group.Add(1)
		go func() {
			defer group.Done()
			if validateErr := change.validate(); validateErr != nil {
				errors <- validateErr
				return
			}
			if change.Version() != DesiredChangeVersion {
				errors <- fmt.Errorf("version = %q", change.Version())
			}
		}()
	}
	group.Wait()
	if !sameDesiredChange(change, before) {
		t.Fatalf("concurrent validation mutated change: %#v != %#v", change, before)
	}
	close(errors)
	for err := range errors {
		t.Error(err)
	}
}

func FuzzDesiredChangeConstructor(f *testing.F) {
	for _, seed := range []struct{ transformer, content string }{
		{testTransformerVersion, "replicas: 4\n"},
		{"invalid", "secret\x00value"},
		{"renderer/2026-07-22", "name: café\r\n"},
	} {
		f.Add(seed.transformer, seed.content)
	}
	f.Fuzz(func(t *testing.T, transformer, content string) {
		snapshot := mustGitSourceSnapshot(t)
		change, err := newDesiredChange(snapshot, transformer, content, validDesiredChangeEvidence(snapshot))
		if err != nil {
			return
		}
		if change.transformerVersion != transformer || change.desiredContent != content ||
			!sameGitSourceSnapshot(change.snapshot, snapshot) {
			t.Fatalf("accepted change altered its exact binding: %#v", change)
		}
		if validateErr := change.validate(); validateErr != nil {
			t.Fatalf("accepted change did not revalidate: %v", validateErr)
		}
	})
}

type desiredChangeFixture struct {
	snapshot           GitSourceSnapshot
	transformerVersion string
	desiredContent     string
	evidenceRefs       []fleet.ResourceRef
}

func validDesiredChangeFixture(t testing.TB) desiredChangeFixture {
	t.Helper()
	snapshot := mustGitSourceSnapshot(t)
	return desiredChangeFixture{
		snapshot:           snapshot,
		transformerVersion: testTransformerVersion,
		desiredContent:     "replicas: 4\n",
		evidenceRefs:       validDesiredChangeEvidence(snapshot),
	}
}

func mustGitSourceSnapshot(t testing.TB) GitSourceSnapshot {
	t.Helper()
	snapshot, err := NewGitSourceSnapshot(validGitSourceSnapshotInput())
	if err != nil {
		t.Fatalf("NewGitSourceSnapshot() error = %v", err)
	}
	return snapshot
}

func validDesiredChangeEvidence(snapshot GitSourceSnapshot) []fleet.ResourceRef {
	return []fleet.ResourceRef{cloneResourceRef(snapshot.subject), testBlobRef()}
}

func sameGitSourceSnapshot(left, right GitSourceSnapshot) bool {
	return left.version == right.version && left.workspace == right.workspace &&
		sameResourceRef(left.subject, right.subject) && left.source == right.source &&
		left.observedAt.Equal(right.observedAt) && left.validUntil.Equal(right.validUntil) &&
		left.repository == right.repository && left.baseRef == right.baseRef &&
		left.baseCommit == right.baseCommit && left.filePath == right.filePath &&
		left.observedBlobSHA == right.observedBlobSHA && left.currentContent == right.currentContent &&
		slices.EqualFunc(left.evidenceRefs, right.evidenceRefs, sameResourceRef)
}

func sameDesiredChange(left, right DesiredChange) bool {
	return left.version == right.version && sameGitSourceSnapshot(left.snapshot, right.snapshot) &&
		left.transformerVersion == right.transformerVersion && left.desiredContent == right.desiredContent &&
		slices.EqualFunc(left.evidenceRefs, right.evidenceRefs, sameResourceRef)
}
