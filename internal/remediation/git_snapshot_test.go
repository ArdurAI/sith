// SPDX-License-Identifier: Apache-2.0

package remediation

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ArdurAI/sith/internal/fleet"
)

const testSnapshotBlobSHA = "c63745ccdd30a4492aed8a39e04b7f482ace3612"

const testSnapshotSHA256Blob = "c611609efc93463233f57218a49edeea21922ab692dbb3b1fe535a52afe4545a"

const emptyGitBlobSHA = "e69de29bb2d1d6434b8b29ae775ad8c2e48c5391"

func TestGitSourceSnapshotPreservesExactObservedState(t *testing.T) {
	t.Parallel()
	input := validGitSourceSnapshotInput()
	slices.Reverse(input.EvidenceRefs)
	snapshot, err := NewGitSourceSnapshot(input)
	if err != nil {
		t.Fatalf("NewGitSourceSnapshot() error = %v", err)
	}
	if snapshot.Version() != GitSourceSnapshotVersion || snapshot.workspace != testWorkspace ||
		!sameResourceRef(snapshot.subject, testSubjectRef()) || snapshot.source != input.Sources[0] ||
		snapshot.repository != input.Repository || snapshot.baseRef != "dev" || snapshot.baseCommit != testBaseSHA ||
		snapshot.filePath != "deploy/payments.yaml" || snapshot.observedBlobSHA != testSnapshotBlobSHA ||
		snapshot.currentContent != input.CurrentContent || !snapshot.observedAt.Equal(input.ObservedAt) ||
		!snapshot.validUntil.Equal(input.ValidUntil) {
		t.Fatalf("snapshot did not preserve exact observed state: %#v", snapshot)
	}
	if len(snapshot.evidenceRefs) != 2 || !sameResourceRef(snapshot.evidenceRefs[0], testBlobRef()) ||
		!sameResourceRef(snapshot.evidenceRefs[1], testSubjectRef()) {
		t.Fatalf("evidence = %#v, want canonical blob and subject order", snapshot.evidenceRefs)
	}
	got, err := snapshot.Freshness(testNow)
	if err != nil || got != GitSourceFresh {
		t.Fatalf("Freshness() = %q, %v, want %q", got, err, GitSourceFresh)
	}

	reordered := validGitSourceSnapshotInput()
	reorderedSnapshot, err := NewGitSourceSnapshot(reordered)
	if err != nil {
		t.Fatalf("NewGitSourceSnapshot(reordered) error = %v", err)
	}
	if !slices.EqualFunc(snapshot.evidenceRefs, reorderedSnapshot.evidenceRefs, sameResourceRef) {
		t.Fatalf("evidence ordering changed with caller order: %#v != %#v", snapshot.evidenceRefs, reorderedSnapshot.evidenceRefs)
	}
}

func TestGitSourceSnapshotConstructionIsMutationIsolated(t *testing.T) {
	t.Parallel()
	input := validGitSourceSnapshotInput()
	snapshot, err := NewGitSourceSnapshot(input)
	if err != nil {
		t.Fatal(err)
	}

	input.Workspace = "workspace-b"
	input.Subject.Name = "mutated-subject"
	input.Sources[0].NativeID = "github.com/other/repository"
	input.Repository.Repository = "other"
	input.BaseRef = "main"
	input.BaseCommit = strings.Repeat("c", 40)
	input.FilePath = "other/file.yaml"
	input.ObservedBlobSHA = strings.Repeat("d", 40)
	input.CurrentContent = "mutated"
	input.EvidenceRefs[0].Name = "mutated-evidence"

	if err := snapshot.validate(); err != nil {
		t.Fatalf("snapshot retained caller mutation: %v", err)
	}
	if snapshot.workspace != testWorkspace || snapshot.subject.Name != "payments" ||
		snapshot.source.NativeID != "github.com/ArdurAI/sith" || snapshot.repository.Repository != "sith" ||
		snapshot.baseRef != "dev" || snapshot.baseCommit != testBaseSHA || snapshot.filePath != "deploy/payments.yaml" ||
		snapshot.observedBlobSHA != testSnapshotBlobSHA || snapshot.currentContent == "mutated" ||
		snapshot.evidenceRefs[1].Name == "mutated-evidence" {
		t.Fatalf("snapshot retained caller-owned state: %#v", snapshot)
	}
}

