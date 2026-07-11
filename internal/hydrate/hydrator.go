// SPDX-License-Identifier: Apache-2.0

// Package hydrate owns connector access and reconciles background data into the local store.
package hydrate

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/ArdurAI/sith/internal/connector"
	"github.com/ArdurAI/sith/internal/fleet"
	"github.com/ArdurAI/sith/internal/fleetcache"
)

var tierOneKinds = []string{"Pod", "Deployment", "Event", "Node"}

const (
	defaultResyncInterval = 2 * time.Minute
	watchReconnectDelay   = 2 * time.Second
)

// TierOneKinds returns the frequency-ordered daily-loop lenses for the Slice-2 fleet view.
func TierOneKinds() []string {
	return append([]string(nil), tierOneKinds...)
}

// ErrPaused reports a sync request while the store is deliberately frozen.
var ErrPaused = errors.New("fleet cache is paused")

// ErrSyncInProgress reports a duplicate background sync request.
var ErrSyncInProgress = errors.New("fleet cache sync is already in progress")

// Option configures background hydration.
type Option func(*options) error

type options struct {
	kinds          []string
	maxConcurrency int
	resyncInterval time.Duration
}

// WithResyncInterval sets the slow safety resync used around primary watch streams.
func WithResyncInterval(interval time.Duration) Option {
	return func(settings *options) error {
		if interval <= 0 {
			return fmt.Errorf("resync interval must be positive")
		}
		settings.resyncInterval = interval
		return nil
	}
}

// WithKinds limits hydration to an explicit resource-kind set.
func WithKinds(kinds ...string) Option {
	return func(settings *options) error {
		if len(kinds) == 0 {
			return fmt.Errorf("at least one hydration kind is required")
		}
		settings.kinds = append([]string(nil), kinds...)
		return nil
	}
}

// WithMaxConcurrency bounds simultaneous connector queries.
func WithMaxConcurrency(limit int) Option {
	return func(settings *options) error {
		if limit <= 0 {
			return fmt.Errorf("maximum concurrency must be positive")
		}
		settings.maxConcurrency = limit
		return nil
	}
}

// Hydrator is the only interaction-layer component allowed to call a connector.
type Hydrator struct {
	reader  connector.Reader
	store   *fleetcache.Store
	mu      sync.RWMutex
	kinds   []string
	changed chan struct{}
	limit   int
	resync  time.Duration
}

// New validates and constructs a background hydrator.
func New(reader connector.Reader, store *fleetcache.Store, opts ...Option) (*Hydrator, error) {
	if reader == nil {
		return nil, fmt.Errorf("construct hydrator: reader is nil")
	}
	if store == nil {
		return nil, fmt.Errorf("construct hydrator: store is nil")
	}
	settings := options{
		kinds: TierOneKinds(), maxConcurrency: len(tierOneKinds), resyncInterval: defaultResyncInterval,
	}
	for _, option := range opts {
		if option == nil {
			return nil, fmt.Errorf("construct hydrator: option is nil")
		}
		if err := option(&settings); err != nil {
			return nil, fmt.Errorf("construct hydrator: %w", err)
		}
	}
	kinds, err := normalizeKinds(settings.kinds)
	if err != nil {
		return nil, fmt.Errorf("construct hydrator: %w", err)
	}
	return &Hydrator{
		reader: reader, store: store, kinds: kinds, changed: make(chan struct{}),
		limit: settings.maxConcurrency, resync: settings.resyncInterval,
	}, nil
}

// Kinds returns the deterministic resource-kind set this hydrator reconciles.
func (hydrator *Hydrator) Kinds() []string {
	hydrator.mu.RLock()
	defer hydrator.mu.RUnlock()
	return append([]string(nil), hydrator.kinds...)
}

// SyncOnce discovers contexts and independently reconciles each configured lens.
func (hydrator *Hydrator) SyncOnce(ctx context.Context) error {
	return hydrator.sync(ctx, hydrator.Kinds())
}

// SyncKinds discovers contexts and reconciles an on-demand resource-kind set.
func (hydrator *Hydrator) SyncKinds(ctx context.Context, kinds ...string) error {
	normalized, err := normalizeKinds(kinds)
	if err != nil {
		return fmt.Errorf("sync hydration kinds: %w", err)
	}
	syncErr := hydrator.sync(ctx, normalized)
	hydrator.registerKinds(normalized)
	return syncErr
}

// Run keeps the configured cache warm, preferring live watch deltas with a slow safety resync.
func (hydrator *Hydrator) Run(ctx context.Context) error {
	if err := hydrator.SyncOnce(ctx); err != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	watcher, supportsWatch := hydrator.reader.(connector.Watcher)
	if !supportsWatch {
		return hydrator.runPollingFallback(ctx)
	}
	return hydrator.runWatch(ctx, watcher)
}

