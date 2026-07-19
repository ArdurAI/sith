// SPDX-License-Identifier: Apache-2.0

package github

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/ArdurAI/sith/internal/connector"
	"github.com/ArdurAI/sith/internal/fleet"
	"github.com/ArdurAI/sith/internal/intent"
	"github.com/ArdurAI/sith/internal/intentargs"
)

var (
	openPRBaseSHA   = strings.Repeat("a", 40)
	openPRUpdateSHA = strings.Repeat("b", 40)
	openPRDeleteSHA = strings.Repeat("c", 40)
)

func TestOpenPRPlannerRegistersOnlyBoundedPlanningCapability(t *testing.T) {
	t.Parallel()
	planner := newTestOpenPRPlanner(t)
	registry := connector.NewRegistry()
	if err := registry.Register(func() (connector.Connector, error) { return planner, nil }); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	descriptors := registry.Descriptors()
	if len(descriptors) != 1 {
		t.Fatalf("descriptors = %#v", descriptors)
	}
	descriptor := descriptors[0]
	if descriptor.Kind != Kind || descriptor.ConnKind != connector.KindTypedAction || descriptor.AdapterVersion != "gitops-open-pr/2026-03-10" ||
		!slices.Equal(descriptor.WireVersions, []connector.WireVersion{connector.CurrentWireVersion()}) ||
		!slices.Equal(descriptor.Capabilities, []connector.Capability{connector.CapPlan}) ||
		!slices.Equal(descriptor.Verbs, []intent.Verb{intent.VerbGitOpsOpenPR}) || len(descriptor.ArgSchemas) != 1 {
		t.Fatalf("descriptor = %#v", descriptor)
	}
	if _, err := registry.PlannerForVerb(intent.VerbGitOpsOpenPR); err != nil {
		t.Fatalf("PlannerForVerb() error = %v", err)
	}
	if _, err := registry.ExecutorForVerb(intent.VerbGitOpsOpenPR); !strings.Contains(err.Error(), "execute") {
		t.Fatalf("ExecutorForVerb() error = %v, want unavailable execute", err)
	}
	if _, err := registry.VerifierForVerb(intent.VerbGitOpsOpenPR); !strings.Contains(err.Error(), "verify") {
		t.Fatalf("VerifierForVerb() error = %v, want unavailable verify", err)
	}
}

func TestNewOpenPRPlannerRejectsUnsafeRepositoryPolicies(t *testing.T) {
	t.Parallel()
	valid := testOpenPRConfig()
	tests := []struct {
		name   string
		mutate func(*OpenPRPlannerConfig)
	}{
		{"host scheme", func(config *OpenPRPlannerConfig) { config.Host = "https://github.com" }},
		{"uppercase host", func(config *OpenPRPlannerConfig) { config.Host = "GitHub.com" }},
		{"owner path", func(config *OpenPRPlannerConfig) { config.Owner = "ArdurAI/other" }},
		{"repository suffix", func(config *OpenPRPlannerConfig) { config.Repository = "sith.git" }},
		{"base traversal", func(config *OpenPRPlannerConfig) { config.BaseRef = "release/../main" }},
		{"base reflog", func(config *OpenPRPlannerConfig) { config.BaseRef = "main@{1}" }},
		{"base lock", func(config *OpenPRPlannerConfig) { config.BaseRef = "main.lock" }},
		{"base hidden", func(config *OpenPRPlannerConfig) { config.BaseRef = ".hidden" }},
		{"base control", func(config *OpenPRPlannerConfig) { config.BaseRef = "main\nother" }},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			config := valid
			test.mutate(&config)
			if planner, err := NewOpenPRPlanner(config); err == nil || planner != nil {
				t.Fatalf("NewOpenPRPlanner() = %#v, %v, want rejection", planner, err)
			}
		})
	}
}

