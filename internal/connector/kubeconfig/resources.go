// SPDX-License-Identifier: Apache-2.0

package kubeconfig

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"

	"github.com/ArdurAI/sith/internal/connector"
	"github.com/ArdurAI/sith/internal/fleet"
)

// ErrUnknownScope reports a context that was not present during discovery.
var ErrUnknownScope = errors.New("kubeconfig scope is unknown")

// ErrUnreachableScope reports a discovered context without a live client.
var ErrUnreachableScope = errors.New("kubeconfig scope is unreachable")

// ErrUnsupportedResource reports a resource kind outside this adapter's typed map.
var ErrUnsupportedResource = errors.New("resource kind is unsupported")

// ErrUnsupportedSelector reports a selector not yet expressible by this adapter.
var ErrUnsupportedSelector = errors.New("query selector is unsupported")

// ErrInvalidReference reports a resource address that is incomplete or inconsistent.
var ErrInvalidReference = errors.New("resource reference is invalid")

type resourceSpec struct {
	kind       string
	gvr        schema.GroupVersionResource
	namespaced bool
}

var resourceSpecs = map[string]resourceSpec{
	"deployment":  {kind: "Deployment", gvr: schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}, namespaced: true},
	"deployments": {kind: "Deployment", gvr: schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}, namespaced: true},
	"replicaset":  {kind: "ReplicaSet", gvr: schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "replicasets"}, namespaced: true},
	"replicasets": {kind: "ReplicaSet", gvr: schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "replicasets"}, namespaced: true},
	"pod":         {kind: "Pod", gvr: schema.GroupVersionResource{Version: "v1", Resource: "pods"}, namespaced: true},
	"pods":        {kind: "Pod", gvr: schema.GroupVersionResource{Version: "v1", Resource: "pods"}, namespaced: true},
	"node":        {kind: "Node", gvr: schema.GroupVersionResource{Version: "v1", Resource: "nodes"}},
	"nodes":       {kind: "Node", gvr: schema.GroupVersionResource{Version: "v1", Resource: "nodes"}},
	"service":     {kind: "Service", gvr: schema.GroupVersionResource{Version: "v1", Resource: "services"}, namespaced: true},
	"services":    {kind: "Service", gvr: schema.GroupVersionResource{Version: "v1", Resource: "services"}, namespaced: true},
	"namespace":   {kind: "Namespace", gvr: schema.GroupVersionResource{Version: "v1", Resource: "namespaces"}},
	"namespaces":  {kind: "Namespace", gvr: schema.GroupVersionResource{Version: "v1", Resource: "namespaces"}},
	"event":       {kind: "Event", gvr: schema.GroupVersionResource{Version: "v1", Resource: "events"}, namespaced: true},
	"events":      {kind: "Event", gvr: schema.GroupVersionResource{Version: "v1", Resource: "events"}, namespaced: true},
	"configmap":   {kind: "ConfigMap", gvr: schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}, namespaced: true},
	"configmaps":  {kind: "ConfigMap", gvr: schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}, namespaced: true},
	"secret":      {kind: "Secret", gvr: schema.GroupVersionResource{Version: "v1", Resource: "secrets"}, namespaced: true},
	"secrets":     {kind: "Secret", gvr: schema.GroupVersionResource{Version: "v1", Resource: "secrets"}, namespaced: true},
	"rollout":     {kind: "Rollout", gvr: schema.GroupVersionResource{Group: "argoproj.io", Version: "v1alpha1", Resource: "rollouts"}, namespaced: true},
	"rollouts":    {kind: "Rollout", gvr: schema.GroupVersionResource{Group: "argoproj.io", Version: "v1alpha1", Resource: "rollouts"}, namespaced: true},
}

