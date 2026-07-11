// SPDX-License-Identifier: Apache-2.0

package fleetcache

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ArdurAI/sith/internal/connector"
	"github.com/ArdurAI/sith/internal/fleet"
)

func TestStorePreservesLastKnownRowsAndSurfacesCoverage(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 10, 20, 0, 0, 0, time.UTC)
	store := newStore(func() time.Time { return now }, 15*time.Second)
	if !store.BeginSync() {
		t.Fatal("BeginSync() = false, want initial sync")
	}
	store.SetDiscovery(fleet.LocalWorkspace, connector.Discovery{Scopes: []connector.Scope{
		{Name: "alpha", Reachable: true, ObservedAt: now},
		{Name: "beta", Reachable: true, ObservedAt: now},
	}})
	initial := fleet.QueryResult{
		Facts: []fleet.Fact{
			podFact(t, "alpha", "api-0", "Running", "registry/api:v1", now),
			podFact(t, "beta", "api-0", "Running", "registry/api:v1", now),
		},
		Coverage: fleet.Coverage{Requested: 2, Reachable: 2},
	}
	if err := store.Replace("pods", initial); err != nil {
		t.Fatalf("Replace(initial) error = %v", err)
	}
	store.EndSync(nil)

	snapshot := store.Query(fleet.LocalWorkspace, Query{Kind: "Pod"})
	if snapshot.State != StateWarm || len(snapshot.Records) != 2 || !snapshot.Coverage.Complete() {
		t.Fatalf("initial snapshot = %#v, want two complete warm records", snapshot)
	}

	now = now.Add(20 * time.Second)
	if !store.BeginSync() {
		t.Fatal("BeginSync(second) = false")
	}
	store.SetDiscovery(fleet.LocalWorkspace, connector.Discovery{
		Scopes: []connector.Scope{
			{Name: "alpha", Reachable: true, ObservedAt: now},
			{Name: "beta", ObservedAt: now.Add(-20 * time.Second)},
		},
		Unreachable: []string{"beta"},
	})
	partial := fleet.QueryResult{
		Facts:    []fleet.Fact{podFact(t, "alpha", "api-1", "Running", "registry/api:v2", now)},
		Coverage: fleet.Coverage{Requested: 2, Reachable: 1, Unreachable: []string{"beta"}},
	}
	if err := store.Replace("Pod", partial); err != nil {
		t.Fatalf("Replace(partial) error = %v", err)
	}
	store.EndSync(nil)

	snapshot = store.Query(fleet.LocalWorkspace, Query{Kind: "pods"})
	if snapshot.State != StateDegraded || len(snapshot.Records) != 2 {
		t.Fatalf("partial snapshot = %#v, want degraded with last-known beta", snapshot)
	}
	if !slices.Equal(snapshot.Coverage.Unreachable, []string{"beta"}) {
		t.Fatalf("unreachable = %v, want [beta]", snapshot.Coverage.Unreachable)
	}
	if snapshot.Records[0].Name != "api-1" || snapshot.Records[1].Cluster != "beta" || !snapshot.Records[1].Stale {
		t.Fatalf("records = %#v, want fresh alpha replacement and stale beta last-known", snapshot.Records)
	}
}

func TestStoreSearchGrammarRunsOnlyOnNormalizedCache(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 10, 20, 0, 0, 0, time.UTC)
	store := newStore(func() time.Time { return now }, time.Minute)
	store.SetDiscovery(fleet.LocalWorkspace, connector.Discovery{Scopes: []connector.Scope{
		{Name: "prod-eu", Reachable: true, ObservedAt: now},
		{Name: "dev-us", Reachable: true, ObservedAt: now},
	}})
	result := fleet.QueryResult{
		Facts: []fleet.Fact{
			podFact(t, "prod-eu", "payments-0", "CrashLoopBackOff", "registry/payments:log4j-fix", now),
			podFact(t, "dev-us", "worker-0", "Running", "registry/worker:v1", now),
		},
		Coverage: fleet.Coverage{Requested: 2, Reachable: 2},
	}
	if err := store.Replace("Pod", result); err != nil {
		t.Fatalf("Replace() error = %v", err)
	}

	query, err := ParseSearch("pay ctx:prod-* ns:apps status:!Running image:*log4j* label:app=payments restarts:>5")
	if err != nil {
		t.Fatalf("ParseSearch() error = %v", err)
	}
	snapshot := store.Query(fleet.LocalWorkspace, query)
	if len(snapshot.Records) != 1 || snapshot.Records[0].Name != "payments-0" {
		t.Fatalf("records = %#v, want payments pod", snapshot.Records)
	}
	if snapshot.Coverage.Requested != 1 || snapshot.Coverage.Reachable != 1 {
		t.Fatalf("coverage = %#v, want prod scope only", snapshot.Coverage)
	}
}

