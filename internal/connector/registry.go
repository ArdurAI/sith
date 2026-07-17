// SPDX-License-Identifier: Apache-2.0

package connector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync"

	"github.com/ArdurAI/sith/internal/intent"
	"github.com/ArdurAI/sith/internal/intentargs"
)

// ErrNotRegistered reports a lookup for an unknown connector kind.
var ErrNotRegistered = errors.New("connector is not registered")

// ErrCapability reports a lookup for a capability the connector did not opt into.
var ErrCapability = errors.New("connector capability is unavailable")

// Factory constructs a configured connector for fail-safe registration.
type Factory func() (Connector, error)

type registryEntry struct {
	connector  Connector
	descriptor Descriptor
	declared   map[Capability]struct{}
	schemas    map[intent.Verb]*intentargs.Schema
}

// Registry stores one canonical, capability-checked connector per kind.
type Registry struct {
	mu      sync.RWMutex
	entries map[string]registryEntry
	verbs   map[intent.Verb]string
}

// NewRegistry returns an empty connector registry.
func NewRegistry() *Registry {
	return &Registry{entries: make(map[string]registryEntry), verbs: make(map[intent.Verb]string)}
}

// Register builds and validates a connector before atomically adding it.
func (registry *Registry) Register(factory Factory) error {
	if factory == nil {
		return fmt.Errorf("register connector: factory is nil")
	}

	candidate, err := factory()
	if err != nil {
		return fmt.Errorf("register connector: construct: %w", err)
	}
	if connectorIsNil(candidate) {
		return fmt.Errorf("register connector: factory returned nil")
	}

	entry, err := validateConnector(candidate)
	if err != nil {
		return fmt.Errorf("register connector %q: %w", candidate.Kind(), err)
	}

	registry.mu.Lock()
	defer registry.mu.Unlock()
	if _, exists := registry.entries[entry.descriptor.Kind]; exists {
		return fmt.Errorf("register connector %q: kind already registered", entry.descriptor.Kind)
	}
	for _, verb := range entry.descriptor.Verbs {
		if owner, exists := registry.verbs[verb]; exists {
			return fmt.Errorf("register connector %q: action verb %q already belongs to %q", entry.descriptor.Kind, verb, owner)
		}
	}
	registry.entries[entry.descriptor.Kind] = entry
	for _, verb := range entry.descriptor.Verbs {
		registry.verbs[verb] = entry.descriptor.Kind
	}
	return nil
}

// ByKind returns the canonical connector registered for kind.
func (registry *Registry) ByKind(kind string) (Connector, bool) {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	entry, ok := registry.entries[kind]
	return entry.connector, ok
}

// WithCapability lists connectors that both declare and implement a capability.
func (registry *Registry) WithCapability(capability Capability) []Connector {
	if !capability.Valid() {
		return []Connector{}
	}

	registry.mu.RLock()
	entries := make([]registryEntry, 0, len(registry.entries))
	for _, entry := range registry.entries {
		if _, declared := entry.declared[capability]; declared && implementsCapability(entry.connector, capability) {
			entries = append(entries, entry)
		}
	}
	registry.mu.RUnlock()

	sort.Slice(entries, func(left, right int) bool {
		return entries[left].descriptor.Kind < entries[right].descriptor.Kind
	})
	connectors := make([]Connector, 0, len(entries))
	for _, entry := range entries {
		connectors = append(connectors, entry.connector)
	}
	return connectors
}

// Descriptors returns deterministically ordered copies of registered metadata.
func (registry *Registry) Descriptors() []Descriptor {
	registry.mu.RLock()
	descriptors := make([]Descriptor, 0, len(registry.entries))
	for _, entry := range registry.entries {
		descriptors = append(descriptors, cloneDescriptor(entry.descriptor))
	}
	registry.mu.RUnlock()

	sort.Slice(descriptors, func(left, right int) bool {
		return descriptors[left].Kind < descriptors[right].Kind
	})
	return descriptors
}

