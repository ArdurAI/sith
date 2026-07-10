// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/ArdurAI/sith/internal/connector"
	"github.com/ArdurAI/sith/internal/fleet"
	"github.com/ArdurAI/sith/internal/fleetcache"
	"github.com/ArdurAI/sith/internal/fleetrender"
)

func TestNewModelValidatesDependencies(t *testing.T) {
	t.Parallel()
	syncer := &countingSyncer{}
	store := fleetcache.New()
	if _, err := NewModel(nil, store, syncer); err == nil { //nolint:staticcheck // nil is the invalid input under test.
		t.Fatal("NewModel(nil context) error = nil")
	}
	if _, err := NewModel(context.Background(), nil, syncer); err == nil {
		t.Fatal("NewModel(nil store) error = nil")
	}
	if _, err := NewModel(context.Background(), store, nil); err == nil {
		t.Fatal("NewModel(nil syncer) error = nil")
	}
}

func TestColdFirstPaintAndInteractionsDoNotCallSyncer(t *testing.T) {
	t.Parallel()
	syncer := &countingSyncer{}
	model, err := NewModel(context.Background(), fleetcache.New(), syncer)
	if err != nil {
		t.Fatalf("NewModel() error = %v", err)
	}
	started := time.Now()
	view := model.View()
	if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
		t.Fatalf("cold View() took %s, want <250ms", elapsed)
	}
	if !strings.Contains(view.Content, "warming contexts") || !view.AltScreen {
		t.Fatalf("cold view = %q", view.Content)
	}

	_, _ = model.Update(keyMessage("/"))
	_, _ = model.Update(keyMessage("payments"))
	_, _ = model.Update(specialKey(tea.KeyEnter))
	_, _ = model.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	_ = model.View()
	if syncer.calls.Load() != 0 {
		t.Fatalf("sync calls = %d, want no I/O on interaction path", syncer.calls.Load())
	}
}

func TestModelNavigationFilterPauseAndCoverage(t *testing.T) {
	t.Parallel()
	store := populatedStore(t, 2)
	syncer := &countingSyncer{}
	model, err := NewModel(context.Background(), store, syncer)
	if err != nil {
		t.Fatalf("NewModel() error = %v", err)
	}
	model.now = func() time.Time { return time.Date(2026, time.July, 10, 21, 0, 0, 0, time.UTC) }

	_, _ = model.Update(keyMessage(":"))
	_, _ = model.Update(keyMessage("deploy"))
	_, _ = model.Update(specialKey(tea.KeyEnter))
	if model.currentLens() != "Deployment" {
		t.Fatalf("lens = %q", model.currentLens())
	}
	_, _ = model.Update(keyMessage("2"))
	if !slices.Equal(model.scopes, []string{"beta"}) {
		t.Fatalf("scopes = %v, want beta", model.scopes)
	}
	_, _ = model.Update(keyMessage("c"))
	if !strings.Contains(model.View().Content, "COVERAGE") {
		t.Fatal("coverage popover missing")
	}

	_, _ = model.Update(keyMessage(":"))
	_, _ = model.Update(keyMessage("pause"))
	_, _ = model.Update(specialKey(tea.KeyEnter))
	if !store.Paused() || !strings.Contains(strings.ToLower(model.View().Content), "paused") {
		t.Fatalf("paused state/view = %v/%q", store.Paused(), model.View().Content)
	}
	_, command := model.Update(keyMessage("ctrl+r"))
	if command == nil {
		t.Fatal("ctrl-r command = nil")
	}
	message := command()
	if _, ok := message.(syncDoneMsg); !ok || syncer.calls.Load() != 1 {
		t.Fatalf("refresh message/calls = %#v/%d", message, syncer.calls.Load())
	}
}

func TestFuzzyFleetSearchSpansLensesAndCanBeCleared(t *testing.T) {
	t.Parallel()
	model, err := NewModel(context.Background(), populatedStore(t, 2), &countingSyncer{})
	if err != nil {
		t.Fatalf("NewModel() error = %v", err)
	}
	_, _ = model.Update(keyMessage("ctrl+k"))
	_, _ = model.Update(keyMessage("status:Degraded"))
	if snapshot := model.snapshot(); len(snapshot.Records) != 1 || snapshot.Records[0].Kind != "Deployment" {
		t.Fatalf("live search snapshot = %#v", snapshot)
	}
	_, _ = model.Update(specialKey(tea.KeyEnter))
	if !strings.Contains(model.View().Content, "KIND") || !strings.Contains(model.View().Content, "payments") {
		t.Fatalf("search view = %q", model.View().Content)
	}
	_, _ = model.Update(specialKey(tea.KeyEscape))
	if model.filter != "" || model.filterAll {
		t.Fatalf("filter after esc = %q/%t", model.filter, model.filterAll)
	}
}

