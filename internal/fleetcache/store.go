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
	"github.com/ArdurAI/sith/internal/tenancy"
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
	Version     uint64                 `json:"version"`
	State       State                  `json:"state"`
	Syncing     bool                   `json:"syncing"`
	Paused      bool                   `json:"paused"`
	Records     []Record               `json:"records"`
	Coverage    fleet.Coverage         `json:"coverage"`
	UpdatedAt   time.Time              `json:"updated_at,omitempty"`
	LastError   string                 `json:"last_error,omitempty"`
	Scopes      []connector.Scope      `json:"scopes"`
	Diagnostics []connector.Diagnostic `json:"diagnostics,omitempty"`
}

type scopeMetadataKey struct {
	workspace string
	scope     string
}

type workspaceKindKey struct {
	workspace string
	kind      string
}

// Store owns normalized last-known fleet state and never performs network I/O.
type Store struct {
	mu sync.RWMutex

	records         map[string]map[string]Record
	coverage        map[workspaceKindKey]fleet.Coverage
	aliases         map[workspaceKindKey]string
	scopes          map[scopeMetadataKey]connector.Scope
	diagnostics     map[string][]connector.Diagnostic
	scopeWorkspaces map[string]map[string]bool
	warmed          map[workspaceKindKey]bool
	expected        map[workspaceKindKey]bool
	syncing         map[string]bool
	paused          map[string]bool
	syncErrors      map[string]string
	watchErrors     map[workspaceKindKey]string
	updatedAt       map[string]time.Time
	versions        map[string]uint64
	changed         map[string]chan struct{}
	now             func() time.Time
	freshFor        time.Duration
}

// New creates an empty cold store.
func New() *Store {
	return newStore(time.Now, defaultFreshFor)
}

func newStore(now func() time.Time, freshFor time.Duration) *Store {
	return &Store{
		records:         make(map[string]map[string]Record),
		coverage:        make(map[workspaceKindKey]fleet.Coverage),
		aliases:         make(map[workspaceKindKey]string),
		scopes:          make(map[scopeMetadataKey]connector.Scope),
		diagnostics:     make(map[string][]connector.Diagnostic),
		scopeWorkspaces: make(map[string]map[string]bool),
		warmed:          make(map[workspaceKindKey]bool),
		expected:        make(map[workspaceKindKey]bool),
		syncing:         make(map[string]bool),
		paused:          make(map[string]bool),
		syncErrors:      make(map[string]string),
		watchErrors:     make(map[workspaceKindKey]string),
		updatedAt:       make(map[string]time.Time),
		versions:        make(map[string]uint64),
		changed:         make(map[string]chan struct{}),
		now:             now,
		freshFor:        freshFor,
	}
}

