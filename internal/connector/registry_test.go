// SPDX-License-Identifier: Apache-2.0

package connector

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/ArdurAI/sith/internal/fleet"
	"github.com/ArdurAI/sith/internal/intent"
	"github.com/ArdurAI/sith/internal/intentargs"
)

type testReader struct {
	kind       string
	descriptor Descriptor
	discovery  Discovery
	query      fleet.QueryResult
}

func (reader testReader) Kind() string {
	return reader.kind
}

func (reader testReader) Capabilities() []Capability {
	return append([]Capability(nil), reader.descriptor.Capabilities...)
}

func (reader testReader) Descriptor() Descriptor {
	return cloneDescriptor(reader.descriptor)
}

func (reader testReader) Discover(_ context.Context) (Discovery, error) {
	return reader.discovery, nil
}

func (testReader) Read(_ context.Context, ref fleet.ResourceRef) (fleet.Evidence, error) {
	return fleet.Evidence{Ref: ref}, nil
}

func (reader testReader) Query(_ context.Context, _ fleet.Query) (fleet.QueryResult, error) {
	return reader.query, nil
}

type identityOnlyConnector struct {
	descriptor Descriptor
}

func (connector identityOnlyConnector) Kind() string {
	return connector.descriptor.Kind
}

func (connector identityOnlyConnector) Capabilities() []Capability {
	return append([]Capability(nil), connector.descriptor.Capabilities...)
}

func (connector identityOnlyConnector) Descriptor() Descriptor {
	return cloneDescriptor(connector.descriptor)
}

type testExecutor struct {
	descriptor  Descriptor
	planCalls   *atomic.Int64
	plannedArgs *json.RawMessage
}

func (connector testExecutor) Kind() string {
	return connector.descriptor.Kind
}

func (connector testExecutor) Capabilities() []Capability {
	return append([]Capability(nil), connector.descriptor.Capabilities...)
}

func (connector testExecutor) Descriptor() Descriptor {
	return cloneDescriptor(connector.descriptor)
}

func (connector testExecutor) Plan(_ context.Context, request Intent) (ActionPlan, error) {
	if connector.planCalls != nil {
		connector.planCalls.Add(1)
	}
	if connector.plannedArgs != nil {
		*connector.plannedArgs = request.Args
	}
	return ActionPlan{IntentID: request.ID, Verb: request.Verb}, nil
}

func (testExecutor) Execute(_ context.Context, plan ActionPlan) (ExecutionResult, error) {
	return ExecutionResult{IntentID: plan.IntentID, Applied: true}, nil
}

func (testExecutor) Verify(_ context.Context, request VerifyRequest) (Verification, error) {
	return Verification{Satisfied: request.IntentID != ""}, nil
}

func TestRegistryRegisterAndLookupReader(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	reader := newTestReader("zeta")
	if err := registry.Register(func() (Connector, error) { return reader, nil }); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	got, ok := registry.ByKind("zeta")
	if !ok || got.Kind() != "zeta" {
		t.Fatalf("ByKind() = %v/%t", got, ok)
	}
	if _, err := registry.ReaderFor("zeta"); err != nil {
		t.Fatalf("ReaderFor() error = %v", err)
	}
	if _, err := registry.ExecutorFor("zeta"); !errors.Is(err, ErrCapability) {
		t.Fatalf("ExecutorFor() error = %v, want ErrCapability", err)
	}
}

