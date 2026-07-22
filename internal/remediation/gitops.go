// SPDX-License-Identifier: Apache-2.0

// Package remediation resolves inert Brain candidates against source-owned provenance without
// authorizing, persisting, dispatching, or executing an action.
package remediation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/ArdurAI/sith/internal/brain"
	"github.com/ArdurAI/sith/internal/connector"
	"github.com/ArdurAI/sith/internal/fleet"
	"github.com/ArdurAI/sith/internal/intent"
	"github.com/ArdurAI/sith/internal/intentargs"
	"github.com/ArdurAI/sith/internal/tenancy"
)

const (
	// GitOpsProvenanceVersion is the first immutable source-to-resolver contract.
	GitOpsProvenanceVersion = "gitops-provenance/v1"
	// GitOpsSourceAdapterVersion pins the canonical GitHub Git-object observation contract.
	GitOpsSourceAdapterVersion = "github-gitops-provenance/2026-03-10"
	maxIdentityBytes           = 512
	maxBundleContentBytes      = 64 << 10
	maxBundleValidity          = 5 * time.Minute
	gitHubSourceKind           = "github"
	openPRTargetKind           = "Repository"
)

// ResolutionStatus distinguishes a provenance-complete result from a fail-closed abstention.
type ResolutionStatus string

// Closed resolution states.
const (
	ResolutionReady     ResolutionStatus = "ready"
	ResolutionAbstained ResolutionStatus = "abstained"
)

// AbstentionReason is a bounded, non-sensitive explanation for refusing to resolve a candidate.
type AbstentionReason string

// Closed abstention reasons. Callers must branch on these values rather than parse error text.
const (
	ReasonCandidateMissing      AbstentionReason = "candidate-missing"
	ReasonCandidateInvalid      AbstentionReason = "candidate-invalid"
	ReasonCandidateUnsupported  AbstentionReason = "candidate-unsupported"
	ReasonVerdictInvalid        AbstentionReason = "verdict-invalid"
	ReasonVerdictUnconfirmed    AbstentionReason = "verdict-unconfirmed"
	ReasonFleetAmbiguous        AbstentionReason = "fleet-ambiguous"
	ReasonProvenanceMissing     AbstentionReason = "provenance-missing"
	ReasonProvenanceAmbiguous   AbstentionReason = "provenance-ambiguous"
	ReasonProvenanceInvalid     AbstentionReason = "provenance-invalid"
	ReasonProvenanceFuture      AbstentionReason = "provenance-future"
	ReasonProvenanceStale       AbstentionReason = "provenance-stale"
	ReasonWorkspaceMismatch     AbstentionReason = "workspace-mismatch"
	ReasonSubjectMismatch       AbstentionReason = "subject-mismatch"
	ReasonHandlerContractDrift  AbstentionReason = "handler-contract-drift"
	ReasonHandlerRejected       AbstentionReason = "handler-rejected"
	ReasonHandlerTargetMismatch AbstentionReason = "handler-target-mismatch"
	ReasonHandlerOutputMismatch AbstentionReason = "handler-output-mismatch"
)

// SourceIdentity identifies the one canonical adapter observation that owns a provenance bundle.
type SourceIdentity struct {
	Kind           string
	AdapterVersion string
	NativeID       string
}

// HandlerContract pins provenance to the exact typed-action adapter and argument schema it was
// assembled for. A handler upgrade invalidates an older bundle instead of silently reinterpreting
// it.
type HandlerContract struct {
	Kind           string
	AdapterVersion string
	SchemaDigest   string
}

// RepositoryIdentity is one configured Git repository, without a credential or endpoint URL.
type RepositoryIdentity struct {
	Host       string
	Owner      string
	Repository string
}

// GitOpsProvenanceInput is accepted only from a canonical source adapter boundary. Actor, role,
// intent ID, approval state, and policy decisions intentionally have no field here.
type GitOpsProvenanceInput struct {
	Workspace       tenancy.WorkspaceID
	Subject         fleet.ResourceRef
	Sources         []SourceIdentity
	ObservedAt      time.Time
	ValidUntil      time.Time
	Handler         HandlerContract
	Repository      RepositoryIdentity
	BaseRef         string
	BaseCommit      string
	FilePath        string
	ObservedBlobSHA string
	DesiredContent  string
	Title           string
	Body            string
	CommitMessage   string
	EvidenceRefs    []fleet.ResourceRef
}

