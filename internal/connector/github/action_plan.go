// SPDX-License-Identifier: Apache-2.0

package github

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ArdurAI/sith/internal/connector"
	"github.com/ArdurAI/sith/internal/fleet"
	"github.com/ArdurAI/sith/internal/intent"
	"github.com/ArdurAI/sith/internal/intentargs"
	"github.com/ArdurAI/sith/internal/tenancy"
)

const (
	openPRProtocolVersion = "gitops-open-pr/" + APIVersion
	openPRTargetKind      = "Repository"
	maxIntentIDBytes      = 253
	maxBaseRefBytes       = 128
	maxPRTitleBytes       = 256
	maxPRBodyBytes        = 8 << 10
	maxCommitMessageBytes = 512
	maxChanges            = 32
	maxChangePathBytes    = 512
	maxFileContentBytes   = 16 << 10
	maxTotalContentBytes  = 48 << 10
)

const openPRSchema = `{
  "$schema":"https://json-schema.org/draft/2020-12/schema",
  "type":"object",
  "properties":{
    "base_ref":{"type":"string","minLength":1,"maxLength":128},
    "expected_base_sha":{"type":"string","pattern":"^[0-9a-f]{40}([0-9a-f]{24})?$"},
    "title":{"type":"string","minLength":1,"maxLength":256},
    "body":{"type":"string","maxLength":8192},
    "commit_message":{"type":"string","minLength":1,"maxLength":512},
    "changes":{
      "type":"array",
      "minItems":1,
      "maxItems":32,
      "items":{
        "type":"object",
        "properties":{
          "operation":{"type":"string","enum":["create","update","delete"]},
          "path":{"type":"string","minLength":1,"maxLength":512},
          "content":{"type":"string","maxLength":16384},
          "expected_blob_sha":{"type":"string","pattern":"^[0-9a-f]{40}([0-9a-f]{24})?$"}
        },
        "required":["operation","path"],
        "additionalProperties":false
      }
    }
  },
  "required":["base_ref","expected_base_sha","title","commit_message","changes"],
  "additionalProperties":false
}`

// OpenPRPlannerConfig fixes the only repository and base branch this planner may address.
// It intentionally contains no credential, endpoint URL, or HTTP dependency.
type OpenPRPlannerConfig struct {
	Host       string
	Owner      string
	Repository string
	BaseRef    string
}

// OpenPRPlanner produces deterministic, inspectable GitHub API plans without performing I/O.
type OpenPRPlanner struct {
	config OpenPRPlannerConfig
	schema *intentargs.Schema
}

type openPRArgs struct {
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
	Content         *string `json:"content,omitempty"`
	ExpectedBlobSHA *string `json:"expected_blob_sha,omitempty"`
}

type plannedChange struct {
	Operation       string `json:"operation"`
	Path            string `json:"path"`
	ExpectedBlobSHA string `json:"expected_blob_sha,omitempty"`
	ContentDigest   string `json:"content_digest,omitempty"`
}

type refCheckParams struct {
	BaseRef     string `json:"base_ref"`
	ExpectedSHA string `json:"expected_sha"`
}

type commitLookupParams struct {
	CommitSHA string `json:"commit_sha"`
}

type treeVerifyParams struct {
	Tree    string          `json:"tree"`
	Changes []plannedChange `json:"changes"`
}

type treePlanParams struct {
	BaseTree string          `json:"base_tree"`
	Changes  []plannedChange `json:"changes"`
}

type commitPlanParams struct {
	ParentSHA     string `json:"parent_sha"`
	Tree          string `json:"tree"`
	MessageDigest string `json:"message_digest"`
}

type refCreateParams struct {
	HeadRef string `json:"head_ref"`
	Commit  string `json:"commit"`
}

type pullLookupParams struct {
	HeadRef string `json:"head_ref"`
	BaseRef string `json:"base_ref"`
}

type pullCreateParams struct {
	HeadRef     string `json:"head_ref"`
	BaseRef     string `json:"base_ref"`
	TitleDigest string `json:"title_digest"`
	BodyDigest  string `json:"body_digest"`
}

