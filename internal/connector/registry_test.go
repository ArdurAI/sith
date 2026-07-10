// SPDX-License-Identifier: Apache-2.0

package connector

import (
	"context"
	"errors"
	"testing"

	"github.com/ArdurAI/sith/internal/fleet"
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
	descriptor Descriptor
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

func (testExecutor) Execute(_ context.Context, plan ActionPlan) (ExecutionResult, error) {
	return ExecutionResult{IntentID: plan.IntentID, Applied: true}, nil
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
		{name: "unknown taxonomy", connector: identityOnlyConnector{descriptor: Descriptor{Kind: "bad", ConnKind: "other", ProtocolV: "1.0.0", Owner: "test"}}},
		{name: "declared but not implemented", connector: identityOnlyConnector{descriptor: Descriptor{Kind: "bad", ConnKind: KindReadAdapter, ProtocolV: "1.0.0", Owner: "test", Capabilities: []Capability{CapRead}}}},
		{name: "unknown capability", connector: identityOnlyConnector{descriptor: Descriptor{Kind: "bad", ConnKind: KindReadAdapter, ProtocolV: "1.0.0", Owner: "test", Capabilities: []Capability{"shell"}}}},
		{name: "read adapter with verbs", connector: identityOnlyConnector{descriptor: Descriptor{Kind: "bad", ConnKind: KindReadAdapter, ProtocolV: "1.0.0", Owner: "test", Verbs: []string{"gitops.open-pr"}}}},
		{name: "action without verbs", connector: testExecutor{descriptor: Descriptor{Kind: "bad", ConnKind: KindTypedAction, ProtocolV: "1.0.0", Owner: "test", Capabilities: []Capability{CapExecute}}}},
		{name: "action with unknown verb", connector: testExecutor{descriptor: Descriptor{Kind: "bad", ConnKind: KindTypedAction, ProtocolV: "1.0.0", Owner: "test", Capabilities: []Capability{CapExecute}, Verbs: []string{"shell.exec"}}}},
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

func TestRegistryExecutorRequiresTypedAction(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	executor := testExecutor{descriptor: Descriptor{
		Kind:         "argocd",
		ConnKind:     KindTypedAction,
		ProtocolV:    "1.0.0",
		Owner:        "test",
		Capabilities: []Capability{CapExecute},
		Verbs:        []string{"argocd.sync"},
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

func TestValidVerb(t *testing.T) {
	t.Parallel()

	if !ValidVerb("gitops.open-pr") || ValidVerb("shell.exec") {
		t.Fatal("ValidVerb() does not enforce the closed vocabulary")
	}
}

func newTestReader(kind string) testReader {
	capabilities := []Capability{CapDiscover, CapRead, CapQuery}
	return testReader{
		kind: kind,
		descriptor: Descriptor{
			Kind:         kind,
			ConnKind:     KindReadAdapter,
			ProtocolV:    "1.0.0",
			Owner:        "test",
			Capabilities: capabilities,
		},
	}
}
