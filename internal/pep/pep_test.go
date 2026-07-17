// SPDX-License-Identifier: Apache-2.0

package pep

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ArdurAI/sith/internal/tenancy"
)

func TestEnforcerAllowsClosedReadAndAudits(t *testing.T) {
	now := time.Date(2026, time.July, 12, 18, 0, 0, 0, time.UTC)
	auditor := &recordingAuditor{}
	enforcer, err := NewEnforcer(Config{
		Hook: AllowReadHook{}, Auditor: auditor, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := enforcer.AuthorizeRead(context.Background(), testScope(t), NewReadInput(VerbFleetCorrelate, []byte("canonical-correlation"))); err != nil {
		t.Fatalf("AuthorizeRead() error = %v", err)
	}
	if len(auditor.events) != 1 {
		t.Fatalf("audit events = %#v, want one allow", auditor.events)
	}
	event := auditor.events[0]
	if !event.At.Equal(now) || event.WorkspaceID != "workspace-a" || event.Actor != "user:reader" ||
		event.Role != tenancy.RoleReader || event.Action != tenancy.ActionRead || event.Verb != VerbFleetCorrelate ||
		event.Verdict != VerdictAllow || event.ReasonCode != "phase-1-read" {
		t.Fatalf("audit event = %#v", event)
	}
}

func TestEnforcerFailsClosedForUnsafePolicyOutcomes(t *testing.T) {
	tests := []struct {
		name     string
		hook     PolicyHook
		verb     Verb
		want     Verdict
		reason   string
		contains string
	}{
		{
			name: "deny", hook: HookFunc(func(context.Context, Request) (Decision, error) {
				return Decision{Verdict: VerdictDeny, ReasonCode: "policy-deny"}, nil
			}), verb: VerbFleetRead, want: VerdictDeny, reason: "policy-deny", contains: "policy denied",
		},
		{
			name: "approval", hook: HookFunc(func(context.Context, Request) (Decision, error) {
				return Decision{Verdict: VerdictRequireApproval, ReasonCode: "approval-required"}, nil
			}), verb: VerbFleetRead, want: VerdictRequireApproval, reason: "approval-required", contains: "requires approval",
		},
		{
			name: "hook error", hook: HookFunc(func(context.Context, Request) (Decision, error) {
				return Decision{}, errors.New("upstream unavailable")
			}), verb: VerbFleetRead, want: VerdictDeny, reason: "hook-error", contains: "policy hook failed",
		},
		{
			name: "invalid decision", hook: HookFunc(func(context.Context, Request) (Decision, error) {
				return Decision{Verdict: "maybe", ReasonCode: "invalid"}, nil
			}), verb: VerbFleetRead, want: VerdictDeny, reason: "invalid-decision", contains: "invalid decision",
		},
		{
			name: "unknown verb", hook: AllowReadHook{}, verb: "fleet.unknown", want: VerdictDeny, reason: "invalid-request", contains: "invalid policy request",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			auditor := &recordingAuditor{}
			enforcer, err := NewEnforcer(Config{Hook: test.hook, Auditor: auditor})
			if err != nil {
				t.Fatal(err)
			}
			err = enforcer.AuthorizeRead(context.Background(), testScope(t), NewReadInput(test.verb, nil))
			if err == nil || !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("AuthorizeRead() error = %v, want %q", err, test.contains)
			}
			if len(auditor.events) != 1 || auditor.events[0].Verdict != test.want || auditor.events[0].ReasonCode != test.reason {
				t.Fatalf("audit events = %#v, want %s/%s", auditor.events, test.want, test.reason)
			}
		})
	}
}

func TestEnforcerFailsClosedWhenAuditCannotRecord(t *testing.T) {
	enforcer, err := NewEnforcer(Config{
		Hook: AllowReadHook{}, Auditor: AuditFunc(func(context.Context, AuditEvent) error { return errors.New("audit offline") }),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := enforcer.AuthorizeRead(context.Background(), testScope(t), NewReadInput(VerbFleetRead, nil)); err == nil || !strings.Contains(err.Error(), "audit policy decision") {
		t.Fatalf("AuthorizeRead() error = %v, want audit failure", err)
	}
}

func TestNewEnforcerRejectsMissingDependencies(t *testing.T) {
	if _, err := NewEnforcer(Config{}); err == nil {
		t.Fatal("NewEnforcer() accepted missing hook and auditor")
	}
	if _, err := NewEnforcer(Config{Hook: AllowReadHook{}}); err == nil {
		t.Fatal("NewEnforcer() accepted missing auditor")
	}
}

func TestDecisionRejectsUnsafeReasonCode(t *testing.T) {
	for _, reasonCode := range []string{"", "reason with spaces", "reason\ncode", "π", "UPPERCASE"} {
		if err := (Decision{Verdict: VerdictAllow, ReasonCode: reasonCode}).Validate(); err == nil {
			t.Fatalf("Decision.Validate() accepted unsafe reason code %q", reasonCode)
		}
	}
}

func TestReadInputBindsCanonicalArgumentsAndRejectsAlteredDigest(t *testing.T) {
	first := NewReadInput(VerbFleetCorrelate, []byte("deployment\x00payments"))
	second := NewReadInput(VerbFleetCorrelate, []byte("deployment\x00payments-canary"))
	if first.ArgumentsDigest == second.ArgumentsDigest || !validDigest(first.ArgumentsDigest) {
		t.Fatalf("argument digests = %q and %q, want distinct valid digests", first.ArgumentsDigest, second.ArgumentsDigest)
	}
	request := Request{WorkspaceID: "workspace-a", Actor: "user:reader", Role: tenancy.RoleReader, Action: tenancy.ActionRead, Verb: VerbFleetRead, ArgumentsDigest: "sha256:ABC"}
	if err := request.Validate(); err == nil {
		t.Fatal("Request.Validate() accepted malformed argument digest")
	}
}

func TestNormalizedAuditRequestErasesPolicyBindingDigest(t *testing.T) {
	request := Request{
		WorkspaceID: "workspace-a", Actor: "user:reader", Role: tenancy.RoleReader,
		Action: tenancy.ActionRead, Verb: VerbFleetRead, ArgumentsDigest: "token=caller-secret",
	}
	normalized, ok := normalizedAuditRequest(request)
	if !ok {
		t.Fatal("normalizedAuditRequest() rejected safe audit identity")
	}
	if normalized.ArgumentsDigest != "" {
		t.Fatalf("normalized audit request retained binding digest %q", normalized.ArgumentsDigest)
	}
}

type recordingAuditor struct {
	events []AuditEvent
}

func (auditor *recordingAuditor) Record(_ context.Context, event AuditEvent) error {
	auditor.events = append(auditor.events, event)
	return nil
}

func testScope(t *testing.T) tenancy.Scope {
	t.Helper()
	principal, err := tenancy.NewPrincipal("user:reader", map[tenancy.WorkspaceID]tenancy.Role{"workspace-a": tenancy.RoleReader})
	if err != nil {
		t.Fatal(err)
	}
	scope, err := principal.Scope("workspace-a")
	if err != nil {
		t.Fatal(err)
	}
	return scope
}