// Read fetches one resource from its explicitly addressed context.
func (adapter *Adapter) Read(ctx context.Context, ref fleet.ResourceRef) (fleet.Evidence, error) {
	if ref.SourceKind != "" && ref.SourceKind != Kind {
		return fleet.Evidence{}, fmt.Errorf("%w: source kind %q", ErrUnsupportedResource, ref.SourceKind)
	}
	if strings.TrimSpace(ref.Scope) == "" || strings.TrimSpace(ref.Name) == "" {
		return fleet.Evidence{}, fmt.Errorf("%w: scope and name are required", ErrInvalidReference)
	}

	spec, known := lookupResource(ref.Kind)
	if err := adapter.ensureDiscovered(ctx); err != nil {
		return fleet.Evidence{}, err
	}

	scope, client, config, ok := adapter.scopeClient(ref.Scope)
	if !ok {
		return fleet.Evidence{}, fmt.Errorf("%w: %s", ErrUnknownScope, ref.Scope)
	}
	if !scope.Reachable || client == nil {
		return fleet.Evidence{}, fmt.Errorf("%w: %s", ErrUnreachableScope, ref.Scope)
	}
	if !known {
		var err error
		spec, err = adapter.resolveResource(ctx, ref.Scope, config, ref.Kind)
		if err != nil {
			return fleet.Evidence{}, err
		}
	}
	if expected := ref.Attributes["gvr"]; expected != "" && expected != spec.gvr.String() {
		return fleet.Evidence{}, fmt.Errorf("%w: GVR %q does not match kind %q", ErrUnsupportedResource, expected, ref.Kind)
	}

	resource := resourceInterface(client, spec, ref.Namespace)
	object, err := callWithTimeout(ctx, adapter.gate, ref.Scope, adapter.settings.requestTimeout, func(requestCtx context.Context) (*unstructured.Unstructured, error) {
		return resource.Get(requestCtx, ref.Name, metav1.GetOptions{})
	})
	if err != nil {
		return fleet.Evidence{}, fmt.Errorf("read %s: %w", ref.String(), err)
	}
	observedAt := adapter.settings.now().UTC()
	evidence, err := evidenceFromObject(*object, spec, ref.Scope, observedAt)
	if err != nil {
		return fleet.Evidence{}, err
	}
	adapter.recordLastSeen(ref.Scope, observedAt)
	return evidence, nil
}

// Query fans a typed resource selection out across independent contexts.
func (adapter *Adapter) Query(ctx context.Context, query fleet.Query) (fleet.QueryResult, error) {
	if err := query.Validate(); err != nil {
		return fleet.QueryResult{}, fmt.Errorf("validate fleet query: %w", err)
	}
	if query.Selector.CVE != "" || query.Selector.Health != "" {
		return fleet.QueryResult{}, fmt.Errorf("%w: health and CVE predicates arrive in later slices", ErrUnsupportedSelector)
	}
	var spec resourceSpec
	if query.Selector.ResourceKind != "" {
		if known, ok := lookupResource(query.Selector.ResourceKind); ok {
			spec = known
		}
		if spec.gvr.Resource != "" && !spec.namespaced && query.Selector.Namespace != "" {
			return fleet.QueryResult{}, fmt.Errorf("%w: namespace cannot select cluster-scoped %s", ErrUnsupportedSelector, spec.kind)
		}
	}
	labelSelector, err := labels.ValidatedSelectorFromSet(query.Selector.Labels)
	if err != nil {
		return fleet.QueryResult{}, fmt.Errorf("%w: invalid Kubernetes label selector: %v", ErrUnsupportedSelector, err)
	}
	if err := adapter.ensureDiscovered(ctx); err != nil {
		return fleet.QueryResult{}, err
	}

	scopes, clients, configs, tables, lastSeen := adapter.stateSnapshot()
	targets := targetScopeNames(query.Scopes, scopes)
	results := make([]scopeQueryResult, len(targets))
	adapter.runBounded(len(targets), func(index int) {
		name := targets[index]
		if !scopes[name].Reachable || clients[name] == nil {
			results[index] = scopeQueryResult{name: name, err: ErrUnreachableScope}
			return
		}
		scopeSpec := spec
		generic := query.Selector.ResourceKind != "" &&
			(scopeSpec.gvr.Resource == "" || scopeSpec.kind == "ConfigMap" || scopeSpec.kind == "Secret")
		if generic {
			var err error
			scopeSpec, err = adapter.resolveResource(ctx, name, configs[name], query.Selector.ResourceKind)
			if err != nil {
				results[index] = scopeQueryResult{name: name, err: err}
				return
			}
		}
		result, err := callWithTimeout(ctx, adapter.gate, name, adapter.settings.requestTimeout, func(requestCtx context.Context) (scopeQueryResult, error) {
			return adapter.queryScope(
				requestCtx, name, clients[name], tables[name], scopeSpec, generic, labelSelector.String(), query,
			), nil
		})
		if err != nil {
			result = scopeQueryResult{name: name, err: err}
		}
		results[index] = result
	})
	if err := ctx.Err(); err != nil {
		return fleet.QueryResult{}, fmt.Errorf("query kubeconfig contexts: %w", err)
	}

	now := adapter.settings.now().UTC()
	coverage := fleet.Coverage{Requested: len(targets)}
	facts := make([]fleet.Fact, 0)
	for _, result := range results {
		if result.err != nil {
			coverage.Unreachable = append(coverage.Unreachable, result.name)
			if isStale(now, lastSeen[result.name], adapter.settings.staleAfter) {
				coverage.Stale = append(coverage.Stale, result.name)
			}
			continue
		}
		coverage.Reachable++
		facts = append(facts, result.facts...)
		if !result.observedAt.IsZero() {
			adapter.recordLastSeen(result.name, result.observedAt)
		} else if isStale(now, lastSeen[result.name], adapter.settings.staleAfter) {
			coverage.Stale = append(coverage.Stale, result.name)
		}
	}

	sort.Slice(facts, func(left, right int) bool {
		return facts[left].Ref.String() < facts[right].Ref.String()
	})
	if query.Limit > 0 && len(facts) > query.Limit {
		facts = facts[:query.Limit]
	}
	if facts == nil {
		facts = []fleet.Fact{}
	}
	sort.Strings(coverage.Unreachable)
	sort.Strings(coverage.Stale)
	return fleet.QueryResult{Facts: facts, Coverage: coverage}, nil
}