// ReaderFor returns a registered connector that declared read and implements Reader.
func (registry *Registry) ReaderFor(kind string) (Reader, error) {
	entry, err := registry.entryFor(kind, CapRead, false)
	if err != nil {
		return nil, err
	}
	reader, ok := entry.connector.(Reader)
	if !ok {
		return nil, fmt.Errorf("%w: %s does not implement reader", ErrCapability, kind)
	}
	return reader, nil
}

// DifferFor returns a registered connector that declared and implements diff.
func (registry *Registry) DifferFor(kind string) (Differ, error) {
	entry, err := registry.entryFor(kind, CapDiff, false)
	if err != nil {
		return nil, err
	}
	differ, ok := entry.connector.(Differ)
	if !ok {
		return nil, fmt.Errorf("%w: %s does not implement diff", ErrCapability, kind)
	}
	return differ, nil
}

// PlannerFor returns a typed-action connector that declared and implements plan.
func (registry *Registry) PlannerFor(kind string) (Planner, error) {
	entry, err := registry.entryFor(kind, CapPlan, true)
	if err != nil {
		return nil, err
	}
	planner, ok := entry.connector.(Planner)
	if !ok {
		return nil, fmt.Errorf("%w: %s does not implement plan", ErrCapability, kind)
	}
	return planner, nil
}

// ExecutorFor returns a typed-action connector that declared and implements execute.
func (registry *Registry) ExecutorFor(kind string) (Executor, error) {
	entry, err := registry.entryFor(kind, CapExecute, true)
	if err != nil {
		return nil, err
	}
	executor, ok := entry.connector.(Executor)
	if !ok {
		return nil, fmt.Errorf("%w: %s does not implement execute", ErrCapability, kind)
	}
	return executor, nil
}

// VerifierFor returns a typed-action connector that declared and implements verify.
func (registry *Registry) VerifierFor(kind string) (Verifier, error) {
	entry, err := registry.entryFor(kind, CapVerify, true)
	if err != nil {
		return nil, err
	}
	verifier, ok := entry.connector.(Verifier)
	if !ok {
		return nil, fmt.Errorf("%w: %s does not implement verify", ErrCapability, kind)
	}
	return verifier, nil
}

// PlannerForVerb returns the only registered planner classified for verb.
func (registry *Registry) PlannerForVerb(verb intent.Verb) (Planner, error) {
	entry, err := registry.entryForVerb(verb, CapPlan)
	if err != nil {
		return nil, err
	}
	planner, ok := entry.connector.(Planner)
	if !ok {
		return nil, fmt.Errorf("%w: %s does not implement planner", ErrCapability, entry.descriptor.Kind)
	}
	return planner, nil
}

// ValidateArgsForVerb rejects malformed or schema-invalid arguments for a registered verb.
func (registry *Registry) ValidateArgsForVerb(verb intent.Verb, args json.RawMessage) error {
	if !verb.Valid() {
		return fmt.Errorf("%w: unknown action verb", ErrNotRegistered)
	}
	registry.mu.RLock()
	kind, exists := registry.verbs[verb]
	schema := registry.entries[kind].schemas[verb]
	registry.mu.RUnlock()
	if !exists || schema == nil {
		return fmt.Errorf("%w: action verb %q", ErrNotRegistered, verb)
	}
	if err := schema.Validate(args); err != nil {
		return fmt.Errorf("validate action verb %q: %w", verb, err)
	}
	return nil
}

// Plan validates intent arguments before routing the request to its only registered planner.
func (registry *Registry) Plan(ctx context.Context, request Intent) (ActionPlan, error) {
	request.Args = append(json.RawMessage(nil), request.Args...)
	if err := registry.ValidateArgsForVerb(request.Verb, request.Args); err != nil {
		return ActionPlan{}, err
	}
	planner, err := registry.PlannerForVerb(request.Verb)
	if err != nil {
		return ActionPlan{}, err
	}
	return planner.Plan(ctx, request)
}

