// SPDX-License-Identifier: Apache-2.0

package pep

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/ArdurAI/sith/internal/tenancy"
)

func TestSlogAuditorEmitsSanitizedStructuredPolicyEvents(t *testing.T) {
	var output bytes.Buffer
	auditor, err := NewSlogAuditor(slog.New(slog.NewJSONHandler(&output, nil)))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Add(-3 * time.Second).Truncate(time.Second)
	for _, event := range []AuditEvent{
		policyAuditEvent(now, VerdictAllow, "phase-1-read"),
		policyAuditEvent(now.Add(time.Second), VerdictDeny, "policy-deny"),
		policyAuditEvent(now.Add(2*time.Second), VerdictRequireApproval, "approval-required"),
	} {
		if err := auditor.Record(context.Background(), event); err != nil {
			t.Fatalf("Record() error = %v", err)
		}
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("log lines = %q", output.String())
	}
	for index, line := range lines {
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("decode log %d: %v", index, err)
		}
		if record["msg"] != "policy decision" || record["audit"] != true || record["surface"] != "hub-pep" ||
			record["workspace"] != "workspace-a" || record["actor"] != "user:reader" || record["verb"] != string(VerbFleetRead) {
			t.Fatalf("record %d = %#v", index, record)
		}
		if _, exists := record["arguments_digest"]; exists || strings.Contains(line, "payments") || strings.Contains(line, "token") {
			t.Fatalf("record %d leaked non-audit data: %s", index, line)
		}
	}
	if !strings.Contains(lines[0], "\"level\":\"INFO\"") || !strings.Contains(lines[1], "\"level\":\"WARN\"") ||
		!strings.Contains(lines[2], "\"level\":\"WARN\"") {
		t.Fatalf("unexpected policy audit severities: %q", output.String())
	}
}

func TestSlogAuditorRejectsUnsafeEventsWithoutEmission(t *testing.T) {
	var output bytes.Buffer
	auditor, err := NewSlogAuditor(slog.New(slog.NewTextHandler(&output, nil)))
	if err != nil {
		t.Fatal(err)
	}
	unsafe := policyAuditEvent(time.Now().UTC(), VerdictAllow, "phase-1-read")
	unsafe.ReasonCode = "token=secret"
	if err := auditor.Record(context.Background(), unsafe); err == nil {
		t.Fatal("Record() accepted unsafe reason code")
	}
	if output.Len() != 0 {
		t.Fatalf("unsafe event was emitted: %q", output.String())
	}
	if _, err := NewSlogAuditor(nil); err == nil {
		t.Fatal("NewSlogAuditor() accepted nil logger")
	}
}

func TestSlogAuditorTextHandlerPreservesSafeFieldsAndSeverity(t *testing.T) {
	var output bytes.Buffer
	auditor, err := NewSlogAuditor(slog.New(slog.NewTextHandler(&output, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if err := auditor.Record(context.Background(), policyAuditEvent(time.Now().UTC(), VerdictRequireApproval, "approval-required")); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	line := output.String()
	for _, field := range []string{"level=WARN", "msg=\"policy decision\"", "audit=true", "surface=hub-pep", "workspace=workspace-a", "verdict=require-approval", "reason_code=approval-required"} {
		if !strings.Contains(line, field) {
			t.Fatalf("text audit log missing %q: %q", field, line)
		}
	}
	if strings.Contains(line, "arguments_digest") || strings.Contains(line, "payments") || strings.Contains(line, "token") {
		t.Fatalf("text audit log leaked non-audit data: %q", line)
	}
}

func policyAuditEvent(at time.Time, verdict Verdict, reason string) AuditEvent {
	return AuditEvent{
		At: at, WorkspaceID: "workspace-a", Actor: "user:reader", Role: tenancy.RoleReader,
		Action: tenancy.ActionRead, Verb: VerbFleetRead, Verdict: verdict, ReasonCode: reason,
	}
}
