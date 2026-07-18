// SPDX-License-Identifier: Apache-2.0

package hubdb

import (
	"bytes"
	"context"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/ArdurAI/sith/internal/pep"
	"github.com/ArdurAI/sith/internal/tenancy"
	"github.com/ArdurAI/sith/internal/tracing"
)

func TestPolicyAuditEntryHashBindsEveryField(t *testing.T) {
	t.Parallel()

	base := policyAuditTestEntry()
	baseline := policyAuditEntryHash(base)
	if len(baseline) != 32 || !bytes.Equal(baseline, policyAuditEntryHash(base)) {
		t.Fatal("policy audit entry hash is not a stable SHA-256 digest")
	}
	want, err := hex.DecodeString("061c65c9a8af1fa2a8334e86c10f82845c4d8bbeb3f2cf840b2be47c7e1edc8d")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(baseline, want) {
		t.Fatalf("format 1 policy audit hash changed: got %x, want %x", baseline, want)
	}

	mutations := map[string]func(*policyAuditEntry){
		"sequence":      func(entry *policyAuditEntry) { entry.sequence++ },
		"format":        func(entry *policyAuditEntry) { entry.format++ },
		"recorded time": func(entry *policyAuditEntry) { entry.recordedAt = entry.recordedAt.Add(time.Microsecond) },
		"trace":         func(entry *policyAuditEntry) { entry.traceID = "ffffffffffffffffffffffffffffffff" },
		"workspace":     func(entry *policyAuditEntry) { entry.workspaceID = "workspace-b" },
		"actor":         func(entry *policyAuditEntry) { entry.actor = "user:bob" },
		"role":          func(entry *policyAuditEntry) { entry.role = tenancy.RoleAdmin },
		"action":        func(entry *policyAuditEntry) { entry.action = tenancy.ActionProposeIntent },
		"verb":          func(entry *policyAuditEntry) { entry.verb = "deployment.restart" },
		"verdict":       func(entry *policyAuditEntry) { entry.verdict = pep.VerdictDeny },
		"reason":        func(entry *policyAuditEntry) { entry.reasonCode = "policy-deny" },
		"previous hash": func(entry *policyAuditEntry) { entry.previousHash[0] = 1 },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			changed := base
			changed.previousHash = bytes.Clone(base.previousHash)
			mutate(&changed)
			if bytes.Equal(baseline, policyAuditEntryHash(changed)) {
				t.Fatalf("hash did not bind %s", name)
			}
		})
	}
}

func TestApprovalAuditEntryHashBindsLifecycleMetadata(t *testing.T) {
	t.Parallel()

	base := policyAuditTestEntry()
	base.format = approvalAuditFormatVersion
	base.role = tenancy.RoleApprover
	base.action = tenancy.ActionApproveIntent
	base.verb = approvalAuditVerb
	base.reasonCode = approvalCreatedEventKind
	base.eventKind = approvalCreatedEventKind
	base.evidence = "sha256:" + strings.Repeat("a", 64)
	baseline := policyAuditEntryHash(base)

	for name, mutate := range map[string]func(*policyAuditEntry){
		"event kind": func(entry *policyAuditEntry) {
			entry.eventKind = approvalConsumedEventKind
			entry.reasonCode = approvalConsumedEventKind
		},
		"evidence": func(entry *policyAuditEntry) {
			entry.evidence = "sha256:" + strings.Repeat("b", 64)
		},
	} {
		t.Run(name, func(t *testing.T) {
			changed := base
			mutate(&changed)
			if bytes.Equal(baseline, policyAuditEntryHash(changed)) {
				t.Fatalf("format 2 hash did not bind %s", name)
			}
		})
	}
}