type scopeQueryResult struct {
	name       string
	facts      []fleet.Fact
	observedAt time.Time
	err        error
}

func (adapter *Adapter) queryScope(
	ctx context.Context,
	name string,
	client dynamic.Interface,
	table tablePrinter,
	spec resourceSpec,
	generic bool,
	labelSelector string,
	query fleet.Query,
) scopeQueryResult {
	result := scopeQueryResult{name: name}
	if client == nil {
		result.err = ErrUnreachableScope
		return result
	}
	if query.Selector.ResourceKind == "" {
		return result
	}
	if !spec.namespaced && query.Selector.Namespace != "" {
		result.err = fmt.Errorf("%w: namespace cannot select cluster-scoped %s", ErrUnsupportedSelector, spec.kind)
		return result
	}

	resource := resourceInterface(client, spec, query.Selector.Namespace)
	list, err := resource.List(ctx, metav1.ListOptions{LabelSelector: labelSelector})
	if err != nil {
		if spec.kind == "Rollout" && apierrors.IsNotFound(err) {
			return result
		}
		result.err = fmt.Errorf("list %s in %s: %w", spec.kind, name, err)
		return result
	}

	result.observedAt = adapter.settings.now().UTC()
	if !wantsInventory(query.Kinds) {
		result.facts = []fleet.Fact{}
		return result
	}
	result.facts = make([]fleet.Fact, 0, len(list.Items))
	display := map[string][]fleet.DisplayField{}
	if generic {
		if table == nil {
			result.err = fmt.Errorf("server table client is unavailable for %s", name)
			return result
		}
		var err error
		display, err = table(ctx, spec, query.Selector.Namespace, "", labelSelector)
		if err != nil {
			result.err = err
			return result
		}
	}
	for _, object := range list.Items {
		if query.Selector.Name != "" && object.GetName() != query.Selector.Name {
			continue
		}
		if query.Selector.NamePrefix != "" && !strings.HasPrefix(object.GetName(), query.Selector.NamePrefix) {
			continue
		}
		if query.Selector.Image != "" && !objectUsesImage(object, query.Selector.Image) {
			continue
		}
		evidence, err := evidenceFromObject(object, spec, name, result.observedAt)
		if err != nil {
			result.err = err
			return result
		}
		evidence.Display = append([]fleet.DisplayField(nil), display[tableObjectKey(object.GetNamespace(), object.GetName())]...)
		result.facts = append(result.facts, fleet.Fact{Evidence: evidence, Workspace: fleet.LocalWorkspace})
	}
	return result
}