// ExecutorForVerb returns the only registered executor classified for verb.
func (registry *Registry) ExecutorForVerb(verb intent.Verb) (Executor, error) {
	entry, err := registry.entryForVerb(verb, CapExecute)
	if err != nil {
		return nil, err
	}
	executor, ok := entry.connector.(Executor)
	if !ok {
		return nil, fmt.Errorf("%w: %s does not implement executor", ErrCapability, entry.descriptor.Kind)
	}
	return executor, nil
}

// VerifierForVerb returns the only registered post-condition verifier classified for verb.
func (registry *Registry) VerifierForVerb(verb intent.Verb) (Verifier, error) {
	entry, err := registry.entryForVerb(verb, CapVerify)
	if err != nil {
		return nil, err
	}
	verifier, ok := entry.connector.(Verifier)
	if !ok {
		return nil, fmt.Errorf("%w: %s does not implement verifier", ErrCapability, entry.descriptor.Kind)
	}
	return verifier, nil
}

func (registry *Registry) entryForVerb(verb intent.Verb, capability Capability) (registryEntry, error) {
	if !verb.Valid() {
		return registryEntry{}, fmt.Errorf("%w: unknown action verb", ErrNotRegistered)
	}
	registry.mu.RLock()
	kind, exists := registry.verbs[verb]
	entry := registry.entries[kind]
	registry.mu.RUnlock()
	if !exists {
		return registryEntry{}, fmt.Errorf("%w: action verb %q", ErrNotRegistered, verb)
	}
	if entry.descriptor.ConnKind != KindTypedAction {
		return registryEntry{}, fmt.Errorf("%w: %s is not a typed-action connector", ErrCapability, kind)
	}
	if _, declared := entry.declared[capability]; !declared {
		return registryEntry{}, fmt.Errorf("%w: %s did not declare %s", ErrCapability, kind, capability)
	}
	return entry, nil
}

func (registry *Registry) entryFor(kind string, capability Capability, typedAction bool) (registryEntry, error) {
	registry.mu.RLock()
	entry, exists := registry.entries[kind]
	registry.mu.RUnlock()
	if !exists {
		return registryEntry{}, fmt.Errorf("%w: %s", ErrNotRegistered, kind)
	}
	if _, declared := entry.declared[capability]; !declared {
		return registryEntry{}, fmt.Errorf("%w: %s did not declare %s", ErrCapability, kind, capability)
	}
	if typedAction && entry.descriptor.ConnKind != KindTypedAction {
		return registryEntry{}, fmt.Errorf("%w: %s is not a typed-action connector", ErrCapability, kind)
	}
	return entry, nil
}

