// SPDX-License-Identifier: Apache-2.0

package kubeconfig

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/ArdurAI/sith/internal/fleet"
)

func TestNewRejectsInvalidOptions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		option Option
	}{
		{name: "nil option", option: nil},
		{name: "nil loading rules", option: WithLoadingRules(nil)},
		{name: "zero probe timeout", option: WithProbeTimeout(0)},
		{name: "zero request timeout", option: WithRequestTimeout(0)},
		{name: "zero concurrency", option: WithMaxConcurrency(0)},
		{name: "nil clock", option: withClock(nil)},
		{name: "nil probe", option: withProbe(nil)},
		{name: "nil dynamic factory", option: withDynamicFactory(nil)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := New(test.option); err == nil {
				t.Fatal("New() error = nil, want invalid option error")
			}
		})
	}
}

func TestDiscoverIsIndependentAndPreservesLastSeen(t *testing.T) {
	t.Parallel()
	firstObserved := time.Date(2026, time.July, 10, 12, 0, 0, 0, time.UTC)
	secondObserved := firstObserved.Add(5 * time.Minute)
	currentTime := firstObserved
	var stateMu sync.Mutex
	failures := map[string]bool{}
	clients := map[string]*dynamicfake.FakeDynamicClient{
		"https://alpha.invalid": fakeClient(),
		"https://beta.invalid":  fakeClient(),
	}

	adapter, err := New(
		WithLoadingRules(testLoadingRules(t, testConfig("alpha", "beta"))),
		WithMaxConcurrency(2),
		withClock(func() time.Time {
			stateMu.Lock()
			defer stateMu.Unlock()
			return currentTime
		}),
		withProbe(func(_ context.Context, config *rest.Config) error {
			stateMu.Lock()
			defer stateMu.Unlock()
			if failures[config.Host] {
				return errors.New("synthetic reachability failure")
			}
			return nil
		}),
		withDynamicFactory(func(config *rest.Config) (dynamic.Interface, error) {
			return clients[config.Host], nil
		}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	discovery, err := adapter.Discover(context.Background())
	if err != nil {
		t.Fatalf("first Discover() error = %v", err)
	}
	if len(discovery.Scopes) != 2 || len(discovery.Unreachable) != 0 {
		t.Fatalf("first Discover() = %#v, want two reachable scopes", discovery)
	}

	stateMu.Lock()
	currentTime = secondObserved
	failures["https://beta.invalid"] = true
	stateMu.Unlock()
	discovery, err = adapter.Discover(context.Background())
	if err != nil {
		t.Fatalf("second Discover() error = %v", err)
	}
	if !slices.Equal(discovery.Unreachable, []string{"beta"}) {
		t.Fatalf("Unreachable = %v, want [beta]", discovery.Unreachable)
	}
	if !discovery.Scopes[0].Reachable || discovery.Scopes[0].ObservedAt != secondObserved {
		t.Fatalf("alpha scope = %#v, want newly observed reachable scope", discovery.Scopes[0])
	}
	if discovery.Scopes[1].Reachable || discovery.Scopes[1].ObservedAt != firstObserved {
		t.Fatalf("beta scope = %#v, want unreachable scope preserving last seen", discovery.Scopes[1])
	}
}

func TestDiscoverTimesOutProbeThatIgnoresContext(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	adapter, err := New(
		WithLoadingRules(testLoadingRules(t, testConfig("blocked"))),
		WithProbeTimeout(20*time.Millisecond),
		withProbe(func(_ context.Context, _ *rest.Config) error {
			<-release
			return nil
		}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	started := time.Now()
	discovery, err := adapter.Discover(context.Background())
	close(release)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("Discover() took %s, want bounded probe timeout", elapsed)
	}
	if !slices.Equal(discovery.Unreachable, []string{"blocked"}) {
		t.Fatalf("Unreachable = %v, want [blocked]", discovery.Unreachable)
	}
}

func TestQueryAndReadReturnSourceStampedEvidenceWithPartialCoverage(t *testing.T) {
	t.Parallel()
	observedAt := time.Date(2026, time.July, 10, 13, 0, 0, 0, time.UTC)
	alphaClient := fakeClient(
		pod("api-0", "apps", "registry.example/api:v2", map[string]string{"app": "api"}),
		pod("worker-0", "apps", "registry.example/worker:v1", map[string]string{"app": "worker"}),
	)
	adapter, err := New(
		WithLoadingRules(testLoadingRules(t, testConfig("alpha", "beta"))),
		withClock(func() time.Time { return observedAt }),
		withProbe(func(_ context.Context, config *rest.Config) error {
			if config.Host == "https://beta.invalid" {
				return errors.New("offline")
			}
			return nil
		}),
		withDynamicFactory(func(_ *rest.Config) (dynamic.Interface, error) { return alphaClient, nil }),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := adapter.Query(context.Background(), fleet.Query{
		Kinds:  []fleet.FactKind{fleet.FactInventory},
		Scopes: []string{"alpha", "beta", "missing"},
		Selector: fleet.Selector{
			ResourceKind: "Pod",
			Namespace:    "apps",
			NamePrefix:   "api-",
			Labels:       map[string]string{"app": "api"},
			Image:        "api:v2",
		},
	})
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	if result.Coverage.Requested != 3 || result.Coverage.Reachable != 1 {
		t.Fatalf("Coverage = %#v, want one of three reachable", result.Coverage)
	}
	if !slices.Equal(result.Coverage.Unreachable, []string{"beta", "missing"}) {
		t.Fatalf("Unreachable = %v, want [beta missing]", result.Coverage.Unreachable)
	}
	if len(result.Facts) != 1 {
		t.Fatalf("Facts = %#v, want one selected pod", result.Facts)
	}
	fact := result.Facts[0]
	if fact.Ref.SourceKind != Kind || fact.Ref.Scope != "alpha" || fact.Ref.Name != "api-0" {
		t.Fatalf("Fact ref = %#v, want source-stamped alpha/api-0", fact.Ref)
	}
	if fact.Workspace != fleet.LocalWorkspace || fact.Provenance.Adapter != Kind {
		t.Fatalf("Fact provenance = %#v, workspace = %q", fact.Provenance, fact.Workspace)
	}

	evidence, err := adapter.Read(context.Background(), fact.Ref)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if evidence.Ref.Name != "api-0" || evidence.ObservedAt != observedAt {
		t.Fatalf("Read() evidence = %#v", evidence)
	}
	var object map[string]any
	if err := json.Unmarshal(evidence.Observed, &object); err != nil {
		t.Fatalf("unmarshal observed evidence: %v", err)
	}
	metadata, _ := object["metadata"].(map[string]any)
	if metadata["name"] != "api-0" {
		t.Fatalf("observed metadata = %#v, want api-0", metadata)
	}

	_, err = adapter.Read(context.Background(), fleet.ResourceRef{Scope: "missing", Kind: "Pod", Name: "x"})
	if !errors.Is(err, ErrUnknownScope) {
		t.Fatalf("Read(unknown) error = %v, want ErrUnknownScope", err)
	}
	_, err = adapter.Read(context.Background(), fleet.ResourceRef{Scope: "beta", Kind: "Pod", Name: "x"})
	if !errors.Is(err, ErrUnreachableScope) {
		t.Fatalf("Read(unreachable) error = %v, want ErrUnreachableScope", err)
	}
}

func TestInvalidInputsFailBeforeDiscovery(t *testing.T) {
	t.Parallel()
	probeCalls := 0
	adapter, err := New(
		WithLoadingRules(testLoadingRules(t, testConfig("alpha"))),
		withProbe(func(_ context.Context, _ *rest.Config) error {
			probeCalls++
			return nil
		}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = adapter.Read(context.Background(), fleet.ResourceRef{Scope: "alpha", Kind: "Pod"})
	if !errors.Is(err, ErrInvalidReference) {
		t.Fatalf("Read(invalid) error = %v, want ErrInvalidReference", err)
	}
	_, err = adapter.Query(context.Background(), fleet.Query{
		Selector: fleet.Selector{ResourceKind: "Pod", Labels: map[string]string{"bad key": "value"}},
	})
	if !errors.Is(err, ErrUnsupportedSelector) {
		t.Fatalf("Query(invalid label) error = %v, want ErrUnsupportedSelector", err)
	}
	if probeCalls != 0 {
		t.Fatalf("probe calls = %d, want invalid inputs rejected before credential/network work", probeCalls)
	}
}

func TestQueryTimesOutClientThatIgnoresContext(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	finished := make(chan struct{})
	client := fakeClient()
	client.PrependReactor("list", "pods", func(_ k8stesting.Action) (bool, runtime.Object, error) {
		<-release
		close(finished)
		return true, &unstructured.UnstructuredList{}, nil
	})
	adapter, err := New(
		WithLoadingRules(testLoadingRules(t, testConfig("alpha"))),
		WithRequestTimeout(20*time.Millisecond),
		withProbe(func(_ context.Context, _ *rest.Config) error { return nil }),
		withDynamicFactory(func(_ *rest.Config) (dynamic.Interface, error) { return client, nil }),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	started := time.Now()
	result, err := adapter.Query(context.Background(), fleet.Query{Selector: fleet.Selector{ResourceKind: "Pod"}})
	close(release)
	<-finished
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("Query() took %s, want bounded request timeout", elapsed)
	}
	if result.Coverage.Reachable != 0 || !slices.Equal(result.Coverage.Unreachable, []string{"alpha"}) {
		t.Fatalf("Coverage = %#v, want timed-out alpha surfaced as unreachable", result.Coverage)
	}
}

func TestDefaultProbeExecutesExecCredentialLocally(t *testing.T) {
	if os.Getenv("SITH_EXEC_HELPER") == "1" {
		runExecCredentialHelper()
	}

	const token = "ephemeral-test-token"
	marker := filepath.Join(t.TempDir(), "exec-called")
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/version" {
			http.NotFound(writer, request)
			return
		}
		if request.Header.Get("Authorization") != "Bearer "+token {
			http.Error(writer, "unauthorized", http.StatusUnauthorized)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"gitVersion":"v1.36.1"}`))
	}))
	t.Cleanup(server.Close)

	certificate := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	config := clientcmdapi.Config{
		Clusters: map[string]*clientcmdapi.Cluster{
			"exec": {Server: server.URL, CertificateAuthorityData: certificate},
		},
		AuthInfos: map[string]*clientcmdapi.AuthInfo{
			"exec": {Exec: &clientcmdapi.ExecConfig{
				Command:         os.Args[0],
				Args:            []string{"-test.run=TestDefaultProbeExecutesExecCredentialLocally"},
				APIVersion:      "client.authentication.k8s.io/v1",
				InteractiveMode: clientcmdapi.NeverExecInteractiveMode,
				Env: []clientcmdapi.ExecEnvVar{
					{Name: "SITH_EXEC_HELPER", Value: "1"},
					{Name: "SITH_EXEC_MARKER", Value: marker},
					{Name: "SITH_EXEC_TOKEN", Value: token},
				},
			}},
		},
		Contexts: map[string]*clientcmdapi.Context{
			"exec": {Cluster: "exec", AuthInfo: "exec"},
		},
		CurrentContext: "exec",
	}
	adapter, err := New(
		WithLoadingRules(testLoadingRules(t, config)),
		withDynamicFactory(func(_ *rest.Config) (dynamic.Interface, error) { return fakeClient(), nil }),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	discovery, err := adapter.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(discovery.Scopes) != 1 || !discovery.Scopes[0].Reachable {
		t.Fatalf("Discover() = %#v, want reachable exec context", discovery)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("exec marker: %v", err)
	}
}

func runExecCredentialHelper() {
	marker := os.Getenv("SITH_EXEC_MARKER")
	if marker == "" || os.WriteFile(marker, []byte("called"), 0o600) != nil {
		os.Exit(2)
	}
	credential := map[string]any{
		"apiVersion": "client.authentication.k8s.io/v1",
		"kind":       "ExecCredential",
		"status":     map[string]any{"token": os.Getenv("SITH_EXEC_TOKEN")},
	}
	if json.NewEncoder(os.Stdout).Encode(credential) != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

func testLoadingRules(t *testing.T, config clientcmdapi.Config) *clientcmd.ClientConfigLoadingRules {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config")
	if err := clientcmd.WriteToFile(config, path); err != nil {
		t.Fatalf("write test kubeconfig: %v", err)
	}
	return &clientcmd.ClientConfigLoadingRules{ExplicitPath: path}
}

func testConfig(contexts ...string) clientcmdapi.Config {
	config := clientcmdapi.NewConfig()
	for _, name := range contexts {
		config.Clusters[name] = &clientcmdapi.Cluster{Server: "https://" + name + ".invalid"}
		config.AuthInfos[name] = &clientcmdapi.AuthInfo{}
		config.Contexts[name] = &clientcmdapi.Context{Cluster: name, AuthInfo: name}
	}
	return *config
}

func fakeClient(objects ...runtime.Object) *dynamicfake.FakeDynamicClient {
	listKinds := map[schema.GroupVersionResource]string{
		{Version: "v1", Resource: "pods"}: "PodList",
	}
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), listKinds, objects...)
}

func pod(name, namespace, image string, labels map[string]string) *unstructured.Unstructured {
	unstructuredLabels := make(map[string]any, len(labels))
	for key, value := range labels {
		unstructuredLabels[key] = value
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Pod",
		"metadata": map[string]any{
			"name":      name,
			"namespace": namespace,
			"uid":       name + "-uid",
			"labels":    unstructuredLabels,
		},
		"spec": map[string]any{
			"containers": []any{map[string]any{"name": "app", "image": image}},
		},
	}}
}