func TestGitSourceSnapshotAcceptsSHA256ObjectIdentity(t *testing.T) {
	t.Parallel()
	input := validGitSourceSnapshotInput()
	input.BaseCommit = strings.Repeat("a", 64)
	input.ObservedBlobSHA = testSnapshotSHA256Blob
	input.EvidenceRefs[1].Name = testSnapshotSHA256Blob
	snapshot, err := NewGitSourceSnapshot(input)
	if err != nil {
		t.Fatalf("NewGitSourceSnapshot() error = %v", err)
	}
	if snapshot.baseCommit != input.BaseCommit || snapshot.observedBlobSHA != testSnapshotSHA256Blob {
		t.Fatalf("SHA-256 identities were not preserved: %#v", snapshot)
	}
}

func TestGitSourceSnapshotAcceptsExactEmptyFile(t *testing.T) {
	t.Parallel()
	input := validGitSourceSnapshotInput()
	input.CurrentContent = ""
	input.ObservedBlobSHA = emptyGitBlobSHA
	input.EvidenceRefs[1].Name = emptyGitBlobSHA
	snapshot, err := NewGitSourceSnapshot(input)
	if err != nil {
		t.Fatalf("NewGitSourceSnapshot() error = %v", err)
	}
	if snapshot.currentContent != "" || snapshot.observedBlobSHA != emptyGitBlobSHA {
		t.Fatalf("empty file observation was not preserved: %#v", snapshot)
	}
}