// BeginSync marks one workspace's background reconciliation as active without blocking readers.
func (store *Store) BeginSync(workspace string, kinds ...string) bool {
	if tenancy.ValidateWorkspaceID(tenancy.WorkspaceID(workspace)) != nil {
		return false
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.paused[workspace] || store.syncing[workspace] {
		return false
	}
	store.syncing[workspace] = true
	delete(store.syncErrors, workspace)
	for _, kind := range kinds {
		if canonical := canonicalKind(kind); canonical != "" {
			store.expected[workspaceKindKey{workspace: workspace, kind: canonical}] = true
			store.aliases[workspaceKindKey{workspace: workspace, kind: kindAlias(kind)}] = canonical
		}
	}
	store.notifyLocked(workspace)
	return true
}

// SetDiscovery refreshes one workspace's known context set while preserving last-known facts.
func (store *Store) SetDiscovery(workspace string, discovery connector.Discovery) error {
	if err := tenancy.ValidateWorkspaceID(tenancy.WorkspaceID(workspace)); err != nil {
		return fmt.Errorf("set cache discovery: %w", err)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.paused[workspace] {
		return nil
	}
	for name, memberships := range store.scopeWorkspaces {
		if memberships[workspace] {
			delete(store.scopes, scopeMetadataKey{workspace: workspace, scope: name})
		}
		delete(memberships, workspace)
		if len(memberships) == 0 {
			delete(store.scopeWorkspaces, name)
		}
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
	for name, scope := range known {
		store.scopes[scopeMetadataKey{workspace: workspace, scope: name}] = scope
		store.markScopeWorkspaceLocked(workspace, name)
	}
	store.diagnostics[workspace] = cloneDiagnostics(discovery.Diagnostics)
	store.notifyLocked(workspace)
	return nil
}

// Replace reconciles one workspace's resource kind while preserving last-known rows for failed scopes.
func (store *Store) Replace(workspace, kind string, result fleet.QueryResult) error {
	if err := tenancy.ValidateWorkspaceID(tenancy.WorkspaceID(workspace)); err != nil {
		return fmt.Errorf("replace cache records: %w", err)
	}
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
		if record.Workspace != workspace {
			return fmt.Errorf("replace cache records: fact workspace %q does not match mutation workspace %q", record.Workspace, workspace)
		}
		normalized = append(normalized, record)
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	if store.paused[workspace] {
		return nil
	}
	if store.records[canonical] == nil {
		store.records[canonical] = make(map[string]Record)
	}
	store.aliases[workspaceKindKey{workspace: workspace, kind: kindAlias(kind)}] = canonical
	for _, record := range normalized {
		store.aliases[workspaceKindKey{workspace: workspace, kind: kindAlias(record.Kind)}] = canonical
		store.markScopeWorkspaceLocked(record.Workspace, record.Cluster)
	}
	preserve := stringSet(result.Coverage.Unreachable)
	for _, scope := range result.Coverage.Truncated {
		preserve[scope] = struct{}{}
	}
	for key, record := range store.records[canonical] {
		if record.Workspace != workspace {
			continue
		}
		if _, incomplete := preserve[record.Cluster]; !incomplete {
			delete(store.records[canonical], key)
		}
	}
	for _, record := range normalized {
		store.records[canonical][recordKey(record)] = record
	}
	key := workspaceKindKey{workspace: workspace, kind: canonical}
	store.coverage[key] = cloneCoverage(result.Coverage)
	if len(result.Coverage.Unreachable) == 0 {
		delete(store.watchErrors, key)
	}
	store.warmed[key] = true
	store.updatedAt[workspace] = store.now().UTC()
	store.notifyLocked(workspace)
	return nil
}

// ApplyWatchEvent atomically reconciles one workspace-bound live-reader delta without network access.
func (store *Store) ApplyWatchEvent(event connector.WatchEvent) error {
	workspace := event.Workspace
	if err := tenancy.ValidateWorkspaceID(tenancy.WorkspaceID(workspace)); err != nil {
		return fmt.Errorf("apply watch event: %w", err)
	}
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
			if record.Workspace != workspace {
				return fmt.Errorf("apply watch event: fact workspace %q does not match stream workspace %q", record.Workspace, workspace)
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
		if record.Workspace != workspace {
			return fmt.Errorf("apply watch event: fact workspace %q does not match stream workspace %q", record.Workspace, workspace)
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
	if store.paused[workspace] {
		return nil
	}
	if !store.scopeWorkspaces[event.Scope][workspace] {
		return fmt.Errorf("apply watch event: scope %q is not discovered in workspace %q", event.Scope, workspace)
	}
	store.aliases[workspaceKindKey{workspace: workspace, kind: kindAlias(event.Kind)}] = canonical
	if store.records[canonical] == nil {
		store.records[canonical] = make(map[string]Record)
	}
	for _, record := range normalized {
		store.aliases[workspaceKindKey{workspace: workspace, kind: kindAlias(record.Kind)}] = canonical
		store.markScopeWorkspaceLocked(record.Workspace, record.Cluster)
	}

	switch event.Type {
	case connector.WatchSnapshot:
		for key, record := range store.records[canonical] {
			if record.Workspace == workspace && record.Cluster == event.Scope {
				delete(store.records[canonical], key)
			}
		}
		for _, record := range normalized {
			store.records[canonical][recordKey(record)] = record
		}
		store.markScopeReachableLocked(workspace, canonical, event.Scope, event.ObservedAt)
		coverageKey := workspaceKindKey{workspace: workspace, kind: canonical}
		coverage := store.coverage[coverageKey]
		coverage.Truncated = removeString(coverage.Truncated, event.Scope)
		store.coverage[coverageKey] = coverage
	case connector.WatchUpsert:
		store.records[canonical][recordKey(normalized[0])] = normalized[0]
		store.markScopeReachableLocked(workspace, canonical, event.Scope, event.ObservedAt)
	case connector.WatchDelete:
		for key, record := range store.records[canonical] {
			if record.Workspace == workspace && record.Cluster == event.Scope && record.Namespace == event.Ref.Namespace && record.Name == event.Ref.Name {
				delete(store.records[canonical], key)
			}
		}
		store.markScopeReachableLocked(workspace, canonical, event.Scope, event.ObservedAt)
	case connector.WatchError:
		store.markScopeUnreachableLocked(workspace, canonical, event.Scope, event.Err)
	}
	store.warmed[workspaceKindKey{workspace: workspace, kind: canonical}] = true
	store.updatedAt[workspace] = store.now().UTC()
	store.notifyLocked(workspace)
	return nil
}

func (store *Store) markScopeReachableLocked(workspace, kind, scope string, observedAt time.Time) {
	scopeKey := scopeMetadataKey{workspace: workspace, scope: scope}
	current := store.scopes[scopeKey]
	current.Name = scope
	current.Reachable = true
	if !observedAt.IsZero() {
		current.ObservedAt = observedAt
	}
	store.scopes[scopeKey] = current
	key := workspaceKindKey{workspace: workspace, kind: kind}
	coverage := store.coverage[key]
	coverage.Requested = max(coverage.Requested, store.workspaceScopeCountLocked(workspace))
	coverage.Unreachable = removeString(coverage.Unreachable, scope)
	coverage.Stale = removeString(coverage.Stale, scope)
	coverage.Reachable = max(coverage.Requested-len(coverage.Unreachable), 0)
	store.coverage[key] = coverage
	if len(coverage.Unreachable) == 0 {
		delete(store.watchErrors, key)
	}
}

func (store *Store) markScopeUnreachableLocked(workspace, kind, scope string, watchErr error) {
	key := workspaceKindKey{workspace: workspace, kind: kind}
	coverage := store.coverage[key]
	coverage.Requested = max(coverage.Requested, store.workspaceScopeCountLocked(workspace))
	coverage.Unreachable = appendUniqueSorted(coverage.Unreachable, scope)
	coverage.Stale = appendUniqueSorted(coverage.Stale, scope)
	coverage.Reachable = max(coverage.Requested-len(coverage.Unreachable), 0)
	store.coverage[key] = coverage
	store.watchErrors[key] = fmt.Sprintf("watch %s in %s: %v", kind, scope, watchErr)
}

// EndSync marks one workspace's reconciliation complete and retains prior data on failure.
func (store *Store) EndSync(workspace string, err error) error {
	if validationErr := tenancy.ValidateWorkspaceID(tenancy.WorkspaceID(workspace)); validationErr != nil {
		return fmt.Errorf("end cache sync: %w", validationErr)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	store.syncing[workspace] = false
	if err != nil {
		store.syncErrors[workspace] = err.Error()
	} else {
		delete(store.syncErrors, workspace)
	}
	store.notifyLocked(workspace)
	return nil
}

// SetPaused freezes or resumes one workspace's background mutations while keeping snapshots available.
func (store *Store) SetPaused(workspace string, paused bool) error {
	if err := tenancy.ValidateWorkspaceID(tenancy.WorkspaceID(workspace)); err != nil {
		return fmt.Errorf("set cache pause: %w", err)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.paused[workspace] == paused {
		return nil
	}
	store.paused[workspace] = paused
	store.notifyLocked(workspace)
	return nil
}

// Paused reports whether one workspace's background reconciliation is frozen.
func (store *Store) Paused(workspace string) bool {
	if tenancy.ValidateWorkspaceID(tenancy.WorkspaceID(workspace)) != nil {
		return false
	}
	store.mu.RLock()
	defer store.mu.RUnlock()
	return store.paused[workspace]
}

// Query returns a deterministic workspace-scoped answer without connector or network access.
func (store *Store) Query(workspace string, query Query) Snapshot {
	store.mu.RLock()
	defer store.mu.RUnlock()
	if strings.TrimSpace(workspace) == "" {
		return Snapshot{State: StateCold, Records: []Record{}, Scopes: []connector.Scope{}, Diagnostics: []connector.Diagnostic{}}
	}
	now := store.now().UTC()
	records := make([]Record, 0)
	selectedKind := store.resolveKindLocked(workspace, query.Kind)
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
			if matchQuery.matches(workspace, record) {
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
	coverage := store.coverageLocked(workspace, query, records, now)
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
	pending := selectedKind != "" && !store.warmed[workspaceKindKey{workspace: workspace, kind: selectedKind}]
	lastError := store.lastErrorLocked(workspace, selectedKind)
	return Snapshot{
		Version:     store.versions[workspace],
		State:       store.stateLocked(workspace, coverage, store.recordCountLocked(workspace), pending, lastError),
		Syncing:     store.syncing[workspace],
		Paused:      store.paused[workspace],
		Records:     records,
		Coverage:    coverage,
		UpdatedAt:   store.updatedAt[workspace],
		LastError:   lastError,
		Scopes:      store.scopesLocked(workspace, query.Scopes),
		Diagnostics: cloneDiagnostics(store.diagnostics[workspace]),
	}
}

// WaitForChange blocks until one workspace advances beyond a known version or the context ends.
func (store *Store) WaitForChange(ctx context.Context, workspace string, after uint64) (uint64, error) {
	if err := tenancy.ValidateWorkspaceID(tenancy.WorkspaceID(workspace)); err != nil {
		return 0, fmt.Errorf("wait for cache change: %w", err)
	}
	for {
		store.mu.Lock()
		if store.versions[workspace] > after {
			version := store.versions[workspace]
			store.mu.Unlock()
			return version, nil
		}
		if store.changed[workspace] == nil {
			store.changed[workspace] = make(chan struct{})
		}
		changed := store.changed[workspace]
		store.mu.Unlock()
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-changed:
		}
	}
}

func (store *Store) coverageLocked(workspace string, query Query, records []Record, now time.Time) fleet.Coverage {
	targets := store.targetScopesLocked(workspace, query.Scopes)
	unreachable := make(map[string]struct{})
	stale := make(map[string]struct{})
	truncated := make(map[string]struct{})
	kind := store.resolveKindLocked(workspace, query.Kind)
	if kind != "" {
		key := workspaceKindKey{workspace: workspace, kind: kind}
		if !store.warmed[key] {
			return fleet.Coverage{Requested: len(targets)}
		}
		for _, name := range store.coverage[key].Unreachable {
			unreachable[name] = struct{}{}
		}
		for _, name := range store.coverage[key].Stale {
			stale[name] = struct{}{}
		}
		for _, name := range store.coverage[key].Truncated {
			truncated[name] = struct{}{}
		}
	} else {
		for key, coverage := range store.coverage {
			if key.workspace != workspace {
				continue
			}
			for _, name := range coverage.Unreachable {
				unreachable[name] = struct{}{}
			}
			for _, name := range coverage.Stale {
				stale[name] = struct{}{}
			}
			for _, name := range coverage.Truncated {
				truncated[name] = struct{}{}
			}
		}
	}
	for _, name := range targets {
		scope, exists := store.scopes[scopeMetadataKey{workspace: workspace, scope: name}]
		if !exists || !store.scopeWorkspaces[name][workspace] || !scope.Reachable {
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
		if _, partial := truncated[name]; partial {
			coverage.Truncated = append(coverage.Truncated, name)
		}
	}
	return coverage
}

func (store *Store) resolveKindLocked(workspace, kind string) string {
	canonical := canonicalKind(kind)
	if canonical == "" {
		return ""
	}
	if resolved := store.aliases[workspaceKindKey{workspace: workspace, kind: kindAlias(kind)}]; resolved != "" {
		return resolved
	}
	return canonical
}

func kindAlias(kind string) string {
	return strings.ToLower(canonicalKind(kind))
}

func (store *Store) targetScopesLocked(workspace string, patterns []string) []string {
	set := make(map[string]struct{})
	if len(patterns) == 0 {
		for name, memberships := range store.scopeWorkspaces {
			if memberships[workspace] {
				set[name] = struct{}{}
			}
		}
	} else {
		for _, pattern := range patterns {
			matched := false
			for name, memberships := range store.scopeWorkspaces {
				if memberships[workspace] && matchesGlob(name, pattern) {
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

func (store *Store) scopesLocked(workspace string, patterns []string) []connector.Scope {
	names := store.targetScopesLocked(workspace, patterns)
	result := make([]connector.Scope, 0, len(names))
	for _, name := range names {
		scope, exists := store.scopes[scopeMetadataKey{workspace: workspace, scope: name}]
		if !exists || !store.scopeWorkspaces[name][workspace] {
			scope = connector.Scope{Name: name}
		}
		result = append(result, cloneScope(scope))
	}
	return result
}

func (store *Store) stateLocked(workspace string, coverage fleet.Coverage, recordCount int, pending bool, lastError string) State {
	syncing := store.syncing[workspace]
	switch {
	case store.paused[workspace]:
		return StatePaused
	case pending && syncing:
		return StateWarming
	case pending && lastError == "":
		return StateCold
	case recordCount == 0 && syncing:
		return StateWarming
	case recordCount == 0 && !store.workspaceWarmedLocked(workspace):
		return StateCold
	case coverage.Reachable == 0 && (recordCount > 0 || lastError != ""):
		return StateOffline
	case len(coverage.Unreachable) > 0 || len(coverage.Stale) > 0 || len(coverage.Truncated) > 0 || lastError != "":
		return StateDegraded
	case syncing:
		return StateWarming
	default:
		return StateWarm
	}
}

func (store *Store) lastErrorLocked(workspace, kind string) string {
	result := make([]string, 0, 1)
	if syncError := store.syncErrors[workspace]; syncError != "" {
		result = append(result, syncError)
	}
	if kind != "" {
		if watchError := store.watchErrors[workspaceKindKey{workspace: workspace, kind: kind}]; watchError != "" {
			result = append(result, watchError)
		}
		return strings.Join(result, "; ")
	}
	keys := make([]workspaceKindKey, 0)
	for key, watchError := range store.watchErrors {
		if key.workspace == workspace && watchError != "" {
			keys = append(keys, key)
		}
	}
	sort.Slice(keys, func(left, right int) bool { return keys[left].kind < keys[right].kind })
	for _, key := range keys {
		result = append(result, store.watchErrors[key])
	}
	return strings.Join(result, "; ")
}

func (store *Store) workspaceWarmedLocked(workspace string) bool {
	for key, warmed := range store.warmed {
		if key.workspace == workspace && warmed {
			return true
		}
	}
	return false
}

func (store *Store) workspaceScopeCountLocked(workspace string) int {
	count := 0
	for _, memberships := range store.scopeWorkspaces {
		if memberships[workspace] {
			count++
		}
	}
	return count
}

func (store *Store) recordCountLocked(workspace string) int {
	count := 0
	for _, records := range store.records {
		for _, record := range records {
			if record.Workspace == workspace {
				count++
			}
		}
	}
	return count
}

func (store *Store) markScopeWorkspaceLocked(workspace, scope string) {
	if strings.TrimSpace(workspace) == "" || strings.TrimSpace(scope) == "" {
		return
	}
	if store.scopeWorkspaces[scope] == nil {
		store.scopeWorkspaces[scope] = make(map[string]bool)
	}
	store.scopeWorkspaces[scope][workspace] = true
	key := scopeMetadataKey{workspace: workspace, scope: scope}
	if _, exists := store.scopes[key]; !exists {
		store.scopes[key] = connector.Scope{Name: scope}
	}
}

func (store *Store) notifyLocked(workspace string) {
	store.versions[workspace]++
	if changed := store.changed[workspace]; changed != nil {
		close(changed)
	}
	store.changed[workspace] = make(chan struct{})
}

func recordKey(record Record) string {
	return strings.Join([]string{record.Workspace, record.Kind, record.Cluster, record.Namespace, record.Name}, "\x00")
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
	record.ImageDigests = append([]string(nil), record.ImageDigests...)
	record.ImageRepoDigests = append([]string(nil), record.ImageRepoDigests...)
	record.CVEs = append([]string(nil), record.CVEs...)
	record.Conditions = append([]string(nil), record.Conditions...)
	record.Reasons = append([]string(nil), record.Reasons...)
	record.Display = append([]fleet.DisplayField(nil), record.Display...)
	record.Labels = cloneMap(record.Labels)
	return record
}

func cloneCoverage(coverage fleet.Coverage) fleet.Coverage {
	coverage.Unreachable = append([]string(nil), coverage.Unreachable...)
	coverage.Stale = append([]string(nil), coverage.Stale...)
	coverage.Truncated = append([]string(nil), coverage.Truncated...)
	return coverage
}

func cloneScope(scope connector.Scope) connector.Scope {
	scope.Kinds = append([]string(nil), scope.Kinds...)
	return scope
}

func cloneDiagnostics(values []connector.Diagnostic) []connector.Diagnostic {
	return append([]connector.Diagnostic(nil), values...)
}