// GitOpsProvenanceBundle is immutable after construction. Its fields stay private so downstream
// request code cannot rewrite source identity or Git preconditions after validation.
type GitOpsProvenanceBundle struct {
	version         string
	workspace       tenancy.WorkspaceID
	subject         fleet.ResourceRef
	source          SourceIdentity
	observedAt      time.Time
	validUntil      time.Time
	handler         HandlerContract
	repository      RepositoryIdentity
	baseRef         string
	baseCommit      string
	filePath        string
	observedBlobSHA string
	desiredContent  string
	title           string
	body            string
	commitMessage   string
	evidenceRefs    []fleet.ResourceRef
}

// Version reports the closed bundle contract without exposing mutable provenance fields.
func (bundle GitOpsProvenanceBundle) Version() string { return bundle.version }

// NewGitOpsProvenanceBundle validates and defensively copies one source-owned bundle. Exact
// GitHub path, SHA, size, and repository policy remain handler-owned and are revalidated during
// resolution.
func NewGitOpsProvenanceBundle(input GitOpsProvenanceInput) (GitOpsProvenanceBundle, error) {
	if len(input.Sources) != 1 {
		return GitOpsProvenanceBundle{}, fmt.Errorf("construct GitOps provenance: exactly one source is required")
	}
	bundle := GitOpsProvenanceBundle{
		version:         GitOpsProvenanceVersion,
		workspace:       input.Workspace,
		subject:         cloneResourceRef(input.Subject),
		source:          input.Sources[0],
		observedAt:      input.ObservedAt.UTC(),
		validUntil:      input.ValidUntil.UTC(),
		handler:         input.Handler,
		repository:      input.Repository,
		baseRef:         input.BaseRef,
		baseCommit:      input.BaseCommit,
		filePath:        input.FilePath,
		observedBlobSHA: input.ObservedBlobSHA,
		desiredContent:  input.DesiredContent,
		title:           input.Title,
		body:            input.Body,
		commitMessage:   input.CommitMessage,
		evidenceRefs:    cloneResourceRefs(input.EvidenceRefs),
	}
	if err := bundle.validate(); err != nil {
		return GitOpsProvenanceBundle{}, fmt.Errorf("construct GitOps provenance: bundle is invalid")
	}
	sort.Slice(bundle.evidenceRefs, func(left, right int) bool {
		return resourceRefLess(bundle.evidenceRefs[left], bundle.evidenceRefs[right])
	})
	for index := 1; index < len(bundle.evidenceRefs); index++ {
		if sameResourceRef(bundle.evidenceRefs[index-1], bundle.evidenceRefs[index]) {
			return GitOpsProvenanceBundle{}, fmt.Errorf("construct GitOps provenance: evidence references are not unique")
		}
	}
	return bundle, nil
}

// GitOpsHandler is the pure validation seam implemented by the planning-only GitHub adapter.
// CanonicalizeOpenPRArgs performs no network request or mutation.
type GitOpsHandler interface {
	Descriptor() connector.Descriptor
	CanonicalizeOpenPRArgs(json.RawMessage) (fleet.ResourceRef, json.RawMessage, error)
}

// GitOpsResolver resolves one candidate using a trusted server clock and the live handler
// contract. It retains no bundle or result state.
type GitOpsResolver struct {
	handler GitOpsHandler
	now     func() time.Time
}

// NewGitOpsResolver validates the injected pure handler and server clock before accepting work.
func NewGitOpsResolver(handler GitOpsHandler, now func() time.Time) (*GitOpsResolver, error) {
	if isNilHandler(handler) || now == nil {
		return nil, fmt.Errorf("construct GitOps resolver: handler and clock are required")
	}
	if _, _, err := inspectHandler(handler.Descriptor()); err != nil {
		return nil, fmt.Errorf("construct GitOps resolver: handler contract is invalid")
	}
	return &GitOpsResolver{handler: handler, now: now}, nil
}

// HandlerContractFor returns the immutable adapter/schema binding a canonical source uses when it
// constructs provenance. It does not validate provenance or grant authority.
func HandlerContractFor(handler GitOpsHandler) (HandlerContract, error) {
	if isNilHandler(handler) {
		return HandlerContract{}, fmt.Errorf("inspect GitOps handler: handler is required")
	}
	contract, _, err := inspectHandler(handler.Descriptor())
	if err != nil {
		return HandlerContract{}, fmt.Errorf("inspect GitOps handler: contract is invalid")
	}
	return contract, nil
}

