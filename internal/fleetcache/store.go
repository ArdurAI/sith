// SPDX-License-Identifier: Apache-2.0

package fleetcache

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ArdurAI/sith/internal/connector"
	"github.com/ArdurAI/sith/internal/fleet"
)

const defaultFreshFor = 15 * time.Second

// State describes the user-visible lifecycle of the local store.
type State string

// Store lifecycle states.
const (
	StateCold     State = "cold"
	StateWarming  State = "warming"
	StateWarm     State = "warm"
	StateDegraded State = "degraded"
	StateOffline  State = "offline"
	StatePaused   State = "paused"
)

// Snapshot is an immutable cache-only answer for one render interaction.
type Snapshot struct {
	Version   uint64            `json:"version"`
	State     State             `json:"state"`
	Syncing   bool              `json:"syncing"`
	Paused    bool              `json:"paused"`
	Records   []Record          `json:"records"`
	Coverage  fleet.Coverage    `json:"coverage"`
	UpdatedAt time.Time         `json:"updated_at,omitempty"`
	LastError string            `json:"last_error,omitempty"`
	Scopes    []connector.Scope `json:"scopes"`
}

// Store owns normalized last-known fleet state and never performs network I/O.
type Store struct {
	mu sync.RWMutex

	records   map[string]map[string]Record
	coverage  map[string]fleet.Coverage
	aliases   map[string]string
	scopes    map[string]connector.Scope
	warmed    map[string]bool
	expected  map[string]bool
	syncing   bool
	paused    bool
	lastError string
	updatedAt time.Time
	version   uint64
	changed   chan struct{}
	now       func() time.Time
	freshFor  time.Duration
}

// New creates an empty cold store.
func New() *Store {
	return newStore(time.Now, defaultFreshFor)
}

func newStore(now func() time.Time, freshFor time.Duration) *Store {
	return &Store{
		records:  make(map[string]map[string]Record),
		coverage: make(map[string]fleet.Coverage),
		aliases:  make(map[string]string),
		scopes:   make(map[string]connector.Scope),
		warmed:   make(map[string]bool),
		expected: make(map[string]bool),
		changed:  make(chan struct{}),
		now:      now,
		freshFor: freshFor,
	}
}

// BeginSync marks background reconciliation as active without blocking readers.
func (store *Store) BeginSync(kinds ...string) bool {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.paused || store.syncing {
		return false
	}
	store.syncing = true
	store.lastError = ""
	for _, kind := range kinds {
		if canonical := canonicalKind(kind); canonical != "" {
			store.expected[canonical] = true
			store.aliases[kindAlias(kind)] = canonical
		}
	}
	store.notifyLocked()
	return true
}

// SetDiscovery refreshes the known context set while preserving last-known facts.
func (store *Store) SetDiscovery(discovery connector.Discovery) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.paused {
		return
	}
	known := make(map[string]connector.Scope, len(discovery.Scopes)+len(discovery.Unreachable))
	for _, scope := range discovery.Scopes {
		known[scope.Name] = cloneScope(scope)
	}
	for _, name := range discovery.Unreachable {
		if _, exists := known[name]; !exists {
			known[name] = connector.Scope{Name: name}
		}
	}
	store.scopes = known
	store.notifyLocked()
}

