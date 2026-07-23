// SPDX-License-Identifier: Apache-2.0

package auditrecord

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"

	"github.com/ArdurAI/sith/internal/tenancy"
)

const (
	// PageSchemaV1 identifies one bounded interval from an immutable retained-chain snapshot.
	PageSchemaV1 = "sith.policy-audit-page/v1"
	// PageCursorChars is the exact canonical base64url length of one continuation descriptor.
	PageCursorChars = 151

	pageCursorVersion byte = 1
	pageCursorBytes        = 113
	pageCursorDomain       = "sith-policy-audit-page-cursor/v1"
)

// Page is one bounded, consecutive interval from a declared workspace chain snapshot. An
// intermediate page proves only its own internal links; PageSequenceVerifier establishes complete
// genesis-to-head continuity across an ordered set of pages.
type Page struct {
	Schema        string  `json:"schema"`
	WorkspaceID   string  `json:"workspace_id"`
	Snapshot      Chain   `json:"snapshot"`
	StartSequence int64   `json:"start_sequence"`
	PreviousHash  string  `json:"previous_hash"`
	Entries       []Entry `json:"entries"`
	NextCursor    string  `json:"next_cursor,omitempty"`
}

// PageRequest is a validated first-page or continuation request. Its fields remain private so
// storage code cannot accidentally trust caller-controlled cursor components without parsing and
// workspace binding first.
type PageRequest struct {
	workspaceDigest [sha256.Size]byte
	initial         bool
	headSequence    int64
	headHash        string
	nextSequence    int64
	previousHash    string
}

// FirstPage creates a request that will bind itself to the current workspace head in the backing
// repeatable-read transaction.
func FirstPage(workspaceID tenancy.WorkspaceID) (PageRequest, error) {
	if workspaceInvalid(workspaceID) {
		return PageRequest{}, fmt.Errorf("construct first audit page request: workspace is invalid")
	}
	return PageRequest{workspaceDigest: pageWorkspaceDigest(workspaceID), initial: true}, nil
}

// ContinuePage strictly parses one canonical, fixed-size base64url continuation descriptor and
// binds it to the expected workspace. The descriptor is not a credential; the database must still
// validate its snapshot and boundary hashes after normal authentication and authorization.
func ContinuePage(workspaceID tenancy.WorkspaceID, encoded string) (PageRequest, error) {
	if workspaceInvalid(workspaceID) || len(encoded) != PageCursorChars {
		return PageRequest{}, fmt.Errorf("parse audit page cursor: cursor is invalid")
	}
	payload, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	if err != nil || len(payload) != pageCursorBytes ||
		base64.RawURLEncoding.EncodeToString(payload) != encoded || payload[0] != pageCursorVersion {
		return PageRequest{}, fmt.Errorf("parse audit page cursor: cursor is invalid")
	}
	wantWorkspace := pageWorkspaceDigest(workspaceID)
	if !bytes.Equal(payload[1:33], wantWorkspace[:]) {
		return PageRequest{}, fmt.Errorf("parse audit page cursor: cursor is invalid")
	}
	headUnsigned := binary.BigEndian.Uint64(payload[33:41])
	nextUnsigned := binary.BigEndian.Uint64(payload[73:81])
	if headUnsigned > math.MaxInt64 || nextUnsigned > math.MaxInt64 {
		return PageRequest{}, fmt.Errorf("parse audit page cursor: cursor is invalid")
	}
	request := PageRequest{
		workspaceDigest: wantWorkspace,
		headSequence:    int64(headUnsigned),
		headHash:        "sha256:" + hex.EncodeToString(payload[41:73]),
		nextSequence:    int64(nextUnsigned),
		previousHash:    "sha256:" + hex.EncodeToString(payload[81:113]),
	}
	if err := request.ValidateForWorkspace(workspaceID); err != nil {
		return PageRequest{}, fmt.Errorf("parse audit page cursor: cursor is invalid")
	}
	return request, nil
}

// EncodePageCursor creates the only accepted continuation encoding. The next sequence must still
// belong to the declared snapshot; final pages carry no continuation.
func EncodePageCursor(
	workspaceID tenancy.WorkspaceID,
	headSequence int64,
	headHash string,
	nextSequence int64,
	previousHash string,
) (string, error) {
	if workspaceInvalid(workspaceID) || headSequence <= 1 || nextSequence <= 1 ||
		nextSequence > headSequence || !validHash(headHash) || !validHash(previousHash) {
		return "", fmt.Errorf("encode audit page cursor: cursor boundary is invalid")
	}
	headBytes, err := decodeHash(headHash)
	if err != nil {
		return "", fmt.Errorf("encode audit page cursor: cursor boundary is invalid")
	}
	previousBytes, err := decodeHash(previousHash)
	if err != nil {
		return "", fmt.Errorf("encode audit page cursor: cursor boundary is invalid")
	}
	payload := make([]byte, pageCursorBytes)
	payload[0] = pageCursorVersion
	workspaceDigest := pageWorkspaceDigest(workspaceID)
	copy(payload[1:33], workspaceDigest[:])
	binary.BigEndian.PutUint64(payload[33:41], uint64(headSequence))
	copy(payload[41:73], headBytes)
	binary.BigEndian.PutUint64(payload[73:81], uint64(nextSequence))
	copy(payload[81:113], previousBytes)
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	if len(encoded) != PageCursorChars {
		return "", fmt.Errorf("encode audit page cursor: canonical encoding is invalid")
	}
	return encoded, nil
}