func TestStoreQueryRequiresAndEnforcesWorkspace(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	store := newStore(func() time.Time { return now }, time.Minute)
	local := podFact(t, "alpha", "local-api", "Running", "registry/api:v1", now)
	other := podFact(t, "beta", "other-api", "Running", "registry/api:v1", now)
	other.Workspace = "other"
	if err := store.Replace("Pod", fleet.QueryResult{
		Facts: []fleet.Fact{local, other}, Coverage: fleet.Coverage{Requested: 2, Reachable: 2},
	}); err != nil {
		t.Fatal(err)
	}
	if records := store.Query("", Query{Kind: "Pod"}).Records; len(records) != 0 {
		t.Fatalf("unscoped records = %#v", records)
	}
	localRecords := store.Query(fleet.LocalWorkspace, Query{Kind: "Pod"}).Records
	if len(localRecords) != 1 || localRecords[0].Name != "local-api" {
		t.Fatalf("local records = %#v", localRecords)
	}
	otherRecords := store.Query("other", Query{Kind: "Pod"}).Records
	if len(otherRecords) != 1 || otherRecords[0].Name != "other-api" {
		t.Fatalf("other records = %#v", otherRecords)
	}
}

func TestStoreSearchesCanonicalCVEFactsByImageAndIdentifier(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	store := newStore(func() time.Time { return now }, time.Minute)
	store.SetDiscovery(fleet.LocalWorkspace, connector.Discovery{Scopes: []connector.Scope{{Name: "alpha", Reachable: true, ObservedAt: now}}})
	payload, err := json.Marshal(fleet.CVEObservation{
		Image: "registry.example/payments:v4", IDs: []string{"CVE-2026-1234"}, Severity: "high",
	})
	if err != nil {
		t.Fatal(err)
	}
	fact := fleet.Fact{Evidence: fleet.Evidence{
		Ref:  fleet.ResourceRef{SourceKind: "scanner", Scope: "alpha", Kind: "Image", Name: "payments-v4"},
		Kind: fleet.FactCVE, Observed: payload, ObservedAt: now, Source: "scanner",
	}, Workspace: fleet.LocalWorkspace}
	if err := store.Replace("CVE", fleet.QueryResult{
		Facts: []fleet.Fact{fact}, Coverage: fleet.Coverage{Requested: 1, Reachable: 1},
	}); err != nil {
		t.Fatal(err)
	}
	query, err := ParseSearch("image:*payments* cve:CVE-2026-1234")
	if err != nil {
		t.Fatal(err)
	}
	records := store.Query(fleet.LocalWorkspace, query).Records
	if len(records) != 1 || !slices.Equal(records[0].CVEs, []string{"CVE-2026-1234"}) || records[0].Status != "high" {
		t.Fatalf("CVE records = %#v", records)
	}
}

func TestStoreMapsGenericResourceAliasToAdvertisedKind(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	store := newStore(func() time.Time { return now }, time.Minute)
	store.SetDiscovery(fleet.LocalWorkspace, connector.Discovery{Scopes: []connector.Scope{{Name: "alpha", Reachable: true, ObservedAt: now}}})
	fact := objectFact(t, "ConfigMap", map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata":   map[string]any{"name": "settings", "namespace": "apps"},
	}, now)
	if err := store.Replace("configmaps", fleet.QueryResult{
		Facts: []fleet.Fact{fact}, Coverage: fleet.Coverage{Requested: 1, Reachable: 1},
	}); err != nil {
		t.Fatalf("Replace() error = %v", err)
	}
	for _, kind := range []string{"configmaps", "ConfigMap"} {
		snapshot := store.Query(fleet.LocalWorkspace, Query{Kind: kind})
		if len(snapshot.Records) != 1 || snapshot.Records[0].Kind != "ConfigMap" || !snapshot.Coverage.Complete() {
			t.Fatalf("Query(%q) = %#v, want advertised ConfigMap", kind, snapshot)
		}
	}
}