func TestOpenPRPlannerProducesDeterministicDigestOnlyPlan(t *testing.T) {
	t.Parallel()
	planner := newTestOpenPRPlanner(t)
	registry := connector.NewRegistry()
	if err := registry.Register(func() (connector.Connector, error) { return planner, nil }); err != nil {
		t.Fatal(err)
	}

	first := validOpenPRIntent(t)
	second := validOpenPRIntent(t)
	var decoded openPRArgs
	if err := json.Unmarshal(second.Args, &decoded); err != nil {
		t.Fatal(err)
	}
	slices.Reverse(decoded.Changes)
	second.Args = encodeOpenPRArgs(t, decoded)

	firstPlan, err := registry.Plan(context.Background(), first)
	if err != nil {
		t.Fatalf("first Plan() error = %v", err)
	}
	secondPlan, err := registry.Plan(context.Background(), second)
	if err != nil {
		t.Fatalf("second Plan() error = %v", err)
	}
	wantTarget := fleet.ResourceRef{SourceKind: Kind, Scope: "github.com", Kind: openPRTargetKind, Namespace: "ArdurAI", Name: "sith"}
	if firstPlan.IntentID != "intent-228" || firstPlan.Verb != intent.VerbGitOpsOpenPR || !firstPlan.Reversible ||
		!firstPlan.Target.Equal(wantTarget) || len(firstPlan.Target.Attributes) != 0 {
		t.Fatalf("plan identity = %#v", firstPlan)
	}
	if !firstPlan.Diff.Ref.Equal(firstPlan.Target) || !firstPlan.Diff.Drifted || len(firstPlan.Diff.Hunks) != 3 || len(firstPlan.Steps) != 8 {
		t.Fatalf("plan diff/steps = %#v / %#v", firstPlan.Diff, firstPlan.Steps)
	}
	for index, wantPath := range []string{"config/new.yaml", "deploy/api.yaml", "deploy/old.yaml"} {
		if firstPlan.Diff.Hunks[index].Path != wantPath {
			t.Fatalf("hunk paths = %#v", firstPlan.Diff.Hunks)
		}
	}
	if !slices.Equal(firstPlan.Diff.Hunks, secondPlan.Diff.Hunks) {
		t.Fatalf("equivalent change order produced different hunks:\n%#v\n%#v", firstPlan.Diff.Hunks, secondPlan.Diff.Hunks)
	}

	firstHead := plannedHeadRef(t, firstPlan)
	secondHead := plannedHeadRef(t, secondPlan)
	if firstHead != secondHead || !strings.HasPrefix(firstHead, "sith/intent-") || len(firstHead) != len("sith/intent-")+24 {
		t.Fatalf("head refs = %q and %q", firstHead, secondHead)
	}
	for _, step := range firstPlan.Steps {
		if strings.Contains(step.API, "PATCH") || strings.Contains(step.API, "/contents/") || strings.Contains(step.API, "git/refs/{ref}") {
			t.Fatalf("plan contains forbidden mutation API: %#v", step)
		}
	}
	encoded, err := json.Marshal(firstPlan)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"replicas: 3", "old manifest", "sensitive PR title", "private rationale", "secret commit message"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("plan leaked raw input %q: %s", secret, encoded)
		}
	}
	for _, expected := range []string{"sha256:", "config/new.yaml", "verified-base-tree", "planned-commit"} {
		if !strings.Contains(string(encoded), expected) {
			t.Fatalf("plan is missing inspectable marker %q: %s", expected, encoded)
		}
	}

	secondBeforeMutation := slices.Clone(second.Args)
	first.Args[0] = '!'
	if !slices.Equal(secondBeforeMutation, second.Args) {
		t.Fatal("test setup unexpectedly shared argument storage")
	}
	if plannedHeadRef(t, firstPlan) != firstHead {
		t.Fatal("plan retained mutable caller argument storage")
	}
}

