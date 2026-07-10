// SPDX-License-Identifier: Apache-2.0

// Package kubeconfig implements the local kubeconfig source adapter.
package kubeconfig

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/ArdurAI/sith/internal/connector"
)

const (
	// Kind is the stable registry identifier for the local kubeconfig adapter.
	Kind = "local-kubeconfig"

	protocolVersion       = "1.0.0"
	defaultProbeTimeout   = 2 * time.Second
	defaultRequestTimeout = 10 * time.Second
	defaultStaleAfter     = 2 * time.Minute
	defaultConcurrency    = 16
)

var supportedKinds = []string{
	"Deployment",
	"ReplicaSet",
	"Pod",
	"Rollout",
	"Node",
	"Service",
	"Namespace",
	"Event",
}

type probeFunc func(ctx context.Context, config *rest.Config) error
type dynamicFactory func(config *rest.Config) (dynamic.Interface, error)
type resourceResolver func(ctx context.Context, config *rest.Config, kind string) (resourceSpec, error)

type options struct {
	loadingRules   *clientcmd.ClientConfigLoadingRules
	probeTimeout   time.Duration
	requestTimeout time.Duration
	staleAfter     time.Duration
	maxConcurrency int
	now            func() time.Time
	probe          probeFunc
	dynamic        dynamicFactory
	resolve        resourceResolver
}

// Option configures the local kubeconfig adapter.
type Option func(*options) error

// WithLoadingRules replaces client-go's default kubeconfig loading rules.
func WithLoadingRules(rules *clientcmd.ClientConfigLoadingRules) Option {
	return func(settings *options) error {
		if rules == nil {
			return fmt.Errorf("kubeconfig loading rules must not be nil")
		}
		copyRules := *rules
		settings.loadingRules = &copyRules
		return nil
	}
}

// WithExplicitPath reads one explicitly selected kubeconfig path.
func WithExplicitPath(path string) Option {
	return func(settings *options) error {
		if path != "" {
			settings.loadingRules.ExplicitPath = path
		}
		return nil
	}
}

// WithProbeTimeout sets the independent reachability deadline for each context.
func WithProbeTimeout(timeout time.Duration) Option {
	return func(settings *options) error {
		if timeout <= 0 {
			return fmt.Errorf("probe timeout must be positive")
		}
		settings.probeTimeout = timeout
		return nil
	}
}

// WithRequestTimeout sets the deadline for resource reads and queries.
func WithRequestTimeout(timeout time.Duration) Option {
	return func(settings *options) error {
		if timeout <= 0 {
			return fmt.Errorf("request timeout must be positive")
		}
		settings.requestTimeout = timeout
		return nil
	}
}

// WithMaxConcurrency bounds simultaneous context operations.
func WithMaxConcurrency(limit int) Option {
	return func(settings *options) error {
		if limit <= 0 {
			return fmt.Errorf("maximum concurrency must be positive")
		}
		settings.maxConcurrency = limit
		return nil
	}
}

func withClock(now func() time.Time) Option {
	return func(settings *options) error {
		if now == nil {
			return fmt.Errorf("clock must not be nil")
		}
		settings.now = now
		return nil
	}
}

func withProbe(probe probeFunc) Option {
	return func(settings *options) error {
		if probe == nil {
			return fmt.Errorf("probe must not be nil")
		}
		settings.probe = probe
		return nil
	}
}

func withDynamicFactory(factory dynamicFactory) Option {
	return func(settings *options) error {
		if factory == nil {
			return fmt.Errorf("dynamic client factory must not be nil")
		}
		settings.dynamic = factory
		return nil
	}
}

func withResourceResolver(resolver resourceResolver) Option {
	return func(settings *options) error {
		if resolver == nil {
			return fmt.Errorf("resource resolver must not be nil")
		}
		settings.resolve = resolver
		return nil
	}
}

// Adapter discovers contexts and performs independent local client-go reads.
type Adapter struct {
	settings options

	mu         sync.RWMutex
	discovered bool
	scopes     map[string]connector.Scope
	clients    map[string]dynamic.Interface
	configs    map[string]*rest.Config
	resources  map[string]map[string]resourceSpec
	lastSeen   map[string]time.Time
}

var _ connector.Reader = (*Adapter)(nil)

