// SPDX-License-Identifier: Apache-2.0

package pep

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/ArdurAI/sith/internal/tenancy"
	"github.com/ArdurAI/sith/internal/tracing"
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
			record["trace_id"] != "0123456789abcdef0123456789abcdef" || record["workspace"] != "workspace-a" ||
			record["actor"] != "user:reader" || record["verb"] != string(VerbFleetRead) {
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

func TestSlogAuditorReportsHandlerFailure(t *testing.T) {
	t.Parallel()

	want := errors.New("structured audit sink unavailable")
	auditor, err := NewSlogAuditor(slog.New(failingAuditHandler{err: want}))
	if err != nil {
		t.Fatal(err)
	}
	if err := auditor.Record(context.Background(), policyAuditEvent(time.Now().UTC(), VerdictAllow, "phase-1-read")); !errors.Is(err, want) {
		t.Fatalf("Record() error = %v, want handler failure", err)
	}
}

func TestSlogAuditorFailsClosedWhenEventLevelIsDisabled(t *testing.T) {
	t.Parallel()

	auditor, err := NewSlogAuditor(slog.New(slog.NewTextHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelWarn})))
	if err != nil {
		t.Fatal(err)
	}
	if err := auditor.Record(context.Background(), policyAuditEvent(time.Now().UTC(), VerdictAllow, "phase-1-read")); err == nil || !strings.Contains(err.Error(), "level is disabled") {
		t.Fatalf("Record() error = %v, want disabled-level refusal", err)
	}
}

func TestSlogAuditorRejectsUnsafeTraceIdentifierWithoutEmission(t *testing.T) {
	var output bytes.Buffer
	auditor, err := NewSlogAuditor(slog.New(slog.NewTextHandler(&output, nil)))
	if err != nil {
		t.Fatal(err)
	}
	unsafe := policyAuditEvent(time.Now().UTC(), VerdictAllow, "phase-1-read")
	unsafe.TraceID = "workspace-a/token=secret"
	if err := auditor.Record(context.Background(), unsafe); err == nil {
		t.Fatal("Record() accepted unsafe trace identifier")
	}
	if output.Len() != 0 {
		t.Fatalf("unsafe event was emitted: %q", output.String())
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
	for _, field := range []string{"level=WARN", "msg=\"policy decision\"", "audit=true", "surface=hub-pep", "trace_id=0123456789abcdef0123456789abcdef", "workspace=workspace-a", "verdict=require-approval", "reason_code=approval-required"} {
		if !strings.Contains(line, field) {
			t.Fatalf("text audit log missing %q: %q", field, line)
		}
	}
	if strings.Contains(line, "arguments_digest") || strings.Contains(line, "payments") || strings.Contains(line, "token") {
		t.Fatalf("text audit log leaked non-audit data: %q", line)
	}
}

func TestSlogAuditorAcceptsProposalAndConstrainedInvalidRequestEvents(t *testing.T) {
	var output bytes.Buffer
	auditor, err := NewSlogAuditor(slog.New(slog.NewJSONHandler(&output, nil)))
	if err != nil {
		t.Fatal(err)
	}
	proposal := policyAuditEvent(time.Now().UTC(), VerdictAllow, "proposal-allowed")
	proposal.Actor = "user:operator"
	proposal.Role = tenancy.RoleOperator
	proposal.Action = tenancy.ActionProposeIntent
	proposal.Verb = "deployment.restart"
	invalid := proposal
	invalid.Verb = invalidVerb
	invalid.Verdict = VerdictDeny
	invalid.ReasonCode = "invalid-request"
	invalidRead := policyAuditEvent(time.Now().UTC(), VerdictDeny, "invalid-request")
	invalidRead.Verb = invalidVerb
	for _, event := range []AuditEvent{proposal, invalid, invalidRead} {
		if err := auditor.Record(context.Background(), event); err != nil {
			t.Fatalf("Record(%s) error = %v", event.Verb, err)
		}
	}
	logged := output.String()
	for _, field := range []string{`"action":"propose-intent"`, `"verb":"deployment.restart"`, `"verb":"invalid"`} {
		if !strings.Contains(logged, field) {
			t.Fatalf("proposal audit missing %s: %s", field, logged)
		}
	}
	for _, forbidden := range []string{"arguments_digest", "resolved_digest", "payments", "token", "secret"} {
		if strings.Contains(logged, forbidden) {
			t.Fatalf("proposal audit leaked %q: %s", forbidden, logged)
		}
	}
}

func TestAuditEventRejectsUnsafeInvalidRequestSentinel(t *testing.T) {
	base := policyAuditEvent(time.Now().UTC(), VerdictDeny, "invalid-request")
	base.Verb = invalidVerb
	if err := base.Validate(); err != nil {
		t.Fatalf("baseline invalid-request event should validate: %v", err)
	}
	for _, mutate := range []func(*AuditEvent){
		func(event *AuditEvent) { event.Verdict = VerdictAllow },
		func(event *AuditEvent) { event.Verdict = VerdictRequireApproval },
		func(event *AuditEvent) { event.ReasonCode = "policy-deny" },
	} {
		event := base
		mutate(&event)
		if err := event.Validate(); err == nil {
			t.Fatalf("AuditEvent.Validate() accepted unsafe invalid sentinel: %#v", event)
		}
	}
}

func TestAuditEventRejectsImpossibleProposalRoleOutcome(t *testing.T) {
	base := policyAuditEvent(time.Now().UTC(), VerdictDeny, "role-denied")
	base.Action = tenancy.ActionProposeIntent
	base.Verb = "deployment.restart"
	if err := base.Validate(); err != nil {
		t.Fatalf("role-denied proposal event should validate: %v", err)
	}

	invalidRequest := base
	invalidRequest.ReasonCode = "invalid-request"
	if err := invalidRequest.Validate(); err != nil {
		t.Fatalf("pre-role invalid-request event should validate: %v", err)
	}

	for _, mutate := range []func(*AuditEvent){
		func(event *AuditEvent) { event.Verdict = VerdictAllow; event.ReasonCode = "proposal-allowed" },
		func(event *AuditEvent) {
			event.Verdict = VerdictRequireApproval
			event.ReasonCode = "approval-required"
		},
		func(event *AuditEvent) { event.ReasonCode = "policy-deny" },
	} {
		event := base
		mutate(&event)
		if err := event.Validate(); err == nil {
			t.Fatalf("AuditEvent.Validate() accepted impossible role outcome: %#v", event)
		}
	}
}

func policyAuditEvent(at time.Time, verdict Verdict, reason string) AuditEvent {
	return AuditEvent{
		At: at, TraceID: tracing.ID("0123456789abcdef0123456789abcdef"), WorkspaceID: "workspace-a", Actor: "user:reader", Role: tenancy.RoleReader,
		Action: tenancy.ActionRead, Verb: VerbFleetRead, Verdict: verdict, ReasonCode: reason,
	}
}

type failingAuditHandler struct {
	err error
}

func (handler failingAuditHandler) Enabled(context.Context, slog.Level) bool { return true }

func (handler failingAuditHandler) Handle(context.Context, slog.Record) error { return handler.err }

func (handler failingAuditHandler) WithAttrs([]slog.Attr) slog.Handler { return handler }

func (handler failingAuditHandler) WithGroup(string) slog.Handler { return handler }
