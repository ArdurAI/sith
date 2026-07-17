// SPDX-License-Identifier: Apache-2.0

package argocd

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/ArdurAI/sith/internal/fleet"
)

func TestProjectApplicationEmitsSanitizedFourLensFacts(t *testing.T) {
	t.Parallel()
	input := Projection{
		Workspace: "workspace-a", Scope: "cluster-a", ObservedAt: time.Date(2026, 7, 16, 21, 0, 0, 0, time.FixedZone("CDT", -5*60*60)),
		Application: completeApplication(),
	}
	facts, err := ProjectApplication(input)
	if err != nil {
		t.Fatalf("ProjectApplication() error = %v", err)
	}
	if len(facts) != 5 {
		t.Fatalf("fact count = %d, want 5: %#v", len(facts), facts)
	}
	wantKinds := []fleet.FactKind{fleet.FactDesired, fleet.FactHealth, fleet.FactDrift, fleet.FactChange, fleet.FactChange}
	wantLenses := []fleet.Lens{fleet.LensDesired, fleet.LensLive, fleet.LensDesired, fleet.LensTimeline, fleet.LensTimeline}
	for index, fact := range facts {
		if fact.Fact.Kind != wantKinds[index] || fact.Lens != wantLenses[index] {
			t.Fatalf("fact %d kind/lens = %s/%s, want %s/%s", index, fact.Fact.Kind, fact.Lens, wantKinds[index], wantLenses[index])
		}
		if fact.Entity == nil || fact.Entity.Cluster != "cluster-a" || fact.Entity.Namespace != "argocd" ||
			fact.Entity.Kind != "Application" || fact.Entity.Name != "payments" {
			t.Fatalf("fact %d entity = %#v", index, fact.Entity)
		}
		if fact.Fact.Ref.SourceKind != Kind || fact.Fact.Provenance.Adapter != Kind || fact.Fact.Provenance.ProtocolV != ProtocolVersion {
			t.Fatalf("fact %d provenance = %#v", index, fact.Fact.Provenance)
		}
	}

	var desired desiredObservation
	if err := json.Unmarshal(facts[0].Fact.Observed, &desired); err != nil {
		t.Fatalf("decode desired fact: %v", err)
	}
	if len(desired.Sources) != 1 || desired.Sources[0].Repository != "https://github.example/ardur/payments.git" ||
		desired.Destination.Server != "https://kubernetes.default.svc" || desired.Destination.Namespace != "payments" {
		t.Fatalf("desired fact = %#v", desired)
	}
	var drift driftObservation
	if err := json.Unmarshal(facts[2].Fact.Observed, &drift); err != nil {
		t.Fatalf("decode drift fact: %v", err)
	}
	if drift.SyncStatus != "OutOfSync" || drift.Drifted == nil || !*drift.Drifted || drift.Revision != "abc123" {
		t.Fatalf("drift fact = %#v", drift)
	}
	var firstChange, secondChange changeObservation
	if err := json.Unmarshal(facts[3].Fact.Observed, &firstChange); err != nil {
		t.Fatalf("decode first change: %v", err)
	}
	if err := json.Unmarshal(facts[4].Fact.Observed, &secondChange); err != nil {
		t.Fatalf("decode second change: %v", err)
	}
	if !firstChange.EventAt.Before(secondChange.EventAt) || secondChange.Phase != "Succeeded" {
		t.Fatalf("changes = %#v then %#v", firstChange, secondChange)
	}

	encoded, err := json.Marshal(facts)
	if err != nil {
		t.Fatalf("marshal facts: %v", err)
	}
	for _, secret := range []string{"git-token", "git-password", "access_token", "cluster-password", "cluster-token", "raw-helm-secret"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("projected facts retained secret marker %q: %s", secret, encoded)
		}
	}
	graph, err := fleet.NewGraph(input.Workspace, facts)
	if err != nil {
		t.Fatalf("NewGraph() error = %v", err)
	}
	if len(graph.Nodes) != 1 || len(graph.Nodes[0].Facts) != len(facts) || len(graph.Unattached) != 0 {
		t.Fatalf("graph = %#v", graph)
	}
}

func TestProjectApplicationAbstainsWhenEvidenceIsMissing(t *testing.T) {
	t.Parallel()
	application := completeApplication()
	delete(application.Object, "status")
	facts, err := ProjectApplication(Projection{
		Workspace: "workspace-a", Scope: "cluster-a", ObservedAt: time.Now(), Application: application,
	})
	if err != nil {
		t.Fatalf("ProjectApplication() error = %v", err)
	}
	if len(facts) != 1 || facts[0].Fact.Kind != fleet.FactDesired || facts[0].Lens != fleet.LensDesired {
		t.Fatalf("facts = %#v, want only desired evidence", facts)
	}

	delete(application.Object, "spec")
	facts, err = ProjectApplication(Projection{
		Workspace: "workspace-a", Scope: "cluster-a", ObservedAt: time.Now(), Application: application,
	})
	if err != nil {
		t.Fatalf("ProjectApplication(no spec/status) error = %v", err)
	}
	if len(facts) != 0 {
		t.Fatalf("facts = %#v, want no fabricated evidence", facts)
	}
}