// Default constructs an adapter using client-go's KUBECONFIG and home-directory rules.
func Default() *Adapter {
	return newAdapter(defaultOptions())
}

// New constructs a local kubeconfig adapter without performing network I/O.
func New(opts ...Option) (*Adapter, error) {
	settings := defaultOptions()
	for _, option := range opts {
		if option == nil {
			return nil, fmt.Errorf("configure local kubeconfig adapter: option is nil")
		}
		if err := option(&settings); err != nil {
			return nil, fmt.Errorf("configure local kubeconfig adapter: %w", err)
		}
	}

	return newAdapter(settings), nil
}

func defaultOptions() options {
	return options{
		loadingRules:   clientcmd.NewDefaultClientConfigLoadingRules(),
		probeTimeout:   defaultProbeTimeout,
		requestTimeout: defaultRequestTimeout,
		staleAfter:     defaultStaleAfter,
		maxConcurrency: defaultConcurrency,
		now:            time.Now,
		probe:          defaultProbe,
		dynamic: func(config *rest.Config) (dynamic.Interface, error) {
			return dynamic.NewForConfig(config)
		},
		resolve: defaultResourceResolver,
	}
}

func newAdapter(settings options) *Adapter {
	return &Adapter{
		settings:  settings,
		scopes:    make(map[string]connector.Scope),
		clients:   make(map[string]dynamic.Interface),
		configs:   make(map[string]*rest.Config),
		resources: make(map[string]map[string]resourceSpec),
		lastSeen:  make(map[string]time.Time),
	}
}

// Kind identifies this connector in the registry and resource address space.
func (*Adapter) Kind() string {
	return Kind
}

// Capabilities declares the read-only verbs implemented by local kubeconfig.
func (*Adapter) Capabilities() []connector.Capability {
	return []connector.Capability{connector.CapDiscover, connector.CapRead, connector.CapQuery}
}

// Descriptor returns immutable registration metadata for this adapter.
func (adapter *Adapter) Descriptor() connector.Descriptor {
	return connector.Descriptor{
		Kind:         adapter.Kind(),
		ConnKind:     connector.KindReadAdapter,
		ProtocolV:    protocolVersion,
		Owner:        "sith-core",
		Capabilities: adapter.Capabilities(),
	}
}

// Discover enumerates every context and probes each independently.
func (adapter *Adapter) Discover(ctx context.Context) (connector.Discovery, error) {
	rawConfig, err := adapter.settings.loadingRules.Load()
	if err != nil {
		return connector.Discovery{}, fmt.Errorf("load kubeconfig: %w", err)
	}

	names := make([]string, 0, len(rawConfig.Contexts))
	for name := range rawConfig.Contexts {
		names = append(names, name)
	}
	sort.Strings(names)
	priorLastSeen := adapter.lastSeenSnapshot()

	results := make([]contextResult, len(names))
	adapter.runBounded(len(names), func(index int) {
		results[index] = adapter.probeContext(ctx, *rawConfig, names[index], priorLastSeen[names[index]])
	})
	if err := ctx.Err(); err != nil {
		return connector.Discovery{}, fmt.Errorf("discover kubeconfig contexts: %w", err)
	}

	scopes := make([]connector.Scope, 0, len(results))
	unreachable := make([]string, 0)
	clients := make(map[string]dynamic.Interface, len(results))
	configs := make(map[string]*rest.Config, len(results))
	lastSeen := make(map[string]time.Time, len(results))
	for _, result := range results {
		scopes = append(scopes, result.scope)
		if result.scope.Reachable {
			clients[result.scope.Name] = result.client
			configs[result.scope.Name] = rest.CopyConfig(result.config)
		} else {
			unreachable = append(unreachable, result.scope.Name)
		}
		if !result.scope.ObservedAt.IsZero() {
			lastSeen[result.scope.Name] = result.scope.ObservedAt
		}
	}

	adapter.mu.Lock()
	adapter.discovered = true
	adapter.scopes = make(map[string]connector.Scope, len(scopes))
	for _, scope := range scopes {
		adapter.scopes[scope.Name] = cloneScope(scope)
	}
	adapter.clients = clients
	adapter.configs = configs
	adapter.resources = make(map[string]map[string]resourceSpec)
	adapter.lastSeen = lastSeen
	adapter.mu.Unlock()

	return connector.Discovery{Scopes: cloneScopes(scopes), Unreachable: append([]string(nil), unreachable...)}, nil
}