// NewOpenPRPlanner constructs the planning-only GitHub typed-action adapter.
func NewOpenPRPlanner(config OpenPRPlannerConfig) (*OpenPRPlanner, error) {
	if err := validateOpenPRConfig(config); err != nil {
		return nil, err
	}
	schema, err := intentargs.Compile(json.RawMessage(openPRSchema))
	if err != nil {
		return nil, fmt.Errorf("construct GitHub pull request planner: argument schema is invalid")
	}
	return &OpenPRPlanner{config: config, schema: schema}, nil
}

// Kind returns the canonical GitHub connector kind.
func (*OpenPRPlanner) Kind() string { return Kind }

// Capabilities declares planning only. Execution remains blocked on E3 and E5.
func (*OpenPRPlanner) Capabilities() []connector.Capability {
	return []connector.Capability{connector.CapPlan}
}

// Descriptor binds gitops.open-pr to its exact handler-owned argument schema.
func (*OpenPRPlanner) Descriptor() connector.Descriptor {
	return connector.Descriptor{
		Kind: Kind, ConnKind: connector.KindTypedAction,
		WireVersions: []connector.WireVersion{connector.CurrentWireVersion()}, AdapterVersion: openPRProtocolVersion, Owner: "sith",
		Capabilities: []connector.Capability{connector.CapPlan},
		Verbs:        []intent.Verb{intent.VerbGitOpsOpenPR},
		ArgSchemas:   map[intent.Verb]json.RawMessage{intent.VerbGitOpsOpenPR: json.RawMessage(openPRSchema)},
	}
}

// Plan returns a deterministic digest-only API plan. It never performs a provider request and
// never retains file content, PR text, credentials, or caller-controlled references in the plan.
func (planner *OpenPRPlanner) Plan(_ context.Context, request connector.Intent) (connector.ActionPlan, error) {
	if planner == nil || planner.schema == nil {
		return connector.ActionPlan{}, fmt.Errorf("plan GitHub pull request: planner is required")
	}
	if err := validateOpenPRIntent(planner.config, request); err != nil {
		return connector.ActionPlan{}, err
	}
	if err := planner.schema.Validate(request.Args); err != nil {
		return connector.ActionPlan{}, fmt.Errorf("plan GitHub pull request: args are invalid")
	}

	var args openPRArgs
	if err := json.Unmarshal(request.Args, &args); err != nil {
		return connector.ActionPlan{}, fmt.Errorf("plan GitHub pull request: decode validated args")
	}
	if err := validateOpenPRArgs(planner.config, args); err != nil {
		return connector.ActionPlan{}, err
	}
	args.Changes = append([]fileChange(nil), args.Changes...)
	sort.Slice(args.Changes, func(left, right int) bool { return args.Changes[left].Path < args.Changes[right].Path })

	canonical, err := json.Marshal(args)
	if err != nil {
		return connector.ActionPlan{}, fmt.Errorf("plan GitHub pull request: canonicalize args")
	}
	identityInput := strings.Join([]string{request.Workspace, planner.config.Host, planner.config.Owner, planner.config.Repository}, "\x00") + "\x00"
	identity := sha256.Sum256(append([]byte(identityInput), canonical...))
	headRef := "sith/intent-" + hex.EncodeToString(identity[:12])

	planned := make([]plannedChange, 0, len(args.Changes))
	hunks := make([]fleet.DiffHunk, 0, len(args.Changes))
	for _, change := range args.Changes {
		entry := plannedChange{Operation: change.Operation, Path: change.Path}
		observed := "absent"
		desired := "deleted"
		if change.ExpectedBlobSHA != nil {
			entry.ExpectedBlobSHA = *change.ExpectedBlobSHA
			observed = "blob:" + *change.ExpectedBlobSHA
		}
		if change.Content != nil {
			entry.ContentDigest = digestText(*change.Content)
			desired = entry.ContentDigest
		}
		planned = append(planned, entry)
		hunks = append(hunks, fleet.DiffHunk{Path: change.Path, Observed: observed, Desired: desired})
	}

	target := normalizedOpenPRTarget(planner.config)
	steps, err := openPRPlanSteps(args, planned, headRef)
	if err != nil {
		return connector.ActionPlan{}, err
	}
	return connector.ActionPlan{
		IntentID: request.ID, Verb: intent.VerbGitOpsOpenPR, Target: target,
		Diff:  fleet.Diff{Ref: target, Drifted: true, Hunks: hunks},
		Steps: steps, Reversible: true,
	}, nil
}

