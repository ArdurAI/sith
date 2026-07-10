// SPDX-License-Identifier: Apache-2.0

// Package hydrate owns connector access and reconciles background data into the local store.
package hydrate

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/ArdurAI/sith/internal/connector"
	"github.com/ArdurAI/sith/internal/fleet"
	"github.com/ArdurAI/sith/internal/fleetcache"
)

var tierOneKinds = []string{"Pod", "Deployment", "Event", "Node"}

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
	reader connector.Reader
	store  *fleetcache.Store
	kinds  []string
	limit  int
}

// New validates and constructs a background hydrator.
func New(reader connector.Reader, store *fleetcache.Store, opts ...Option) (*Hydrator, error) {
	if reader == nil {
		return nil, fmt.Errorf("construct hydrator: reader is nil")
	}
	if store == nil {
		return nil, fmt.Errorf("construct hydrator: store is nil")
	}
	settings := options{kinds: TierOneKinds(), maxConcurrency: len(tierOneKinds)}
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
	return &Hydrator{reader: reader, store: store, kinds: kinds, limit: settings.maxConcurrency}, nil
}

// Kinds returns the deterministic resource-kind set this hydrator reconciles.
func (hydrator *Hydrator) Kinds() []string {
	return append([]string(nil), hydrator.kinds...)
}

// SyncOnce discovers contexts and independently reconciles each configured lens.
func (hydrator *Hydrator) SyncOnce(ctx context.Context) error {
	return hydrator.sync(ctx, hydrator.kinds)
}

// SyncKinds discovers contexts and reconciles an on-demand resource-kind set.
func (hydrator *Hydrator) SyncKinds(ctx context.Context, kinds ...string) error {
	normalized, err := normalizeKinds(kinds)
	if err != nil {
		return fmt.Errorf("sync hydration kinds: %w", err)
	}
	return hydrator.sync(ctx, normalized)
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
	hydrator.store.SetDiscovery(discovery)

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
