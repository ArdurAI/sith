// SPDX-License-Identifier: Apache-2.0

package auditrecord

import (
	"crypto/sha256"
	"encoding/hex"
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
		"sub-microsecond time": func(value *Export) {
			value.Entries[0].RecordedAt = value.Entries[0].RecordedAt.Add(time.Nanosecond)
		},
		"non-UTC time": func(value *Export) {
			value.Entries[0].RecordedAt = value.Entries[0].RecordedAt.In(time.FixedZone("offset", -5*60*60))
		},
		"unsafe actor": func(value *Export) { value.Entries[0].Actor = "user:alice\ntoken=secret" },
		"invalid UTF-8 actor": func(value *Export) {
			value.Entries[0].Actor = string([]byte{'u', 's', 'e', 'r', ':', 0xff})
		},
		"unknown role": func(value *Export) { value.Entries[0].Role = "owner" },
		"ordinary audit verb as read": func(value *Export) {
			value.Entries[0].Action = "read"
			value.Entries[0].Verb = "audit.export"
		},
		"approval payload": func(value *Export) {
			value.Entries[0].FormatVersion = 2
			value.Entries[0].EvidenceDigest = "token=secret"
		},
		"unknown invalid-request action": func(value *Export) {
			value.Entries[0].Action = "caller-action"
			value.Entries[0].Verb = "invalid"
			value.Entries[0].Verdict = "deny"
			value.Entries[0].ReasonCode = "invalid-request"
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

func TestRecomputeEntryHashGoldenFormats(t *testing.T) {
	t.Parallel()

	mixed := validMixedTestExport()
	tests := []struct {
		name  string
		entry Entry
		want  string
	}{
		{name: "format 1 policy decision", entry: mixed.Entries[0], want: "sha256:67544ba8ac180f834bc221aa136c7d121c0e63228b02bc7c7dce2508de26c4ea"},
		{name: "format 2 approval lifecycle", entry: mixed.Entries[1], want: "sha256:dfbfb98dda5768b259314faa5ed57f40ae4575c466e899ad79c23cea06277ece"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := RecomputeEntryHash("workspace-a", test.entry)
			if err != nil {
				t.Fatalf("RecomputeEntryHash() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("RecomputeEntryHash() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestExportVerifyRecomputesEveryEntryAndHead(t *testing.T) {
	t.Parallel()

	if err := validMixedTestExport().Verify(); err != nil {
		t.Fatalf("Verify() error = %v", err)
	}

	tests := map[string]func(*Export){
		"workspace":     func(value *Export) { value.WorkspaceID = "workspace-b" },
		"recorded time": func(value *Export) { value.Entries[0].RecordedAt = value.Entries[0].RecordedAt.Add(time.Second) },
		"sub-microsecond time": func(value *Export) {
			value.Entries[0].RecordedAt = value.Entries[0].RecordedAt.Add(time.Nanosecond)
		},
		"non-UTC time": func(value *Export) {
			value.Entries[0].RecordedAt = value.Entries[0].RecordedAt.In(time.FixedZone("offset", -5*60*60))
		},
		"trace identifier": func(value *Export) { value.Entries[0].TraceID = strings.Repeat("2", 32) },
		"actor":            func(value *Export) { value.Entries[0].Actor = "user:mallory" },
		"role":             func(value *Export) { value.Entries[0].Role = "operator" },
		"action":           func(value *Export) { value.Entries[0].Action = "read" },
		"verb":             func(value *Export) { value.Entries[0].Verb = "fleet.read" },
		"verdict":          func(value *Export) { value.Entries[0].Verdict = "deny" },
		"reason":           func(value *Export) { value.Entries[0].ReasonCode = "role-denied" },
		"event kind":       func(value *Export) { value.Entries[1].EventKind = "approval-consumed" },
		"evidence":         func(value *Export) { value.Entries[1].EvidenceDigest = hash("c") },
		"entry hash":       func(value *Export) { value.Entries[1].EntryHash = hash("d"); value.Chain.HeadHash = hash("d") },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			value := validMixedTestExport()
			mutate(&value)
			if err := value.Verify(); err == nil {
				t.Fatal("Verify() accepted a tampered export")
			}
		})
	}
}

func FuzzExportVerifyBindsCanonicalFields(f *testing.F) {
	f.Add(uint8(0), "seed")
	f.Fuzz(func(t *testing.T, field uint8, seed string) {
		value := validMixedTestExport()
		value.Entries = append([]Entry(nil), value.Entries...)
		digest := sha256.Sum256([]byte(seed))
		encoded := hex.EncodeToString(digest[:])
		switch field % 14 {
		case 0:
			value.Entries[0].Sequence++
		case 1:
			entry := &value.Entries[0]
			entry.FormatVersion = 2
			entry.Actor, entry.Role, entry.Action = "user:approver", "approver", "approve-intent"
			entry.Verb, entry.Verdict, entry.ReasonCode = "approval.grant", "allow", "approval-created"
			entry.EventKind, entry.EvidenceDigest = "approval-created", hash("e")
		case 2:
			value.Entries[0].RecordedAt = value.Entries[0].RecordedAt.Add(time.Microsecond)
		case 3:
			value.Entries[0].TraceID = encoded[:32]
		case 4:
			value.WorkspaceID = "workspace-" + encoded[:16]
		case 5:
			value.Entries[0].Actor = "user:" + encoded
		case 6:
			entry := &value.Entries[0]
			entry.Role, entry.Action, entry.Verb = "operator", "propose-intent", "deployment.restart"
		case 7:
			entry := &value.Entries[0]
			entry.Action, entry.Verb = "read", "fleet.read"
		case 8:
			value.Entries[0].Verdict, value.Entries[0].ReasonCode = "deny", "policy-deny"
		case 9:
			value.Entries[0].ReasonCode = "phase-1-read"
		case 10:
			value.Entries[0].PreviousHash = hash("f")
		case 11:
			value.Entries[1].EntryHash, value.Chain.HeadHash = hash("d"), hash("d")
		case 12:
			entry := &value.Entries[1]
			entry.Actor, entry.Role, entry.Action = "user:operator", "operator", "propose-intent"
			entry.ReasonCode, entry.EventKind = "approval-consumed", "approval-consumed"
		case 13:
			value.Entries[1].EvidenceDigest = "sha256:" + encoded
		}
		if err := value.Verify(); err == nil {
			t.Fatal("Verify() accepted a canonical-field mutation without rehashing")
		}
	})
}

func validTestExport() Export {
	exported := Export{
		Schema: SchemaV1, WorkspaceID: "workspace-a",
		Chain: Chain{HashAlgorithm: HashAlgorithm, HeadSequence: 1},
		Entries: []Entry{{
			Sequence: 1, FormatVersion: 1, RecordedAt: time.Date(2026, time.July, 18, 9, 30, 0, 0, time.UTC),
			TraceID: strings.Repeat("1", 32), Actor: "user:alice", Role: "admin", Action: "export-audit",
			Verb: "audit.export", Verdict: "allow", ReasonCode: "phase-1-audit-export",
			EventKind: "policy-decision", PreviousHash: hash("0"),
		}},
	}
	entryHash, err := RecomputeEntryHash("workspace-a", exported.Entries[0])
	if err != nil {
		panic(err)
	}
	exported.Entries[0].EntryHash = entryHash
	exported.Chain.HeadHash = entryHash
	return exported
}

func validMixedTestExport() Export {
	exported := validTestExport()
	second := Entry{
		Sequence: 2, FormatVersion: 2,
		RecordedAt: time.Date(2026, time.July, 18, 9, 31, 0, 123456000, time.UTC),
		TraceID:    strings.Repeat("2", 32), Actor: "user:bob", Role: "approver", Action: "approve-intent",
		Verb: "approval.grant", Verdict: "allow", ReasonCode: "approval-created",
		EventKind: "approval-created", EvidenceDigest: hash("b"), PreviousHash: exported.Entries[0].EntryHash,
	}
	secondHash, err := RecomputeEntryHash("workspace-a", second)
	if err != nil {
		panic(err)
	}
	second.EntryHash = secondHash
	exported.Entries = append(exported.Entries, second)
	exported.Chain.HeadSequence = 2
	exported.Chain.HeadHash = secondHash
	return exported
}

func hash(character string) string { return "sha256:" + strings.Repeat(character, 64) }
