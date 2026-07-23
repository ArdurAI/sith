// SPDX-License-Identifier: Apache-2.0

package remediation

import (
	"crypto/sha1" //nolint:gosec // GitHub Git object identity is SHA-1; this is not a security digest.
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/ArdurAI/sith/internal/fleet"
	"github.com/ArdurAI/sith/internal/tenancy"
)

const (
	// GitSourceSnapshotVersion is the first immutable observed-Git-state contract.
	GitSourceSnapshotVersion = "git-source-snapshot/v1"
	// GitSourceSnapshotAdapterVersion pins the canonical GitHub Git-object observation contract.
	GitSourceSnapshotAdapterVersion = "github-git-source-snapshot/2026-03-10"

	maxGitSourceSnapshotContentBytes = 64 << 10
	maxGitSourceSnapshotValidity     = 5 * time.Minute
	maxGitSourceSnapshotEvidenceRefs = 32
)

// GitSourceFreshness classifies an immutable observation against a caller-supplied trusted time.
type GitSourceFreshness string

// Closed freshness states. The interval is fresh exactly when ObservedAt <= now < ValidUntil.
const (
	GitSourceFresh  GitSourceFreshness = "fresh"
	GitSourceFuture GitSourceFreshness = "future"
	GitSourceStale  GitSourceFreshness = "stale"
)

// GitSourceSnapshotInput is accepted only from a canonical source adapter boundary. It contains
// observed Git state, never desired bytes, PR metadata, authority, credentials, or execution state.
type GitSourceSnapshotInput struct {
	Workspace       tenancy.WorkspaceID
	Subject         fleet.ResourceRef
	Sources         []SourceIdentity
	ObservedAt      time.Time
	ValidUntil      time.Time
	Repository      RepositoryIdentity
	BaseRef         string
	BaseCommit      string
	FilePath        string
	ObservedBlobSHA string
	CurrentContent  string
	EvidenceRefs    []fleet.ResourceRef
}

// GitSourceSnapshot is immutable after construction. Fields remain private so later composition
// cannot rewrite an observed identity, Git precondition, or byte sequence.
type GitSourceSnapshot struct {
	version         string
	workspace       tenancy.WorkspaceID
	subject         fleet.ResourceRef
	source          SourceIdentity
	observedAt      time.Time
	validUntil      time.Time
	repository      RepositoryIdentity
	baseRef         string
	baseCommit      string
	filePath        string
	observedBlobSHA string
	currentContent  string
	evidenceRefs    []fleet.ResourceRef
}

// Version reports the closed snapshot contract without exposing mutable observation fields.
func (snapshot GitSourceSnapshot) Version() string { return snapshot.version }

// NewGitSourceSnapshot validates and defensively copies one canonical Git observation. It performs
// no I/O and does not infer or render a desired change.
func NewGitSourceSnapshot(input GitSourceSnapshotInput) (GitSourceSnapshot, error) {
	if len(input.Sources) != 1 {
		return GitSourceSnapshot{}, fmt.Errorf("construct Git source snapshot: exactly one source is required")
	}
	snapshot := GitSourceSnapshot{
		version:         GitSourceSnapshotVersion,
		workspace:       input.Workspace,
		subject:         cloneResourceRef(input.Subject),
		source:          input.Sources[0],
		observedAt:      input.ObservedAt.UTC(),
		validUntil:      input.ValidUntil.UTC(),
		repository:      input.Repository,
		baseRef:         input.BaseRef,
		baseCommit:      input.BaseCommit,
		filePath:        input.FilePath,
		observedBlobSHA: input.ObservedBlobSHA,
		currentContent:  input.CurrentContent,
		evidenceRefs:    cloneResourceRefs(input.EvidenceRefs),
	}
	sort.Slice(snapshot.evidenceRefs, func(left, right int) bool {
		return resourceRefLess(snapshot.evidenceRefs[left], snapshot.evidenceRefs[right])
	})
	if err := snapshot.validate(); err != nil {
		return GitSourceSnapshot{}, fmt.Errorf("construct Git source snapshot: snapshot is invalid")
	}
	return snapshot, nil
}

// Freshness classifies the snapshot against a trusted clock value. Construction deliberately does
// not read a clock so identical source input always produces the same immutable value.
func (snapshot GitSourceSnapshot) Freshness(now time.Time) (GitSourceFreshness, error) {
	if err := snapshot.validate(); err != nil {
		return "", fmt.Errorf("inspect Git source snapshot: snapshot is invalid")
	}
	now = now.UTC()
	if now.IsZero() {
		return "", fmt.Errorf("inspect Git source snapshot: trusted time is required")
	}
	if now.Before(snapshot.observedAt) {
		return GitSourceFuture, nil
	}
	if !now.Before(snapshot.validUntil) {
		return GitSourceStale, nil
	}
	return GitSourceFresh, nil
}