func TestProjectApplicationBoundsHistoryAndMarksTruncation(t *testing.T) {
	t.Parallel()
	application := minimalApplication()
	history := make([]any, maxApplicationHistory+8)
	for index := range history {
		history[index] = map[string]any{
			"id": int64(index), "revision": fmt.Sprintf("revision-%02d", index),
			"deployedAt": time.Date(2026, 7, 1, 0, index, 0, 0, time.UTC).Format(time.RFC3339),
		}
	}
	application.Object["status"] = map[string]any{"history": history}
	input := Projection{Workspace: "workspace-a", Scope: "cluster-a", ObservedAt: time.Now(), Application: application}
	first, err := ProjectApplication(input)
	if err != nil {
		t.Fatalf("ProjectApplication() error = %v", err)
	}
	second, err := ProjectApplication(input)
	if err != nil {
		t.Fatalf("second ProjectApplication() error = %v", err)
	}
	if len(first) != maxApplicationHistory+1 {
		t.Fatalf("fact count = %d, want desired + %d history", len(first), maxApplicationHistory)
	}
	firstJSON, _ := json.Marshal(first)
	secondJSON, _ := json.Marshal(second)
	if !slices.Equal(firstJSON, secondJSON) {
		t.Fatalf("projection is not deterministic\nfirst:  %s\nsecond: %s", firstJSON, secondJSON)
	}
	var oldest, newest changeObservation
	if err := json.Unmarshal(first[1].Fact.Observed, &oldest); err != nil {
		t.Fatalf("decode oldest change: %v", err)
	}
	if err := json.Unmarshal(first[len(first)-1].Fact.Observed, &newest); err != nil {
		t.Fatalf("decode newest change: %v", err)
	}
	if oldest.HistoryID != "8" || !oldest.HistoryTruncated || newest.HistoryID != "39" || newest.HistoryTruncated {
		t.Fatalf("oldest/newest = %#v / %#v", oldest, newest)
	}
}

func TestProjectApplicationRejectsMalformedOrAmbiguousEvidence(t *testing.T) {
	t.Parallel()
	valid := Projection{Workspace: "workspace-a", Scope: "cluster-a", ObservedAt: time.Now(), Application: completeApplication()}
	tests := []struct {
		name   string
		mutate func(*Projection)
	}{
		{"missing workspace", func(input *Projection) { input.Workspace = "" }},
		{"invalid scope", func(input *Projection) { input.Scope = " cluster-a" }},
		{"zero observation time", func(input *Projection) { input.ObservedAt = time.Time{} }},
		{"wrong API group", func(input *Projection) { input.Application.SetAPIVersion("example.io/v1") }},
		{"wrong kind", func(input *Projection) { input.Application.SetKind("Deployment") }},
		{"missing name", func(input *Projection) { input.Application.SetName("") }},
		{"missing namespace", func(input *Projection) { input.Application.SetNamespace("") }},
		{"source and sources", func(input *Projection) {
			input.Application.Object["spec"].(map[string]any)["sources"] = []any{map[string]any{"repoURL": "https://github.example/other/repo.git"}}
		}},
		{"too many sources", func(input *Projection) {
			spec := input.Application.Object["spec"].(map[string]any)
			delete(spec, "source")
			sources := make([]any, maxApplicationSources+1)
			for index := range sources {
				sources[index] = map[string]any{"repoURL": fmt.Sprintf("https://github.example/owner/repo-%d.git", index)}
			}
			spec["sources"] = sources
		}},
		{"ambiguous destination", func(input *Projection) {
			input.Application.Object["spec"].(map[string]any)["destination"] = map[string]any{
				"name": "in-cluster", "server": "https://kubernetes.default.svc",
			}
		}},
		{"unsupported health", func(input *Projection) {
			input.Application.Object["status"].(map[string]any)["health"] = map[string]any{"status": "DefinitelyFine"}
		}},
		{"unsupported sync", func(input *Projection) {
			input.Application.Object["status"].(map[string]any)["sync"] = map[string]any{"status": "Maybe"}
		}},
		{"malformed history", func(input *Projection) {
			input.Application.Object["status"].(map[string]any)["history"] = []any{"not-an-object"}
		}},
		{"invalid history time", func(input *Projection) {
			input.Application.Object["status"].(map[string]any)["history"] = []any{map[string]any{"deployedAt": "yesterday"}}
		}},
		{"oversized revision", func(input *Projection) {
			input.Application.Object["spec"].(map[string]any)["source"].(map[string]any)["targetRevision"] = strings.Repeat("a", maxRevisionBytes+1)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := cloneProjection(valid)
			test.mutate(&input)
			if facts, err := ProjectApplication(input); err == nil {
				t.Fatalf("ProjectApplication() facts = %#v, error = nil", facts)
			}
		})
	}
}