func TestNewGitSourceSnapshotRejectsInvalidClaims(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*GitSourceSnapshotInput)
	}{
		{"no source", func(input *GitSourceSnapshotInput) { input.Sources = nil }},
		{"multiple sources", func(input *GitSourceSnapshotInput) { input.Sources = append(input.Sources, input.Sources[0]) }},
		{"invalid workspace", func(input *GitSourceSnapshotInput) { input.Workspace = " workspace-a" }},
		{"subject attributes", func(input *GitSourceSnapshotInput) { input.Subject.Attributes = map[string]string{"uid": "private"} }},
		{"source kind", func(input *GitSourceSnapshotInput) { input.Sources[0].Kind = "gitlab" }},
		{"source adapter", func(input *GitSourceSnapshotInput) { input.Sources[0].AdapterVersion = "future/v2" }},
		{"source repository mismatch", func(input *GitSourceSnapshotInput) { input.Sources[0].NativeID = "github.com/ArdurAI/other" }},
		{"repository URL", func(input *GitSourceSnapshotInput) { input.Repository.Host = "https://github.com" }},
		{"repository suffix", func(input *GitSourceSnapshotInput) { input.Repository.Repository = "sith.git" }},
		{"zero observation", func(input *GitSourceSnapshotInput) { input.ObservedAt = time.Time{} }},
		{"zero validity", func(input *GitSourceSnapshotInput) { input.ValidUntil = time.Time{} }},
		{"reversed validity", func(input *GitSourceSnapshotInput) { input.ValidUntil = input.ObservedAt }},
		{"unbounded validity", func(input *GitSourceSnapshotInput) {
			input.ValidUntil = input.ObservedAt.Add(maxGitSourceSnapshotValidity + time.Nanosecond)
		}},
		{"symbolic base", func(input *GitSourceSnapshotInput) { input.BaseRef = "HEAD" }},
		{"full base ref", func(input *GitSourceSnapshotInput) { input.BaseRef = "refs/heads/dev" }},
		{"option-shaped base", func(input *GitSourceSnapshotInput) { input.BaseRef = "--upload-pack=evil" }},
		{"commit-shaped base", func(input *GitSourceSnapshotInput) { input.BaseRef = testBaseSHA }},
		{"reflog base", func(input *GitSourceSnapshotInput) { input.BaseRef = "dev@{1}" }},
		{"single at base", func(input *GitSourceSnapshotInput) { input.BaseRef = "@" }},
		{"ambiguous base", func(input *GitSourceSnapshotInput) { input.BaseRef = "release..next" }},
		{"locked base", func(input *GitSourceSnapshotInput) { input.BaseRef = "dev.lock" }},
		{"invalid base commit", func(input *GitSourceSnapshotInput) { input.BaseCommit = strings.ToUpper(testBaseSHA) }},
		{"short base commit", func(input *GitSourceSnapshotInput) { input.BaseCommit = testBaseSHA[:12] }},
		{"mixed object algorithms", func(input *GitSourceSnapshotInput) { input.BaseCommit = strings.Repeat("a", 64) }},
		{"invalid blob", func(input *GitSourceSnapshotInput) { input.ObservedBlobSHA = "not-a-blob" }},
		{"blob content mismatch", func(input *GitSourceSnapshotInput) { input.ObservedBlobSHA = testBlobSHA }},
		{"unsafe relative path", func(input *GitSourceSnapshotInput) { input.FilePath = "../secret.yaml" }},
		{"absolute path", func(input *GitSourceSnapshotInput) { input.FilePath = "/deploy/payments.yaml" }},
		{"unclean path", func(input *GitSourceSnapshotInput) { input.FilePath = "deploy//payments.yaml" }},
		{"backslash path", func(input *GitSourceSnapshotInput) { input.FilePath = "deploy\\payments.yaml" }},
		{"Git metadata path", func(input *GitSourceSnapshotInput) { input.FilePath = ".git/config" }},
		{"invalid UTF-8 content", func(input *GitSourceSnapshotInput) { input.CurrentContent = string([]byte{0xff}) }},
		{"NUL content", func(input *GitSourceSnapshotInput) { input.CurrentContent = "secret\x00value" }},
		{"unbounded content", func(input *GitSourceSnapshotInput) {
			input.CurrentContent = strings.Repeat("x", maxGitSourceSnapshotContentBytes+1)
		}},
		{"no evidence", func(input *GitSourceSnapshotInput) { input.EvidenceRefs = nil }},
		{"subject-only evidence", func(input *GitSourceSnapshotInput) { input.EvidenceRefs = []fleet.ResourceRef{input.Subject} }},
		{"blob-only evidence", func(input *GitSourceSnapshotInput) { input.EvidenceRefs = []fleet.ResourceRef{testBlobRef()} }},
		{"foreign blob evidence", func(input *GitSourceSnapshotInput) { input.EvidenceRefs[1].Name = strings.Repeat("e", 40) }},
		{"duplicate evidence", func(input *GitSourceSnapshotInput) {
			input.EvidenceRefs = append(input.EvidenceRefs, input.EvidenceRefs[0])
		}},
		{"unsafe evidence", func(input *GitSourceSnapshotInput) { input.EvidenceRefs[0].Name = "commit\nforged" }},
		{"too much evidence", func(input *GitSourceSnapshotInput) {
			for index := len(input.EvidenceRefs); index <= maxGitSourceSnapshotEvidenceRefs; index++ {
				input.EvidenceRefs = append(input.EvidenceRefs, fleet.ResourceRef{
					SourceKind: "github", Scope: "github.com", Kind: "Observation",
					Namespace: "ArdurAI/sith", Name: fmt.Sprintf("evidence-%02d", index),
				})
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := validGitSourceSnapshotInput()
			test.mutate(&input)
			snapshot, err := NewGitSourceSnapshot(input)
			if err == nil || snapshot.Version() != "" {
				t.Fatalf("NewGitSourceSnapshot() = %#v, %v, want rejection", snapshot, err)
			}
			if strings.Contains(err.Error(), "secret") || len(err.Error()) > 160 {
				t.Fatalf("constructor leaked or returned unbounded error: %q", err)
			}
		})
	}
}