func TestGenericLensHydratesOnDemand(t *testing.T) {
	t.Parallel()
	syncer := &countingSyncer{}
	model, err := NewModel(context.Background(), populatedStore(t, 2), syncer)
	if err != nil {
		t.Fatalf("NewModel() error = %v", err)
	}
	_, _ = model.Update(keyMessage(":"))
	_, _ = model.Update(keyMessage("configmaps"))
	_, command := model.Update(specialKey(tea.KeyEnter))
	if model.currentLens() != "Configmaps" || command == nil {
		t.Fatalf("generic lens/command = %q/%v", model.currentLens(), command)
	}
	message := command()
	if _, ok := message.(syncDoneMsg); !ok || syncer.kindCalls.Load() != 1 {
		t.Fatalf("generic sync message/calls = %#v/%d", message, syncer.kindCalls.Load())
	}
	kinds, _ := syncer.lastKinds.Load().([]string)
	if !slices.Equal(kinds, []string{"Configmaps"}) {
		t.Fatalf("generic sync kinds = %v", kinds)
	}
}

func TestWarmViewP95UnderOneHundredMilliseconds(t *testing.T) {
	if testing.Short() {
		t.Skip("performance acceptance test")
	}
	store := populatedStore(t, 3000)
	model, err := NewModel(context.Background(), store, &countingSyncer{})
	if err != nil {
		t.Fatalf("NewModel() error = %v", err)
	}
	model.height = 30
	durations := make([]time.Duration, 40)
	for index := range durations {
		started := time.Now()
		_ = model.View()
		durations[index] = time.Since(started)
	}
	slices.Sort(durations)
	p95 := durations[37]
	budget := 100 * time.Millisecond
	if raceDetectorEnabled {
		budget = 250 * time.Millisecond
	}
	if p95 >= budget {
		t.Fatalf("warm View() p95 = %s, want <%s (samples=%v)", p95, budget, durations)
	}
}

func TestTUIAndCLIBuildIdenticalSharedTable(t *testing.T) {
	t.Parallel()
	store := populatedStore(t, 2)
	model, err := NewModel(context.Background(), store, &countingSyncer{})
	if err != nil {
		t.Fatalf("NewModel() error = %v", err)
	}
	snapshot := model.snapshot()
	fromTUI := fleetrender.Build(snapshot, fleetrender.Options{Lens: model.currentLens(), MaxRows: model.maxRows()})
	fromCLI := fleetrender.Build(store.Query(fleetcache.Query{Kind: "Pod"}), fleetrender.Options{Lens: "Pod", MaxRows: model.maxRows()})
	if !slices.EqualFunc(fromTUI.Rows, fromCLI.Rows, func(left, right []string) bool { return slices.Equal(left, right) }) ||
		!slices.Equal(fromTUI.Columns, fromCLI.Columns) {
		t.Fatalf("TUI table = %#v, CLI table = %#v", fromTUI, fromCLI)
	}
}

func TestUpdateHandlesBackgroundMessagesAndCommands(t *testing.T) {
	t.Parallel()
	store := populatedStore(t, 4)
	syncer := &countingSyncer{}
	model, err := NewModel(context.Background(), store, syncer)
	if err != nil {
		t.Fatalf("NewModel() error = %v", err)
	}
	if model.Init() == nil {
		t.Fatal("Init() command = nil")
	}
	_, command := model.Update(cacheChangedMsg{version: store.Query(fleetcache.Query{}).Version})
	if command == nil {
		t.Fatal("cache change did not resubscribe")
	}
	_, _ = model.Update(syncDoneMsg{err: errors.New("sync broke")})
	if !strings.Contains(model.View().Content, "sync broke") {
		t.Fatal("sync error missing from view")
	}
	_, _ = model.Update(syncDoneMsg{})
	if model.lastError != "" {
		t.Fatalf("last error = %q, want cleared", model.lastError)
	}
	_, command = model.Update(refreshTickMsg(time.Now()))
	if command == nil {
		t.Fatal("refresh tick command = nil")
	}

	for _, commandText := range []string{"ctx", "unknown", "ctx alpha", "refresh", "resume"} {
		_, _ = model.Update(keyMessage(":"))
		_, _ = model.Update(keyMessage(commandText))
		_, _ = model.Update(specialKey(tea.KeyEnter))
	}
	if !slices.Equal(model.scopes, []string{"alpha"}) {
		t.Fatalf("scopes = %v", model.scopes)
	}
	_, _ = model.Update(keyMessage("down"))
	_, _ = model.Update(keyMessage("up"))
	_, _ = model.Update(keyMessage("0"))
	if len(model.scopes) != 0 {
		t.Fatalf("scopes = %v, want all", model.scopes)
	}
	_, _ = model.Update(keyMessage(":"))
	_, _ = model.Update(keyMessage("quit"))
	_, command = model.Update(specialKey(tea.KeyEnter))
	if command == nil {
		t.Fatal(":quit command = nil")
	}
}

