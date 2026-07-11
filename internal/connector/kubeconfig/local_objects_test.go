// SPDX-License-Identifier: Apache-2.0

package kubeconfig

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"

	"github.com/ArdurAI/sith/internal/localops"
)

func TestLocalViewMasksSecretsAndDescribeComposesEvents(t *testing.T) {
	t.Parallel()
	secret := localObject("v1", "Secret", "apps", "credentials")
	secret.SetUID("secret-uid")
	secret.Object["data"] = map[string]any{"token": "c2VjcmV0"}
	event := localObject("v1", "Event", "apps", "credentials-updated")
	event.Object["involvedObject"] = map[string]any{
		"kind": "Secret", "name": "credentials", "uid": "secret-uid",
	}
	client := localFakeClient(secret, event)
	adapter := localTestAdapter(t, client)
	target := localops.Target{Context: "alpha", Namespace: "apps", Kind: "Secret", Name: "credentials"}

	masked, err := adapter.View(context.Background(), target, false)
	if err != nil {
		t.Fatalf("View(masked) error = %v", err)
	}
	if !strings.Contains(string(masked.YAML), "<redacted>") || strings.Contains(string(masked.YAML), "c2VjcmV0") {
		t.Fatalf("masked YAML = %q", masked.YAML)
	}
	revealed, err := adapter.View(context.Background(), target, true)
	if err != nil {
		t.Fatalf("View(revealed) error = %v", err)
	}
	if !strings.Contains(string(revealed.YAML), "c2VjcmV0") {
		t.Fatalf("revealed YAML = %q", revealed.YAML)
	}
	description, err := adapter.Describe(context.Background(), target)
	if err != nil {
		t.Fatalf("Describe() error = %v", err)
	}
	if len(description.Events) != 1 || description.Events[0].Ref.Name != "credentials-updated" {
		t.Fatalf("events = %#v", description.Events)
	}
}

func TestLocalApplyRequiresIdentityAndPerformsDryRun(t *testing.T) {
	t.Parallel()
	configMap := localObject("v1", "ConfigMap", "apps", "settings")
	configMap.SetUID("settings-uid")
	configMap.SetResourceVersion("7")
	configMap.Object["data"] = map[string]any{"mode": "old"}
	client := localFakeClient(configMap)
	var updateMu sync.Mutex
	updates := make([]metav1.UpdateOptions, 0, 3)
	client.PrependReactor("update", "configmaps", func(action k8stesting.Action) (bool, runtime.Object, error) {
		update := action.(k8stesting.UpdateActionImpl)
		updateMu.Lock()
		updates = append(updates, update.UpdateOptions)
		updateMu.Unlock()
		return true, update.GetObject().DeepCopyObject(), nil
	})
	adapter := localTestAdapter(t, client)
	target := localops.Target{Context: "alpha", Namespace: "apps", Kind: "ConfigMap", Name: "settings"}
	manifest := []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  name: settings
  namespace: apps
  resourceVersion: "7"
  uid: settings-uid
data:
  mode: new
`)
	preview, err := adapter.PreviewApply(context.Background(), target, manifest)
	if err != nil {
		t.Fatalf("PreviewApply() error = %v", err)
	}
	if !strings.Contains(string(preview.CurrentYAML), "mode: old") || !strings.Contains(string(preview.DryRunYAML), "mode: new") {
		t.Fatalf("preview = %#v", preview)
	}
	evidence, err := adapter.Apply(context.Background(), target, manifest)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if evidence.Ref.Scope != "alpha" || evidence.Ref.Name != "settings" {
		t.Fatalf("evidence = %#v", evidence)
	}
	updateMu.Lock()
	dryRuns := make([]bool, 0, len(updates))
	for _, options := range updates {
		dryRuns = append(dryRuns, slices.Equal(options.DryRun, []string{metav1.DryRunAll}))
	}
	updateMu.Unlock()
	if !slices.Equal(dryRuns, []bool{true, true, false}) {
		t.Fatalf("dry-run sequence = %v", dryRuns)
	}

	wrongName := strings.ReplaceAll(string(manifest), "name: settings", "name: other")
	_, err = adapter.PreviewApply(context.Background(), target, []byte(wrongName))
	if !errors.Is(err, localops.ErrInvalidTarget) {
		t.Fatalf("PreviewApply(identity mutation) error = %v", err)
	}
}

func TestLocalOperationBootstrapsOnlyExplicitContext(t *testing.T) {
	t.Parallel()
	pod := localObject("v1", "Pod", "apps", "api")
	client := fakeClient(pod)
	var probeMu sync.Mutex
	probed := make([]string, 0, 3)
	adapter, err := New(
		WithLoadingRules(testLoadingRules(t, testConfig("alpha", "beta"))),
		withProbe(func(_ context.Context, config *rest.Config) error {
			probeMu.Lock()
			probed = append(probed, config.Host)
			probeMu.Unlock()
			return nil
		}),
		withDynamicFactory(func(_ *rest.Config) (dynamic.Interface, error) { return client, nil }),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, err = adapter.View(context.Background(), localops.Target{
		Context: "alpha", Namespace: "apps", Kind: "Pod", Name: "api",
	}, false)
	if err != nil {
		t.Fatalf("View() error = %v", err)
	}
	probeMu.Lock()
	initial := append([]string(nil), probed...)
	probeMu.Unlock()
	if !slices.Equal(initial, []string{"https://alpha.invalid"}) {
		t.Fatalf("local operation probed = %v, want alpha only", initial)
	}
	if _, err := adapter.Discover(context.Background()); err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	probeMu.Lock()
	defer probeMu.Unlock()
	if len(probed) != 3 || !slices.Contains(probed, "https://beta.invalid") {
		t.Fatalf("probes after full discovery = %v", probed)
	}
}

func TestLocalOperationRejectsUnsafeURLSegmentsBeforeContextAccess(t *testing.T) {
	t.Parallel()
	adapter := localTestAdapter(t, localFakeClient())
	_, err := adapter.View(context.Background(), localops.Target{
		Context: "alpha", Namespace: "apps", Kind: "Pod", Name: "..",
	}, false)
	if !errors.Is(err, localops.ErrInvalidTarget) || !strings.Contains(err.Error(), "safe Kubernetes path segment") {
		t.Fatalf("View(unsafe name) error = %v", err)
	}
	adapter.mu.RLock()
	knownScopes := len(adapter.scopes)
	adapter.mu.RUnlock()
	if knownScopes != 0 {
		t.Fatalf("known scopes = %d, want validation before context access", knownScopes)
	}
}

func localTestAdapter(t *testing.T, client *dynamicfake.FakeDynamicClient) *Adapter {
	t.Helper()
	adapter, err := New(
		WithLoadingRules(testLoadingRules(t, testConfig("alpha"))),
		withProbe(func(_ context.Context, _ *rest.Config) error { return nil }),
		withDynamicFactory(func(_ *rest.Config) (dynamic.Interface, error) { return client, nil }),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return adapter
}

func localFakeClient(objects ...runtime.Object) *dynamicfake.FakeDynamicClient {
	listKinds := map[schema.GroupVersionResource]string{
		{Version: "v1", Resource: "events"}:     "EventList",
		{Version: "v1", Resource: "secrets"}:    "SecretList",
		{Version: "v1", Resource: "configmaps"}: "ConfigMapList",
	}
	return fakeClientWithKinds(listKinds, objects...)
}

func localObject(apiVersion, kind, namespace, name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": apiVersion,
		"kind":       kind,
		"metadata": map[string]any{
			"name": name, "namespace": namespace,
		},
	}}
}