// Resolution contains either a provenance-complete, handler-validated argument document or one
// or more closed abstention reasons. Ready output is still not a proposal or authorization.
type Resolution struct {
	Status          ResolutionStatus
	Target          fleet.ResourceRef
	Arguments       json.RawMessage
	ArgumentsDigest string
	EvidenceRefs    []fleet.ResourceRef
	Reasons         []AbstentionReason
}

// Resolve fails closed unless one confirmed R2/R4 candidate and one exact source-owned bundle
// satisfy the live handler contract. workspace must later come from authenticated server scope;
// it is deliberately separate from candidate and provenance data.
func (resolver *GitOpsResolver) Resolve(
	ctx context.Context,
	workspace tenancy.WorkspaceID,
	verdict brain.Verdict,
	bundles []GitOpsProvenanceBundle,
) (Resolution, error) {
	if resolver == nil || isNilHandler(resolver.handler) || resolver.now == nil || ctx == nil {
		return Resolution{}, fmt.Errorf("resolve GitOps provenance: resolver and context are required")
	}
	if err := ctx.Err(); err != nil {
		return Resolution{}, fmt.Errorf("resolve GitOps provenance: %w", err)
	}
	if err := tenancy.ValidateWorkspaceID(workspace); err != nil {
		return Resolution{}, fmt.Errorf("resolve GitOps provenance: workspace is invalid")
	}
	if verdict.RemediationCandidate == nil {
		return abstain(ReasonCandidateMissing), nil
	}
	if err := verdict.RemediationCandidate.Validate(); err != nil {
		return abstain(ReasonCandidateInvalid), nil
	}
	if !validBrainVerdictRef(verdict) {
		return abstain(ReasonVerdictInvalid), nil
	}
	if verdict.RemediationCandidate.Verb != intent.VerbGitOpsOpenPR ||
		(verdict.Rule != brain.RuleOOMKilled && verdict.Rule != brain.RuleConfigDrift) {
		return abstain(ReasonCandidateUnsupported), nil
	}
	if verdict.FleetWide {
		return abstain(ReasonFleetAmbiguous), nil
	}
	if verdict.Status != brain.StatusConfirmed {
		return abstain(ReasonVerdictUnconfirmed), nil
	}
	if len(bundles) == 0 {
		return abstain(ReasonProvenanceMissing), nil
	}
	if len(bundles) != 1 {
		return abstain(ReasonProvenanceAmbiguous), nil
	}

	bundle := bundles[0]
	if err := bundle.validate(); err != nil {
		return abstain(ReasonProvenanceInvalid), nil
	}
	reason, err := resolver.freshness(bundle)
	if err != nil {
		return Resolution{}, err
	}
	if reason != "" {
		return abstain(reason), nil
	}
	if bundle.workspace != workspace {
		return abstain(ReasonWorkspaceMismatch), nil
	}
	if !sameResourceRef(bundle.subject, verdict.Ref) {
		return abstain(ReasonSubjectMismatch), nil
	}

	contract, schema, err := inspectHandler(resolver.handler.Descriptor())
	if err != nil || contract != bundle.handler || bundle.source.Kind != contract.Kind {
		return abstain(ReasonHandlerContractDrift), nil
	}
	arguments, err := bundle.arguments()
	if err != nil {
		return abstain(ReasonProvenanceInvalid), nil
	}
	target, canonical, err := resolver.handler.CanonicalizeOpenPRArgs(arguments)
	if err != nil {
		return abstain(ReasonHandlerRejected), nil
	}
	if err := ctx.Err(); err != nil {
		return Resolution{}, fmt.Errorf("resolve GitOps provenance: %w", err)
	}
	if !targetMatchesRepository(target, contract.Kind, bundle.repository) {
		return abstain(ReasonHandlerTargetMismatch), nil
	}
	if err := schema.Validate(canonical); err != nil {
		return abstain(ReasonHandlerContractDrift), nil
	}
	if !canonicalMatchesBundle(canonical, bundle) {
		return abstain(ReasonHandlerOutputMismatch), nil
	}
	postContract, _, err := inspectHandler(resolver.handler.Descriptor())
	if err != nil || postContract != contract {
		return abstain(ReasonHandlerContractDrift), nil
	}
	if err := ctx.Err(); err != nil {
		return Resolution{}, fmt.Errorf("resolve GitOps provenance: %w", err)
	}
	reason, err = resolver.freshness(bundle)
	if err != nil {
		return Resolution{}, err
	}
	if reason != "" {
		return abstain(reason), nil
	}
	digest := sha256.Sum256(canonical)
	return Resolution{
		Status:          ResolutionReady,
		Target:          cloneResourceRef(target),
		Arguments:       append(json.RawMessage(nil), canonical...),
		ArgumentsDigest: "sha256:" + hex.EncodeToString(digest[:]),
		EvidenceRefs:    cloneResourceRefs(bundle.evidenceRefs),
	}, nil
}