func TestStoreAppliesWatchDeltasAndPreservesFailedScopeRows(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	store := newStore(func() time.Time { return now }, time.Minute)
	store.SetDiscovery(fleet.LocalWorkspace, connector.Discovery{Scopes: []connector.Scope{
		{Name: "alpha", Reachable: true, ObservedAt: now},
		{Name: "beta", Reachable: true, ObservedAt: now},
	}})
	alpha := podFact(t, "alpha", "api-0", "Running", "image:v1", now)
	beta := podFact(t, "beta", "api-0", "Running", "image:v1", now)
	if err := store.Replace("Pod", fleet.QueryResult{
		Facts: []fleet.Fact{alpha, beta}, Coverage: fleet.Coverage{Requested: 2, Reachable: 2},
	}); err != nil {
		t.Fatalf("Replace() error = %v", err)
	}

	now = now.Add(time.Second)
	updated := podFact(t, "alpha", "api-1", "Running", "image:v2", now)
	if err := store.ApplyWatchEvent(connector.WatchEvent{
		Type: connector.WatchSnapshot, Kind: "pods", Scope: "alpha", Facts: []fleet.Fact{updated}, ObservedAt: now,
	}); err != nil {
		t.Fatalf("ApplyWatchEvent(snapshot) error = %v", err)
	}
	if err := store.ApplyWatchEvent(connector.WatchEvent{
		Type: connector.WatchError, Kind: "Pod", Scope: "beta", Err: errors.New("watch disconnected"),
	}); err != nil {
		t.Fatalf("ApplyWatchEvent(error) error = %v", err)
	}
	snapshot := store.Query(fleet.LocalWorkspace, Query{Kind: "Pod"})
	if len(snapshot.Records) != 2 || snapshot.Records[0].Name != "api-1" || !snapshot.Records[1].Stale {
		t.Fatalf("snapshot after deltas = %#v", snapshot)
	}
	if !slices.Equal(snapshot.Coverage.Unreachable, []string{"beta"}) {
		t.Fatalf("unreachable = %v, want beta", snapshot.Coverage.Unreachable)
	}

	if err := store.ApplyWatchEvent(connector.WatchEvent{
		Type: connector.WatchDelete, Kind: "pods", Scope: "alpha",
		Ref: fleet.ResourceRef{Scope: "alpha", Kind: "Pod", Namespace: "apps", Name: "api-1"}, ObservedAt: now,
	}); err != nil {
		t.Fatalf("ApplyWatchEvent(delete) error = %v", err)
	}
	if records := store.Query(fleet.LocalWorkspace, Query{Kind: "Pod"}).Records; len(records) != 1 || records[0].Cluster != "beta" {
		t.Fatalf("records after delete = %#v", records)
	}
}

func TestStoreRejectsCrossScopeWatchFacts(t *testing.T) {
	t.Parallel()
	store := New()
	wrongScope := podFact(t, "beta", "api-0", "Running", "image:v1", time.Now().UTC())
	err := store.ApplyWatchEvent(connector.WatchEvent{
		Type: connector.WatchUpsert, Kind: "Pod", Scope: "alpha", Fact: wrongScope,
	})
	if err == nil || !strings.Contains(err.Error(), "does not match stream scope") {
		t.Fatalf("ApplyWatchEvent() error = %v, want scope rejection", err)
	}
	if snapshot := store.Query(fleet.LocalWorkspace, Query{}); len(snapshot.Records) != 0 {
		t.Fatalf("records = %#v, want atomic rejection", snapshot.Records)
	}
}

