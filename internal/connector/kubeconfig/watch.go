// SPDX-License-Identifier: Apache-2.0

package kubeconfig

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"

	"github.com/ArdurAI/sith/internal/connector"
	"github.com/ArdurAI/sith/internal/fleet"
)

const (
	watchBuffer         = 256
	initialWatchBackoff = 250 * time.Millisecond
	maximumWatchBackoff = 5 * time.Second
	watchTimeoutSeconds = int64(300)
)

// Watch opens independent list-watch loops for every reachable scope and requested kind.
func (adapter *Adapter) Watch(ctx context.Context, kinds ...string) (<-chan connector.WatchEvent, error) {
	normalized, err := normalizeWatchKinds(kinds)
	if err != nil {
		return nil, err
	}
	if err := adapter.ensureDiscovered(ctx); err != nil {
		return nil, err
	}
	scopes, clients, configs, tables := adapter.watchStateSnapshot()
	events := make(chan connector.WatchEvent, watchBuffer)
	var waitGroup sync.WaitGroup
	for name, scope := range scopes {
		for _, kind := range normalized {
			waitGroup.Add(1)
			if !scope.Reachable || clients[name] == nil {
				go func(scopeName, resourceKind string) {
					defer waitGroup.Done()
					sendWatchEvent(ctx, events, connector.WatchEvent{
						Type: connector.WatchError, Workspace: fleet.LocalWorkspace,
						Kind: resourceKind, Scope: scopeName, Err: ErrUnreachableScope,
					})
				}(name, kind)
				continue
			}
			go func(
				scopeName, resourceKind string,
				client dynamic.Interface,
				config *rest.Config,
				table tablePrinter,
			) {
				defer waitGroup.Done()
				adapter.watchScope(ctx, events, scopeName, resourceKind, client, config, table)
			}(name, kind, clients[name], configs[name], tables[name])
		}
	}
	go func() {
		waitGroup.Wait()
		close(events)
	}()
	return events, nil
}

func (adapter *Adapter) watchScope(
	ctx context.Context,
	events chan<- connector.WatchEvent,
	scope, kind string,
	client dynamic.Interface,
	config *rest.Config,
	table tablePrinter,
) {
	backoff := initialWatchBackoff
	for ctx.Err() == nil {
		spec, known := lookupResource(kind)
		generic := !known
		if !known {
			var err error
			spec, err = adapter.resolveResource(ctx, scope, config, kind)
			if err != nil {
				if !adapter.reportWatchError(ctx, events, kind, scope, err) || !waitForWatchRetry(ctx, backoff) {
					return
				}
				backoff = min(backoff*2, maximumWatchBackoff)
				continue
			}
		}
		resource := resourceInterface(client, spec, "")
		list, err := callWithTimeout(ctx, adapter.gate, scope, adapter.settings.requestTimeout, func(requestCtx context.Context) (*unstructured.UnstructuredList, error) {
			return resource.List(requestCtx, metav1.ListOptions{})
		})
		if err != nil {
			if !adapter.reportWatchError(ctx, events, kind, scope, fmt.Errorf("list before watch: %w", err)) ||
				!waitForWatchRetry(ctx, backoff) {
				return
			}
			backoff = min(backoff*2, maximumWatchBackoff)
			continue
		}
		observedAt := adapter.settings.now().UTC()
		display := map[string][]fleet.DisplayField{}
		if generic {
			if table == nil {
				if !adapter.reportWatchError(ctx, events, kind, scope, errors.New("server table client is unavailable")) {
					return
				}
				return
			}
			display, err = table(ctx, spec, tableRequest{rowBudget: len(list.Items)})
			if err != nil {
				if !adapter.reportWatchError(ctx, events, kind, scope, err) || !waitForWatchRetry(ctx, backoff) {
					return
				}
				backoff = min(backoff*2, maximumWatchBackoff)
				continue
			}
		}
		facts, err := factsFromObjects(list.Items, spec, scope, observedAt, display)
		if err != nil {
			if !adapter.reportWatchError(ctx, events, kind, scope, err) {
				return
			}
			return
		}
		if !sendWatchEvent(ctx, events, connector.WatchEvent{
			Type: connector.WatchSnapshot, Workspace: fleet.LocalWorkspace,
			Kind: kind, Scope: scope, Facts: facts, ObservedAt: observedAt,
		}) {
			return
		}
		adapter.recordLastSeen(scope, observedAt)
		backoff = initialWatchBackoff

		stream, err := resource.Watch(ctx, metav1.ListOptions{
			ResourceVersion: list.GetResourceVersion(), AllowWatchBookmarks: true, TimeoutSeconds: pointer(watchTimeoutSeconds),
		})
		if err != nil {
			if !adapter.reportWatchError(ctx, events, kind, scope, fmt.Errorf("open watch: %w", err)) ||
				!waitForWatchRetry(ctx, backoff) {
				return
			}
			backoff = min(backoff*2, maximumWatchBackoff)
			continue
		}
		watchErr := adapter.consumeWatch(ctx, events, stream, kind, scope, spec, table, generic)
		stream.Stop()
		if ctx.Err() != nil {
			return
		}
		if watchErr != nil && !adapter.reportWatchError(ctx, events, kind, scope, watchErr) {
			return
		}
		if !waitForWatchRetry(ctx, backoff) {
			return
		}
		if watchErr != nil {
			backoff = min(backoff*2, maximumWatchBackoff)
		}
	}
}