func evidenceFromObject(
	object unstructured.Unstructured,
	spec resourceSpec,
	scope string,
	observedAt time.Time,
) (fleet.Evidence, error) {
	payload, err := json.Marshal(object.Object)
	if err != nil {
		return fleet.Evidence{}, fmt.Errorf("marshal %s/%s: %w", spec.kind, object.GetName(), err)
	}
	return fleet.Evidence{
		Ref: fleet.ResourceRef{
			SourceKind: Kind,
			Scope:      scope,
			Kind:       spec.kind,
			Namespace:  object.GetNamespace(),
			Name:       object.GetName(),
			Attributes: map[string]string{"gvr": spec.gvr.String()},
		},
		Kind:       fleet.FactInventory,
		Observed:   payload,
		ObservedAt: observedAt,
		Source:     scope,
		Provenance: fleet.Provenance{
			Adapter:   Kind,
			ProtocolV: protocolVersion,
			NativeID:  string(object.GetUID()),
		},
	}, nil
}

func lookupResource(kind string) (resourceSpec, bool) {
	spec, ok := resourceSpecs[strings.ToLower(strings.TrimSpace(kind))]
	return spec, ok
}

func (adapter *Adapter) resolveResource(
	ctx context.Context,
	scope string,
	config *rest.Config,
	kind string,
) (resourceSpec, error) {
	key := strings.ToLower(strings.TrimSpace(kind))
	adapter.mu.RLock()
	if cached, ok := adapter.resources[scope][key]; ok {
		adapter.mu.RUnlock()
		return cached, nil
	}
	adapter.mu.RUnlock()
	if config == nil {
		return resourceSpec{}, fmt.Errorf("%w: no client config for %s", ErrUnreachableScope, scope)
	}
	resolved, err := callWithTimeout(ctx, adapter.gate, scope, adapter.settings.requestTimeout, func(resolveCtx context.Context) (resourceSpec, error) {
		return adapter.settings.resolve(resolveCtx, rest.CopyConfig(config), kind)
	})
	if err != nil {
		return resourceSpec{}, fmt.Errorf("%w: resolve %q in %s: %v", ErrUnsupportedResource, kind, scope, err)
	}
	adapter.mu.Lock()
	if adapter.resources[scope] == nil {
		adapter.resources[scope] = make(map[string]resourceSpec)
	}
	for _, alias := range []string{key, strings.ToLower(resolved.kind), strings.ToLower(resolved.gvr.Resource)} {
		adapter.resources[scope][alias] = resolved
	}
	adapter.mu.Unlock()
	return resolved, nil
}

func defaultResourceResolver(_ context.Context, config *rest.Config, kind string) (resourceSpec, error) {
	client, err := discovery.NewDiscoveryClientForConfig(config)
	if err != nil {
		return resourceSpec{}, fmt.Errorf("create discovery client: %w", err)
	}
	lists, discoveryErr := client.ServerPreferredResources()
	wanted := strings.ToLower(strings.TrimSpace(kind))
	for _, list := range lists {
		groupVersion, err := schema.ParseGroupVersion(list.GroupVersion)
		if err != nil {
			continue
		}
		for _, resource := range list.APIResources {
			if strings.Contains(resource.Name, "/") || !supportsVerb(resource.Verbs, "list") {
				continue
			}
			if !strings.EqualFold(resource.Kind, kind) && strings.ToLower(resource.Name) != wanted &&
				strings.ToLower(resource.SingularName) != wanted && !containsShortName(resource.ShortNames, wanted) {
				continue
			}
			return resourceSpec{
				kind: resource.Kind,
				gvr: schema.GroupVersionResource{
					Group: groupVersion.Group, Version: groupVersion.Version, Resource: resource.Name,
				},
				namespaced: resource.Namespaced,
			}, nil
		}
	}
	if discoveryErr != nil {
		return resourceSpec{}, fmt.Errorf("discover API resources: %w", discoveryErr)
	}
	return resourceSpec{}, fmt.Errorf("resource %q was not advertised by the API server", kind)
}