func TestGitSourceSnapshotFreshnessUsesTrustedTime(t *testing.T) {
	t.Parallel()
	snapshot, err := NewGitSourceSnapshot(validGitSourceSnapshotInput())
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		now  time.Time
		want GitSourceFreshness
	}{
		{"future", snapshot.observedAt.Add(-time.Nanosecond), GitSourceFuture},
		{"observed boundary", snapshot.observedAt, GitSourceFresh},
		{"fresh", testNow, GitSourceFresh},
		{"expiry boundary", snapshot.validUntil, GitSourceStale},
		{"stale", snapshot.validUntil.Add(time.Nanosecond), GitSourceStale},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := snapshot.Freshness(test.now)
			if err != nil || got != test.want {
				t.Fatalf("Freshness(%s) = %q, %v, want %q", test.now, got, err, test.want)
			}
		})
	}
	if got, err := snapshot.Freshness(time.Time{}); err == nil || got != "" {
		t.Fatalf("Freshness(zero) = %q, %v, want closed error", got, err)
	}
	forged := snapshot
	forged.version = "forged/v9"
	if got, err := forged.Freshness(testNow); err == nil || got != "" {
		t.Fatalf("Freshness(forged) = %q, %v, want closed error", got, err)
	}
}

func TestGitSourceSnapshotFreshnessIsConcurrentAndReadOnly(t *testing.T) {
	t.Parallel()
	snapshot, err := NewGitSourceSnapshot(validGitSourceSnapshotInput())
	if err != nil {
		t.Fatal(err)
	}
	const readers = 64
	errors := make(chan error, readers)
	var group sync.WaitGroup
	for range readers {
		group.Add(1)
		go func() {
			defer group.Done()
			got, freshnessErr := snapshot.Freshness(testNow)
			if freshnessErr != nil {
				errors <- freshnessErr
				return
			}
			if got != GitSourceFresh {
				errors <- fmt.Errorf("freshness = %q", got)
			}
		}()
	}
	group.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
}

func FuzzGitSourceSnapshotConstructor(f *testing.F) {
	for _, seed := range []struct{ baseRef, filePath, content string }{
		{"dev", "deploy/payments.yaml", "replicas: 3\n"},
		{"HEAD", "../secret", "\x00"},
		{"release/v1", "manifests/café.yaml", "name: café\r\n"},
	} {
		f.Add(seed.baseRef, seed.filePath, seed.content)
	}
	f.Fuzz(func(t *testing.T, baseRef, filePath, content string) {
		input := validGitSourceSnapshotInput()
		input.BaseRef = baseRef
		input.FilePath = filePath
		input.CurrentContent = content
		snapshot, err := NewGitSourceSnapshot(input)
		if err != nil {
			return
		}
		if snapshot.baseRef != baseRef || snapshot.filePath != filePath || snapshot.currentContent != content {
			t.Fatalf("accepted snapshot changed exact source bytes: %#v", snapshot)
		}
		if got, freshnessErr := snapshot.Freshness(testNow); freshnessErr != nil || got != GitSourceFresh {
			t.Fatalf("accepted snapshot Freshness() = %q, %v", got, freshnessErr)
		}
	})
}

func validGitSourceSnapshotInput() GitSourceSnapshotInput {
	subject := testSubjectRef()
	return GitSourceSnapshotInput{
		Workspace: testWorkspace,
		Subject:   subject,
		Sources: []SourceIdentity{{
			Kind: gitHubSourceKind, AdapterVersion: GitSourceSnapshotAdapterVersion,
			NativeID: "github.com/ArdurAI/sith",
		}},
		ObservedAt: testNow.Add(-time.Minute), ValidUntil: testNow.Add(time.Minute),
		Repository: RepositoryIdentity{Host: "github.com", Owner: "ArdurAI", Repository: "sith"},
		BaseRef:    "dev", BaseCommit: testBaseSHA, FilePath: "deploy/payments.yaml",
		ObservedBlobSHA: testSnapshotBlobSHA,
		CurrentContent:  "apiVersion: v1\r\nmetadata:\n  name: café\t\n",
		EvidenceRefs:    []fleet.ResourceRef{subject, testBlobRef()},
	}
}

func testBlobRef() fleet.ResourceRef {
	return fleet.ResourceRef{
		SourceKind: gitHubSourceKind, Scope: "github.com", Kind: "Blob",
		Namespace: "ArdurAI/sith", Name: testSnapshotBlobSHA,
	}
}
