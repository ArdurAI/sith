// SPDX-License-Identifier: Apache-2.0

package auditrecord

import (
	"strings"
	"testing"
	"time"

	"github.com/ArdurAI/sith/internal/tenancy"
)

func TestExportValidateForWorkspaceAcceptsClosedPortableChain(t *testing.T) {
	t.Parallel()

	exported := validTestExport()
	if err := exported.ValidateForWorkspace("workspace-a"); err != nil {
		t.Fatalf("ValidateForWorkspace() error = %v", err)
	}

	approval := validTestExport()
	approval.Entries[0].FormatVersion = 2
	approval.Entries[0].Role = "approver"
	approval.Entries[0].Action = "approve-intent"
	approval.Entries[0].Verb = "approval.grant"
	approval.Entries[0].ReasonCode = "approval-created"
	approval.Entries[0].EventKind = "approval-created"
	approval.Entries[0].EvidenceDigest = hash("b")
	if err := approval.ValidateForWorkspace("workspace-a"); err != nil {
		t.Fatalf("approval ValidateForWorkspace() error = %v", err)
	}
}

func TestExportValidateForWorkspaceRejectsForeignAndMalformedDocuments(t *testing.T) {
	t.Parallel()

	tests := map[string]func(*Export){
		"foreign workspace": func(value *Export) { value.WorkspaceID = "workspace-b" },
		"unknown schema":    func(value *Export) { value.Schema = "caller-schema" },
		"wrong head count":  func(value *Export) { value.Chain.HeadSequence = 2 },
		"wrong head hash":   func(value *Export) { value.Chain.HeadHash = hash("b") },
		"wrong sequence":    func(value *Export) { value.Entries[0].Sequence = 2 },
		"broken link":       func(value *Export) { value.Entries[0].PreviousHash = hash("b") },
		"unsafe actor":      func(value *Export) { value.Entries[0].Actor = "user:alice\ntoken=secret" },
		"unknown role":      func(value *Export) { value.Entries[0].Role = "owner" },
		"ordinary audit verb as read": func(value *Export) {
			value.Entries[0].Action = "read"
			value.Entries[0].Verb = "audit.export"
		},
		"approval payload": func(value *Export) {
			value.Entries[0].FormatVersion = 2
			value.Entries[0].EvidenceDigest = "token=secret"
		},
		"oversized": func(value *Export) {
			value.Entries = make([]Entry, MaxEntries+1)
			value.Chain.HeadSequence = int64(len(value.Entries))
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			value := validTestExport()
			mutate(&value)
			if err := value.ValidateForWorkspace(tenancy.WorkspaceID("workspace-a")); err == nil {
				t.Fatalf("malformed export accepted: %#v", value)
			}
		})
	}
}

func validTestExport() Export {
	entryHash := hash("a")
	return Export{
		Schema: SchemaV1, WorkspaceID: "workspace-a",
		Chain: Chain{HashAlgorithm: HashAlgorithm, HeadSequence: 1, HeadHash: entryHash},
		Entries: []Entry{{
			Sequence: 1, FormatVersion: 1, RecordedAt: time.Date(2026, time.July, 18, 9, 30, 0, 0, time.UTC),
			TraceID: strings.Repeat("1", 32), Actor: "user:alice", Role: "admin", Action: "export-audit",
			Verb: "audit.export", Verdict: "allow", ReasonCode: "phase-1-audit-export",
			EventKind: "policy-decision", PreviousHash: hash("0"), EntryHash: entryHash,
		}},
	}
}

func hash(character string) string { return "sha256:" + strings.Repeat(character, 64) }