func TestRegistryRejectsInvalidConnectors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		connector Connector
	}{
		{name: "unknown taxonomy", connector: identityOnlyConnector{descriptor: testDescriptor("bad", "other")}},
		{name: "missing wire versions", connector: identityOnlyConnector{descriptor: Descriptor{Kind: "bad", ConnKind: KindReadAdapter, AdapterVersion: "1.0.0", Owner: "test"}}},
		{name: "zero wire major", connector: identityOnlyConnector{descriptor: Descriptor{Kind: "bad", ConnKind: KindReadAdapter, WireVersions: []WireVersion{{Minor: 1}}, AdapterVersion: "1.0.0", Owner: "test"}}},
		{name: "duplicate wire version", connector: identityOnlyConnector{descriptor: Descriptor{Kind: "bad", ConnKind: KindReadAdapter, WireVersions: []WireVersion{CurrentWireVersion(), CurrentWireVersion()}, AdapterVersion: "1.0.0", Owner: "test"}}},
		{name: "missing adapter version", connector: identityOnlyConnector{descriptor: Descriptor{Kind: "bad", ConnKind: KindReadAdapter, WireVersions: []WireVersion{CurrentWireVersion()}, Owner: "test"}}},
		{name: "blank adapter version", connector: identityOnlyConnector{descriptor: Descriptor{Kind: "bad", ConnKind: KindReadAdapter, WireVersions: []WireVersion{CurrentWireVersion()}, AdapterVersion: " \t", Owner: "test"}}},
		{name: "declared but not implemented", connector: identityOnlyConnector{descriptor: testDescriptorWithCapabilities("bad", KindReadAdapter, CapRead)}},
		{name: "unknown capability", connector: identityOnlyConnector{descriptor: testDescriptorWithCapabilities("bad", KindReadAdapter, "shell")}},
		{name: "read adapter with verbs", connector: identityOnlyConnector{descriptor: func() Descriptor {
			descriptor := testDescriptor("bad", KindReadAdapter)
			descriptor.Verbs = []intent.Verb{intent.VerbGitOpsOpenPR}
			return descriptor
		}()}},
		{name: "action without verbs", connector: testExecutor{descriptor: testDescriptorWithCapabilities("bad", KindTypedAction, CapExecute)}},
		{name: "action with unknown verb", connector: testExecutor{descriptor: func() Descriptor {
			descriptor := testDescriptorWithCapabilities("bad", KindTypedAction, CapExecute)
			descriptor.Verbs = []intent.Verb{"shell.exec"}
			return descriptor
		}()}},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			registry := NewRegistry()
			if err := registry.Register(func() (Connector, error) { return test.connector, nil }); err == nil {
				t.Fatal("Register() error = nil, want rejection")
			}
			if len(registry.Descriptors()) != 0 {
				t.Fatal("invalid connector was partially registered")
			}
		})
	}
}

func TestRegistryRejectsDuplicateKind(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	reader := newTestReader("duplicate")
	if err := registry.Register(func() (Connector, error) { return reader, nil }); err != nil {
		t.Fatalf("first Register() error = %v", err)
	}
	if err := registry.Register(func() (Connector, error) { return reader, nil }); err == nil {
		t.Fatal("second Register() error = nil")
	}
}

func TestRegistryRejectsDuplicateCanonicalToolAcrossTaxonomies(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	reader := newTestReader("github")
	if err := registry.Register(func() (Connector, error) { return reader, nil }); err != nil {
		t.Fatalf("first Register() error = %v", err)
	}
	brokered := identityOnlyConnector{descriptor: testDescriptor("github", KindBrokeredRead)}
	brokered.descriptor.AdapterVersion = "deep-link/v1"
	if err := registry.Register(func() (Connector, error) { return brokered, nil }); err == nil {
		t.Fatal("second canonical connector for github was accepted")
	}
	got, ok := registry.ByKind("github")
	gotReader, isReader := got.(testReader)
	if !ok || !isReader || gotReader.descriptor.AdapterVersion != reader.descriptor.AdapterVersion {
		t.Fatalf("canonical github connector changed after rejection: %v/%t", got, ok)
	}
}

func TestRegistryWithCapabilityIsDeterministic(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	for _, kind := range []string{"zeta", "alpha"} {
		reader := newTestReader(kind)
		if err := registry.Register(func() (Connector, error) { return reader, nil }); err != nil {
			t.Fatalf("Register(%s) error = %v", kind, err)
		}
	}

	got := registry.WithCapability(CapQuery)
	if len(got) != 2 || got[0].Kind() != "alpha" || got[1].Kind() != "zeta" {
		t.Fatalf("WithCapability() = %#v, want alpha then zeta", got)
	}
	if got := registry.WithCapability("unknown"); got == nil || len(got) != 0 {
		t.Fatalf("unknown WithCapability() = %#v, want allocated empty slice", got)
	}
}

func TestRegistryCanonicalizesWireVersionsAndJSONDomains(t *testing.T) {
	t.Parallel()

	reader := newTestReader("versions")
	reader.descriptor.WireVersions = []WireVersion{
		{Major: 2, Minor: 0},
		{Major: 1, Minor: 2},
		CurrentWireVersion(),
	}
	reader.descriptor.AdapterVersion = "search/ecs-v1"
	registry := NewRegistry()
	if err := registry.Register(func() (Connector, error) { return reader, nil }); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	descriptor := registry.Descriptors()[0]
	want := []WireVersion{CurrentWireVersion(), {Major: 1, Minor: 2}, {Major: 2, Minor: 0}}
	if !slices.Equal(descriptor.WireVersions, want) {
		t.Fatalf("WireVersions = %#v, want %#v", descriptor.WireVersions, want)
	}
	document, err := json.Marshal(descriptor)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if !bytes.Contains(document, []byte(`"wire_versions":[{"major":1,"minor":0}`)) ||
		!bytes.Contains(document, []byte(`"adapter_version":"search/ecs-v1"`)) ||
		bytes.Contains(document, []byte(`"protocol_version"`)) {
		t.Fatalf("descriptor JSON = %s", document)
	}
}