func (adapter *Adapter) consumeWatch(
	ctx context.Context,
	events chan<- connector.WatchEvent,
	stream watch.Interface,
	kind, scope string,
	spec resourceSpec,
	table tablePrinter,
	generic bool,
) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, open := <-stream.ResultChan():
			if !open {
				return nil
			}
			object, ok := event.Object.(*unstructured.Unstructured)
			if event.Type == watch.Error {
				if err := apierrors.FromObject(event.Object); err != nil {
					return fmt.Errorf("watch API error: %w", err)
				}
				return errors.New("watch API returned an unknown error")
			}
			if event.Type == watch.Bookmark || !ok {
				continue
			}
			observedAt := adapter.settings.now().UTC()
			evidence, err := evidenceFromObject(*object, spec, scope, observedAt)
			if err != nil {
				return err
			}
			watchEvent := connector.WatchEvent{
				Workspace: fleet.LocalWorkspace, Kind: kind, Scope: scope, ObservedAt: observedAt, Ref: evidence.Ref,
			}
			switch event.Type {
			case watch.Added, watch.Modified:
				if generic {
					display, tableErr := table(ctx, spec, tableRequest{
						namespace: object.GetNamespace(),
						name:      object.GetName(),
					})
					if tableErr != nil {
						return tableErr
					}
					evidence.Display = append(
						[]fleet.DisplayField(nil), display[tableObjectKey(object.GetNamespace(), object.GetName())]...,
					)
				}
				watchEvent.Type = connector.WatchUpsert
				watchEvent.Fact = fleet.Fact{Evidence: evidence, Workspace: fleet.LocalWorkspace}
			case watch.Deleted:
				watchEvent.Type = connector.WatchDelete
			default:
				continue
			}
			if !sendWatchEvent(ctx, events, watchEvent) {
				return ctx.Err()
			}
			adapter.recordLastSeen(scope, observedAt)
		}
	}
}

func (adapter *Adapter) reportWatchError(
	ctx context.Context,
	events chan<- connector.WatchEvent,
	kind, scope string,
	err error,
) bool {
	return sendWatchEvent(ctx, events, connector.WatchEvent{
		Type: connector.WatchError, Workspace: fleet.LocalWorkspace, Kind: kind, Scope: scope, Err: err,
	})
}

func (adapter *Adapter) watchStateSnapshot() (
	map[string]connector.Scope,
	map[string]dynamic.Interface,
	map[string]*rest.Config,
	map[string]tablePrinter,
) {
	adapter.mu.RLock()
	defer adapter.mu.RUnlock()
	scopes := make(map[string]connector.Scope, len(adapter.scopes))
	for name, scope := range adapter.scopes {
		scopes[name] = cloneScope(scope)
	}
	clients := make(map[string]dynamic.Interface, len(adapter.watchers))
	for name, client := range adapter.watchers {
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
	return scopes, clients, configs, tables
}

func normalizeWatchKinds(kinds []string) ([]string, error) {
	if len(kinds) == 0 {
		return nil, fmt.Errorf("watch resource kind is required")
	}
	seen := make(map[string]struct{}, len(kinds))
	result := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		trimmed := strings.TrimSpace(kind)
		if trimmed == "" {
			return nil, fmt.Errorf("watch resource kind must not be empty")
		}
		key := strings.ToLower(trimmed)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, trimmed)
	}
	return result, nil
}

func factsFromObjects(
	objects []unstructured.Unstructured,
	spec resourceSpec,
	scope string,
	observedAt time.Time,
	display map[string][]fleet.DisplayField,
) ([]fleet.Fact, error) {
	facts := make([]fleet.Fact, 0, len(objects))
	for _, object := range objects {
		evidence, err := evidenceFromObject(object, spec, scope, observedAt)
		if err != nil {
			return nil, err
		}
		evidence.Display = append([]fleet.DisplayField(nil), display[tableObjectKey(object.GetNamespace(), object.GetName())]...)
		facts = append(facts, fleet.Fact{Evidence: evidence, Workspace: fleet.LocalWorkspace})
	}
	return facts, nil
}

func sendWatchEvent(ctx context.Context, output chan<- connector.WatchEvent, event connector.WatchEvent) bool {
	select {
	case output <- event:
		return true
	case <-ctx.Done():
		return false
	}
}

func waitForWatchRetry(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func pointer[T any](value T) *T { return &value }
