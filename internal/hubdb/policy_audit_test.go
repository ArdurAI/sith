// SPDX-License-Identifier: Apache-2.0

package hubdb

import (
	"bytes"
	"context"
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

func policyAuditTestEntry() policyAuditEntry {
	return policyAuditEntry{
		sequence: 1, format: policyAuditFormatVersion,
		recordedAt:  time.Date(2026, time.July, 17, 12, 34, 56, 123456000, time.UTC),
		traceID:     tracing.ID("0123456789abcdef0123456789abcdef"),
		workspaceID: "workspace-a", actor: "user:alice", role: tenancy.RoleReader,
		action: tenancy.ActionRead, verb: pep.VerbFleetRead, verdict: pep.VerdictAllow,
		reasonCode: "phase-1-read", previousHash: make([]byte, 32),
	}
}
