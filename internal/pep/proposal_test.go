// SPDX-License-Identifier: Apache-2.0

package pep

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/ArdurAI/sith/internal/fleet"
	"github.com/ArdurAI/sith/internal/intent"
	"github.com/ArdurAI/sith/internal/tenancy"
)

func TestEnforcerAuthorizesBoundTypedProposalAndAudits(t *testing.T) {
	input := testProposalInput(t)
	var captured Request
	auditor := &recordingAuditor{}
	enforcer, err := NewEnforcer(Config{
		Hook: HookFunc(func(_ context.Context, request Request) (Decision, error) {
			captured = request
			return Decision{Verdict: VerdictAllow, ReasonCode: "proposal-allowed"}, nil
		}),
		Auditor: auditor,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := enforcer.AuthorizeProposal(context.Background(), operatorScope(t), input); err != nil {
		t.Fatalf("AuthorizeProposal() error = %v", err)
	}
	if captured.WorkspaceID != "workspace-a" || captured.Actor != "user:operator" || captured.Role != tenancy.RoleOperator ||
		captured.Action != tenancy.ActionProposeIntent || captured.Verb != Verb(intent.VerbDeploymentRestart) ||
		captured.ArgumentsDigest != input.resolvedDigest || !validDigest(captured.ArgumentsDigest) {
		t.Fatalf("policy request = %#v", captured)
	}
	if len(auditor.events) != 1 {
		t.Fatalf("audit events = %#v, want one", auditor.events)
	}
	event := auditor.events[0]
	if event.WorkspaceID != "workspace-a" || event.Actor != "user:operator" || event.Role != tenancy.RoleOperator ||
		event.Action != tenancy.ActionProposeIntent || event.Verb != Verb(intent.VerbDeploymentRestart) ||
		event.Verdict != VerdictAllow || event.ReasonCode != "proposal-allowed" {
		t.Fatalf("audit event = %#v", event)
	}
}

func TestAllowReadHookDeniesProposalByDefault(t *testing.T) {
	enforcer, err := NewEnforcer(Config{Hook: AllowReadHook{}, Auditor: &recordingAuditor{}})
	if err != nil {
		t.Fatal(err)
	}
	err = enforcer.AuthorizeProposal(context.Background(), operatorScope(t), testProposalInput(t))
	if !errors.Is(err, ErrDenied) {
		t.Fatalf("AuthorizeProposal() error = %v, want ErrDenied", err)
	}
}

func TestEnforcerRejectsNilProposalContext(t *testing.T) {
	var hookCalls atomic.Int64
	var auditCalls atomic.Int64
	enforcer, err := NewEnforcer(Config{
		Hook: HookFunc(func(context.Context, Request) (Decision, error) {
			hookCalls.Add(1)
			return Decision{Verdict: VerdictAllow, ReasonCode: "unexpected"}, nil
		}),
		Auditor: AuditFunc(func(context.Context, AuditEvent) error {
			auditCalls.Add(1)
			return nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	var nilContext context.Context
	if err := enforcer.AuthorizeProposal(nilContext, operatorScope(t), testProposalInput(t)); err == nil || !strings.Contains(err.Error(), "context are required") {
		t.Fatalf("AuthorizeProposal(nil) error = %v", err)
	}
	if hookCalls.Load() != 0 || auditCalls.Load() != 0 {
		t.Fatalf("nil-context hook/audit calls = %d/%d, want zero", hookCalls.Load(), auditCalls.Load())
	}
}

func TestEnforcerFailsClosedForUnsafeProposalPolicyOutcomes(t *testing.T) {
	tests := []struct {
		name       string
		hook       PolicyHook
		want       Verdict
		reason     string
		wantError  error
		contains   string
		auditFails bool
	}{
		{
			name: "deny", hook: proposalDecision(VerdictDeny, "policy-deny"),
			want: VerdictDeny, reason: "policy-deny", wantError: ErrDenied,
		},
		{
			name: "approval", hook: proposalDecision(VerdictRequireApproval, "approval-required"),
			want: VerdictRequireApproval, reason: "approval-required", wantError: ErrApprovalRequired,
		},
		{
			name: "hook error", hook: HookFunc(func(context.Context, Request) (Decision, error) {
				return Decision{}, errors.New("pdp token=secret")
			}),
			want: VerdictDeny, reason: "hook-error", contains: "policy hook failed",
		},
		{
			name: "invalid decision", hook: proposalDecision("maybe", "invalid"),
			want: VerdictDeny, reason: "invalid-decision", contains: "invalid decision",
		},
		{
			name: "audit failure", hook: proposalDecision(VerdictAllow, "proposal-allowed"),
			contains: "audit policy decision", auditFails: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			auditor := &recordingAuditor{}
			var sink Auditor = auditor
			if test.auditFails {
				sink = AuditFunc(func(context.Context, AuditEvent) error { return errors.New("audit unavailable") })
			}
			enforcer, err := NewEnforcer(Config{Hook: test.hook, Auditor: sink})
			if err != nil {
				t.Fatal(err)
			}
			err = enforcer.AuthorizeProposal(context.Background(), operatorScope(t), testProposalInput(t))
			if err == nil {
				t.Fatal("AuthorizeProposal() returned nil")
			}
			if test.wantError != nil && !errors.Is(err, test.wantError) {
				t.Fatalf("AuthorizeProposal() error = %v, want %v", err, test.wantError)
			}
			if test.contains != "" && !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("AuthorizeProposal() error = %v, want %q", err, test.contains)
			}
			if strings.Contains(err.Error(), "token=secret") {
				t.Fatalf("AuthorizeProposal() leaked hook error: %v", err)
			}
			if !test.auditFails && strings.Contains(fmt.Sprintf("%#v", auditor.events), "token=secret") {
				t.Fatalf("AuthorizeProposal() leaked hook error into audit: %#v", auditor.events)
			}
			if !test.auditFails && (len(auditor.events) != 1 || auditor.events[0].Verdict != test.want || auditor.events[0].ReasonCode != test.reason) {
				t.Fatalf("audit events = %#v, want %s/%s", auditor.events, test.want, test.reason)
			}
		})
	}
}

func TestEnforcerProposalRoleMatrixFailsClosed(t *testing.T) {
	for _, role := range []tenancy.Role{tenancy.RoleReader, tenancy.RoleApprover, tenancy.RoleAdmin} {
		t.Run(string(role), func(t *testing.T) {
			var hookCalls atomic.Int64
			auditor := &recordingAuditor{}
			enforcer, err := NewEnforcer(Config{
				Hook: HookFunc(func(context.Context, Request) (Decision, error) {
					hookCalls.Add(1)
					return Decision{Verdict: VerdictAllow, ReasonCode: "unexpected"}, nil
				}),
				Auditor: auditor,
			})
			if err != nil {
				t.Fatal(err)
			}
			err = enforcer.AuthorizeProposal(context.Background(), scopeForRole(t, role), testProposalInputFor(t, "user:"+string(role), "workspace-a"))
			if err == nil || !errors.Is(err, ErrDenied) || !strings.Contains(err.Error(), "role does not permit") {
				t.Fatalf("AuthorizeProposal() error = %v, want role denial", err)
			}
			if hookCalls.Load() != 0 {
				t.Fatalf("policy hook calls = %d, want zero", hookCalls.Load())
			}
			if len(auditor.events) != 1 || auditor.events[0].Verdict != VerdictDeny || auditor.events[0].ReasonCode != "role-denied" {
				t.Fatalf("audit events = %#v", auditor.events)
			}
		})
	}
}

func TestEnforcerRejectsTamperedProposalBindingBeforePolicyHook(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ProposalInput)
	}{
		{name: "workspace", mutate: func(input *ProposalInput) { input.workspaceID = "workspace-b" }},
		{name: "actor", mutate: func(input *ProposalInput) { input.actor = "user:other" }},
		{name: "verb", mutate: func(input *ProposalInput) { input.verb = "deployment.delete" }},
		{name: "target", mutate: func(input *ProposalInput) { input.target.Name = "payments-canary" }},
		{name: "arguments digest", mutate: func(input *ProposalInput) { input.argumentsDigest = digestFor("different") }},
		{name: "resolved digest", mutate: func(input *ProposalInput) { input.resolvedDigest = digestFor("forged") }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := testProposalInput(t)
			test.mutate(&input)
			var hookCalls atomic.Int64
			auditor := &recordingAuditor{}
			enforcer, err := NewEnforcer(Config{
				Hook: HookFunc(func(context.Context, Request) (Decision, error) {
					hookCalls.Add(1)
					return Decision{Verdict: VerdictAllow, ReasonCode: "unexpected"}, nil
				}),
				Auditor: auditor,
			})
			if err != nil {
				t.Fatal(err)
			}
			err = enforcer.AuthorizeProposal(context.Background(), operatorScope(t), input)
			if err == nil || !errors.Is(err, ErrDenied) || !strings.Contains(err.Error(), "invalid policy request") {
				t.Fatalf("AuthorizeProposal() error = %v, want invalid request", err)
			}
			if hookCalls.Load() != 0 {
				t.Fatalf("policy hook calls = %d, want zero", hookCalls.Load())
			}
			if len(auditor.events) != 1 || auditor.events[0].Verdict != VerdictDeny || auditor.events[0].ReasonCode != "invalid-request" {
				t.Fatalf("audit events = %#v", auditor.events)
			}
		})
	}
}

func TestProposalDigestBindsEveryResolvedEnvelopeField(t *testing.T) {
	target := testProposalTarget()
	base := proposalCase{
		intentID: "intent-230", workspaceID: "workspace-a", actor: "user:operator",
		verb: intent.VerbDeploymentRestart, target: target, argumentsDigest: digestFor("validated-arguments"),
	}
	variants := []proposalCase{
		base,
		base.with(func(value *proposalCase) { value.intentID = "intent-231" }),
		base.with(func(value *proposalCase) { value.workspaceID = "workspace-b" }),
		base.with(func(value *proposalCase) { value.actor = "user:operator-2" }),
		base.with(func(value *proposalCase) { value.verb = intent.VerbDeploymentScale }),
		base.with(func(value *proposalCase) { value.target.SourceKind = "kubernetes" }),
		base.with(func(value *proposalCase) { value.target.Scope = "cluster-b" }),
		base.with(func(value *proposalCase) { value.target.Kind = "statefulset" }),
		base.with(func(value *proposalCase) { value.target.Namespace = "operations" }),
		base.with(func(value *proposalCase) { value.target.Name = "payments-canary" }),
		base.with(func(value *proposalCase) { value.argumentsDigest = digestFor("other-arguments") }),
	}
	seen := make(map[string]struct{}, len(variants))
	for index, variant := range variants {
		input, err := NewProposalInput(variant.intentID, variant.workspaceID, variant.actor, variant.verb, variant.target, variant.argumentsDigest)
		if err != nil {
			t.Fatalf("NewProposalInput(variant %d) error = %v", index, err)
		}
		if !validDigest(input.resolvedDigest) {
			t.Fatalf("variant %d digest = %q", index, input.resolvedDigest)
		}
		if _, exists := seen[input.resolvedDigest]; exists {
			t.Fatalf("variant %d reused resolved digest %q", index, input.resolvedDigest)
		}
		seen[input.resolvedDigest] = struct{}{}
	}
}

func TestNewProposalInputRejectsMalformedEnvelopeFields(t *testing.T) {
	base := proposalCase{
		intentID: "intent-230", workspaceID: "workspace-a", actor: "user:operator",
		verb: intent.VerbDeploymentRestart, target: testProposalTarget(), argumentsDigest: digestFor("validated-arguments"),
	}
	tests := []struct {
		name   string
		mutate func(*proposalCase)
	}{
		{name: "empty intent ID", mutate: func(value *proposalCase) { value.intentID = "" }},
		{name: "control intent ID", mutate: func(value *proposalCase) { value.intentID = "intent\n230" }},
		{name: "empty workspace", mutate: func(value *proposalCase) { value.workspaceID = "" }},
		{name: "foreign whitespace actor", mutate: func(value *proposalCase) { value.actor = " user:operator" }},
		{name: "unknown verb", mutate: func(value *proposalCase) { value.verb = "deployment.delete" }},
		{name: "empty target source", mutate: func(value *proposalCase) { value.target.SourceKind = "" }},
		{name: "control target name", mutate: func(value *proposalCase) { value.target.Name = "payments\nsecret" }},
		{name: "target attributes", mutate: func(value *proposalCase) { value.target.Attributes = map[string]string{"token": "secret"} }},
		{name: "malformed arguments digest", mutate: func(value *proposalCase) { value.argumentsDigest = "sha256:ABC" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := base
			test.mutate(&value)
			if _, err := NewProposalInput(value.intentID, value.workspaceID, value.actor, value.verb, value.target, value.argumentsDigest); err == nil {
				t.Fatal("NewProposalInput() accepted malformed envelope")
			}
		})
	}
}

func TestNewProposalInputSeversEmptyTargetAttributeAlias(t *testing.T) {
	attributes := map[string]string{}
	target := testProposalTarget()
	target.Attributes = attributes
	input, err := NewProposalInput(
		"intent-230", "workspace-a", "user:operator", intent.VerbDeploymentRestart,
		target, digestFor("validated-arguments"),
	)
	if err != nil {
		t.Fatalf("NewProposalInput() error = %v", err)
	}
	attributes["token"] = "caller-secret"
	if input.target.Attributes != nil {
		t.Fatalf("proposal retained caller-owned target attributes: %#v", input.target.Attributes)
	}
	enforcer, err := NewEnforcer(Config{
		Hook:    proposalDecision(VerdictAllow, "proposal-allowed"),
		Auditor: AuditFunc(func(context.Context, AuditEvent) error { return nil }),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := enforcer.AuthorizeProposal(context.Background(), operatorScope(t), input); err != nil {
		t.Fatalf("AuthorizeProposal() changed after caller map mutation: %v", err)
	}
}

func TestEnforcerAuthorizesBoundProposalConcurrently(t *testing.T) {
	const workers = 64
	var hookCalls atomic.Int64
	var auditCalls atomic.Int64
	enforcer, err := NewEnforcer(Config{
		Hook: HookFunc(func(context.Context, Request) (Decision, error) {
			hookCalls.Add(1)
			return Decision{Verdict: VerdictAllow, ReasonCode: "proposal-allowed"}, nil
		}),
		Auditor: AuditFunc(func(context.Context, AuditEvent) error {
			auditCalls.Add(1)
			return nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	input := testProposalInput(t)
	scope := operatorScope(t)
	var group sync.WaitGroup
	errorsByWorker := make(chan error, workers)
	for worker := 0; worker < workers; worker++ {
		group.Add(1)
		go func() {
			defer group.Done()
			errorsByWorker <- enforcer.AuthorizeProposal(context.Background(), scope, input)
		}()
	}
	group.Wait()
	close(errorsByWorker)
	for err := range errorsByWorker {
		if err != nil {
			t.Fatalf("AuthorizeProposal() concurrent error = %v", err)
		}
	}
	if hookCalls.Load() != workers || auditCalls.Load() != workers {
		t.Fatalf("hook/audit calls = %d/%d, want %d/%d", hookCalls.Load(), auditCalls.Load(), workers, workers)
	}
}

func FuzzProposalInputRejectsTamperedBinding(f *testing.F) {
	f.Add("target-a", uint8(0))
	f.Add("target-b", uint8(5))
	f.Fuzz(func(t *testing.T, replacement string, field uint8) {
		input := testProposalInput(t)
		marker := fmt.Sprintf("caller-%x", sha256.Sum256([]byte(replacement)))
		switch field % 6 {
		case 0:
			input.intentID = marker
		case 1:
			input.actor = marker
		case 2:
			input.verb = intent.Verb(marker)
		case 3:
			input.target.Name = marker
		case 4:
			input.argumentsDigest = marker
		case 5:
			input.resolvedDigest = marker
		}
		var hookCalls atomic.Int64
		var audits []AuditEvent
		enforcer, err := NewEnforcer(Config{
			Hook: HookFunc(func(context.Context, Request) (Decision, error) {
				hookCalls.Add(1)
				return Decision{Verdict: VerdictAllow, ReasonCode: "unexpected"}, nil
			}),
			Auditor: AuditFunc(func(_ context.Context, event AuditEvent) error {
				audits = append(audits, event)
				return nil
			}),
		})
		if err != nil {
			t.Fatal(err)
		}
		err = enforcer.AuthorizeProposal(context.Background(), operatorScope(t), input)
		if err == nil {
			t.Fatal("AuthorizeProposal() accepted a tampered proposal")
		}
		if strings.Contains(err.Error(), marker) || strings.Contains(fmt.Sprintf("%#v", audits), marker) {
			t.Fatalf("AuthorizeProposal() leaked caller material")
		}
		if hookCalls.Load() != 0 {
			t.Fatalf("policy hook calls = %d, want zero", hookCalls.Load())
		}
		if len(audits) != 1 || audits[0].Action != tenancy.ActionProposeIntent || audits[0].Verdict != VerdictDeny || audits[0].ReasonCode != "invalid-request" {
			t.Fatalf("tampered proposal audits = %#v, want one sanitized invalid-request denial", audits)
		}
	})
}

func proposalDecision(verdict Verdict, reason string) PolicyHook {
	return HookFunc(func(context.Context, Request) (Decision, error) {
		return Decision{Verdict: verdict, ReasonCode: reason}, nil
	})
}

func testProposalInput(t testing.TB) ProposalInput {
	t.Helper()
	return testProposalInputFor(t, "user:operator", "workspace-a")
}

func testProposalInputFor(t testing.TB, actor string, workspaceID tenancy.WorkspaceID) ProposalInput {
	t.Helper()
	input, err := NewProposalInput(
		"intent-230", workspaceID, actor, intent.VerbDeploymentRestart,
		testProposalTarget(), digestFor("validated-arguments"),
	)
	if err != nil {
		t.Fatalf("NewProposalInput() error = %v", err)
	}
	return input
}

func testProposalTarget() fleet.ResourceRef {
	return fleet.ResourceRef{SourceKind: "argocd", Scope: "cluster-a", Kind: "deployment", Namespace: "payments", Name: "payments"}
}

func digestFor(value string) string {
	return NewReadInput(VerbFleetRead, []byte(value)).ArgumentsDigest
}

func operatorScope(t testing.TB) tenancy.Scope {
	t.Helper()
	return scopeForRole(t, tenancy.RoleOperator)
}

func scopeForRole(t testing.TB, role tenancy.Role) tenancy.Scope {
	t.Helper()
	subject := "user:" + string(role)
	principal, err := tenancy.NewPrincipal(subject, map[tenancy.WorkspaceID]tenancy.Role{"workspace-a": role})
	if err != nil {
		t.Fatal(err)
	}
	scope, err := principal.Scope("workspace-a")
	if err != nil {
		t.Fatal(err)
	}
	return scope
}

type proposalCase struct {
	intentID        string
	workspaceID     tenancy.WorkspaceID
	actor           string
	verb            intent.Verb
	target          fleet.ResourceRef
	argumentsDigest string
}

func (value proposalCase) with(mutate func(*proposalCase)) proposalCase {
	mutate(&value)
	return value
}

func (value proposalCase) String() string {
	return fmt.Sprintf("%s/%s/%s", value.workspaceID, value.verb, value.intentID)
}
