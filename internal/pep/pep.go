// SPDX-License-Identifier: Apache-2.0

package pep

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/ArdurAI/sith/internal/fleet"
	"github.com/ArdurAI/sith/internal/intent"
	"github.com/ArdurAI/sith/internal/tenancy"
	"github.com/ArdurAI/sith/internal/tracing"
)

const (
	maxActorBytes           = 256
	maxIntentIDBytes        = 253
	maxReasonCodeBytes      = 64
	maxTargetComponentBytes = 256
	invalidVerb             = Verb("invalid")
	proposalDigestDomain    = "sith-pep-proposal/v1"
)

// Stable policy refusal classes let later approval and action orchestration distinguish a deny
// from a pending approval without parsing error text. Both remain fail-closed outcomes.
var (
	ErrDenied           = errors.New("policy denied request")
	ErrApprovalRequired = errors.New("policy requires approval")
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

func (verb Verb) validForAction(action tenancy.Action) bool {
	switch action {
	case tenancy.ActionRead:
		return verb.Valid()
	case tenancy.ActionProposeIntent:
		return intent.Verb(verb).Valid()
	default:
		return false
	}
}

// Verdict is the PDP-compatible outcome returned at the policy hook.
type Verdict string

// Supported policy outcomes. A non-allow outcome never reaches the downstream operation.
const (
	VerdictAllow           Verdict = "allow"
	VerdictDeny            Verdict = "deny"
	VerdictRequireApproval Verdict = "require-approval"
)

// Request is the normalized, post-authentication input to the policy hook. For reads,
// ArgumentsDigest binds canonical validated arguments. For proposals, it binds the complete
// resolved proposal envelope. Scope identity comes only from a signed tenancy scope; raw
// credentials, headers, selectors, targets, arguments, and result data are never carried into the
// hook or audit event.
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

// ProposalInput is an immutable, privacy-minimizing binding for a validated and resolved typed
// proposal. Callers construct it only after handler-owned argument validation and target
// resolution. It retains digests and normalized identifiers, never raw arguments.
type ProposalInput struct {
	intentID        string
	workspaceID     tenancy.WorkspaceID
	actor           string
	verb            intent.Verb
	target          fleet.ResourceRef
	argumentsDigest string
	resolvedDigest  string
}

// NewReadInput hashes canonical typed arguments for the policy hook. Callers must validate their
// concrete argument schema before constructing this input.
func NewReadInput(verb Verb, canonicalArguments []byte) ReadInput {
	digest := sha256.Sum256(canonicalArguments)
	return ReadInput{Verb: verb, ArgumentsDigest: "sha256:" + hex.EncodeToString(digest[:])}
}

// NewProposalInput binds one exact resolved proposal. target must be the normalized target
// returned by planning and argumentsDigest must come from the already schema-validated argument
// document. The resulting digest changes if any bound identity, verb, target, or argument digest
// changes.
func NewProposalInput(
	intentID string,
	workspaceID tenancy.WorkspaceID,
	actor string,
	verb intent.Verb,
	target fleet.ResourceRef,
	argumentsDigest string,
) (ProposalInput, error) {
	input := ProposalInput{
		intentID: intentID, workspaceID: workspaceID, actor: actor, verb: verb,
		target: target, argumentsDigest: argumentsDigest,
	}
	if err := input.validateFields(); err != nil {
		return ProposalInput{}, fmt.Errorf("construct policy proposal input: %w", err)
	}
	// An empty non-nil attributes map is semantically valid but remains caller-mutable. Discard it
	// so the retained proposal binding cannot be changed or raced after construction.
	input.target.Attributes = nil
	input.resolvedDigest = input.digest()
	return input, nil
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

// Auditor durably records one PEP decision. An audit failure fails closed before an operation runs.
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

// Enforcer applies the fixed policy pipeline and creates one audit record for every decision.
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
	request := Request{
		WorkspaceID: scope.WorkspaceID(), Actor: scope.Subject(), Role: scope.Role(), Action: tenancy.ActionRead,
		Verb: input.Verb, ArgumentsDigest: input.ArgumentsDigest,
	}
	return enforcer.authorize(ctx, scope, request, true, "read")
}

// AuthorizeProposal runs a resolved typed proposal through the same policy hook and mandatory
// audit boundary as reads. It grants no execution capability: AllowReadHook denies this action, and
// both deny and require-approval return typed fail-closed errors.
func (enforcer *Enforcer) AuthorizeProposal(ctx context.Context, scope tenancy.Scope, input ProposalInput) error {
	request := Request{
		WorkspaceID: scope.WorkspaceID(), Actor: scope.Subject(), Role: scope.Role(), Action: tenancy.ActionProposeIntent,
		Verb: Verb(input.verb), ArgumentsDigest: input.resolvedDigest,
	}
	inputValid := input.validate() == nil && input.workspaceID == scope.WorkspaceID() && input.actor == scope.Subject()
	return enforcer.authorize(ctx, scope, request, inputValid, "proposal")
}

func (enforcer *Enforcer) authorize(
	ctx context.Context,
	scope tenancy.Scope,
	request Request,
	inputValid bool,
	operation string,
) error {
	if enforcer == nil || enforcer.hook == nil || enforcer.auditor == nil || ctx == nil {
		return fmt.Errorf("authorize %s: enforcer and context are required", operation)
	}
	traceContext, _, err := tracing.Ensure(ctx)
	if err != nil {
		return fmt.Errorf("authorize %s: establish trace context: %w", operation, err)
	}
	ctx = traceContext
	startedAt := time.Now()
	outcome := DecisionOutcomeError
	defer func() {
		enforcer.observeDecision(request.Verb, outcome, time.Since(startedAt))
		enforcer.observeTrace(ctx, outcome, time.Since(startedAt))
	}()
	if !inputValid || request.Validate() != nil {
		outcome = DecisionOutcomeDeny
		return enforcer.refuse(ctx, request, VerdictDeny, "invalid-request", fmt.Sprintf("authorize %s: invalid policy request", operation), ErrDenied)
	}
	if err := scope.Authorize(request.Action); err != nil {
		outcome = DecisionOutcomeDeny
		return enforcer.refuse(ctx, request, VerdictDeny, "role-denied", fmt.Sprintf("authorize %s: role does not permit %s", operation, request.Action), ErrDenied)
	}
	decision, err := enforcer.hook.Decide(ctx, request)
	if err != nil {
		outcome = DecisionOutcomeError
		return enforcer.refuse(ctx, request, VerdictDeny, "hook-error", fmt.Sprintf("authorize %s: policy hook failed", operation), nil)
	}
	if err := decision.Validate(); err != nil {
		outcome = DecisionOutcomeError
		return enforcer.refuse(ctx, request, VerdictDeny, "invalid-decision", fmt.Sprintf("authorize %s: policy hook returned an invalid decision", operation), nil)
	}
	if decision.Verdict != VerdictAllow {
		if decision.Verdict == VerdictRequireApproval {
			outcome = DecisionOutcomeRequireApproval
		} else {
			outcome = DecisionOutcomeDeny
		}
		if err := enforcer.record(ctx, request, decision); err != nil {
			outcome = DecisionOutcomeError
			return fmt.Errorf("authorize %s: audit policy refusal: %w", operation, err)
		}
		if decision.Verdict == VerdictRequireApproval {
			return fmt.Errorf("authorize %s: %w", operation, ErrApprovalRequired)
		}
		return fmt.Errorf("authorize %s: %w", operation, ErrDenied)
	}
	if err := enforcer.record(ctx, request, decision); err != nil {
		outcome = DecisionOutcomeError
		return fmt.Errorf("authorize %s: audit policy decision: %w", operation, err)
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
	if !request.Role.Valid() || !request.Verb.validForAction(request.Action) || !validDigest(request.ArgumentsDigest) {
		return fmt.Errorf("policy request uses an unsupported role, action, or verb")
	}
	return nil
}

// Validate rejects incomplete, unknown, or unsafe PDP responses before they affect an operation.
func (decision Decision) Validate() error {
	switch decision.Verdict {
	case VerdictAllow, VerdictDeny, VerdictRequireApproval:
	default:
		return fmt.Errorf("policy decision has unsupported verdict %q", decision.Verdict)
	}
	return validateReasonCode(decision.ReasonCode)
}

func (enforcer *Enforcer) refuse(ctx context.Context, request Request, verdict Verdict, reasonCode, message string, classification error) error {
	request, auditable := normalizedAuditRequest(request)
	if !auditable {
		if classification != nil {
			return fmt.Errorf("%s: %w", message, classification)
		}
		return fmt.Errorf("%s", message)
	}
	if err := enforcer.record(ctx, request, Decision{Verdict: verdict, ReasonCode: reasonCode}); err != nil {
		if classification != nil {
			return fmt.Errorf("%s: %w: audit refusal: %w", message, classification, err)
		}
		return fmt.Errorf("%s: audit refusal: %w", message, err)
	}
	if classification != nil {
		return fmt.Errorf("%s: %w", message, classification)
	}
	return fmt.Errorf("%s", message)
}

func normalizedAuditRequest(request Request) (Request, bool) {
	if tenancy.ValidateWorkspaceID(request.WorkspaceID) != nil || validateSafeText("policy actor", request.Actor, maxActorBytes) != nil ||
		!request.Role.Valid() || (request.Action != tenancy.ActionRead && request.Action != tenancy.ActionProposeIntent) {
		return Request{}, false
	}
	if !request.Verb.validForAction(request.Action) {
		request.Verb = invalidVerb
	}
	// AuditEvent deliberately has no binding-digest field. Clear it here as defense in depth so a
	// malformed or caller-supplied value cannot survive refusal normalization into future sinks.
	request.ArgumentsDigest = ""
	return request, true
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

func (input ProposalInput) validate() error {
	if err := input.validateFields(); err != nil {
		return err
	}
	if !validDigest(input.resolvedDigest) || input.resolvedDigest != input.digest() {
		return fmt.Errorf("proposal binding digest is invalid")
	}
	return nil
}

func (input ProposalInput) validateFields() error {
	if validateSafeText("proposal intent identifier", input.intentID, maxIntentIDBytes) != nil ||
		tenancy.ValidateWorkspaceID(input.workspaceID) != nil ||
		validateSafeText("proposal actor", input.actor, maxActorBytes) != nil || !input.verb.Valid() ||
		validateProposalTarget(input.target) != nil || !validDigest(input.argumentsDigest) {
		return fmt.Errorf("proposal fields are invalid")
	}
	return nil
}

func (input ProposalInput) digest() string {
	values := []string{
		proposalDigestDomain, input.intentID, string(input.workspaceID), input.actor, string(input.verb),
		input.target.SourceKind, input.target.Scope, input.target.Kind, input.target.Namespace, input.target.Name,
		input.argumentsDigest,
	}
	digest := sha256.Sum256([]byte(strings.Join(values, "\x00")))
	return "sha256:" + hex.EncodeToString(digest[:])
}

func validateProposalTarget(target fleet.ResourceRef) error {
	if len(target.Attributes) != 0 ||
		validateSafeText("proposal target source", target.SourceKind, maxTargetComponentBytes) != nil ||
		validateSafeText("proposal target scope", target.Scope, maxTargetComponentBytes) != nil ||
		validateSafeText("proposal target kind", target.Kind, maxTargetComponentBytes) != nil ||
		validateSafeText("proposal target name", target.Name, maxTargetComponentBytes) != nil {
		return fmt.Errorf("proposal target is invalid")
	}
	if target.Namespace != "" && validateSafeText("proposal target namespace", target.Namespace, maxTargetComponentBytes) != nil {
		return fmt.Errorf("proposal target is invalid")
	}
	return nil
}
