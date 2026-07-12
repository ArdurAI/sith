// SPDX-License-Identifier: Apache-2.0

package fleetcache

import (
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/ArdurAI/sith/internal/fleet"
)

const testRepoDigest = "registry.example/payments@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestNormalizePodPreservesCurrentAndPreviousFailureReasons(t *testing.T) {
	payload, err := json.Marshal(map[string]any{
		"apiVersion": "v1", "kind": "Pod",
		"metadata": map[string]any{"name": "payments-0", "namespace": "prod"},
		"spec":     map[string]any{"containers": []any{map[string]any{"name": "app", "image": "registry/payments:v2"}}},
		"status": map[string]any{"containerStatuses": []any{map[string]any{
			"restartCount": 5, "imageID": "docker-pullable://" + testRepoDigest,
			"state":     map[string]any{"waiting": map[string]any{"reason": "CrashLoopBackOff"}},
			"lastState": map[string]any{"terminated": map[string]any{"reason": "OOMKilled"}},
		}}},
	})
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	record, err := normalize(fleet.Fact{Evidence: fleet.Evidence{
		Ref:  fleet.ResourceRef{SourceKind: "kubeconfig", Scope: "alpha", Kind: "Pod", Namespace: "prod", Name: "payments-0"},
		Kind: fleet.FactInventory, Observed: payload, ObservedAt: time.Now().UTC(), Source: "alpha",
	}, Workspace: fleet.LocalWorkspace})
	if err != nil {
		t.Fatalf("normalize() error = %v", err)
	}
	if record.Status != "CrashLoopBackOff" || !slices.Equal(record.Reasons, []string{"CrashLoopBackOff", "OOMKilled"}) ||
		!slices.Equal(record.ImageDigests, []string{"sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}) ||
		!slices.Equal(record.ImageRepoDigests, []string{testRepoDigest}) {
		t.Fatalf("record = %#v", record)
	}
}

func TestNormalizePodKeepsUnprovenImageIDsNonCorrelating(t *testing.T) {
	t.Parallel()

	fullDigest := "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	for _, test := range []struct {
		name    string
		imageID string
	}{
		{name: "runtime only", imageID: "containerd://" + fullDigest},
		{name: "mutable tag", imageID: "registry.example/payments:latest@" + fullDigest},
		{name: "short digest", imageID: "registry.example/payments@sha256:abc123"},
		{name: "empty", imageID: ""},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			payload, err := json.Marshal(map[string]any{
				"apiVersion": "v1", "kind": "Pod",
				"metadata": map[string]any{"name": "payments-0", "namespace": "prod"},
				"status":   map[string]any{"containerStatuses": []any{map[string]any{"imageID": test.imageID}}},
			})
			if err != nil {
				t.Fatalf("marshal fixture: %v", err)
			}
			record, err := normalize(fleet.Fact{Evidence: fleet.Evidence{
				Ref:  fleet.ResourceRef{SourceKind: "kubeconfig", Scope: "alpha", Kind: "Pod", Namespace: "prod", Name: "payments-0"},
				Kind: fleet.FactInventory, Observed: payload, ObservedAt: time.Now().UTC(), Source: "alpha",
			}, Workspace: fleet.LocalWorkspace})
			if err != nil {
				t.Fatalf("normalize() error = %v", err)
			}
			if len(record.ImageRepoDigests) != 0 {
				t.Fatalf("unproven image ID %q yielded repo digests %#v", test.imageID, record.ImageRepoDigests)
			}
		})
	}
}