func TestRenderHelpersHandleUnicodeAndBounds(t *testing.T) {
	t.Parallel()
	if got := trimLastRune("fleet✓"); got != "fleet" {
		t.Fatalf("trimLastRune() = %q", got)
	}
	if got := limitWidth("abcdef\nxy", 3); got != "abc\nxy" {
		t.Fatalf("limitWidth() = %q", got)
	}
	if got := ageLabel(time.Now, time.Time{}); got != "never" {
		t.Fatalf("ageLabel(zero) = %q", got)
	}
	if got := contextStrip([]connector.Scope{{Name: "a"}, {Name: "b"}}); !strings.Contains(got, "[2] b") {
		t.Fatalf("contextStrip() = %q", got)
	}
	if got := fleetSummary([]connector.Scope{{Name: "a"}, {Name: "b"}}, fleet.Coverage{
		Requested: 2, Reachable: 1, Stale: []string{"a"}, Unreachable: []string{"b"},
	}); got != "2 ctx (0✓ 1~ 1✗)" {
		t.Fatalf("fleetSummary() = %q", got)
	}
}

func TestRunHonorsCanceledContext(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := Run(ctx, fleetcache.New(), &countingSyncer{}, strings.NewReader("q"), io.Discard)
	if err == nil {
		t.Fatal("Run(canceled) error = nil")
	}
}

type countingSyncer struct {
	calls     atomic.Int64
	kindCalls atomic.Int64
	lastKinds atomic.Value
}

func (syncer *countingSyncer) SyncOnce(_ context.Context) error {
	syncer.calls.Add(1)
	return nil
}

func (syncer *countingSyncer) SyncKinds(_ context.Context, kinds ...string) error {
	syncer.kindCalls.Add(1)
	syncer.lastKinds.Store(append([]string(nil), kinds...))
	return nil
}

func populatedStore(t *testing.T, pods int) *fleetcache.Store {
	t.Helper()
	now := time.Date(2026, time.July, 10, 21, 0, 0, 0, time.UTC)
	store := fleetcache.New()
	store.SetDiscovery(connector.Discovery{Scopes: []connector.Scope{
		{Name: "alpha", Reachable: true, ObservedAt: now},
		{Name: "beta", Reachable: true, ObservedAt: now},
	}})
	podFacts := make([]fleet.Fact, 0, pods)
	for index := range pods {
		scope := "alpha"
		if index%2 == 1 {
			scope = "beta"
		}
		podFacts = append(podFacts, tuiFact("Pod", scope, fmt.Sprintf("pod-%04d", index), map[string]any{
			"spec": map[string]any{"containers": []any{map[string]any{"image": "registry/api:v1"}}},
			"status": map[string]any{
				"phase": "Running",
				"containerStatuses": []any{map[string]any{
					"ready": true, "restartCount": 0, "state": map[string]any{},
				}},
			},
		}, now))
	}
	if err := store.Replace("Pod", fleet.QueryResult{Facts: podFacts, Coverage: fleet.Coverage{Requested: 2, Reachable: 2}}); err != nil {
		t.Fatalf("Replace(Pod) error = %v", err)
	}
	deployFacts := []fleet.Fact{
		tuiFact("Deployment", "alpha", "payments", map[string]any{
			"spec": map[string]any{"replicas": 3}, "status": map[string]any{"availableReplicas": 3, "updatedReplicas": 3},
		}, now),
		tuiFact("Deployment", "beta", "payments", map[string]any{
			"spec": map[string]any{"replicas": 3}, "status": map[string]any{"availableReplicas": 0, "updatedReplicas": 0},
		}, now),
	}
	if err := store.Replace("Deployment", fleet.QueryResult{Facts: deployFacts, Coverage: fleet.Coverage{Requested: 2, Reachable: 2}}); err != nil {
		t.Fatalf("Replace(Deployment) error = %v", err)
	}
	return store
}

func tuiFact(kind, scope, name string, body map[string]any, observed time.Time) fleet.Fact {
	body["apiVersion"] = "v1"
	body["kind"] = kind
	body["metadata"] = map[string]any{
		"name": name, "namespace": "apps", "creationTimestamp": observed.Add(-time.Hour).Format(time.RFC3339),
	}
	payload, _ := json.Marshal(body)
	return fleet.Fact{Evidence: fleet.Evidence{
		Ref:  fleet.ResourceRef{SourceKind: "test", Scope: scope, Kind: kind, Namespace: "apps", Name: name},
		Kind: fleet.FactInventory, Observed: payload, ObservedAt: observed, Source: scope,
	}, Workspace: fleet.LocalWorkspace}
}

func keyMessage(text string) tea.KeyPressMsg {
	if text == "ctrl+r" {
		return tea.KeyPressMsg(tea.Key{Code: 'r', Mod: tea.ModCtrl})
	}
	if text == "ctrl+k" {
		return tea.KeyPressMsg(tea.Key{Code: 'k', Mod: tea.ModCtrl})
	}
	runes := []rune(text)
	code := rune(0)
	if len(runes) == 1 {
		code = runes[0]
	}
	return tea.KeyPressMsg(tea.Key{Code: code, Text: text})
}

func specialKey(code rune) tea.KeyPressMsg {
	return tea.KeyPressMsg(tea.Key{Code: code})
}