func validateConnector(candidate Connector) (registryEntry, error) {
	descriptor := cloneDescriptor(candidate.Descriptor())
	if descriptor.Kind == "" || candidate.Kind() == "" {
		return registryEntry{}, fmt.Errorf("kind must not be empty")
	}
	if descriptor.Kind != candidate.Kind() {
		return registryEntry{}, fmt.Errorf("descriptor kind %q does not match connector kind %q", descriptor.Kind, candidate.Kind())
	}
	if !descriptor.ConnKind.Valid() {
		return registryEntry{}, fmt.Errorf("invalid connector kind %q", descriptor.ConnKind)
	}
	if descriptor.ProtocolV == "" {
		return registryEntry{}, fmt.Errorf("protocol version must not be empty")
	}
	if descriptor.Owner == "" {
		return registryEntry{}, fmt.Errorf("owner must not be empty")
	}

	declared, err := capabilitySet(candidate.Capabilities())
	if err != nil {
		return registryEntry{}, err
	}
	descriptorSet, err := capabilitySet(descriptor.Capabilities)
	if err != nil {
		return registryEntry{}, fmt.Errorf("descriptor: %w", err)
	}
	if !sameCapabilities(declared, descriptorSet) {
		return registryEntry{}, fmt.Errorf("descriptor capabilities do not match connector declaration")
	}
	for capability := range declared {
		if !implementsCapability(candidate, capability) {
			return registryEntry{}, fmt.Errorf("declares %s without implementing its interface", capability)
		}
	}

	if descriptor.ConnKind == KindTypedAction {
		if len(descriptor.Verbs) == 0 {
			return registryEntry{}, fmt.Errorf("typed-action connector must declare at least one verb")
		}
		seen := make(map[intent.Verb]struct{}, len(descriptor.Verbs))
		for _, verb := range descriptor.Verbs {
			if !verb.Valid() {
				return registryEntry{}, fmt.Errorf("invalid action verb %q", verb)
			}
			if _, duplicate := seen[verb]; duplicate {
				return registryEntry{}, fmt.Errorf("duplicate action verb %q", verb)
			}
			seen[verb] = struct{}{}
		}
		if len(descriptor.ArgSchemas) != len(seen) {
			return registryEntry{}, fmt.Errorf("typed-action connector must declare exactly one argument schema per verb")
		}
		schemas := make(map[intent.Verb]*intentargs.Schema, len(seen))
		for verb, document := range descriptor.ArgSchemas {
			if _, declared := seen[verb]; !declared {
				return registryEntry{}, fmt.Errorf("argument schema belongs to undeclared action verb %q", verb)
			}
			compiled, err := intentargs.Compile(document)
			if err != nil {
				return registryEntry{}, fmt.Errorf("compile argument schema for action verb %q: %w", verb, err)
			}
			schemas[verb] = compiled
		}
		for verb := range seen {
			if schemas[verb] == nil {
				return registryEntry{}, fmt.Errorf("action verb %q is missing an argument schema", verb)
			}
		}
		return registryEntry{connector: candidate, descriptor: descriptor, declared: declared, schemas: schemas}, nil
	} else if len(descriptor.Verbs) != 0 || len(descriptor.ArgSchemas) != 0 {
		return registryEntry{}, fmt.Errorf("non-action connector must not declare action verbs or argument schemas")
	}

	return registryEntry{connector: candidate, descriptor: descriptor, declared: declared}, nil
}

func capabilitySet(capabilities []Capability) (map[Capability]struct{}, error) {
	set := make(map[Capability]struct{}, len(capabilities))
	for _, capability := range capabilities {
		if !capability.Valid() {
			return nil, fmt.Errorf("invalid capability %q", capability)
		}
		if _, duplicate := set[capability]; duplicate {
			return nil, fmt.Errorf("duplicate capability %q", capability)
		}
		set[capability] = struct{}{}
	}
	return set, nil
}

func sameCapabilities(left, right map[Capability]struct{}) bool {
	if len(left) != len(right) {
		return false
	}
	for capability := range left {
		if _, exists := right[capability]; !exists {
			return false
		}
	}
	return true
}

func implementsCapability(candidate Connector, capability Capability) bool {
	switch capability {
	case CapDiscover, CapRead, CapQuery:
		_, ok := candidate.(Reader)
		return ok
	case CapDiff:
		_, ok := candidate.(Differ)
		return ok
	case CapPlan:
		_, ok := candidate.(Planner)
		return ok
	case CapExecute:
		_, ok := candidate.(Executor)
		return ok
	case CapVerify:
		_, ok := candidate.(Verifier)
		return ok
	default:
		return false
	}
}

func connectorIsNil(candidate Connector) bool {
	if candidate == nil {
		return true
	}
	value := reflect.ValueOf(candidate)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func cloneDescriptor(descriptor Descriptor) Descriptor {
	descriptor.Capabilities = append([]Capability(nil), descriptor.Capabilities...)
	descriptor.Verbs = append([]intent.Verb(nil), descriptor.Verbs...)
	if descriptor.ArgSchemas != nil {
		schemas := make(map[intent.Verb]json.RawMessage, len(descriptor.ArgSchemas))
		for verb, schema := range descriptor.ArgSchemas {
			schemas[verb] = append(json.RawMessage(nil), schema...)
		}
		descriptor.ArgSchemas = schemas
	}
	return descriptor
}