// ValidateForWorkspace rechecks that a typed request is complete and belongs to one workspace.
func (request PageRequest) ValidateForWorkspace(workspaceID tenancy.WorkspaceID) error {
	if workspaceInvalid(workspaceID) {
		return fmt.Errorf("audit page request workspace is invalid")
	}
	wantWorkspace := pageWorkspaceDigest(workspaceID)
	if !bytes.Equal(request.workspaceDigest[:], wantWorkspace[:]) {
		return fmt.Errorf("audit page request workspace is invalid")
	}
	if request.initial {
		if request.headSequence != 0 || request.headHash != "" || request.nextSequence != 0 || request.previousHash != "" {
			return fmt.Errorf("first audit page request is invalid")
		}
		return nil
	}
	if request.headSequence <= 1 || request.nextSequence <= 1 || request.nextSequence > request.headSequence ||
		!validHash(request.headHash) || !validHash(request.previousHash) {
		return fmt.Errorf("continuation audit page request is invalid")
	}
	return nil
}

// Initial reports whether the backing store should capture a new snapshot head.
func (request PageRequest) Initial() bool { return request.initial }

// HeadSequence returns the immutable snapshot head sequence for a continuation.
func (request PageRequest) HeadSequence() int64 { return request.headSequence }

// HeadHash returns the immutable snapshot head hash for a continuation.
func (request PageRequest) HeadHash() string { return request.headHash }

// NextSequence returns the first entry requested by a continuation.
func (request PageRequest) NextSequence() int64 { return request.nextSequence }

// PreviousHash returns the expected hash immediately before NextSequence.
func (request PageRequest) PreviousHash() string { return request.previousHash }

// ValidatePage binds a returned page to the exact parsed request. A first request must start at
// genesis; a continuation must preserve its declared snapshot, next sequence, and prior hash.
func (request PageRequest) ValidatePage(page Page) error {
	workspaceID := tenancy.WorkspaceID(page.WorkspaceID)
	if err := request.ValidateForWorkspace(workspaceID); err != nil {
		return fmt.Errorf("audit page does not match request")
	}
	if err := page.ValidateForWorkspace(workspaceID); err != nil {
		return fmt.Errorf("audit page does not match request")
	}
	if request.initial {
		if page.StartSequence != 1 || page.PreviousHash != zeroHash() {
			return fmt.Errorf("audit page does not match first request")
		}
		return nil
	}
	if page.Snapshot.HeadSequence != request.headSequence || page.Snapshot.HeadHash != request.headHash ||
		page.StartSequence != request.nextSequence || page.PreviousHash != request.previousHash {
		return fmt.Errorf("audit page does not match continuation")
	}
	return nil
}

// ValidateForWorkspace checks a page's closed schema, snapshot, bounds, links, and continuation
// without recomputing entry hashes.
func (page Page) ValidateForWorkspace(workspaceID tenancy.WorkspaceID) error {
	return page.validateForWorkspace(workspaceID, false)
}

// Verify checks one page's internal canonical hashes and links. Complete-chain proof requires an
// ordered PageSequenceVerifier reaching the declared snapshot head.
func (page Page) Verify() error {
	return page.VerifyForWorkspace(tenancy.WorkspaceID(page.WorkspaceID))
}

// VerifyForWorkspace binds a page to an expected tenant before recomputing every contained hash.
func (page Page) VerifyForWorkspace(workspaceID tenancy.WorkspaceID) error {
	return page.validateForWorkspace(workspaceID, true)
}