func TestRegistryExecutorRequiresTypedAction(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	executor := testExecutor{descriptor: Descriptor{
		Kind:           "argocd",
		ConnKind:       KindTypedAction,
		WireVersions:   []WireVersion{CurrentWireVersion()},
		AdapterVersion: "1.0.0",
		Owner:          "test",
		Capabilities:   []Capability{CapExecute},
		Verbs:          []intent.Verb{intent.VerbArgoCDSync},
		ArgSchemas:     testArgSchemas(intent.VerbArgoCDSync),
	}}
	if err := registry.Register(func() (Connector, error) { return executor, nil }); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	got, err := registry.ExecutorFor("argocd")
	if err != nil {
		t.Fatalf("ExecutorFor() error = %v", err)
	}
	result, err := got.Execute(context.Background(), ActionPlan{IntentID: "intent-1"})
	if err != nil || !result.Applied {
		t.Fatalf("Execute() = %#v, %v", result, err)
	}
}

func TestRegistryFactoryFailuresAreAtomic(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	wantErr := errors.New("construction failed")
	if err := registry.Register(func() (Connector, error) { return nil, wantErr }); !errors.Is(err, wantErr) {
		t.Fatalf("Register() error = %v, want wrapped construction error", err)
	}
	if err := registry.Register(nil); err == nil {
		t.Fatal("Register(nil) error = nil")
	}
	if len(registry.Descriptors()) != 0 {
		t.Fatal("failed factory modified registry")
	}
}

func TestRegistryRejectsDuplicateVerbOwnershipAtomically(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	first := testExecutor{descriptor: Descriptor{
		Kind: "first", ConnKind: KindTypedAction, WireVersions: []WireVersion{CurrentWireVersion()}, AdapterVersion: "1.0.0", Owner: "test",
		Capabilities: []Capability{CapExecute}, Verbs: []intent.Verb{intent.VerbGitOpsOpenPR},
		ArgSchemas: testArgSchemas(intent.VerbGitOpsOpenPR),
	}}
	second := testExecutor{descriptor: Descriptor{
		Kind: "second", ConnKind: KindTypedAction, WireVersions: []WireVersion{CurrentWireVersion()}, AdapterVersion: "1.0.0", Owner: "test",
		Capabilities: []Capability{CapExecute}, Verbs: []intent.Verb{intent.VerbGitOpsOpenPR, intent.VerbArgoCDSync},
		ArgSchemas: testArgSchemas(intent.VerbGitOpsOpenPR, intent.VerbArgoCDSync),
	}}
	if err := registry.Register(func() (Connector, error) { return first, nil }); err != nil {
		t.Fatalf("first Register() error = %v", err)
	}
	if err := registry.Register(func() (Connector, error) { return second, nil }); err == nil {
		t.Fatal("duplicate verb owner was accepted")
	}
	if _, ok := registry.ByKind("second"); ok {
		t.Fatal("failed registration modified kind index")
	}
	if _, err := registry.ExecutorForVerb(intent.VerbArgoCDSync); !errors.Is(err, ErrNotRegistered) {
		t.Fatalf("failed registration modified verb index: %v", err)
	}
	if executor, err := registry.ExecutorForVerb(intent.VerbGitOpsOpenPR); err != nil || executor.Kind() != "first" {
		t.Fatalf("original verb owner = %v, %v", executor, err)
	}
}