// Replace reconciles one resource kind while preserving last-known rows for failed scopes.
func (store *Store) Replace(kind string, result fleet.QueryResult) error {
	canonical := canonicalKind(kind)
	if canonical == "" {
		return fmt.Errorf("replace cache records: resource kind is required")
	}
	normalized := make([]Record, 0, len(result.Facts))
	for _, fact := range result.Facts {
		record, err := normalize(fact)
		if err != nil {
			return err
		}
		normalized = append(normalized, record)
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	if store.paused {
		return nil
	}
	if store.records[canonical] == nil {
		store.records[canonical] = make(map[string]Record)
	}
	store.aliases[kindAlias(kind)] = canonical
	for _, record := range normalized {
		store.aliases[kindAlias(record.Kind)] = canonical
	}
	unreachable := stringSet(result.Coverage.Unreachable)
	for key, record := range store.records[canonical] {
		if _, failed := unreachable[record.Cluster]; !failed {
			delete(store.records[canonical], key)
		}
	}
	for _, record := range normalized {
		store.records[canonical][recordKey(record)] = record
	}
	store.coverage[canonical] = cloneCoverage(result.Coverage)
	store.warmed[canonical] = true
	store.updatedAt = store.now().UTC()
	store.notifyLocked()
	return nil
}

// ApplyWatchEvent atomically reconciles one live-reader delta without network access.
func (store *Store) ApplyWatchEvent(event connector.WatchEvent) error {
	canonical := canonicalKind(event.Kind)
	if canonical == "" || strings.TrimSpace(event.Scope) == "" {
		return fmt.Errorf("apply watch event: kind and scope are required")
	}
	if event.Type == connector.WatchError && event.Err == nil {
		return fmt.Errorf("apply watch event: error event has no error")
	}

	var normalized []Record
	switch event.Type {
	case connector.WatchSnapshot:
		normalized = make([]Record, 0, len(event.Facts))
		for _, fact := range event.Facts {
			record, err := normalize(fact)
			if err != nil {
				return err
			}
			if record.Cluster != event.Scope {
				return fmt.Errorf("apply watch event: fact scope %q does not match stream scope %q", record.Cluster, event.Scope)
			}
			normalized = append(normalized, record)
		}
	case connector.WatchUpsert:
		record, err := normalize(event.Fact)
		if err != nil {
			return err
		}
		if record.Cluster != event.Scope {
			return fmt.Errorf("apply watch event: fact scope %q does not match stream scope %q", record.Cluster, event.Scope)
		}
		normalized = []Record{record}
	case connector.WatchDelete:
		if event.Ref.Scope != "" && event.Ref.Scope != event.Scope {
			return fmt.Errorf("apply watch event: delete scope %q does not match stream scope %q", event.Ref.Scope, event.Scope)
		}
	case connector.WatchError:
	default:
		return fmt.Errorf("apply watch event: unsupported type %q", event.Type)
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	if store.paused {
		return nil
	}
	store.aliases[kindAlias(event.Kind)] = canonical
	if store.records[canonical] == nil {
		store.records[canonical] = make(map[string]Record)
	}
	for _, record := range normalized {
		store.aliases[kindAlias(record.Kind)] = canonical
	}

	switch event.Type {
	case connector.WatchSnapshot:
		for key, record := range store.records[canonical] {
			if record.Cluster == event.Scope {
				delete(store.records[canonical], key)
			}
		}
		for _, record := range normalized {
			store.records[canonical][recordKey(record)] = record
		}
		store.markScopeReachableLocked(canonical, event.Scope, event.ObservedAt)
	case connector.WatchUpsert:
		store.records[canonical][recordKey(normalized[0])] = normalized[0]
		store.markScopeReachableLocked(canonical, event.Scope, event.ObservedAt)
	case connector.WatchDelete:
		for key, record := range store.records[canonical] {
			if record.Cluster == event.Scope && record.Namespace == event.Ref.Namespace && record.Name == event.Ref.Name {
				delete(store.records[canonical], key)
			}
		}
		store.markScopeReachableLocked(canonical, event.Scope, event.ObservedAt)
	case connector.WatchError:
		store.markScopeUnreachableLocked(canonical, event.Scope, event.Err)
	}
	store.warmed[canonical] = true
	store.updatedAt = store.now().UTC()
	store.notifyLocked()
	return nil
}

func (store *Store) markScopeReachableLocked(kind, scope string, observedAt time.Time) {
	current := store.scopes[scope]
	current.Name = scope
	current.Reachable = true
	if !observedAt.IsZero() {
		current.ObservedAt = observedAt
	}
	store.scopes[scope] = current
	coverage := store.coverage[kind]
	coverage.Requested = max(coverage.Requested, len(store.scopes))
	coverage.Unreachable = removeString(coverage.Unreachable, scope)
	coverage.Stale = removeString(coverage.Stale, scope)
	coverage.Reachable = max(coverage.Requested-len(coverage.Unreachable), 0)
	store.coverage[kind] = coverage
	if store.allCoverageCompleteLocked() {
		store.lastError = ""
	}
}

func (store *Store) markScopeUnreachableLocked(kind, scope string, watchErr error) {
	coverage := store.coverage[kind]
	coverage.Requested = max(coverage.Requested, len(store.scopes))
	coverage.Unreachable = appendUniqueSorted(coverage.Unreachable, scope)
	coverage.Stale = appendUniqueSorted(coverage.Stale, scope)
	coverage.Reachable = max(coverage.Requested-len(coverage.Unreachable), 0)
	store.coverage[kind] = coverage
	store.lastError = fmt.Sprintf("watch %s in %s: %v", kind, scope, watchErr)
}

func (store *Store) allCoverageCompleteLocked() bool {
	for _, coverage := range store.coverage {
		if len(coverage.Unreachable) > 0 {
			return false
		}
	}
	return true
}

// EndSync marks reconciliation complete and retains any prior data on failure.
func (store *Store) EndSync(err error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.syncing = false
	if err != nil {
		store.lastError = err.Error()
	} else {
		store.lastError = ""
	}
	store.notifyLocked()
}

// SetPaused freezes or resumes background mutations while keeping snapshots available.
func (store *Store) SetPaused(paused bool) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.paused == paused {
		return
	}
	store.paused = paused
	store.notifyLocked()
}

// Paused reports whether background reconciliation is frozen.
func (store *Store) Paused() bool {
	store.mu.RLock()
	defer store.mu.RUnlock()
	return store.paused
}

// Query returns a deterministic immutable answer without connector or network access.
func (store *Store) Query(query Query) Snapshot {
	store.mu.RLock()
	defer store.mu.RUnlock()
	now := store.now().UTC()
	records := make([]Record, 0)
	selectedKind := store.resolveKindLocked(query.Kind)
	matchQuery := query
	if selectedKind != "" {
		matchQuery.Kind = ""
	}
	for kind, byKey := range store.records {
		if selectedKind != "" && selectedKind != kind {
			continue
		}
		for _, cached := range byKey {
			record := cloneRecord(cached, !query.MetadataOnly)
			age := now.Sub(record.ObservedAt)
			if age > store.freshFor {
				record.Stale = true
				record.StaleFor = age
				record.Fact.Stale = true
				record.Fact.StaleFor = age.Round(time.Second).String()
			}
			if matchQuery.matches(record) {
				records = append(records, record)
			}
		}
	}
	sort.Slice(records, func(left, right int) bool {
		return recordKey(records[left]) < recordKey(records[right])
	})
	if query.Limit > 0 && len(records) > query.Limit {
		records = records[:query.Limit]
	}
	coverage := store.coverageLocked(query, records, now)
	unreachable := stringSet(coverage.Unreachable)
	for index := range records {
		if _, failed := unreachable[records[index].Cluster]; failed {
			records[index].Stale = true
			records[index].Fact.Stale = true
			if records[index].Fact.StaleFor == "" {
				records[index].Fact.StaleFor = "unreachable"
			}
		}
	}
	pending := selectedKind != "" && !store.warmed[selectedKind]
	return Snapshot{
		Version:   store.version,
		State:     store.stateLocked(coverage, store.recordCountLocked(), pending),
		Syncing:   store.syncing,
		Paused:    store.paused,
		Records:   records,
		Coverage:  coverage,
		UpdatedAt: store.updatedAt,
		LastError: store.lastError,
		Scopes:    store.scopesLocked(query.Scopes),
	}
}

// WaitForChange blocks until the store advances beyond a known version or the context ends.
func (store *Store) WaitForChange(ctx context.Context, after uint64) (uint64, error) {
	for {
		store.mu.RLock()
		if store.version > after {
			version := store.version
			store.mu.RUnlock()
			return version, nil
		}
		changed := store.changed
		store.mu.RUnlock()
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-changed:
		}
	}
}