func (resolver *GitOpsResolver) freshness(bundle GitOpsProvenanceBundle) (AbstentionReason, error) {
	now := resolver.now().UTC()
	if now.IsZero() {
		return "", fmt.Errorf("resolve GitOps provenance: clock returned an invalid time")
	}
	if now.Before(bundle.observedAt) {
		return ReasonProvenanceFuture, nil
	}
	if !now.Before(bundle.validUntil) {
		return ReasonProvenanceStale, nil
	}
	return "", nil
}

type openPRArguments struct {
	BaseRef         string       `json:"base_ref"`
	ExpectedBaseSHA string       `json:"expected_base_sha"`
	Title           string       `json:"title"`
	Body            string       `json:"body,omitempty"`
	CommitMessage   string       `json:"commit_message"`
	Changes         []fileChange `json:"changes"`
}

type fileChange struct {
	Operation       string  `json:"operation"`
	Path            string  `json:"path"`
	Content         *string `json:"content"`
	ExpectedBlobSHA *string `json:"expected_blob_sha"`
}

func (bundle GitOpsProvenanceBundle) arguments() (json.RawMessage, error) {
	content := bundle.desiredContent
	observed := bundle.observedBlobSHA
	encoded, err := json.Marshal(openPRArguments{
		BaseRef: bundle.baseRef, ExpectedBaseSHA: bundle.baseCommit,
		Title: bundle.title, Body: bundle.body, CommitMessage: bundle.commitMessage,
		Changes: []fileChange{{
			Operation: "update", Path: bundle.filePath,
			Content: &content, ExpectedBlobSHA: &observed,
		}},
	})
	if err != nil {
		return nil, fmt.Errorf("encode GitOps provenance arguments")
	}
	return json.RawMessage(encoded), nil
}

func (bundle GitOpsProvenanceBundle) validate() error {
	if bundle.version != GitOpsProvenanceVersion || tenancy.ValidateWorkspaceID(bundle.workspace) != nil ||
		validateStableRef(bundle.subject) != nil || bundle.observedAt.IsZero() || bundle.validUntil.IsZero() ||
		!bundle.observedAt.Before(bundle.validUntil) || bundle.validUntil.Sub(bundle.observedAt) > maxBundleValidity ||
		validateSource(bundle.source) != nil ||
		validateHandlerContract(bundle.handler) != nil || bundle.source.Kind != bundle.handler.Kind ||
		validateRepository(bundle.repository) != nil || bundle.source.NativeID != bundle.repository.nativeID() ||
		!validProvenanceBaseRef(bundle.baseRef) || !validObjectID(bundle.baseCommit) ||
		validateSafeText(bundle.filePath, maxIdentityBytes, false) != nil ||
		!validObjectID(bundle.observedBlobSHA) ||
		validateSafeText(bundle.title, maxIdentityBytes, false) != nil ||
		validateMultilineText(bundle.body, 16<<10, true) != nil ||
		validateSafeText(bundle.commitMessage, maxIdentityBytes, false) != nil ||
		!validBundleContent(bundle.desiredContent) || len(bundle.evidenceRefs) == 0 {
		return fmt.Errorf("GitOps provenance bundle is invalid")
	}
	for _, ref := range bundle.evidenceRefs {
		if validateStableRef(ref) != nil {
			return fmt.Errorf("GitOps provenance evidence is invalid")
		}
	}
	return nil
}