func TestRegistryVerbLookupFailsClosed(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	executor := testExecutor{descriptor: Descriptor{
		Kind: "git", ConnKind: KindTypedAction, WireVersions: []WireVersion{CurrentWireVersion()}, AdapterVersion: "1.0.0", Owner: "test",
		Capabilities: []Capability{CapExecute}, Verbs: []intent.Verb{intent.VerbGitOpsOpenPR},
		ArgSchemas: testArgSchemas(intent.VerbGitOpsOpenPR),
	}}
	if err := registry.Register(func() (Connector, error) { return executor, nil }); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if got, err := registry.ExecutorForVerb(intent.VerbGitOpsOpenPR); err != nil || got.Kind() != "git" {
		t.Fatalf("ExecutorForVerb() = %v, %v", got, err)
	}
	if _, err := registry.ExecutorForVerb("shell.exec"); !errors.Is(err, ErrNotRegistered) {
		t.Fatalf("unknown ExecutorForVerb() error = %v", err)
	}
	if _, err := registry.ExecutorForVerb(intent.VerbArgoCDSync); !errors.Is(err, ErrNotRegistered) {
		t.Fatalf("unregistered ExecutorForVerb() error = %v", err)
	}
	if _, err := registry.PlannerForVerb(intent.VerbGitOpsOpenPR); !errors.Is(err, ErrCapability) {
		t.Fatalf("wrong-capability PlannerForVerb() error = %v", err)
	}
	if _, err := registry.VerifierForVerb(intent.VerbGitOpsOpenPR); !errors.Is(err, ErrCapability) {
		t.Fatalf("wrong-capability VerifierForVerb() error = %v", err)
	}
}

func TestRegistryVerbLookupRoutesDeclaredCapabilities(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	action := testExecutor{descriptor: Descriptor{
		Kind: "git", ConnKind: KindTypedAction, WireVersions: []WireVersion{CurrentWireVersion()}, AdapterVersion: "1.0.0", Owner: "test",
		Capabilities: []Capability{CapPlan, CapExecute, CapVerify}, Verbs: []intent.Verb{intent.VerbGitOpsOpenPR},
		ArgSchemas: testArgSchemas(intent.VerbGitOpsOpenPR),
	}}
	if err := registry.Register(func() (Connector, error) { return action, nil }); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	planner, err := registry.PlannerForVerb(intent.VerbGitOpsOpenPR)
	if err != nil || planner.Kind() != "git" {
		t.Fatalf("PlannerForVerb() = %v, %v", planner, err)
	}
	executor, err := registry.ExecutorForVerb(intent.VerbGitOpsOpenPR)
	if err != nil || executor.Kind() != "git" {
		t.Fatalf("ExecutorForVerb() = %v, %v", executor, err)
	}
	verifier, err := registry.VerifierForVerb(intent.VerbGitOpsOpenPR)
	if err != nil || verifier.Kind() != "git" {
		t.Fatalf("VerifierForVerb() = %v, %v", verifier, err)
	}
}

func TestRegistryRejectsInvalidSchemaOwnershipAtomically(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		schemas map[intent.Verb]json.RawMessage
	}{
		{name: "missing schema", schemas: nil},
		{name: "schema for undeclared verb", schemas: testArgSchemas(intent.VerbGitOpsOpenPR, intent.VerbArgoCDSync)},
		{name: "invalid schema", schemas: map[intent.Verb]json.RawMessage{intent.VerbGitOpsOpenPR: json.RawMessage(`{"type":"object"}`)}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			registry := NewRegistry()
			action := testExecutor{descriptor: Descriptor{
				Kind: "git", ConnKind: KindTypedAction, WireVersions: []WireVersion{CurrentWireVersion()}, AdapterVersion: "1.0.0", Owner: "test",
				Capabilities: []Capability{CapPlan}, Verbs: []intent.Verb{intent.VerbGitOpsOpenPR}, ArgSchemas: test.schemas,
			}}
			if err := registry.Register(func() (Connector, error) { return action, nil }); err == nil {
				t.Fatal("Register() error = nil, want schema rejection")
			}
			if len(registry.Descriptors()) != 0 {
				t.Fatal("failed schema registration modified registry")
			}
			if _, err := registry.PlannerForVerb(intent.VerbGitOpsOpenPR); !errors.Is(err, ErrNotRegistered) {
				t.Fatalf("failed schema registration modified verb index: %v", err)
			}
		})
	}
}