func supportsVerb(verbs metav1.Verbs, wanted string) bool {
	for _, verb := range verbs {
		if verb == wanted {
			return true
		}
	}
	return false
}

func containsShortName(names []string, wanted string) bool {
	for _, name := range names {
		if strings.EqualFold(name, wanted) {
			return true
		}
	}
	return false
}

func resourceInterface(client dynamic.Interface, spec resourceSpec, namespace string) dynamic.ResourceInterface {
	resource := client.Resource(spec.gvr)
	if spec.namespaced {
		return resource.Namespace(namespace)
	}
	return resource
}

func wantsInventory(kinds []fleet.FactKind) bool {
	if len(kinds) == 0 {
		return true
	}
	for _, kind := range kinds {
		if kind == fleet.FactInventory {
			return true
		}
	}
	return false
}

func objectUsesImage(object unstructured.Unstructured, image string) bool {
	paths := [][]string{
		{"spec", "containers"},
		{"spec", "initContainers"},
		{"spec", "template", "spec", "containers"},
		{"spec", "template", "spec", "initContainers"},
	}
	for _, path := range paths {
		containers, found, err := unstructured.NestedSlice(object.Object, path...)
		if err != nil || !found {
			continue
		}
		for _, raw := range containers {
			container, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			value, ok := container["image"].(string)
			if ok && strings.Contains(value, image) {
				return true
			}
		}
	}
	return false
}

func targetScopeNames(requested []string, scopes map[string]connector.Scope) []string {
	set := make(map[string]struct{})
	if len(requested) == 0 {
		for name := range scopes {
			set[name] = struct{}{}
		}
	} else {
		for _, name := range requested {
			if name != "" {
				set[name] = struct{}{}
			}
		}
	}
	result := make([]string, 0, len(set))
	for name := range set {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

func (adapter *Adapter) scopeClient(name string) (connector.Scope, dynamic.Interface, *rest.Config, bool) {
	adapter.mu.RLock()
	defer adapter.mu.RUnlock()
	scope, exists := adapter.scopes[name]
	var config *rest.Config
	if adapter.configs[name] != nil {
		config = rest.CopyConfig(adapter.configs[name])
	}
	return cloneScope(scope), adapter.clients[name], config, exists
}

func (adapter *Adapter) stateSnapshot() (
	map[string]connector.Scope,
	map[string]dynamic.Interface,
	map[string]*rest.Config,
	map[string]tablePrinter,
	map[string]time.Time,
) {
	adapter.mu.RLock()
	defer adapter.mu.RUnlock()
	scopes := make(map[string]connector.Scope, len(adapter.scopes))
	for name, scope := range adapter.scopes {
		scopes[name] = cloneScope(scope)
	}
	clients := make(map[string]dynamic.Interface, len(adapter.clients))
	for name, client := range adapter.clients {
		clients[name] = client
	}
	configs := make(map[string]*rest.Config, len(adapter.configs))
	for name, config := range adapter.configs {
		configs[name] = rest.CopyConfig(config)
	}
	tables := make(map[string]tablePrinter, len(adapter.tables))
	for name, table := range adapter.tables {
		tables[name] = table
	}
	lastSeen := make(map[string]time.Time, len(adapter.lastSeen))
	for name, observed := range adapter.lastSeen {
		lastSeen[name] = observed
	}
	return scopes, clients, configs, tables, lastSeen
}

func (adapter *Adapter) recordLastSeen(name string, observed time.Time) {
	adapter.mu.Lock()
	adapter.lastSeen[name] = observed
	scope := adapter.scopes[name]
	scope.ObservedAt = observed
	scope.Reachable = true
	adapter.scopes[name] = scope
	adapter.mu.Unlock()
}

func isStale(now, observed time.Time, threshold time.Duration) bool {
	return !observed.IsZero() && now.Sub(observed) > threshold
}