func validateOpenPRConfig(config OpenPRPlannerConfig) error {
	if validateHost(config.Host) != nil || validatePathComponent("owner", config.Owner, maxOwnerBytes) != nil ||
		validatePathComponent("repository", config.Repository, maxRepositoryBytes) != nil ||
		strings.HasSuffix(strings.ToLower(config.Repository), ".git") || !validBaseRef(config.BaseRef) {
		return fmt.Errorf("construct GitHub pull request planner: repository policy is invalid")
	}
	return nil
}

func validateOpenPRIntent(config OpenPRPlannerConfig, request connector.Intent) error {
	if err := tenancy.ValidateWorkspaceID(tenancy.WorkspaceID(request.Workspace)); err != nil {
		return fmt.Errorf("plan GitHub pull request: workspace is invalid")
	}
	if validateBoundedText(request.ID, maxIntentIDBytes, false, false) != nil {
		return fmt.Errorf("plan GitHub pull request: intent identifier is invalid")
	}
	if request.Verb != intent.VerbGitOpsOpenPR {
		return fmt.Errorf("plan GitHub pull request: verb is invalid")
	}
	if request.Target.SourceKind != Kind || request.Target.Scope != config.Host || request.Target.Kind != openPRTargetKind ||
		request.Target.Namespace != config.Owner || request.Target.Name != config.Repository || len(request.Target.Attributes) != 0 {
		return fmt.Errorf("plan GitHub pull request: target is outside repository policy")
	}
	return nil
}

func validateOpenPRArgs(config OpenPRPlannerConfig, args openPRArgs) error {
	if args.BaseRef != config.BaseRef || !validCommitSHA(args.ExpectedBaseSHA) ||
		validateBoundedText(args.Title, maxPRTitleBytes, false, false) != nil ||
		validateBoundedText(args.Body, maxPRBodyBytes, true, true) != nil ||
		validateBoundedText(args.CommitMessage, maxCommitMessageBytes, false, false) != nil ||
		len(args.Changes) == 0 || len(args.Changes) > maxChanges {
		return fmt.Errorf("plan GitHub pull request: args violate repository policy")
	}

	seen := make(map[string]struct{}, len(args.Changes))
	totalContent := 0
	for _, change := range args.Changes {
		if !validChangePath(change.Path) {
			return fmt.Errorf("plan GitHub pull request: change path is invalid")
		}
		key := strings.ToLower(change.Path)
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("plan GitHub pull request: change paths collide")
		}
		seen[key] = struct{}{}
		content := ""
		if change.Content != nil {
			content = *change.Content
		}
		if !validFileContent(content) || len(content) > maxFileContentBytes {
			return fmt.Errorf("plan GitHub pull request: change content is invalid")
		}
		switch change.Operation {
		case "create":
			if change.Content == nil || change.ExpectedBlobSHA != nil {
				return fmt.Errorf("plan GitHub pull request: create precondition is invalid")
			}
		case "update":
			if change.Content == nil || change.ExpectedBlobSHA == nil || !validCommitSHA(*change.ExpectedBlobSHA) {
				return fmt.Errorf("plan GitHub pull request: update precondition is invalid")
			}
		case "delete":
			if change.Content != nil || change.ExpectedBlobSHA == nil || !validCommitSHA(*change.ExpectedBlobSHA) {
				return fmt.Errorf("plan GitHub pull request: delete precondition is invalid")
			}
		default:
			return fmt.Errorf("plan GitHub pull request: change operation is invalid")
		}
		totalContent += len(content)
		if totalContent > maxTotalContentBytes {
			return fmt.Errorf("plan GitHub pull request: aggregate content is too large")
		}
	}
	return nil
}