func inspectHandler(descriptor connector.Descriptor) (HandlerContract, *intentargs.Schema, error) {
	if descriptor.Kind != gitHubSourceKind ||
		descriptor.ConnKind != connector.KindTypedAction || descriptor.Owner != "sith" ||
		validateSafeText(descriptor.AdapterVersion, maxIdentityBytes, false) != nil ||
		!slices.Equal(descriptor.WireVersions, []connector.WireVersion{connector.CurrentWireVersion()}) ||
		!slices.Equal(descriptor.Capabilities, []connector.Capability{connector.CapPlan}) ||
		!slices.Equal(descriptor.Verbs, []intent.Verb{intent.VerbGitOpsOpenPR}) ||
		len(descriptor.ArgSchemas) != 1 {
		return HandlerContract{}, nil, fmt.Errorf("handler descriptor is not the exact GitOps planning contract")
	}
	raw, present := descriptor.ArgSchemas[intent.VerbGitOpsOpenPR]
	if !present || len(raw) == 0 {
		return HandlerContract{}, nil, fmt.Errorf("handler schema is missing")
	}
	schema, err := intentargs.Compile(append(json.RawMessage(nil), raw...))
	if err != nil {
		return HandlerContract{}, nil, fmt.Errorf("handler schema is invalid")
	}
	digest := sha256.Sum256(raw)
	return HandlerContract{
		Kind: descriptor.Kind, AdapterVersion: descriptor.AdapterVersion,
		SchemaDigest: "sha256:" + hex.EncodeToString(digest[:]),
	}, schema, nil
}

func validateHandlerContract(contract HandlerContract) error {
	if validateSafeText(contract.Kind, maxIdentityBytes, false) != nil ||
		validateSafeText(contract.AdapterVersion, maxIdentityBytes, false) != nil || !validDigest(contract.SchemaDigest) {
		return fmt.Errorf("handler contract is invalid")
	}
	return nil
}

func validateSource(source SourceIdentity) error {
	if source.Kind != gitHubSourceKind || source.AdapterVersion != GitOpsSourceAdapterVersion ||
		validateSafeText(source.NativeID, maxIdentityBytes, false) != nil {
		return fmt.Errorf("source identity is invalid")
	}
	return nil
}

func validateRepository(repository RepositoryIdentity) error {
	if validateSafeText(repository.Host, maxIdentityBytes, false) != nil ||
		validateSafeText(repository.Owner, maxIdentityBytes, false) != nil ||
		validateSafeText(repository.Repository, maxIdentityBytes, false) != nil ||
		repository.Host != strings.ToLower(repository.Host) || strings.Contains(repository.Host, "://") ||
		strings.ContainsAny(repository.Host+repository.Owner+repository.Repository, "/\\") ||
		strings.HasSuffix(strings.ToLower(repository.Repository), ".git") {
		return fmt.Errorf("repository identity is invalid")
	}
	return nil
}

func (repository RepositoryIdentity) nativeID() string {
	return repository.Host + "/" + repository.Owner + "/" + repository.Repository
}

func validBrainVerdictRef(verdict brain.Verdict) bool {
	if validateStableRef(verdict.Ref) != nil || len(verdict.Citations) == 0 {
		return false
	}
	attached := false
	for _, citation := range verdict.Citations {
		if citation.Stale || validateStableRef(citation.Ref) != nil {
			return false
		}
		attached = attached || sameResourceRef(citation.Ref, verdict.Ref)
	}
	return attached
}

func targetMatchesRepository(target fleet.ResourceRef, sourceKind string, repository RepositoryIdentity) bool {
	return len(target.Attributes) == 0 && target.SourceKind == sourceKind && target.Scope == repository.Host &&
		target.Kind == openPRTargetKind && target.Namespace == repository.Owner && target.Name == repository.Repository
}

func validateStableRef(ref fleet.ResourceRef) error {
	if len(ref.Attributes) != 0 || validateSafeText(ref.SourceKind, maxIdentityBytes, false) != nil ||
		validateSafeText(ref.Scope, maxIdentityBytes, false) != nil ||
		validateSafeText(ref.Kind, maxIdentityBytes, false) != nil ||
		validateSafeText(ref.Namespace, maxIdentityBytes, true) != nil ||
		validateSafeText(ref.Name, maxIdentityBytes, false) != nil {
		return fmt.Errorf("resource reference is invalid")
	}
	return nil
}

