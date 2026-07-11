// SPDX-License-Identifier: Apache-2.0

package kubeconfig

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	contentvalidation "k8s.io/apimachinery/pkg/api/validate/content"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/yaml"

	"github.com/ArdurAI/sith/internal/fleet"
	"github.com/ArdurAI/sith/internal/localops"
)

const localEditFieldManager = "sith-local-edit"

var _ localops.Applier = (*Adapter)(nil)

// View returns one raw object as YAML from one explicitly named context.
func (adapter *Adapter) View(
	ctx context.Context,
	target localops.Target,
	revealSecrets bool,
) (localops.ObjectView, error) {
	normalized, spec, client, _, err := adapter.localTargetState(ctx, target)
	if err != nil {
		return localops.ObjectView{}, err
	}
	resource := resourceInterface(client, spec, normalized.Namespace)
	object, err := callWithTimeout(ctx, adapter.settings.requestTimeout, func(requestCtx context.Context) (*unstructured.Unstructured, error) {
		return resource.Get(requestCtx, normalized.Name, metav1.GetOptions{})
	})
	if err != nil {
		return localops.ObjectView{}, fmt.Errorf("read local %s/%s: %w", spec.kind, normalized.Name, err)
	}
	observedAt := adapter.settings.now().UTC()
	evidence, err := evidenceFromObject(*object, spec, normalized.Context, observedAt)
	if err != nil {
		return localops.ObjectView{}, err
	}
	adapter.recordLastSeen(normalized.Context, observedAt)
	rendered, err := objectYAML(object, !revealSecrets)
	if err != nil {
		return localops.ObjectView{}, err
	}
	return localops.ObjectView{Evidence: evidence, YAML: rendered}, nil
}

// Describe composes one object read with Kubernetes events that reference its UID.
func (adapter *Adapter) Describe(ctx context.Context, target localops.Target) (localops.Description, error) {
	normalized, _, client, _, err := adapter.localTargetState(ctx, target)
	if err != nil {
		return localops.Description{}, err
	}
	view, err := adapter.View(ctx, normalized, false)
	if err != nil {
		return localops.Description{}, err
	}
	object := &unstructured.Unstructured{}
	if err := json.Unmarshal(view.Evidence.Observed, &object.Object); err != nil {
		return localops.Description{}, fmt.Errorf("decode %s for describe: %w", view.Evidence.Ref.String(), err)
	}
	selectorKey, selectorValue := "involvedObject.uid", string(object.GetUID())
	if selectorValue == "" {
		selectorKey, selectorValue = "involvedObject.name", object.GetName()
	}
	events, err := callWithTimeout(ctx, adapter.settings.requestTimeout, func(requestCtx context.Context) (*unstructured.UnstructuredList, error) {
		return client.Resource(resourceSpecs["events"].gvr).Namespace(normalized.Namespace).List(
			requestCtx,
			metav1.ListOptions{FieldSelector: fields.OneTermEqualSelector(selectorKey, selectorValue).String()},
		)
	})
	if err != nil {
		return localops.Description{}, fmt.Errorf("list events for %s: %w", view.Evidence.Ref.String(), err)
	}
	observedAt := adapter.settings.now().UTC()
	evidence := make([]fleet.Evidence, 0, len(events.Items))
	for _, event := range events.Items {
		item, err := evidenceFromObject(event, resourceSpecs["events"], normalized.Context, observedAt)
		if err != nil {
			return localops.Description{}, err
		}
		evidence = append(evidence, item)
	}
	sort.Slice(evidence, func(left, right int) bool {
		return evidence[left].Ref.String() < evidence[right].Ref.String()
	})
	return localops.Description{Object: view, Events: evidence}, nil
}

// PreviewApply asks the API server to validate/default an edit without persisting it.
func (adapter *Adapter) PreviewApply(
	ctx context.Context,
	target localops.Target,
	manifest []byte,
) (localops.ApplyPreview, error) {
	normalized, spec, client, _, err := adapter.localTargetState(ctx, target)
	if err != nil {
		return localops.ApplyPreview{}, err
	}
	resource := resourceInterface(client, spec, normalized.Namespace)
	current, err := callWithTimeout(ctx, adapter.settings.requestTimeout, func(requestCtx context.Context) (*unstructured.Unstructured, error) {
		return resource.Get(requestCtx, normalized.Name, metav1.GetOptions{})
	})
	if err != nil {
		return localops.ApplyPreview{}, fmt.Errorf("read current %s/%s: %w", spec.kind, normalized.Name, err)
	}
	proposed, err := decodeEditedObject(manifest, normalized, spec, current)
	if err != nil {
		return localops.ApplyPreview{}, err
	}
	dryRun, err := updateLocalObject(ctx, adapter.settings.requestTimeout, resource, proposed, true)
	if err != nil {
		return localops.ApplyPreview{}, err
	}
	currentYAML, err := objectYAML(current, false)
	if err != nil {
		return localops.ApplyPreview{}, err
	}
	dryRunYAML, err := objectYAML(dryRun, false)
	if err != nil {
		return localops.ApplyPreview{}, err
	}
	return localops.ApplyPreview{CurrentYAML: currentYAML, DryRunYAML: dryRunYAML}, nil
}