func TestStorePauseAndChangeNotification(t *testing.T) {
	t.Parallel()
	store := New()
	initial := store.Query(fleet.LocalWorkspace, Query{}).Version
	store.SetPaused(true)
	version, err := store.WaitForChange(context.Background(), initial)
	if err != nil || version <= initial {
		t.Fatalf("WaitForChange() = %d, %v", version, err)
	}
	if store.BeginSync() {
		t.Fatal("BeginSync() while paused = true")
	}
	if state := store.Query(fleet.LocalWorkspace, Query{}).State; state != StatePaused {
		t.Fatalf("state = %q, want paused", state)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.WaitForChange(ctx, version); err == nil {
		t.Fatal("WaitForChange(canceled) error = nil")
	}
}

func TestStoreConcurrentSnapshotsAreImmutable(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	store := newStore(func() time.Time { return now }, time.Minute)
	store.SetDiscovery(fleet.LocalWorkspace, connector.Discovery{Scopes: []connector.Scope{{Name: "alpha", Reachable: true, ObservedAt: now}}})
	var waitGroup sync.WaitGroup
	for index := range 20 {
		waitGroup.Add(2)
		go func(index int) {
			defer waitGroup.Done()
			_ = store.Replace("Pod", fleet.QueryResult{
				Facts:    []fleet.Fact{podFact(t, "alpha", "pod-"+string(rune('a'+index)), "Running", "image:v1", now)},
				Coverage: fleet.Coverage{Requested: 1, Reachable: 1},
			})
		}(index)
		go func() {
			defer waitGroup.Done()
			snapshot := store.Query(fleet.LocalWorkspace, Query{Kind: "Pod"})
			if len(snapshot.Records) > 0 {
				snapshot.Records[0].Labels["mutated"] = "yes"
			}
		}()
	}
	waitGroup.Wait()
	for _, record := range store.Query(fleet.LocalWorkspace, Query{Kind: "Pod"}).Records {
		if record.Labels["mutated"] != "" {
			t.Fatal("snapshot mutation escaped into store")
		}
	}
}

func TestReplaceRejectsInvalidEvidenceAtomically(t *testing.T) {
	t.Parallel()
	store := New()
	result := fleet.QueryResult{Facts: []fleet.Fact{{Evidence: fleet.Evidence{
		Ref:      fleet.ResourceRef{Scope: "alpha", Kind: "Pod", Name: "bad"},
		Observed: json.RawMessage(`{"broken"`),
	}}}}
	if err := store.Replace("Pod", result); err == nil {
		t.Fatal("Replace(invalid) error = nil")
	}
	if snapshot := store.Query(fleet.LocalWorkspace, Query{}); len(snapshot.Records) != 0 {
		t.Fatalf("records = %#v, want atomic rejection", snapshot.Records)
	}
}

func TestNormalizeTierOneRecords(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	tests := []struct {
		name       string
		kind       string
		object     map[string]any
		wantStatus string
		wantReady  string
	}{
		{
			name: "deployment progressing",
			kind: "Deployment",
			object: map[string]any{
				"spec": map[string]any{
					"replicas": 3,
					"template": map[string]any{"spec": map[string]any{
						"containers": []any{map[string]any{"image": "registry/payments:v2"}},
					}},
				},
				"status": map[string]any{"availableReplicas": 1, "updatedReplicas": 2},
			},
			wantStatus: "Progressing",
			wantReady:  "1/3",
		},
		{
			name: "warning event",
			kind: "Event",
			object: map[string]any{
				"type":           "Warning",
				"reason":         "BackOff",
				"message":        "container is backing off",
				"involvedObject": map[string]any{"kind": "Pod", "name": "api-0"},
			},
			wantStatus: "Warning",
			wantReady:  "Pod/api-0",
		},
		{
			name: "not ready node",
			kind: "Node",
			object: map[string]any{
				"status": map[string]any{
					"conditions": []any{map[string]any{"type": "Ready", "status": "False", "reason": "KubeletDown"}},
					"nodeInfo":   map[string]any{"kubeletVersion": "v1.36.1"},
				},
			},
			wantStatus: "NotReady",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			test.object["apiVersion"] = "v1"
			test.object["kind"] = test.kind
			test.object["metadata"] = map[string]any{"name": "object", "uid": "uid"}
			fact := objectFact(t, test.kind, test.object, now)
			record, err := normalize(fact)
			if err != nil {
				t.Fatalf("normalize() error = %v", err)
			}
			if record.Status != test.wantStatus || record.Ready != test.wantReady {
				t.Fatalf("record = %#v, want status=%q ready=%q", record, test.wantStatus, test.wantReady)
			}
			if test.kind == "Deployment" && !slices.Equal(record.Images, []string{"registry/payments:v2"}) {
				t.Fatalf("images = %v", record.Images)
			}
			if test.kind == "Node" && record.Version != "v1.36.1" {
				t.Fatalf("version = %q", record.Version)
			}
		})
	}
}