func validateSafeText(value string, maximum int, allowEmpty bool) error {
	if (!allowEmpty && value == "") || len(value) > maximum || !utf8.ValidString(value) ||
		strings.TrimSpace(value) != value || strings.ContainsRune(value, '\x00') {
		return fmt.Errorf("text is invalid")
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return fmt.Errorf("text is invalid")
		}
	}
	return nil
}

func validateMultilineText(value string, maximum int, allowEmpty bool) error {
	if (!allowEmpty && value == "") || len(value) > maximum || !utf8.ValidString(value) ||
		strings.TrimSpace(value) != value || strings.ContainsRune(value, '\x00') {
		return fmt.Errorf("text is invalid")
	}
	for _, character := range value {
		if unicode.IsControl(character) && character != '\n' && character != '\t' {
			return fmt.Errorf("text is invalid")
		}
	}
	return nil
}

func validBundleContent(value string) bool {
	return len(value) <= maxBundleContentBytes && utf8.ValidString(value) && !strings.ContainsRune(value, '\x00')
}

func validDigest(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+sha256.Size*2 {
		return false
	}
	hexValue := strings.TrimPrefix(value, "sha256:")
	_, err := hex.DecodeString(hexValue)
	return err == nil && hexValue == strings.ToLower(hexValue)
}

func validObjectID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && value == strings.ToLower(value)
}

func validProvenanceBaseRef(value string) bool {
	return validateSafeText(value, maxIdentityBytes, false) == nil && !strings.EqualFold(value, "HEAD") &&
		!strings.HasPrefix(value, "-") && !strings.HasPrefix(strings.ToLower(value), "refs/") && !validObjectID(value)
}

func canonicalMatchesBundle(canonical json.RawMessage, bundle GitOpsProvenanceBundle) bool {
	var decoded openPRArguments
	if err := json.Unmarshal(canonical, &decoded); err != nil || len(decoded.Changes) != 1 {
		return false
	}
	change := decoded.Changes[0]
	return decoded.BaseRef == bundle.baseRef && decoded.ExpectedBaseSHA == bundle.baseCommit &&
		decoded.Title == bundle.title && decoded.Body == bundle.body && decoded.CommitMessage == bundle.commitMessage &&
		change.Operation == "update" && change.Path == bundle.filePath && change.Content != nil &&
		*change.Content == bundle.desiredContent && change.ExpectedBlobSHA != nil &&
		*change.ExpectedBlobSHA == bundle.observedBlobSHA
}

func abstain(reasons ...AbstentionReason) Resolution {
	return Resolution{Status: ResolutionAbstained, Reasons: append([]AbstentionReason(nil), reasons...)}
}

func isNilHandler(handler GitOpsHandler) bool {
	if handler == nil {
		return true
	}
	value := reflect.ValueOf(handler)
	return (value.Kind() == reflect.Chan || value.Kind() == reflect.Func || value.Kind() == reflect.Interface ||
		value.Kind() == reflect.Map || value.Kind() == reflect.Pointer || value.Kind() == reflect.Slice) && value.IsNil()
}

func cloneResourceRefs(refs []fleet.ResourceRef) []fleet.ResourceRef {
	cloned := make([]fleet.ResourceRef, len(refs))
	for index, ref := range refs {
		cloned[index] = cloneResourceRef(ref)
	}
	return cloned
}

func cloneResourceRef(ref fleet.ResourceRef) fleet.ResourceRef {
	cloned := ref
	if ref.Attributes != nil {
		cloned.Attributes = make(map[string]string, len(ref.Attributes))
		for key, value := range ref.Attributes {
			cloned.Attributes[key] = value
		}
	}
	return cloned
}

func sameResourceRef(left, right fleet.ResourceRef) bool {
	return left.SourceKind == right.SourceKind && left.Scope == right.Scope && left.Kind == right.Kind &&
		left.Namespace == right.Namespace && left.Name == right.Name && len(left.Attributes) == 0 && len(right.Attributes) == 0
}

func resourceRefLess(left, right fleet.ResourceRef) bool {
	leftParts := [...]string{left.SourceKind, left.Scope, left.Kind, left.Namespace, left.Name}
	rightParts := [...]string{right.SourceKind, right.Scope, right.Kind, right.Namespace, right.Name}
	for index := range leftParts {
		if leftParts[index] != rightParts[index] {
			return leftParts[index] < rightParts[index]
		}
	}
	return false
}
