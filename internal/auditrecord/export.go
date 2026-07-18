// SPDX-License-Identifier: Apache-2.0

// Package auditrecord defines the portable, privacy-minimized audit export contract shared by the
// durable store and authenticated HTTP boundary.
package auditrecord

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/ArdurAI/sith/internal/intent"
	"github.com/ArdurAI/sith/internal/pep"
	"github.com/ArdurAI/sith/internal/tenancy"
)

const (
	// SchemaV1 identifies the first portable retained-chain document.
	SchemaV1 = "sith.policy-audit-export/v1"
	// HashAlgorithm identifies the digest used by the retained chain.
	HashAlgorithm = "sha-256"
	// MaxEntries bounds one synchronous online export before database or network work begins.
	MaxEntries = 512
)

// Export is one complete, verified workspace snapshot. It is constructed only after the backing
// repeatable-read transaction commits, so encoding it cannot pin a database transaction.
type Export struct {
	Schema      string  `json:"schema"`
	WorkspaceID string  `json:"workspace_id"`
	Chain       Chain   `json:"chain"`
	Entries     []Entry `json:"entries"`
}

// Chain identifies the exact retained-history head covered by an export.
type Chain struct {
	HashAlgorithm string `json:"hash_algorithm"`
	HeadSequence  int64  `json:"head_sequence"`
	HeadHash      string `json:"head_hash"`
}

// Entry is the privacy-minimized, independently rehashable projection of one retained event. The
// workspace is carried once by Export and is nevertheless bound into each entry hash.
type Entry struct {
	Sequence       int64     `json:"sequence"`
	FormatVersion  int16     `json:"format_version"`
	RecordedAt     time.Time `json:"recorded_at"`
	TraceID        string    `json:"trace_id"`
	Actor          string    `json:"actor"`
	Role           string    `json:"role"`
	Action         string    `json:"action"`
	Verb           string    `json:"verb"`
	Verdict        string    `json:"verdict"`
	ReasonCode     string    `json:"reason_code"`
	EventKind      string    `json:"event_kind"`
	EvidenceDigest string    `json:"evidence_digest"`
	PreviousHash   string    `json:"previous_hash"`
	EntryHash      string    `json:"entry_hash"`
}

// ValidateForWorkspace rechecks the portable disclosure boundary independently of the backing
// store. Cryptographic recomputation remains the exporter's responsibility; this check rejects a
// foreign, oversized, discontinuous, or unsupported document before HTTP serialization.
func (export Export) ValidateForWorkspace(workspaceID tenancy.WorkspaceID) error {
	if export.Schema != SchemaV1 || export.WorkspaceID != string(workspaceID) ||
		workspaceInvalid(workspaceID) || export.Chain.HashAlgorithm != HashAlgorithm ||
		len(export.Entries) == 0 || len(export.Entries) > MaxEntries ||
		export.Chain.HeadSequence != int64(len(export.Entries)) || !validHash(export.Chain.HeadHash) {
		return fmt.Errorf("audit export envelope is invalid")
	}
	previous := "sha256:" + strings.Repeat("0", 64)
	for index, entry := range export.Entries {
		sequence := int64(index + 1)
		if entry.Sequence != sequence || entry.RecordedAt.IsZero() || !validTraceID(entry.TraceID) ||
			!safeText(entry.Actor, 256) || !validRole(entry.Role) || !validVerdict(entry.Verdict) ||
			!validReasonCode(entry.ReasonCode) || entry.PreviousHash != previous || !validHash(entry.EntryHash) ||
			!validEntryShape(entry) {
			return fmt.Errorf("audit export entry %d is invalid", sequence)
		}
		previous = entry.EntryHash
	}
	if previous != export.Chain.HeadHash {
		return fmt.Errorf("audit export head is invalid")
	}
	return nil
}

func workspaceInvalid(workspaceID tenancy.WorkspaceID) bool {
	return tenancy.ValidateWorkspaceID(workspaceID) != nil
}

func validEntryShape(entry Entry) bool {
	role := tenancy.Role(entry.Role)
	action := tenancy.Action(entry.Action)
	verdict := pep.Verdict(entry.Verdict)
	switch entry.FormatVersion {
	case 1:
		if entry.EventKind != "policy-decision" || entry.EvidenceDigest != "" {
			return false
		}
		verb := pep.Verb(entry.Verb)
		if verb == "invalid" {
			return verdict == pep.VerdictDeny && entry.ReasonCode == "invalid-request"
		}
		switch action {
		case tenancy.ActionRead:
			if !verb.Valid() || verb == pep.VerbAuditExport {
				return false
			}
		case tenancy.ActionExportAudit:
			if verb != pep.VerbAuditExport {
				return false
			}
		case tenancy.ActionProposeIntent:
			if !intent.Verb(entry.Verb).Valid() {
				return false
			}
		default:
			return false
		}
		if !role.Allows(action) && (verdict != pep.VerdictDeny || (entry.ReasonCode != "role-denied" && entry.ReasonCode != "invalid-request")) {
			return false
		}
		return true
	case 2:
		if !validHash(entry.EvidenceDigest) || entry.Verb != "approval.grant" || verdict != pep.VerdictAllow ||
			entry.ReasonCode != entry.EventKind {
			return false
		}
		return (entry.EventKind == "approval-created" && role == tenancy.RoleApprover && action == tenancy.ActionApproveIntent) ||
			(entry.EventKind == "approval-consumed" && role == tenancy.RoleOperator && action == tenancy.ActionProposeIntent)
	default:
		return false
	}
}

func validRole(value string) bool { return tenancy.Role(value).Valid() }

func validVerdict(value string) bool {
	switch pep.Verdict(value) {
	case pep.VerdictAllow, pep.VerdictDeny, pep.VerdictRequireApproval:
		return true
	default:
		return false
	}
}

func validTraceID(value string) bool {
	if len(value) != 32 {
		return false
	}
	return lowercaseHex(value)
}

func validHash(value string) bool {
	return len(value) == 71 && strings.HasPrefix(value, "sha256:") && lowercaseHex(value[7:])
}

func lowercaseHex(value string) bool {
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func safeText(value string, maximum int) bool {
	if value == "" || strings.TrimSpace(value) != value || len(value) > maximum {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func validReasonCode(value string) bool {
	if !safeText(value, 64) {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') &&
			character != '-' && character != '_' && character != '.' {
			return false
		}
	}
	return true
}