func (page Page) validateForWorkspace(workspaceID tenancy.WorkspaceID, verifyHashes bool) error {
	if page.Schema != PageSchemaV1 || page.WorkspaceID != string(workspaceID) || workspaceInvalid(workspaceID) ||
		page.Snapshot.HashAlgorithm != HashAlgorithm || page.Snapshot.HeadSequence <= 0 ||
		!validHash(page.Snapshot.HeadHash) || page.StartSequence <= 0 || !validHash(page.PreviousHash) ||
		len(page.Entries) == 0 || len(page.Entries) > MaxEntries {
		return fmt.Errorf("audit page envelope is invalid")
	}
	endSequence := page.StartSequence + int64(len(page.Entries)) - 1
	if endSequence < page.StartSequence || endSequence > page.Snapshot.HeadSequence {
		return fmt.Errorf("audit page range is invalid")
	}
	lastHash, err := validateEntrySequence(
		workspaceID, page.Entries, page.StartSequence, page.PreviousHash, verifyHashes,
	)
	if err != nil {
		return fmt.Errorf("audit page: %w", err)
	}
	if endSequence == page.Snapshot.HeadSequence {
		if page.NextCursor != "" || lastHash != page.Snapshot.HeadHash {
			return fmt.Errorf("final audit page head is invalid")
		}
		return nil
	}
	if len(page.Entries) != MaxEntries || page.NextCursor == "" {
		return fmt.Errorf("intermediate audit page boundary is invalid")
	}
	next, err := ContinuePage(workspaceID, page.NextCursor)
	if err != nil || next.HeadSequence() != page.Snapshot.HeadSequence ||
		next.HeadHash() != page.Snapshot.HeadHash || next.NextSequence() != endSequence+1 ||
		next.PreviousHash() != lastHash {
		return fmt.Errorf("intermediate audit page cursor is invalid")
	}
	return nil
}

// PageSequenceResult is the bounded summary of one fully verified ordered page sequence.
type PageSequenceResult struct {
	Schema      string
	WorkspaceID string
	Pages       int
	Entries     int64
	Chain       Chain
}

// PageSequenceVerifier incrementally verifies ordered page documents without retaining earlier
// page payloads in memory.
type PageSequenceVerifier struct {
	initialized  bool
	complete     bool
	workspaceID  tenancy.WorkspaceID
	chain        Chain
	nextSequence int64
	previousHash string
	pages        int
}

// Add verifies and consumes the next page in sequence.
func (verifier *PageSequenceVerifier) Add(page Page) error {
	if verifier == nil || verifier.complete {
		return fmt.Errorf("audit page sequence cannot accept another page")
	}
	workspaceID := tenancy.WorkspaceID(page.WorkspaceID)
	if err := page.VerifyForWorkspace(workspaceID); err != nil {
		return fmt.Errorf("verify audit page sequence: %w", err)
	}
	if !verifier.initialized {
		if page.StartSequence != 1 || page.PreviousHash != zeroHash() {
			return fmt.Errorf("verify audit page sequence: first page does not start at genesis")
		}
		verifier.initialized = true
		verifier.workspaceID = workspaceID
		verifier.chain = page.Snapshot
		verifier.nextSequence = 1
		verifier.previousHash = zeroHash()
	} else if workspaceID != verifier.workspaceID || page.Snapshot != verifier.chain {
		return fmt.Errorf("verify audit page sequence: page snapshot is inconsistent")
	}
	if page.StartSequence != verifier.nextSequence || page.PreviousHash != verifier.previousHash {
		return fmt.Errorf("verify audit page sequence: page order or continuity is invalid")
	}
	endSequence := page.StartSequence + int64(len(page.Entries)) - 1
	verifier.nextSequence = endSequence + 1
	verifier.previousHash = page.Entries[len(page.Entries)-1].EntryHash
	verifier.pages++
	if page.NextCursor == "" {
		verifier.complete = true
	}
	return nil
}

// Finish succeeds only after the sequence reaches its declared snapshot head.
func (verifier *PageSequenceVerifier) Finish() (PageSequenceResult, error) {
	if verifier == nil || !verifier.initialized || !verifier.complete ||
		verifier.nextSequence-1 != verifier.chain.HeadSequence || verifier.previousHash != verifier.chain.HeadHash {
		return PageSequenceResult{}, fmt.Errorf("audit page sequence is incomplete")
	}
	return PageSequenceResult{
		Schema: PageSchemaV1, WorkspaceID: string(verifier.workspaceID), Pages: verifier.pages,
		Entries: verifier.chain.HeadSequence, Chain: verifier.chain,
	}, nil
}

func pageWorkspaceDigest(workspaceID tenancy.WorkspaceID) [sha256.Size]byte {
	canonical := make([]byte, 0, len(pageCursorDomain)+len(workspaceID)+16)
	canonical = appendCanonicalString(canonical, pageCursorDomain)
	canonical = appendCanonicalString(canonical, string(workspaceID))
	return sha256.Sum256(canonical)
}

func decodeHash(value string) ([]byte, error) {
	if !validHash(value) {
		return nil, fmt.Errorf("hash is invalid")
	}
	decoded, err := hex.DecodeString(value[7:])
	if err != nil || len(decoded) != sha256.Size {
		return nil, fmt.Errorf("hash is invalid")
	}
	return decoded, nil
}