func TestOpenPRPlannerRejectsSchemaAndSemanticAttacksWithoutEcho(t *testing.T) {
	t.Parallel()
	planner := newTestOpenPRPlanner(t)
	valid := validOpenPRIntent(t)
	marker := "do-not-echo-sensitive-marker"
	tests := []struct {
		name   string
		mutate func(*connector.Intent)
	}{
		{"duplicate JSON member", func(request *connector.Intent) {
			request.Args = []byte(strings.Replace(string(request.Args), `"base_ref":"dev"`, `"base_ref":"dev","base_ref":"dev"`, 1))
		}},
		{"unknown schema member", mutateOpenPRArgs(t, func(args *map[string]any) { (*args)["credential"] = marker })},
		{"wrong base", mutateTypedOpenPRArgs(t, func(args *openPRArgs) { args.BaseRef = marker })},
		{"uppercase base SHA", mutateTypedOpenPRArgs(t, func(args *openPRArgs) { args.ExpectedBaseSHA = strings.Repeat("A", 40) })},
		{"title newline", mutateTypedOpenPRArgs(t, func(args *openPRArgs) { args.Title = "title\n" + marker })},
		{"body carriage return", mutateTypedOpenPRArgs(t, func(args *openPRArgs) { args.Body = "body\r" + marker })},
		{"commit tab", mutateTypedOpenPRArgs(t, func(args *openPRArgs) { args.CommitMessage = "message\t" + marker })},
		{"no changes", mutateTypedOpenPRArgs(t, func(args *openPRArgs) { args.Changes = nil })},
		{"create missing content", mutateTypedOpenPRArgs(t, func(args *openPRArgs) { args.Changes[0].Content = nil })},
		{"create unexpected SHA", mutateTypedOpenPRArgs(t, func(args *openPRArgs) { args.Changes[0].ExpectedBlobSHA = stringPointer(openPRUpdateSHA) })},
		{"update missing SHA", mutateTypedOpenPRArgs(t, func(args *openPRArgs) { args.Changes[1].ExpectedBlobSHA = nil })},
		{"delete has content", mutateTypedOpenPRArgs(t, func(args *openPRArgs) { args.Changes[2].Content = stringPointer(marker) })},
		{"case-colliding paths", mutateTypedOpenPRArgs(t, func(args *openPRArgs) { args.Changes[1].Path = strings.ToUpper(args.Changes[0].Path) })},
		{"content NUL", mutateTypedOpenPRArgs(t, func(args *openPRArgs) { args.Changes[0].Content = stringPointer("data\x00" + marker) })},
		{"content control", mutateTypedOpenPRArgs(t, func(args *openPRArgs) { args.Changes[0].Content = stringPointer("data\x01" + marker) })},
		{"oversized file", mutateTypedOpenPRArgs(t, func(args *openPRArgs) {
			args.Changes[0].Content = stringPointer(strings.Repeat("x", maxFileContentBytes+1))
		})},
		{"oversized aggregate", mutateTypedOpenPRArgs(t, func(args *openPRArgs) {
			content := strings.Repeat("x", (maxTotalContentBytes/4)+1)
			args.Changes = []fileChange{
				{Operation: "create", Path: "a.yaml", Content: stringPointer(content)},
				{Operation: "create", Path: "b.yaml", Content: stringPointer(content)},
				{Operation: "create", Path: "c.yaml", Content: stringPointer(content)},
				{Operation: "create", Path: "d.yaml", Content: stringPointer(content)},
			}
		})},
		{"target host", func(request *connector.Intent) { request.Target.Scope = marker + ".example" }},
		{"target attributes", func(request *connector.Intent) { request.Target.Attributes = map[string]string{"token": marker} }},
		{"intent identifier", func(request *connector.Intent) { request.ID = marker + "\nother" }},
		{"wrong verb", func(request *connector.Intent) { request.Verb = intent.VerbArgoCDSync }},
	}
	for _, pathAttack := range []string{
		"/etc/passwd", "../secret", "deploy/../secret", "deploy\\secret", ".git/config", "x/.GIT/config",
		".gitmodules", "nested/.gitmodules", ".github/workflows/release.yml", ".GITHUB/WORKFLOWS/release.yml",
		"deploy/se cret.yaml", "deploy/π.yaml", "deploy//api.yaml", "deploy/api.yaml/",
	} {
		pathAttack := pathAttack
		tests = append(tests, struct {
			name   string
			mutate func(*connector.Intent)
		}{"path " + fmt.Sprintf("%q", pathAttack), mutateTypedOpenPRArgs(t, func(args *openPRArgs) {
			args.Title = marker
			args.Changes[0].Path = pathAttack
		})})
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			request := cloneConnectorIntent(valid)
			test.mutate(&request)
			_, err := planner.Plan(context.Background(), request)
			if err == nil {
				t.Fatal("Plan() accepted malicious request")
			}
			if strings.Contains(err.Error(), marker) || len(err.Error()) > 160 {
				t.Fatalf("error leaked or was unbounded: %q", err)
			}
		})
	}
}

func TestOpenPRPlannerIsConcurrentAndImmutable(t *testing.T) {
	t.Parallel()
	planner := newTestOpenPRPlanner(t)
	request := validOpenPRIntent(t)
	want, err := planner.Plan(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	wantJSON, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}

	const workers = 64
	errors := make(chan error, workers)
	var wait sync.WaitGroup
	for index := 0; index < workers; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			got, planErr := planner.Plan(context.Background(), cloneConnectorIntent(request))
			if planErr != nil {
				errors <- planErr
				return
			}
			gotJSON, marshalErr := json.Marshal(got)
			if marshalErr != nil {
				errors <- marshalErr
				return
			}
			if !slices.Equal(gotJSON, wantJSON) {
				errors <- fmt.Errorf("nondeterministic plan")
			}
		}()
	}
	wait.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
}