// Apply persists one previously previewed edit through the local user's Kubernetes identity.
func (adapter *Adapter) Apply(
	ctx context.Context,
	target localops.Target,
	manifest []byte,
) (fleet.Evidence, error) {
	normalized, spec, client, _, err := adapter.localTargetState(ctx, target)
	if err != nil {
		return fleet.Evidence{}, err
	}
	resource := resourceInterface(client, spec, normalized.Namespace)
	current, err := callWithTimeout(ctx, adapter.settings.requestTimeout, func(requestCtx context.Context) (*unstructured.Unstructured, error) {
		return resource.Get(requestCtx, normalized.Name, metav1.GetOptions{})
	})
	if err != nil {
		return fleet.Evidence{}, fmt.Errorf("read current %s/%s: %w", spec.kind, normalized.Name, err)
	}
	proposed, err := decodeEditedObject(manifest, normalized, spec, current)
	if err != nil {
		return fleet.Evidence{}, err
	}
	if _, err := updateLocalObject(ctx, adapter.settings.requestTimeout, resource, proposed.DeepCopy(), true); err != nil {
		return fleet.Evidence{}, err
	}
	updated, err := updateLocalObject(ctx, adapter.settings.requestTimeout, resource, proposed, false)
	if err != nil {
		return fleet.Evidence{}, err
	}
	observedAt := adapter.settings.now().UTC()
	evidence, err := evidenceFromObject(*updated, spec, normalized.Context, observedAt)
	if err != nil {
		return fleet.Evidence{}, err
	}
	adapter.recordLastSeen(normalized.Context, observedAt)
	return evidence, nil
}

func (adapter *Adapter) localTargetState(
	ctx context.Context,
	target localops.Target,
) (localops.Target, resourceSpec, dynamic.Interface, *rest.Config, error) {
	if err := target.Validate(); err != nil {
		return localops.Target{}, resourceSpec{}, nil, nil, err
	}
	if problems := contentvalidation.IsPathSegmentName(target.Name); len(problems) > 0 {
		return localops.Target{}, resourceSpec{}, nil, nil, fmt.Errorf(
			"%w: resource name %q is not a safe Kubernetes path segment: %s",
			localops.ErrInvalidTarget, target.Name, strings.Join(problems, ", "),
		)
	}
	if target.Namespace != "" {
		if problems := contentvalidation.IsPathSegmentName(target.Namespace); len(problems) > 0 {
			return localops.Target{}, resourceSpec{}, nil, nil, fmt.Errorf(
				"%w: namespace %q is not a safe Kubernetes path segment: %s",
				localops.ErrInvalidTarget, target.Namespace, strings.Join(problems, ", "),
			)
		}
	}
	if err := adapter.ensureLocalContext(ctx, target.Context); err != nil {
		return localops.Target{}, resourceSpec{}, nil, nil, err
	}
	scope, client, config, exists := adapter.scopeClient(target.Context)
	if !exists {
		return localops.Target{}, resourceSpec{}, nil, nil, fmt.Errorf("%w: %s", ErrUnknownScope, target.Context)
	}
	if !scope.Reachable || client == nil || config == nil {
		return localops.Target{}, resourceSpec{}, nil, nil, fmt.Errorf("%w: %s", ErrUnreachableScope, target.Context)
	}
	spec, known := lookupResource(target.Kind)
	if !known {
		var err error
		spec, err = adapter.resolveResource(ctx, target.Context, config, target.Kind)
		if err != nil {
			return localops.Target{}, resourceSpec{}, nil, nil, err
		}
	}
	if spec.namespaced {
		if strings.TrimSpace(target.Namespace) == "" {
			target.Namespace = "default"
		}
	} else if target.Namespace != "" {
		return localops.Target{}, resourceSpec{}, nil, nil, fmt.Errorf(
			"%w: namespace cannot select cluster-scoped %s", ErrUnsupportedSelector, spec.kind,
		)
	}
	target.Kind = spec.kind
	return target, spec, client, config, nil
}