func TestRegistryPlanValidatesBeforePlanner(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	planCalls := &atomic.Int64{}
	plannedArgs := &json.RawMessage{}
	action := testExecutor{descriptor: Descriptor{
		Kind: "scale", ConnKind: KindTypedAction, WireVersions: []WireVersion{CurrentWireVersion()}, AdapterVersion: "1.0.0", Owner: "test",
		Capabilities: []Capability{CapPlan}, Verbs: []intent.Verb{intent.VerbDeploymentScale},
		ArgSchemas: map[intent.Verb]json.RawMessage{intent.VerbDeploymentScale: json.RawMessage(`{
		  "$schema":"https://json-schema.org/draft/2020-12/schema",
		  "type":"object",
		  "properties":{"replicas":{"type":"integer","minimum":0,"maximum":10}},
		  "required":["replicas"],
		  "additionalProperties":false
		}`)},
	}, planCalls: planCalls, plannedArgs: plannedArgs}
	if err := registry.Register(func() (Connector, error) { return action, nil }); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	originalArgs := json.RawMessage(`{"replicas":3}`)
	plan, err := registry.Plan(context.Background(), Intent{
		ID: "intent-1", Verb: intent.VerbDeploymentScale, Args: originalArgs,
	})
	if err != nil || plan.IntentID != "intent-1" || plan.Verb != intent.VerbDeploymentScale {
		t.Fatalf("Plan() = %#v, %v", plan, err)
	}
	if got := planCalls.Load(); got != 1 {
		t.Fatalf("planner calls after valid intent = %d, want 1", got)
	}
	originalArgs[1] = '!'
	if got := string(*plannedArgs); got != `{"replicas":3}` {
		t.Fatalf("planner args changed with caller storage: %q", got)
	}
	for _, args := range []json.RawMessage{
		json.RawMessage(`{"replicas":11}`),
		json.RawMessage(`{"replicas":3,"force":true}`),
		json.RawMessage(`{"replicas":3,"replicas":4}`),
		json.RawMessage(`[]`),
	} {
		if _, err := registry.Plan(context.Background(), Intent{Verb: intent.VerbDeploymentScale, Args: args}); !errors.Is(err, intentargs.ErrInvalidArgs) {
			t.Fatalf("Plan(%s) error = %v, want ErrInvalidArgs", args, err)
		}
		if got := planCalls.Load(); got != 1 {
			t.Fatalf("planner calls after invalid intent = %d, want 1", got)
		}
	}
	if _, err := registry.Plan(context.Background(), Intent{Verb: intent.VerbArgoCDSync, Args: json.RawMessage(`{}`)}); !errors.Is(err, ErrNotRegistered) {
		t.Fatalf("Plan(unregistered) error = %v, want ErrNotRegistered", err)
	}
	if got := planCalls.Load(); got != 1 {
		t.Fatalf("planner calls after unregistered intent = %d, want 1", got)
	}
}

func TestRegistryDescriptorsCloneArgumentSchemas(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	action := testExecutor{descriptor: Descriptor{
		Kind: "git", ConnKind: KindTypedAction, WireVersions: []WireVersion{CurrentWireVersion()}, AdapterVersion: "1.0.0", Owner: "test",
		Capabilities: []Capability{CapPlan}, Verbs: []intent.Verb{intent.VerbGitOpsOpenPR},
		ArgSchemas: testArgSchemas(intent.VerbGitOpsOpenPR),
	}}
	if err := registry.Register(func() (Connector, error) { return action, nil }); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	descriptors := registry.Descriptors()
	document := descriptors[0].ArgSchemas[intent.VerbGitOpsOpenPR]
	document[0] = '!'
	delete(descriptors[0].ArgSchemas, intent.VerbGitOpsOpenPR)
	descriptors[0].WireVersions[0] = WireVersion{Major: 9, Minor: 9}

	again := registry.Descriptors()
	got := again[0].ArgSchemas[intent.VerbGitOpsOpenPR]
	if len(got) == 0 || got[0] == '!' {
		t.Fatal("Descriptors() exposed mutable argument schema storage")
	}
	if again[0].WireVersions[0] != CurrentWireVersion() {
		t.Fatal("Descriptors() exposed mutable wire-version storage")
	}
}

func newTestReader(kind string) testReader {
	capabilities := []Capability{CapDiscover, CapRead, CapQuery}
	return testReader{
		kind: kind,
		descriptor: Descriptor{
			Kind:           kind,
			ConnKind:       KindReadAdapter,
			WireVersions:   []WireVersion{CurrentWireVersion()},
			AdapterVersion: "1.0.0",
			Owner:          "test",
			Capabilities:   capabilities,
		},
	}
}

func testDescriptor(kind string, connKind ConnectorKind) Descriptor {
	return Descriptor{
		Kind: kind, ConnKind: connKind, WireVersions: []WireVersion{CurrentWireVersion()}, AdapterVersion: "1.0.0", Owner: "test",
	}
}

func testDescriptorWithCapabilities(kind string, connKind ConnectorKind, capabilities ...Capability) Descriptor {
	descriptor := testDescriptor(kind, connKind)
	descriptor.Capabilities = capabilities
	return descriptor
}

func testArgSchemas(verbs ...intent.Verb) map[intent.Verb]json.RawMessage {
	result := make(map[intent.Verb]json.RawMessage, len(verbs))
	for _, verb := range verbs {
		result[verb] = json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","additionalProperties":false}`)
	}
	return result
}