func TestProjectApplicationEnforcesEncodedPayloadBudget(t *testing.T) {
	t.Parallel()
	application := minimalApplication()
	spec := application.Object["spec"].(map[string]any)
	delete(spec, "source")
	sources := make([]any, maxApplicationSources)
	for index := range sources {
		sources[index] = map[string]any{
			"repoURL": fmt.Sprintf("https://github.example/owner/repo-%d.git", index),
			"path":    strings.Repeat("a", maxTextBytes-1),
		}
	}
	spec["sources"] = sources
	_, err := ProjectApplication(Projection{
		Workspace: "workspace-a", Scope: "cluster-a", ObservedAt: time.Now(), Application: application,
	})
	if err == nil || !strings.Contains(err.Error(), "encoded bytes") {
		t.Fatalf("ProjectApplication() error = %v, want encoded payload budget rejection", err)
	}
}

func TestSanitizeRepositoryURLSupportsDocumentedArgoForms(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "HTTPS credentials",
			input: "https://user:password@git.example/owner/repo.git?token=secret#credential",
			want:  "https://git.example/owner/repo.git",
		},
		{name: "SCP-like SSH", input: "git@git.example:owner/repo.git", want: "git.example:owner/repo.git"},
		{
			name:  "scheme-less Helm OCI registry",
			input: "registry-1.docker.io/bitnamicharts",
			want:  "registry-1.docker.io/bitnamicharts",
		},
		{
			name:  "OCI Application source",
			input: "oci://user:password@registry.example/owner/artifact?token=secret#credential",
			want:  "oci://registry.example/owner/artifact",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := sanitizeRepositoryURL(test.input)
			if err != nil {
				t.Fatalf("sanitizeRepositoryURL() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("sanitizeRepositoryURL() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestSanitizeRepositoryURLRejectsAmbiguousOrUnsupportedForms(t *testing.T) {
	t.Parallel()
	for _, value := range []string{
		"file://localhost/tmp/repository",
		"registry.example/charts?token=secret",
		"user@registry.example/charts",
		"./local/repository",
		"registry.example\\charts",
	} {
		t.Run(value, func(t *testing.T) {
			if got, err := sanitizeRepositoryURL(value); err == nil {
				t.Fatalf("sanitizeRepositoryURL(%q) = %q, error = nil", value, got)
			}
		})
	}
}

func completeApplication() unstructured.Unstructured {
	application := minimalApplication()
	application.Object["spec"] = map[string]any{
		"project": "default",
		"source": map[string]any{
			"repoURL":        "https://git-token:git-password@github.example/ardur/payments.git?access_token=cluster-token#credential-fragment",
			"path":           "clusters/prod/payments",
			"targetRevision": "main",
			"helm": map[string]any{
				"parameters": []any{map[string]any{"name": "database.password", "value": "raw-helm-secret"}},
			},
		},
		"destination": map[string]any{
			"server":    "https://admin:cluster-password@kubernetes.default.svc?token=cluster-token#credentials",
			"namespace": "payments",
		},
	}
	application.Object["status"] = map[string]any{
		"health": map[string]any{"status": "Healthy", "message": "must not be retained"},
		"sync":   map[string]any{"status": "OutOfSync", "revision": "abc123"},
		"history": []any{
			map[string]any{"id": int64(1), "revision": "old123", "deployedAt": "2026-07-15T20:00:00Z"},
			map[string]any{"id": int64(2), "revision": "abc123", "deployedAt": "2026-07-16T20:00:00Z"},
		},
		"operationState": map[string]any{
			"phase": "Succeeded", "startedAt": "2026-07-16T19:59:00Z", "finishedAt": "2026-07-16T20:00:00Z",
			"message":    "credential-shaped status text must not be retained",
			"syncResult": map[string]any{"revision": "abc123"},
		},
	}
	return application
}

func minimalApplication() unstructured.Unstructured {
	return unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "argoproj.io/v1alpha1",
		"kind":       "Application",
		"metadata": map[string]any{
			"name": "payments", "namespace": "argocd",
		},
		"spec": map[string]any{
			"source":      map[string]any{"repoURL": "https://github.example/ardur/payments.git"},
			"destination": map[string]any{"name": "in-cluster", "namespace": "payments"},
		},
	}}
}

func cloneProjection(input Projection) Projection {
	input.Application = *input.Application.DeepCopy()
	return input
}