type contextResult struct {
	scope  connector.Scope
	client dynamic.Interface
	config *rest.Config
}

func (adapter *Adapter) probeContext(
	ctx context.Context,
	rawConfig clientcmdapi.Config,
	name string,
	lastSeen time.Time,
) contextResult {
	scope := connector.Scope{
		Name:       name,
		Kinds:      append([]string(nil), supportedKinds...),
		ObservedAt: lastSeen,
	}
	clientConfig := clientcmd.NewNonInteractiveClientConfig(
		rawConfig,
		name,
		&clientcmd.ConfigOverrides{},
		adapter.settings.loadingRules,
	)
	restConfig, err := clientConfig.ClientConfig()
	if err != nil {
		return contextResult{scope: scope}
	}
	restConfig.UserAgent = "sith/" + protocolVersion

	probeConfig := rest.CopyConfig(restConfig)
	probeConfig.Timeout = adapter.settings.probeTimeout
	_, err = callWithTimeout(ctx, adapter.settings.probeTimeout, func(probeCtx context.Context) (struct{}, error) {
		return struct{}{}, adapter.settings.probe(probeCtx, probeConfig)
	})
	if err != nil {
		return contextResult{scope: scope}
	}

	requestConfig := rest.CopyConfig(restConfig)
	requestConfig.Timeout = adapter.settings.requestTimeout
	client, err := adapter.settings.dynamic(requestConfig)
	if err != nil {
		return contextResult{scope: scope}
	}

	scope.Reachable = true
	scope.ObservedAt = adapter.settings.now().UTC()
	return contextResult{scope: scope, client: client, config: requestConfig}
}

func (adapter *Adapter) runBounded(count int, operation func(index int)) {
	if count == 0 {
		return
	}
	workers := min(adapter.settings.maxConcurrency, count)
	jobs := make(chan int)
	var waitGroup sync.WaitGroup
	waitGroup.Add(workers)
	for range workers {
		go func() {
			defer waitGroup.Done()
			for index := range jobs {
				operation(index)
			}
		}()
	}
	for index := range count {
		jobs <- index
	}
	close(jobs)
	waitGroup.Wait()
}

type operationResult[T any] struct {
	value T
	err   error
}

func callWithTimeout[T any](
	ctx context.Context,
	timeout time.Duration,
	operation func(context.Context) (T, error),
) (T, error) {
	operationCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	result := make(chan operationResult[T], 1)
	// client-go's exec authenticator uses exec.Command rather than CommandContext. Isolating the
	// call keeps one auth helper that ignores cancellation from stalling the rest of the fleet.
	go func() {
		value, err := operation(operationCtx)
		result <- operationResult[T]{value: value, err: err}
	}()
	select {
	case completed := <-result:
		return completed.value, completed.err
	case <-operationCtx.Done():
		var zero T
		return zero, operationCtx.Err()
	}
}

func (adapter *Adapter) ensureDiscovered(ctx context.Context) error {
	adapter.mu.RLock()
	discovered := adapter.discovered
	adapter.mu.RUnlock()
	if discovered {
		return nil
	}
	_, err := adapter.Discover(ctx)
	return err
}

func (adapter *Adapter) lastSeenSnapshot() map[string]time.Time {
	adapter.mu.RLock()
	defer adapter.mu.RUnlock()
	result := make(map[string]time.Time, len(adapter.lastSeen))
	for name, observed := range adapter.lastSeen {
		result[name] = observed
	}
	return result
}

func defaultProbe(ctx context.Context, config *rest.Config) error {
	client, err := discovery.NewDiscoveryClientForConfig(config)
	if err != nil {
		return fmt.Errorf("create discovery client: %w", err)
	}
	if err := client.RESTClient().Get().AbsPath("/version").Do(ctx).Error(); err != nil {
		return fmt.Errorf("query API version: %w", err)
	}
	return nil
}

func cloneScope(scope connector.Scope) connector.Scope {
	scope.Kinds = append([]string(nil), scope.Kinds...)
	return scope
}

func cloneScopes(scopes []connector.Scope) []connector.Scope {
	result := make([]connector.Scope, 0, len(scopes))
	for _, scope := range scopes {
		result = append(result, cloneScope(scope))
	}
	return result
}