func (store *Store) coverageLocked(query Query, records []Record, now time.Time) fleet.Coverage {
	targets := store.targetScopesLocked(query.Scopes)
	unreachable := make(map[string]struct{})
	stale := make(map[string]struct{})
	kind := store.resolveKindLocked(query.Kind)
	if kind != "" {
		if !store.warmed[kind] {
			return fleet.Coverage{Requested: len(targets)}
		}
		for _, name := range store.coverage[kind].Unreachable {
			unreachable[name] = struct{}{}
		}
		for _, name := range store.coverage[kind].Stale {
			stale[name] = struct{}{}
		}
	} else {
		for _, coverage := range store.coverage {
			for _, name := range coverage.Unreachable {
				unreachable[name] = struct{}{}
			}
			for _, name := range coverage.Stale {
				stale[name] = struct{}{}
			}
		}
	}
	for _, name := range targets {
		scope, exists := store.scopes[name]
		if !exists || !scope.Reachable {
			unreachable[name] = struct{}{}
		}
		if !scope.ObservedAt.IsZero() && now.Sub(scope.ObservedAt) > store.freshFor {
			stale[name] = struct{}{}
		}
	}
	for _, record := range records {
		if record.Stale {
			stale[record.Cluster] = struct{}{}
		}
	}
	coverage := fleet.Coverage{Requested: len(targets)}
	for _, name := range targets {
		if _, failed := unreachable[name]; failed {
			coverage.Unreachable = append(coverage.Unreachable, name)
			continue
		}
		coverage.Reachable++
		if _, aged := stale[name]; aged {
			coverage.Stale = append(coverage.Stale, name)
		}
	}
	return coverage
}