func FuzzOpenPRPlannerNeverPanicsOrEchoesArgs(f *testing.F) {
	planner := newTestOpenPRPlanner(f)
	valid := validOpenPRIntent(f)
	f.Add([]byte(slices.Clone(valid.Args)))
	f.Add([]byte(`{"base_ref":"dev","changes":[]}`))
	f.Add([]byte(`{"base_ref":"marker","base_ref":"marker"}`))
	f.Fuzz(func(t *testing.T, document []byte) {
		request := cloneConnectorIntent(valid)
		request.Args = document
		plan, err := planner.Plan(context.Background(), request)
		if err != nil {
			if len(err.Error()) > 160 {
				t.Fatalf("unbounded error length %d", len(err.Error()))
			}
			return
		}
		encoded, marshalErr := json.Marshal(plan)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if len(encoded) > 32<<10 || strings.Contains(string(encoded), "replicas: 3") {
			t.Fatalf("invalid or leaking plan: %s", encoded)
		}
	})
}

func newTestOpenPRPlanner(t testing.TB) *OpenPRPlanner {
	t.Helper()
	planner, err := NewOpenPRPlanner(testOpenPRConfig())
	if err != nil {
		t.Fatalf("NewOpenPRPlanner() error = %v", err)
	}
	return planner
}

func testOpenPRConfig() OpenPRPlannerConfig {
	return OpenPRPlannerConfig{Host: "github.com", Owner: "ArdurAI", Repository: "sith", BaseRef: "dev"}
}

func validOpenPRIntent(t testing.TB) connector.Intent {
	t.Helper()
	return connector.Intent{
		ID: "intent-228", Workspace: "workspace-a", Actor: "user:operator", Verb: intent.VerbGitOpsOpenPR,
		Target: fleet.ResourceRef{SourceKind: Kind, Scope: "github.com", Kind: openPRTargetKind, Namespace: "ArdurAI", Name: "sith"},
		Args: encodeOpenPRArgs(t, openPRArgs{
			BaseRef: "dev", ExpectedBaseSHA: openPRBaseSHA, Title: "sensitive PR title", Body: "private rationale",
			CommitMessage: "secret commit message",
			Changes: []fileChange{
				{Operation: "create", Path: "config/new.yaml", Content: stringPointer("")},
				{Operation: "update", Path: "deploy/api.yaml", Content: stringPointer("replicas: 3\n"), ExpectedBlobSHA: stringPointer(openPRUpdateSHA)},
				{Operation: "delete", Path: "deploy/old.yaml", ExpectedBlobSHA: stringPointer(openPRDeleteSHA)},
			},
		}),
		Justification: "propose desired-state change", Signature: "future-signed-intent",
	}
}

func encodeOpenPRArgs(t testing.TB, args openPRArgs) json.RawMessage {
	t.Helper()
	document, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	return document
}

func plannedHeadRef(t testing.TB, plan connector.ActionPlan) string {
	t.Helper()
	for _, step := range plan.Steps {
		if step.Description != "create deterministic proposal ref" {
			continue
		}
		var params refCreateParams
		if err := json.Unmarshal(step.Params, &params); err != nil {
			t.Fatalf("decode ref params: %v", err)
		}
		return params.HeadRef
	}
	t.Fatal("plan has no create-ref step")
	return ""
}

func mutateTypedOpenPRArgs(t *testing.T, mutate func(*openPRArgs)) func(*connector.Intent) {
	t.Helper()
	return func(request *connector.Intent) {
		var args openPRArgs
		if err := json.Unmarshal(request.Args, &args); err != nil {
			t.Fatal(err)
		}
		mutate(&args)
		request.Args = encodeOpenPRArgs(t, args)
	}
}

func mutateOpenPRArgs(t *testing.T, mutate func(*map[string]any)) func(*connector.Intent) {
	t.Helper()
	return func(request *connector.Intent) {
		var args map[string]any
		if err := json.Unmarshal(request.Args, &args); err != nil {
			t.Fatal(err)
		}
		mutate(&args)
		document, err := json.Marshal(args)
		if err != nil {
			t.Fatal(err)
		}
		request.Args = document
	}
}

func cloneConnectorIntent(request connector.Intent) connector.Intent {
	request.Args = slices.Clone(request.Args)
	request.EvidenceRefs = slices.Clone(request.EvidenceRefs)
	if request.Target.Attributes != nil {
		original := request.Target.Attributes
		request.Target.Attributes = map[string]string{}
		for key, value := range original {
			request.Target.Attributes[key] = value
		}
	}
	return request
}

func stringPointer(value string) *string { return &value }

func TestOpenPRSchemaRemainsBoundedAndExact(t *testing.T) {
	t.Parallel()
	schema, err := intentargs.Compile(json.RawMessage(openPRSchema))
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	valid := validOpenPRIntent(t)
	if err := schema.Validate(valid.Args); err != nil {
		t.Fatalf("Validate(valid) error = %v", err)
	}
}
