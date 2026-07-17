// SPDX-License-Identifier: Apache-2.0

package pep

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/ArdurAI/sith/internal/tenancy"
	"github.com/ArdurAI/sith/internal/tracing"
)

const (
	maxActorBytes      = 256
	maxReasonCodeBytes = 64
)

// Verb identifies one closed PEP operation. New verbs are an explicit policy-boundary change.
type Verb string

// Phase-1 read verbs. Future write verbs are added only with their typed argument schema and PDP path.
const (
	VerbFleetRead                Verb = "fleet.read"
	VerbFleetCorrelate           Verb = "fleet.correlate"
	VerbFleetInventorySearch     Verb = "fleet.inventory.search"
	VerbFleetImageSearch         Verb = "fleet.image.search"
	VerbFleetCVESearch           Verb = "fleet.cve.search"
	VerbFleetCVEIdentifierSearch Verb = "fleet.cve.identifier.search"
	VerbSpokeSnapshotRefresh     Verb = "fleet.snapshot.refresh"
)

// Valid reports whether a verb belongs to the currently supported closed vocabulary.
func (verb Verb) Valid() bool {
	switch verb {
	case VerbFleetRead, VerbFleetCorrelate, VerbFleetInventorySearch, VerbFleetImageSearch, VerbFleetCVESearch, VerbFleetCVEIdentifierSearch, VerbSpokeSnapshotRefresh:
		return true
	default:
		return false
	}
}

// Verdict is the PDP-compatible outcome returned at the policy hook.
type Verdict string

// Supported policy outcomes. A non-allow outcome never reaches the read dependency.
const (
	VerdictAllow           Verdict = "allow"
	VerdictDeny            Verdict = "deny"
	VerdictRequireApproval Verdict = "require-approval"
)

// Request is the normalized, post-authentication input to the policy hook. Scope identity comes
// only from a signed tenancy scope; raw credentials, headers, selectors, and result data are never
// carried into the audit event.
type Request struct {
	WorkspaceID     tenancy.WorkspaceID
	Actor           string
	Role            tenancy.Role
	Action          tenancy.Action
	Verb            Verb
	ArgumentsDigest string
}

// ReadInput carries the closed verb and a digest of canonical, already-validated read arguments.
// The hook can bind a future decision ledger to the request without receiving the raw selector.
type ReadInput struct {
	Verb            Verb
	ArgumentsDigest string
}

// NewReadInput hashes canonical typed arguments for the policy hook. Callers must validate their
// concrete argument schema before constructing this input.
func NewReadInput(verb Verb, canonicalArguments []byte) ReadInput {
	digest := sha256.Sum256(canonicalArguments)
	return ReadInput{Verb: verb, ArgumentsDigest: "sha256:" + hex.EncodeToString(digest[:])}
}

// Decision records one PDP-compatible policy result using a safe reason code rather than free text.
type Decision struct {
	Verdict    Verdict
	ReasonCode string
}

// PolicyHook is the seam a future Ardur PDP implements. It receives normalized identity, action,
// and tenant scope after authentication, membership, role, verb, and argument validation.
type PolicyHook interface {
	Decide(context.Context, Request) (Decision, error)
}

// HookFunc adapts a function to PolicyHook.
type HookFunc func(context.Context, Request) (Decision, error)

// Decide calls function.
func (function HookFunc) Decide(ctx context.Context, request Request) (Decision, error) {
	return function(ctx, request)
}

// AuditEvent is one privacy-preserving policy decision. It contains only normalized identity and
// fixed operation metadata; it intentionally omits credentials, query arguments, targets, and data.
type AuditEvent struct {
	At          time.Time
	TraceID     tracing.ID
	WorkspaceID tenancy.WorkspaceID
	Actor       string
	Role        tenancy.Role
	Action      tenancy.Action
	Verb        Verb
	Verdict     Verdict
	ReasonCode  string
}