// ensureLocalContext bootstraps only the explicitly named context. It deliberately does not mark
// fleet discovery complete: a later fleet query must still discover every configured context.
func (adapter *Adapter) ensureLocalContext(ctx context.Context, name string) error {
	adapter.mu.RLock()
	_, known := adapter.scopes[name]
	adapter.mu.RUnlock()
	if known {
		return nil
	}
	rawConfig, err := adapter.settings.loadingRules.Load()
	if err != nil {
		return fmt.Errorf("load kubeconfig: %w", err)
	}
	if _, exists := rawConfig.Contexts[name]; !exists {
		return fmt.Errorf("%w: %s", ErrUnknownScope, name)
	}
	lastSeen := adapter.lastSeenSnapshot()[name]
	result := adapter.probeContext(ctx, *rawConfig, name, lastSeen)
	adapter.mu.Lock()
	adapter.scopes[name] = cloneScope(result.scope)
	if result.scope.Reachable {
		adapter.clients[name] = result.client
		adapter.watchers[name] = result.watcher
		adapter.configs[name] = rest.CopyConfig(result.config)
		adapter.tables[name] = result.table
	}
	if !result.scope.ObservedAt.IsZero() {
		adapter.lastSeen[name] = result.scope.ObservedAt
	}
	adapter.mu.Unlock()
	return nil
}

func decodeEditedObject(
	manifest []byte,
	target localops.Target,
	spec resourceSpec,
	current *unstructured.Unstructured,
) (*unstructured.Unstructured, error) {
	if len(manifest) == 0 {
		return nil, fmt.Errorf("decode local edit: manifest is empty")
	}
	jsonPayload, err := yaml.YAMLToJSON(manifest)
	if err != nil {
		return nil, fmt.Errorf("decode local edit YAML: %w", err)
	}
	object := &unstructured.Unstructured{}
	if err := json.Unmarshal(jsonPayload, &object.Object); err != nil {
		return nil, fmt.Errorf("decode local edit object: %w", err)
	}
	groupVersion := object.GroupVersionKind().GroupVersion()
	if !strings.EqualFold(object.GetKind(), spec.kind) || groupVersion.Group != spec.gvr.Group ||
		groupVersion.Version != spec.gvr.Version {
		return nil, fmt.Errorf(
			"%w: manifest %s %s does not match target %s %s/%s",
			localops.ErrInvalidTarget, object.GetAPIVersion(), object.GetKind(), spec.gvr.GroupVersion(), spec.kind,
			target.Name,
		)
	}
	if object.GetName() != target.Name || object.GetNamespace() != target.Namespace {
		return nil, fmt.Errorf(
			"%w: manifest identity %s/%s does not match target %s/%s",
			localops.ErrInvalidTarget, object.GetNamespace(), object.GetName(), target.Namespace, target.Name,
		)
	}
	if object.GetResourceVersion() == "" {
		return nil, fmt.Errorf("%w: manifest must retain metadata.resourceVersion", localops.ErrInvalidTarget)
	}
	if current.GetUID() != "" && object.GetUID() != "" && object.GetUID() != current.GetUID() {
		return nil, fmt.Errorf("%w: manifest UID does not match the live object", localops.ErrInvalidTarget)
	}
	object.SetManagedFields(nil)
	return object, nil
}

func updateLocalObject(
	ctx context.Context,
	timeout time.Duration,
	resource dynamic.ResourceInterface,
	object *unstructured.Unstructured,
	dryRun bool,
) (*unstructured.Unstructured, error) {
	options := metav1.UpdateOptions{
		FieldManager: localEditFieldManager, FieldValidation: metav1.FieldValidationStrict,
	}
	if dryRun {
		options.DryRun = []string{metav1.DryRunAll}
	}
	updated, err := callWithTimeout(ctx, timeout, func(requestCtx context.Context) (*unstructured.Unstructured, error) {
		return resource.Update(requestCtx, object, options)
	})
	if err != nil {
		operation := "apply"
		if dryRun {
			operation = "dry-run"
		}
		return nil, fmt.Errorf("local edit %s rejected by API server: %w", operation, err)
	}
	return updated, nil
}

func objectYAML(object *unstructured.Unstructured, maskSecret bool) ([]byte, error) {
	copy := object.DeepCopy()
	copy.SetManagedFields(nil)
	if maskSecret && strings.EqualFold(copy.GetKind(), "Secret") {
		redactSecretMap(copy.Object, "data")
		redactSecretMap(copy.Object, "stringData")
	}
	payload, err := json.Marshal(copy.Object)
	if err != nil {
		return nil, fmt.Errorf("marshal %s/%s for YAML: %w", copy.GetKind(), copy.GetName(), err)
	}
	rendered, err := yaml.JSONToYAML(payload)
	if err != nil {
		return nil, fmt.Errorf("render %s/%s YAML: %w", copy.GetKind(), copy.GetName(), err)
	}
	return rendered, nil
}

func redactSecretMap(object map[string]any, field string) {
	values, found, err := unstructured.NestedMap(object, field)
	if err != nil || !found {
		return
	}
	for key := range values {
		values[key] = "<redacted>"
	}
	_ = unstructured.SetNestedMap(object, values, field)
}