func TestApprovalGrantEvidenceDigestBindsImmutableGrant(t *testing.T) {
	t.Parallel()

	approvedAt := time.Date(2026, time.July, 17, 12, 34, 56, 123456000, time.UTC)
	base := approvalGrantEvidenceDigest(
		"workspace-a", "AAAAAAAAAAAAAAAAAAAAAA", "intent-a", "user:operator",
		"user:approver", "sha256:"+strings.Repeat("a", 64), approvedAt,
	)
	if !approvalEvidencePattern.MatchString(base) {
		t.Fatalf("evidence digest = %q, want canonical SHA-256", base)
	}
	mutations := []string{
		approvalGrantEvidenceDigest("workspace-b", "AAAAAAAAAAAAAAAAAAAAAA", "intent-a", "user:operator", "user:approver", "sha256:"+strings.Repeat("a", 64), approvedAt),
		approvalGrantEvidenceDigest("workspace-a", "BBBBBBBBBBBBBBBBBBBBBB", "intent-a", "user:operator", "user:approver", "sha256:"+strings.Repeat("a", 64), approvedAt),
		approvalGrantEvidenceDigest("workspace-a", "AAAAAAAAAAAAAAAAAAAAAA", "intent-b", "user:operator", "user:approver", "sha256:"+strings.Repeat("a", 64), approvedAt),
		approvalGrantEvidenceDigest("workspace-a", "AAAAAAAAAAAAAAAAAAAAAA", "intent-a", "user:other", "user:approver", "sha256:"+strings.Repeat("a", 64), approvedAt),
		approvalGrantEvidenceDigest("workspace-a", "AAAAAAAAAAAAAAAAAAAAAA", "intent-a", "user:operator", "user:other", "sha256:"+strings.Repeat("a", 64), approvedAt),
		approvalGrantEvidenceDigest("workspace-a", "AAAAAAAAAAAAAAAAAAAAAA", "intent-a", "user:operator", "user:approver", "sha256:"+strings.Repeat("b", 64), approvedAt),
		approvalGrantEvidenceDigest("workspace-a", "AAAAAAAAAAAAAAAAAAAAAA", "intent-a", "user:operator", "user:approver", "sha256:"+strings.Repeat("a", 64), approvedAt.Add(time.Microsecond)),
	}
	for index, changed := range mutations {
		if changed == base {
			t.Fatalf("evidence digest did not bind immutable field %d", index)
		}
	}
}

func TestPolicyAuditBoundaryRejectsMissingDatabaseAndInvalidScope(t *testing.T) {
	t.Parallel()

	if err := (*AppDB)(nil).Record(context.Background(), pep.AuditEvent{}); err == nil {
		t.Fatal("nil database accepted a policy audit event")
	}
	if err := (*AppDB)(nil).VerifyPolicyAuditChain(context.Background(), tenancy.Scope{}); err == nil {
		t.Fatal("nil database verified a policy audit chain")
	}
	if _, err := auditEventScope(pep.AuditEvent{}); err == nil {
		t.Fatal("invalid audit event produced a workspace scope")
	}
}

func FuzzPolicyAuditEntryHashUsesLengthFraming(f *testing.F) {
	f.Add("user:alice", "phase-1-read")
	f.Add("a", "bc")
	f.Fuzz(func(t *testing.T, left, right string) {
		if right == "" || len(left)+len(right) > 512 {
			t.Skip()
		}
		first := policyAuditTestEntry()
		first.actor, first.reasonCode = left, right
		second := policyAuditTestEntry()
		second.actor, second.reasonCode = left+right, ""
		if bytes.Equal(policyAuditEntryHash(first), policyAuditEntryHash(second)) {
			t.Fatal("length-delimited audit fields produced an ambiguous digest")
		}
	})
}

func FuzzApprovalGrantEvidenceDigestUsesLengthFraming(f *testing.F) {
	f.Add("a", "bc")
	f.Add("user:operator", "user:approver")
	f.Fuzz(func(t *testing.T, left, right string) {
		if left == "" || right == "" || len(left)+len(right) > 512 {
			t.Skip()
		}
		approvedAt := time.Date(2026, time.July, 17, 12, 34, 56, 123456000, time.UTC)
		first := approvalGrantEvidenceDigest(
			"workspace-a", "AAAAAAAAAAAAAAAAAAAAAA", left, right, "user:approver",
			"sha256:"+strings.Repeat("a", 64), approvedAt,
		)
		second := approvalGrantEvidenceDigest(
			"workspace-a", "AAAAAAAAAAAAAAAAAAAAAA", left+right, "user:operator", "user:approver",
			"sha256:"+strings.Repeat("a", 64), approvedAt,
		)
		if first == second {
			t.Fatal("length-delimited approval evidence fields produced an ambiguous digest")
		}
	})
}

func policyAuditTestEntry() policyAuditEntry {
	return policyAuditEntry{
		sequence: 1, format: policyAuditFormatVersion,
		recordedAt:  time.Date(2026, time.July, 17, 12, 34, 56, 123456000, time.UTC),
		traceID:     tracing.ID("0123456789abcdef0123456789abcdef"),
		workspaceID: "workspace-a", actor: "user:alice", role: tenancy.RoleReader,
		action: tenancy.ActionRead, verb: pep.VerbFleetRead, verdict: pep.VerdictAllow,
		reasonCode: "phase-1-read", eventKind: policyDecisionEventKind,
		previousHash: make([]byte, 32),
	}
}