func TestStoreReportsOfflineLastKnownAndPendingLens(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	store := newStore(func() time.Time { return now }, time.Minute)
	store.SetDiscovery(fleet.LocalWorkspace, connector.Discovery{Scopes: []connector.Scope{{Name: "alpha", Reachable: true, ObservedAt: now}}})
	if pending := store.Query(fleet.LocalWorkspace, Query{Kind: "Node"}); pending.Coverage.Reachable != 0 || pending.State != StateCold {
		t.Fatalf("pending snapshot = %#v, want zero covered cold lens", pending)
	}
	if err := store.Replace("Pod", fleet.QueryResult{
		Facts:    []fleet.Fact{podFact(t, "alpha", "api-0", "Running", "image:v1", now)},
		Coverage: fleet.Coverage{Requested: 1, Reachable: 1},
	}); err != nil {
		t.Fatalf("Replace() error = %v", err)
	}
	store.SetDiscovery(fleet.LocalWorkspace, connector.Discovery{Scopes: []connector.Scope{{Name: "alpha", ObservedAt: now}}, Unreachable: []string{"alpha"}})
	store.EndSync(context.DeadlineExceeded)
	offline := store.Query(fleet.LocalWorkspace, Query{Kind: "Pod", Text: []string{"no-match"}})
	if offline.State != StateOffline || len(offline.Records) != 0 {
		t.Fatalf("offline snapshot = %#v, want zero matches over retained offline data", offline)
	}
}

func TestParseSearchRejectsUnsafeGrammar(t *testing.T) {
	t.Parallel()
	for _, expression := range []string{"status:", "label:broken", "restarts:many", "unknown:value"} {
		if _, err := ParseSearch(expression); err == nil {
			t.Errorf("ParseSearch(%q) error = nil", expression)
		}
	}
}

func TestParseCorrelationSupportsHealthAndImageForms(t *testing.T) {
	t.Parallel()
	query, err := ParseCorrelation("deploy/payments status!=Healthy")
	if err != nil || query.Kind != "Deployment" || query.Name != "payments" || query.StatusNot != "Healthy" {
		t.Fatalf("ParseCorrelation(health) = %#v, %v", query, err)
	}
	query, err = ParseCorrelation("image:*log4j*")
	if err != nil || query.Image != "*log4j*" {
		t.Fatalf("ParseCorrelation(image) = %#v, %v", query, err)
	}
	for _, expression := range []string{"", "payments", "deploy/payments", "deploy/payments in:1h"} {
		if _, err := ParseCorrelation(expression); err == nil {
			t.Errorf("ParseCorrelation(%q) error = nil", expression)
		}
	}
}

func podFact(t *testing.T, cluster, name, status, image string, observed time.Time) fleet.Fact {
	t.Helper()
	object := map[string]any{
		"apiVersion": "v1",
		"kind":       "Pod",
		"metadata": map[string]any{
			"name":      name,
			"namespace": "apps",
			"labels":    map[string]any{"app": stringsBefore(name, "-")},
		},
		"spec": map[string]any{
			"nodeName":   "node-1",
			"containers": []any{map[string]any{"name": "app", "image": image}},
		},
		"status": map[string]any{
			"phase": "Running",
			"containerStatuses": []any{map[string]any{
				"ready":        status == "Running",
				"restartCount": 7,
				"state": map[string]any{
					"waiting": map[string]any{"reason": status},
				},
			}},
		},
	}
	payload, err := json.Marshal(object)
	if err != nil {
		t.Fatalf("marshal pod: %v", err)
	}
	return fleet.Fact{
		Evidence: fleet.Evidence{
			Ref: fleet.ResourceRef{
				SourceKind: "test",
				Scope:      cluster,
				Kind:       "Pod",
				Namespace:  "apps",
				Name:       name,
			},
			Kind:       fleet.FactInventory,
			Observed:   payload,
			ObservedAt: observed,
			Source:     cluster,
		},
		Workspace: fleet.LocalWorkspace,
	}
}

func objectFact(t *testing.T, kind string, object map[string]any, observed time.Time) fleet.Fact {
	t.Helper()
	payload, err := json.Marshal(object)
	if err != nil {
		t.Fatalf("marshal object: %v", err)
	}
	return fleet.Fact{Evidence: fleet.Evidence{
		Ref: fleet.ResourceRef{
			SourceKind: "test",
			Scope:      "alpha",
			Kind:       kind,
			Name:       "object",
		},
		Kind:       fleet.FactInventory,
		Observed:   payload,
		ObservedAt: observed,
		Source:     "alpha",
	}, Workspace: fleet.LocalWorkspace}
}

func stringsBefore(value, separator string) string {
	for index := range len(value) {
		if string(value[index]) == separator {
			return value[:index]
		}
	}
	return value
}