func (snapshot GitSourceSnapshot) validate() error {
	if snapshot.version != GitSourceSnapshotVersion || tenancy.ValidateWorkspaceID(snapshot.workspace) != nil ||
		validateStableRef(snapshot.subject) != nil || validateGitSourceSnapshotSource(snapshot.source) != nil ||
		validateRepository(snapshot.repository) != nil || snapshot.source.NativeID != snapshot.repository.nativeID() ||
		snapshot.observedAt.IsZero() || snapshot.validUntil.IsZero() ||
		!snapshot.observedAt.Before(snapshot.validUntil) ||
		snapshot.validUntil.Sub(snapshot.observedAt) > maxGitSourceSnapshotValidity ||
		!validGitSourceBaseRef(snapshot.baseRef) || !validObjectID(snapshot.baseCommit) ||
		!validGitSourcePath(snapshot.filePath) || !validGitSourceContent(snapshot.currentContent) ||
		len(snapshot.baseCommit) != len(snapshot.observedBlobSHA) ||
		!gitSourceBlobMatchesContent(snapshot.observedBlobSHA, snapshot.currentContent) || len(snapshot.evidenceRefs) < 2 ||
		len(snapshot.evidenceRefs) > maxGitSourceSnapshotEvidenceRefs {
		return fmt.Errorf("git source snapshot is invalid")
	}

	subjectAttached := false
	blobAttached := false
	for index, ref := range snapshot.evidenceRefs {
		if validateStableRef(ref) != nil ||
			(index > 0 && !resourceRefLess(snapshot.evidenceRefs[index-1], ref)) {
			return fmt.Errorf("git source snapshot evidence is invalid")
		}
		subjectAttached = subjectAttached || sameResourceRef(ref, snapshot.subject)
		blobAttached = blobAttached || gitSourceBlobRefMatches(ref, snapshot)
	}
	if !subjectAttached || !blobAttached {
		return fmt.Errorf("git source snapshot evidence is unattached")
	}
	return nil
}

func validateGitSourceSnapshotSource(source SourceIdentity) error {
	if source.Kind != gitHubSourceKind || source.AdapterVersion != GitSourceSnapshotAdapterVersion ||
		validateSafeText(source.NativeID, maxIdentityBytes, false) != nil {
		return fmt.Errorf("git source snapshot source is invalid")
	}
	return nil
}

func validGitSourceBaseRef(value string) bool {
	if validateSafeText(value, maxIdentityBytes, false) != nil || strings.HasPrefix(value, ".") ||
		strings.HasPrefix(value, "-") || strings.HasPrefix(value, "/") ||
		strings.HasPrefix(strings.ToLower(value), "refs/") || strings.EqualFold(value, "HEAD") ||
		value == "@" || validObjectID(value) || strings.HasSuffix(value, ".") || strings.HasSuffix(value, "/") ||
		strings.Contains(value, "..") || strings.Contains(value, "//") || strings.Contains(value, "@{") ||
		strings.HasSuffix(strings.ToLower(value), ".lock") {
		return false
	}
	for _, character := range value {
		if unicode.IsSpace(character) || strings.ContainsRune("~^:?*[]\\", character) {
			return false
		}
	}
	for _, component := range strings.Split(value, "/") {
		if component == "" || strings.HasPrefix(component, ".") || strings.HasSuffix(strings.ToLower(component), ".lock") {
			return false
		}
	}
	return true
}

func validGitSourcePath(value string) bool {
	if validateSafeText(value, maxIdentityBytes, false) != nil || strings.HasPrefix(value, "/") ||
		strings.HasSuffix(value, "/") || strings.Contains(value, "\\") || path.Clean(value) != value {
		return false
	}
	for _, component := range strings.Split(value, "/") {
		if component == "" || component == "." || component == ".." || strings.EqualFold(component, ".git") {
			return false
		}
	}
	return true
}

func validGitSourceContent(value string) bool {
	return len(value) <= maxGitSourceSnapshotContentBytes && utf8.ValidString(value) &&
		!strings.ContainsRune(value, '\x00')
}

func gitSourceBlobMatchesContent(objectID, content string) bool {
	if !validObjectID(objectID) {
		return false
	}
	payload := make([]byte, 0, len(content)+64)
	payload = fmt.Appendf(payload, "blob %d\x00", len(content))
	payload = append(payload, content...)
	switch len(objectID) {
	case sha1.Size * 2:
		digest := sha1.Sum(payload) //nolint:gosec // Git object identity, not a security digest.
		return objectID == hex.EncodeToString(digest[:])
	case sha256.Size * 2:
		digest := sha256.Sum256(payload)
		return objectID == hex.EncodeToString(digest[:])
	default:
		return false
	}
}

func gitSourceBlobRefMatches(ref fleet.ResourceRef, snapshot GitSourceSnapshot) bool {
	return ref.SourceKind == snapshot.source.Kind && ref.Scope == snapshot.repository.Host &&
		ref.Kind == "Blob" && ref.Namespace == snapshot.repository.Owner+"/"+snapshot.repository.Repository &&
		ref.Name == snapshot.observedBlobSHA && len(ref.Attributes) == 0
}