func (hydrator *Hydrator) runWatch(ctx context.Context, watcher connector.Watcher) error {
	resync := time.NewTimer(hydrator.resync)
	defer resync.Stop()
	for ctx.Err() == nil {
		kinds, changed := hydrator.watchKinds()
		watchCtx, cancel := context.WithCancel(ctx)
		events, err := watcher.Watch(watchCtx, kinds...)
		if err != nil {
			cancel()
			if !waitForRetry(ctx, watchReconnectDelay) {
				return nil
			}
			_ = hydrator.SyncOnce(ctx)
			continue
		}
		restart := false
		for !restart {
			select {
			case <-ctx.Done():
				cancel()
				return nil
			case <-changed:
				cancel()
				restart = true
			case <-resync.C:
				cancel()
				_ = hydrator.SyncOnce(ctx)
				resync.Reset(hydrator.resync)
				restart = true
			case event, open := <-events:
				if !open {
					cancel()
					if !waitForRetry(ctx, watchReconnectDelay) {
						return nil
					}
					_ = hydrator.SyncOnce(ctx)
					restart = true
					continue
				}
				if err := hydrator.store.ApplyWatchEvent(event); err != nil {
					cancel()
					return fmt.Errorf("apply live cache delta: %w", err)
				}
			}
		}
		cancel()
	}
	return nil
}

func (hydrator *Hydrator) runPollingFallback(ctx context.Context) error {
	ticker := time.NewTicker(hydrator.resync)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			_ = hydrator.SyncOnce(ctx)
		}
	}
}

func (hydrator *Hydrator) registerKinds(kinds []string) {
	hydrator.mu.Lock()
	defer hydrator.mu.Unlock()
	known := make(map[string]struct{}, len(hydrator.kinds))
	for _, kind := range hydrator.kinds {
		known[kind] = struct{}{}
	}
	changed := false
	for _, kind := range kinds {
		if _, exists := known[kind]; exists {
			continue
		}
		hydrator.kinds = append(hydrator.kinds, kind)
		known[kind] = struct{}{}
		changed = true
	}
	if changed {
		close(hydrator.changed)
		hydrator.changed = make(chan struct{})
	}
}

func (hydrator *Hydrator) watchKinds() ([]string, <-chan struct{}) {
	hydrator.mu.RLock()
	defer hydrator.mu.RUnlock()
	return append([]string(nil), hydrator.kinds...), hydrator.changed
}

func waitForRetry(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (hydrator *Hydrator) sync(ctx context.Context, kinds []string) error {
	if hydrator.store.Paused() {
		return ErrPaused
	}
	if !hydrator.store.BeginSync(kinds...) {
		if hydrator.store.Paused() {
			return ErrPaused
		}
		return ErrSyncInProgress
	}

	var syncErr error
	defer func() {
		hydrator.store.EndSync(syncErr)
	}()
	discovery, err := hydrator.reader.Discover(ctx)
	if err != nil {
		syncErr = fmt.Errorf("discover hydration scopes: %w", err)
		return syncErr
	}
	hydrator.store.SetDiscovery(fleet.LocalWorkspace, discovery)

	errorsByKind := make([]error, len(kinds))
	workers := min(hydrator.limit, len(kinds))
	jobs := make(chan int)
	var waitGroup sync.WaitGroup
	waitGroup.Add(workers)
	for range workers {
		go func() {
			defer waitGroup.Done()
			for index := range jobs {
				kind := kinds[index]
				result, queryErr := hydrator.reader.Query(ctx, fleet.Query{
					Kinds:    []fleet.FactKind{fleet.FactInventory},
					Selector: fleet.Selector{ResourceKind: kind},
				})
				if queryErr != nil {
					errorsByKind[index] = fmt.Errorf("query %s cache: %w", kind, queryErr)
					continue
				}
				if replaceErr := hydrator.store.Replace(kind, result); replaceErr != nil {
					errorsByKind[index] = fmt.Errorf("replace %s cache: %w", kind, replaceErr)
				}
			}
		}()
	}
	for index := range kinds {
		select {
		case jobs <- index:
		case <-ctx.Done():
			close(jobs)
			waitGroup.Wait()
			syncErr = errors.Join(errorsByKind...)
			if syncErr == nil {
				syncErr = ctx.Err()
			}
			return syncErr
		}
	}
	close(jobs)
	waitGroup.Wait()
	syncErr = errors.Join(errorsByKind...)
	return syncErr
}

func normalizeKinds(kinds []string) ([]string, error) {
	if len(kinds) == 0 {
		return nil, fmt.Errorf("at least one hydration kind is required")
	}
	set := make(map[string]struct{}, len(kinds))
	result := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		trimmed := strings.TrimSpace(kind)
		if trimmed == "" {
			return nil, fmt.Errorf("hydration kind must not be empty")
		}
		var canonical string
		switch strings.ToLower(trimmed) {
		case "pod", "pods", "po":
			canonical = "Pod"
		case "deployment", "deployments", "deploy":
			canonical = "Deployment"
		case "event", "events", "ev":
			canonical = "Event"
		case "node", "nodes", "no":
			canonical = "Node"
		default:
			canonical = strings.ToUpper(trimmed[:1]) + trimmed[1:]
		}
		if _, exists := set[canonical]; exists {
			continue
		}
		set[canonical] = struct{}{}
		result = append(result, canonical)
	}
	return result, nil
}