// Auditor durably records one PEP decision. An audit failure fails closed before a read runs.
type Auditor interface {
	Record(context.Context, AuditEvent) error
}

// AuditFunc adapts a function to Auditor.
type AuditFunc func(context.Context, AuditEvent) error

// Record calls function.
func (function AuditFunc) Record(ctx context.Context, event AuditEvent) error {
	return function(ctx, event)
}

// Config constructs an enforcement point with mandatory policy and audit dependencies.
type Config struct {
	Hook          PolicyHook
	Auditor       Auditor
	Observer      DecisionObserver
	TraceObserver tracing.Observer
	Now           func() time.Time
}

// Enforcer applies the fixed Phase-1 read pipeline and creates one audit record for every decision.
type Enforcer struct {
	hook     PolicyHook
	auditor  Auditor
	observer DecisionObserver
	tracer   tracing.Observer
	now      func() time.Time
}

// NewEnforcer constructs a fail-closed policy enforcement point.
func NewEnforcer(config Config) (*Enforcer, error) {
	if config.Hook == nil || config.Auditor == nil {
		return nil, fmt.Errorf("construct policy enforcer: hook and auditor are required")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.Observer == nil {
		config.Observer = noopDecisionObserver{}
	}
	if config.TraceObserver == nil {
		config.TraceObserver = tracing.NoopObserver()
	}
	return &Enforcer{hook: config.Hook, auditor: config.Auditor, observer: config.Observer, tracer: config.TraceObserver, now: config.Now}, nil
}

// AllowReadHook is the temporary Phase-1 policy implementation. It allows only the closed read
// vocabulary; it is intentionally unsuitable for future write verbs.
type AllowReadHook struct{}

// Decide returns allow only for an already-validated read request.
func (AllowReadHook) Decide(_ context.Context, request Request) (Decision, error) {
	if request.Action != tenancy.ActionRead || !request.Verb.Valid() {
		return Decision{Verdict: VerdictDeny, ReasonCode: "unsupported-read"}, nil
	}
	return Decision{Verdict: VerdictAllow, ReasonCode: "phase-1-read"}, nil
}

// AuthorizeRead executes the Phase-1 PEP order: verified scope → role → closed read verb → policy
// hook → audit. A deny, approval requirement, malformed decision, hook error, or audit error blocks
// the downstream reader.
func (enforcer *Enforcer) AuthorizeRead(ctx context.Context, scope tenancy.Scope, input ReadInput) error {
	if enforcer == nil || enforcer.hook == nil || enforcer.auditor == nil || ctx == nil {
		return fmt.Errorf("authorize read: enforcer and context are required")
	}
	traceContext, _, err := tracing.Ensure(ctx)
	if err != nil {
		return fmt.Errorf("authorize read: establish trace context: %w", err)
	}
	ctx = traceContext
	startedAt := time.Now()
	outcome := DecisionOutcomeError
	defer func() {
		enforcer.observeDecision(input.Verb, outcome, time.Since(startedAt))
		enforcer.observeTrace(ctx, outcome, time.Since(startedAt))
	}()
	request := Request{
		WorkspaceID: scope.WorkspaceID(), Actor: scope.Subject(), Role: scope.Role(), Action: tenancy.ActionRead,
		Verb: input.Verb, ArgumentsDigest: input.ArgumentsDigest,
	}
	if err := request.Validate(); err != nil {
		outcome = DecisionOutcomeDeny
		return enforcer.refuse(ctx, request, VerdictDeny, "invalid-request", "authorize read: invalid policy request")
	}
	if err := scope.Authorize(tenancy.ActionRead); err != nil {
		outcome = DecisionOutcomeDeny
		return enforcer.refuse(ctx, request, VerdictDeny, "role-denied", "authorize read: role does not permit read")
	}
	decision, err := enforcer.hook.Decide(ctx, request)
	if err != nil {
		outcome = DecisionOutcomeError
		return enforcer.refuse(ctx, request, VerdictDeny, "hook-error", "authorize read: policy hook failed")
	}
	if err := decision.Validate(); err != nil {
		outcome = DecisionOutcomeError
		return enforcer.refuse(ctx, request, VerdictDeny, "invalid-decision", "authorize read: policy hook returned an invalid decision")
	}
	if decision.Verdict != VerdictAllow {
		if decision.Verdict == VerdictRequireApproval {
			outcome = DecisionOutcomeRequireApproval
		} else {
			outcome = DecisionOutcomeDeny
		}
		if err := enforcer.record(ctx, request, decision); err != nil {
			outcome = DecisionOutcomeError
			return fmt.Errorf("authorize read: audit policy refusal: %w", err)
		}
		if decision.Verdict == VerdictRequireApproval {
			return fmt.Errorf("authorize read: policy requires approval")
		}
		return fmt.Errorf("authorize read: policy denied request")
	}
	if err := enforcer.record(ctx, request, decision); err != nil {
		outcome = DecisionOutcomeError
		return fmt.Errorf("authorize read: audit policy decision: %w", err)
	}
	outcome = DecisionOutcomeAllow
	return nil
}

// Validate rejects a request that has not passed the preceding PEP stages.
func (request Request) Validate() error {
	if err := tenancy.ValidateWorkspaceID(request.WorkspaceID); err != nil {
		return fmt.Errorf("policy workspace: %w", err)
	}
	if err := validateSafeText("policy actor", request.Actor, maxActorBytes); err != nil {
		return err
	}
	if !request.Role.Valid() || request.Action != tenancy.ActionRead || !request.Verb.Valid() || !validDigest(request.ArgumentsDigest) {
		return fmt.Errorf("policy request uses an unsupported role, action, or verb")
	}
	return nil
}

// Validate rejects incomplete, unknown, or unsafe PDP responses before they affect a read.
func (decision Decision) Validate() error {
	switch decision.Verdict {
	case VerdictAllow, VerdictDeny, VerdictRequireApproval:
	default:
		return fmt.Errorf("policy decision has unsupported verdict %q", decision.Verdict)
	}
	return validateReasonCode(decision.ReasonCode)
}

func (enforcer *Enforcer) refuse(ctx context.Context, request Request, verdict Verdict, reasonCode, message string) error {
	if err := enforcer.record(ctx, request, Decision{Verdict: verdict, ReasonCode: reasonCode}); err != nil {
		return fmt.Errorf("%s: audit refusal: %w", message, err)
	}
	return fmt.Errorf("%s", message)
}

func (enforcer *Enforcer) record(ctx context.Context, request Request, decision Decision) error {
	traceID, ok := tracing.FromContext(ctx)
	if !ok {
		return fmt.Errorf("record policy audit: trace context is required")
	}
	return enforcer.auditor.Record(ctx, AuditEvent{
		At: enforcer.now().UTC(), TraceID: traceID, WorkspaceID: request.WorkspaceID, Actor: request.Actor, Role: request.Role,
		Action: request.Action, Verb: request.Verb, Verdict: decision.Verdict, ReasonCode: decision.ReasonCode,
	})
}

func validateSafeText(name, value string, maximum int) error {
	if value == "" || strings.TrimSpace(value) != value || len(value) > maximum {
		return fmt.Errorf("%s is invalid", name)
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return fmt.Errorf("%s is invalid", name)
		}
	}
	return nil
}

func validateReasonCode(value string) error {
	if value == "" || strings.TrimSpace(value) != value || len(value) > maxReasonCodeBytes {
		return fmt.Errorf("policy reason code is invalid")
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' && character != '_' && character != '.' {
			return fmt.Errorf("policy reason code is invalid")
		}
	}
	return nil
}

func validDigest(value string) bool {
	const prefix = "sha256:"
	if len(value) != len(prefix)+sha256.Size*2 || !strings.HasPrefix(value, prefix) {
		return false
	}
	for _, character := range value[len(prefix):] {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}