func validBaseRef(value string) bool {
	if validateBoundedText(value, maxBaseRefBytes, false, false) != nil || strings.HasPrefix(value, ".") ||
		strings.HasPrefix(value, "/") || strings.HasSuffix(value, ".") || strings.HasSuffix(value, "/") ||
		strings.Contains(value, "..") || strings.Contains(value, "//") || strings.Contains(value, "@{") ||
		strings.HasSuffix(strings.ToLower(value), ".lock") {
		return false
	}
	for _, character := range value {
		if unicode.IsSpace(character) || strings.ContainsRune("~^:?*[]\\", character) {
			return false
		}
	}
	for _, component := range strings.Split(value, "/") {
		if component == "" || strings.HasPrefix(component, ".") || strings.HasSuffix(strings.ToLower(component), ".lock") {
			return false
		}
	}
	return true
}

func validChangePath(value string) bool {
	if validateBoundedText(value, maxChangePathBytes, false, false) != nil || strings.HasPrefix(value, "/") ||
		strings.HasSuffix(value, "/") || strings.Contains(value, "\\") || path.Clean(value) != value {
		return false
	}
	components := strings.Split(value, "/")
	for _, character := range value {
		letter := (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z')
		digit := character >= '0' && character <= '9'
		if !letter && !digit && character != '-' && character != '_' && character != '.' && character != '/' {
			return false
		}
	}
	for index, component := range components {
		lower := strings.ToLower(component)
		if component == "" || component == "." || component == ".." || lower == ".git" || lower == ".gitmodules" {
			return false
		}
		if index == 0 && lower == ".github" && len(components) > 1 && strings.ToLower(components[1]) == "workflows" {
			return false
		}
	}
	return true
}

func validateBoundedText(value string, maximum int, allowEmpty, allowMultiline bool) error {
	if (!allowEmpty && value == "") || len(value) > maximum || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return fmt.Errorf("text is invalid")
	}
	for _, character := range value {
		if unicode.IsControl(character) && (!allowMultiline || (character != '\n' && character != '\t')) {
			return fmt.Errorf("text is invalid")
		}
	}
	return nil
}

func validFileContent(value string) bool {
	if !utf8.ValidString(value) || strings.ContainsRune(value, '\x00') {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) && character != '\n' && character != '\t' {
			return false
		}
	}
	return true
}

func digestText(value string) string {
	digest := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(digest[:])
}

func normalizedOpenPRTarget(config OpenPRPlannerConfig) fleet.ResourceRef {
	return fleet.ResourceRef{SourceKind: Kind, Scope: config.Host, Kind: openPRTargetKind, Namespace: config.Owner, Name: config.Repository}
}

func openPRPlanSteps(args openPRArgs, changes []plannedChange, headRef string) ([]connector.PlanStep, error) {
	values := []struct {
		description string
		api         string
		params      any
	}{
		{"verify configured base ref", "GET /repos/{owner}/{repo}/git/ref/heads/{base}", refCheckParams{args.BaseRef, args.ExpectedBaseSHA}},
		{"resolve the pinned base commit tree", "GET /repos/{owner}/{repo}/git/commits/{commit}", commitLookupParams{args.ExpectedBaseSHA}},
		{"verify exact path preconditions", "GET /repos/{owner}/{repo}/git/trees/{tree}", treeVerifyParams{"verified-base-tree", changes}},
		{"construct one immutable tree", "POST /repos/{owner}/{repo}/git/trees", treePlanParams{"verified-base-tree", changes}},
		{"construct one immutable commit", "POST /repos/{owner}/{repo}/git/commits", commitPlanParams{args.ExpectedBaseSHA, "planned-tree", digestText(args.CommitMessage)}},
		{"create deterministic proposal ref", "POST /repos/{owner}/{repo}/git/refs", refCreateParams{headRef, "planned-commit"}},
		{"reconcile an idempotent existing pull request", "GET /repos/{owner}/{repo}/pulls", pullLookupParams{headRef, args.BaseRef}},
		{"create the human-reviewed pull request", "POST /repos/{owner}/{repo}/pulls", pullCreateParams{headRef, args.BaseRef, digestText(args.Title), digestText(args.Body)}},
	}
	steps := make([]connector.PlanStep, 0, len(values))
	for _, value := range values {
		encoded, err := json.Marshal(value.params)
		if err != nil {
			return nil, fmt.Errorf("plan GitHub pull request: encode API plan")
		}
		steps = append(steps, connector.PlanStep{Description: value.description, API: value.api, Params: encoded})
	}
	return steps, nil
}