func (store *Store) resolveKindLocked(kind string) string {
	canonical := canonicalKind(kind)
	if canonical == "" {
		return ""
	}
	if resolved := store.aliases[kindAlias(kind)]; resolved != "" {
		return resolved
	}
	return canonical
}

func kindAlias(kind string) string {
	return strings.ToLower(canonicalKind(kind))
}

func (store *Store) targetScopesLocked(patterns []string) []string {
	set := make(map[string]struct{})
	if len(patterns) == 0 {
		for name := range store.scopes {
			set[name] = struct{}{}
		}
	} else {
		for _, pattern := range patterns {
			matched := false
			for name := range store.scopes {
				if matchesGlob(name, pattern) {
					set[name] = struct{}{}
					matched = true
				}
			}
			if !matched && !strings.ContainsAny(pattern, "*?[") {
				set[pattern] = struct{}{}
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

func (store *Store) scopesLocked(patterns []string) []connector.Scope {
	names := store.targetScopesLocked(patterns)
	result := make([]connector.Scope, 0, len(names))
	for _, name := range names {
		scope, exists := store.scopes[name]
		if !exists {
			scope = connector.Scope{Name: name}
		}
		result = append(result, cloneScope(scope))
	}
	return result
}

func (store *Store) stateLocked(coverage fleet.Coverage, recordCount int, pending bool) State {
	switch {
	case store.paused:
		return StatePaused
	case pending && store.syncing:
		return StateWarming
	case pending && store.lastError == "":
		return StateCold
	case recordCount == 0 && store.syncing:
		return StateWarming
	case recordCount == 0 && len(store.warmed) == 0:
		return StateCold
	case coverage.Reachable == 0 && (recordCount > 0 || store.lastError != ""):
		return StateOffline
	case len(coverage.Unreachable) > 0 || len(coverage.Stale) > 0 || store.lastError != "":
		return StateDegraded
	case store.syncing:
		return StateWarming
	default:
		return StateWarm
	}
}

func (store *Store) recordCountLocked() int {
	count := 0
	for _, records := range store.records {
		count += len(records)
	}
	return count
}

func (store *Store) notifyLocked() {
	store.version++
	close(store.changed)
	store.changed = make(chan struct{})
}

func recordKey(record Record) string {
	return strings.Join([]string{record.Kind, record.Cluster, record.Namespace, record.Name}, "\x00")
}

func stringSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func removeString(values []string, unwanted string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value != unwanted {
			result = append(result, value)
		}
	}
	return result
}

func appendUniqueSorted(values []string, added string) []string {
	for _, value := range values {
		if value == added {
			return values
		}
	}
	result := append(append([]string(nil), values...), added)
	sort.Strings(result)
	return result
}

func cloneRecord(record Record, includeEvidence bool) Record {
	if includeEvidence {
		record.Fact = cloneFact(record.Fact)
	} else {
		record.Fact = fleet.Fact{}
	}
	record.Images = append([]string(nil), record.Images...)
	record.Labels = cloneMap(record.Labels)
	return record
}

func cloneCoverage(coverage fleet.Coverage) fleet.Coverage {
	coverage.Unreachable = append([]string(nil), coverage.Unreachable...)
	coverage.Stale = append([]string(nil), coverage.Stale...)
	return coverage
}

func cloneScope(scope connector.Scope) connector.Scope {
	scope.Kinds = append([]string(nil), scope.Kinds...)
	return scope
}
