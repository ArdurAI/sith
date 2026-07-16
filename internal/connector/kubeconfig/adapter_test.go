// SPDX-License-Identifier: Apache-2.0

package kubeconfig

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/ArdurAI/sith/internal/connector"
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
		{name: "nil resource resolver", option: withResourceResolver(nil)},
		{name: "nil table factory", option: withTableFactory(nil)},
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

func TestGenericResourceResolutionIsCached(t *testing.T) {
	t.Parallel()
	gvr := schema.GroupVersionResource{Group: "example.io", Version: "v1", Resource: "widgets"}
	client := fakeClientWithKinds(
		map[schema.GroupVersionResource]string{gvr: "WidgetList"},
		&unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "example.io/v1",
			"kind":       "Widget",
			"metadata": map[string]any{
				"name": "sample", "namespace": "apps", "uid": "sample-uid",
			},
		}},
	)
	var resolveCalls atomic.Int32
	adapter, err := New(
		WithLoadingRules(testLoadingRules(t, testConfig("alpha"))),
		withProbe(func(_ context.Context, _ *rest.Config) error { return nil }),
		withDynamicFactory(func(_ *rest.Config) (dynamic.Interface, error) { return client, nil }),
		withResourceResolver(func(_ context.Context, _ *rest.Config, kind string) (resourceSpec, error) {
			resolveCalls.Add(1)
			if kind != "widgets" {
				return resourceSpec{}, fmt.Errorf("unexpected kind %q", kind)
			}
			return resourceSpec{kind: "Widget", gvr: gvr, namespaced: true}, nil
		}),
		withTableFactory(func(_ *rest.Config) (tablePrinter, error) {
			return func(
				_ context.Context, _ resourceSpec, _, _, _ string,
			) (map[string][]fleet.DisplayField, error) {
				return map[string][]fleet.DisplayField{
					tableObjectKey("apps", "sample"): {{Name: "Name", Value: "sample"}, {Name: "Ready", Value: "1/1"}},
				}, nil
			}, nil
		}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	query := fleet.Query{Selector: fleet.Selector{ResourceKind: "widgets", Namespace: "apps"}}
	var result fleet.QueryResult
	for range 2 {
		result, err = adapter.Query(context.Background(), query)
		if err != nil {
			t.Fatalf("Query() error = %v", err)
		}
		if len(result.Facts) != 1 || result.Facts[0].Ref.Kind != "Widget" || len(result.Facts[0].Display) != 2 {
			t.Fatalf("Facts = %#v, want one Widget", result.Facts)
		}
	}
	if resolveCalls.Load() != 1 {
		t.Fatalf("resolver calls = %d, want one cached resolution", resolveCalls.Load())
	}

	evidence, err := adapter.Read(context.Background(), result.Facts[0].Ref)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if evidence.Ref.Name != "sample" || resolveCalls.Load() != 1 {
		t.Fatalf("Read() = %#v, resolver calls = %d", evidence.Ref, resolveCalls.Load())
	}
}

func TestWatchStreamsSnapshotUpsertAndDelete(t *testing.T) {
	t.Parallel()
	client := fakeClient(pod("api-0", "apps", "registry/api:v1", nil))
	adapter, err := New(
		WithLoadingRules(testLoadingRules(t, testConfig("alpha"))),
		withProbe(func(_ context.Context, _ *rest.Config) error { return nil }),
		withDynamicFactory(func(_ *rest.Config) (dynamic.Interface, error) { return client, nil }),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	events, err := adapter.Watch(ctx, "Pod")
	if err != nil {
		t.Fatalf("Watch() error = %v", err)
	}
	snapshot := receiveWatchEvent(ctx, t, events)
	if snapshot.Type != connector.WatchSnapshot || snapshot.Scope != "alpha" || len(snapshot.Facts) != 1 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	waitForWatchAction(ctx, t, client)

	created := pod("api-1", "apps", "registry/api:v2", nil)
	if _, err := client.Resource(schema.GroupVersionResource{Version: "v1", Resource: "pods"}).
		Namespace("apps").Create(ctx, created, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create watched pod: %v", err)
	}
	upsert := receiveWatchEvent(ctx, t, events)
	if upsert.Type != connector.WatchUpsert || upsert.Fact.Ref.Name != "api-1" {
		t.Fatalf("upsert = %#v", upsert)
	}
	if err := client.Resource(schema.GroupVersionResource{Version: "v1", Resource: "pods"}).
		Namespace("apps").Delete(ctx, "api-1", metav1.DeleteOptions{}); err != nil {
		t.Fatalf("delete watched pod: %v", err)
	}
	deleted := receiveWatchEvent(ctx, t, events)
	if deleted.Type != connector.WatchDelete || deleted.Ref.Name != "api-1" {
		t.Fatalf("delete = %#v", deleted)
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
	t.Cleanup(func() { close(release) })
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

func TestDiscoverBoundsProbeThatIgnoresContextWithoutStallingPeers(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	var blockedCalls atomic.Int32
	var peerCalls atomic.Int32
	adapter, err := New(
		WithLoadingRules(testLoadingRules(t, testConfig("blocked-a", "blocked-b", "peer"))),
		WithProbeTimeout(20*time.Millisecond),
		WithMaxConcurrency(4),
		withProbe(func(_ context.Context, config *rest.Config) error {
			if config.Host == "https://peer.invalid" {
				peerCalls.Add(1)
				return nil
			}
			blockedCalls.Add(1)
			<-release
			return nil
		}),
		withDynamicFactory(func(_ *rest.Config) (dynamic.Interface, error) { return fakeClient(), nil }),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	for run := 1; run <= 3; run++ {
		discovery, err := adapter.Discover(context.Background())
		if err != nil {
			t.Fatalf("Discover() run %d error = %v", run, err)
		}
		if !slices.Equal(discovery.Unreachable, []string{"blocked-a", "blocked-b"}) {
			t.Fatalf("Discover() run %d unreachable = %v, want [blocked-a blocked-b]", run, discovery.Unreachable)
		}
		if len(discovery.Scopes) != 3 || !discovery.Scopes[2].Reachable {
			t.Fatalf("Discover() run %d scopes = %#v, want reachable peer", run, discovery.Scopes)
		}
	}

	if got := blockedCalls.Load(); got != 4 {
		t.Fatalf("blocked probe calls = %d, want each blocked context bounded at 2", got)
	}
	if got := peerCalls.Load(); got != 3 {
		t.Fatalf("peer probe calls = %d, want one healthy probe per discovery", got)
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

func TestDefaultProbeExecCredentialMixedCloudIsolatedAndMemoryOnly(t *testing.T) {
	if os.Getenv("SITH_EXEC_HELPER") == "1" {
		runExecCredentialHelper()
	}

	sandbox := t.TempDir()
	for variable, directory := range map[string]string{
		"HOME":            filepath.Join(sandbox, "home"),
		"TMPDIR":          filepath.Join(sandbox, "tmp"),
		"XDG_CACHE_HOME":  filepath.Join(sandbox, "cache"),
		"XDG_CONFIG_HOME": filepath.Join(sandbox, "config-home"),
	} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatalf("create %s sandbox: %v", variable, err)
		}
		t.Setenv(variable, directory)
	}

	type cloudContext struct {
		name       string
		apiVersion string
		tokenEnv   string
		token      string
		calls      atomic.Int32
	}
	clouds := []*cloudContext{
		{name: "aws", apiVersion: "client.authentication.k8s.io/v1", tokenEnv: "SITH_EXEC_TEST_AWS", token: "aws-ephemeral-token-81"},
		{name: "azure", apiVersion: "client.authentication.k8s.io/v1beta1", tokenEnv: "SITH_EXEC_TEST_AZURE", token: "azure-ephemeral-token-81"},
		{name: "gcp", apiVersion: "client.authentication.k8s.io/v1", tokenEnv: "SITH_EXEC_TEST_GCP", token: "gcp-ephemeral-token-81"},
	}
	config := clientcmdapi.NewConfig()
	secretMaterial := make([]string, 0, len(clouds)+1)
	for _, cloud := range clouds {
		cloud := cloud
		t.Setenv(cloud.tokenEnv, cloud.token)
		secretMaterial = append(secretMaterial, cloud.token)
		server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			cloud.calls.Add(1)
			if request.URL.Path != "/version" {
				http.NotFound(writer, request)
				return
			}
			if request.Header.Get("Authorization") != "Bearer "+cloud.token {
				http.Error(writer, "unauthorized", http.StatusUnauthorized)
				return
			}
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"gitVersion":"v1.36.1"}`))
		}))
		t.Cleanup(server.Close)

		execEnv := []clientcmdapi.ExecEnvVar{
			{Name: "SITH_EXEC_HELPER", Value: "1"},
			{Name: "SITH_EXEC_TOKEN_ENV", Value: cloud.tokenEnv},
		}
		if cloud.name == "gcp" {
			keyDER, err := x509.MarshalPKCS8PrivateKey(server.TLS.Certificates[0].PrivateKey)
			if err != nil {
				t.Fatalf("marshal test client key: %v", err)
			}
			clientCertificate := string(pem.EncodeToMemory(&pem.Block{
				Type: "CERTIFICATE", Bytes: server.Certificate().Raw,
			}))
			clientKey := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
			const certificateEnv = "SITH_EXEC_TEST_GCP_CERTIFICATE"
			const keyEnv = "SITH_EXEC_TEST_GCP_KEY"
			t.Setenv(certificateEnv, clientCertificate)
			t.Setenv(keyEnv, clientKey)
			secretMaterial = append(secretMaterial, clientKey)
			execEnv = append(execEnv,
				clientcmdapi.ExecEnvVar{Name: "SITH_EXEC_CERTIFICATE_ENV", Value: certificateEnv},
				clientcmdapi.ExecEnvVar{Name: "SITH_EXEC_KEY_ENV", Value: keyEnv},
			)
		}

		config.Clusters[cloud.name] = &clientcmdapi.Cluster{
			Server: server.URL,
			CertificateAuthorityData: pem.EncodeToMemory(&pem.Block{
				Type: "CERTIFICATE", Bytes: server.Certificate().Raw,
			}),
		}
		config.AuthInfos[cloud.name] = &clientcmdapi.AuthInfo{Exec: &clientcmdapi.ExecConfig{
			Command:         os.Args[0],
			Args:            []string{"-test.run=TestDefaultProbeExecCredentialMixedCloudIsolatedAndMemoryOnly"},
			APIVersion:      cloud.apiVersion,
			InteractiveMode: clientcmdapi.NeverExecInteractiveMode,
			Env:             execEnv,
		}}
		config.Contexts[cloud.name] = &clientcmdapi.Context{Cluster: cloud.name, AuthInfo: cloud.name}
	}
	config.Clusters["broken"] = config.Clusters["aws"].DeepCopy()
	config.AuthInfos["broken"] = &clientcmdapi.AuthInfo{Exec: &clientcmdapi.ExecConfig{
		Command:         filepath.Join(sandbox, "missing-exec-helper"),
		APIVersion:      "client.authentication.k8s.io/v1",
		InteractiveMode: clientcmdapi.NeverExecInteractiveMode,
	}}
	config.Contexts["broken"] = &clientcmdapi.Context{Cluster: "broken", AuthInfo: "broken"}
	config.CurrentContext = "aws"
	kubeconfigPath := filepath.Join(sandbox, "kubeconfig")
	if err := clientcmd.WriteToFile(*config, kubeconfigPath); err != nil {
		t.Fatalf("write mixed-cloud kubeconfig: %v", err)
	}

	adapter, err := New(
		WithExplicitPath(kubeconfigPath),
		withDynamicFactory(func(_ *rest.Config) (dynamic.Interface, error) { return fakeClient(), nil }),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	discovery, err := adapter.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(discovery.Scopes) != 4 || !slices.Equal(discovery.Unreachable, []string{"broken"}) {
		t.Fatalf("Discover() = %#v, want three reachable contexts and isolated broken helper", discovery)
	}
	for _, cloud := range clouds {
		if cloud.calls.Load() != 1 {
			t.Errorf("%s probe calls = %d, want one authenticated request", cloud.name, cloud.calls.Load())
		}
	}

	discoveryJSON, err := json.Marshal(discovery)
	if err != nil {
		t.Fatalf("marshal discovery: %v", err)
	}
	assertNoSecretMaterial(t, "fleet discovery", discoveryJSON, secretMaterial)
	adapter.mu.RLock()
	for name, retained := range adapter.configs {
		if retained.BearerToken != "" || retained.BearerTokenFile != "" || len(retained.KeyData) != 0 {
			t.Errorf("%s retained credential output in rest.Config", name)
		}
		execConfigJSON, marshalErr := json.Marshal(retained.ExecProvider)
		if marshalErr != nil {
			t.Errorf("marshal %s exec provider: %v", name, marshalErr)
			continue
		}
		assertNoSecretMaterial(t, name+" retained exec config", execConfigJSON, secretMaterial)
	}
	adapter.mu.RUnlock()
	assertSandboxExcludesSecrets(t, sandbox, secretMaterial)
}

func TestDefaultProbeExecCredentialTimeoutIsContained(t *testing.T) {
	if os.Getenv("SITH_EXEC_HELPER") == "1" {
		runExecCredentialHelper()
	}

	sandbox := t.TempDir()
	startPath := filepath.Join(sandbox, "helper-started")
	releasePath := filepath.Join(sandbox, "release-helper")
	t.Cleanup(func() { _ = os.WriteFile(releasePath, []byte("release"), 0o600) })
	const tokenEnv = "SITH_EXEC_TEST_BLOCKED_TOKEN"
	const token = "blocked-ephemeral-token-181"
	t.Setenv(tokenEnv, token)

	var blockedRequests atomic.Int32
	blockedServer := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		blockedRequests.Add(1)
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
	t.Cleanup(blockedServer.Close)

	var peerRequests atomic.Int32
	peerServer := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		peerRequests.Add(1)
		if request.URL.Path != "/version" {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"gitVersion":"v1.36.1"}`))
	}))
	t.Cleanup(peerServer.Close)

	config := clientcmdapi.NewConfig()
	for name, server := range map[string]*httptest.Server{"blocked": blockedServer, "peer": peerServer} {
		config.Clusters[name] = &clientcmdapi.Cluster{
			Server: server.URL,
			CertificateAuthorityData: pem.EncodeToMemory(&pem.Block{
				Type: "CERTIFICATE", Bytes: server.Certificate().Raw,
			}),
		}
		config.AuthInfos[name] = &clientcmdapi.AuthInfo{}
		config.Contexts[name] = &clientcmdapi.Context{Cluster: name, AuthInfo: name}
	}
	config.AuthInfos["blocked"].Exec = &clientcmdapi.ExecConfig{
		Command:         os.Args[0],
		Args:            []string{"-test.run=TestDefaultProbeExecCredentialTimeoutIsContained"},
		APIVersion:      "client.authentication.k8s.io/v1",
		InteractiveMode: clientcmdapi.NeverExecInteractiveMode,
		Env: []clientcmdapi.ExecEnvVar{
			{Name: "SITH_EXEC_HELPER", Value: "1"},
			{Name: "SITH_EXEC_TOKEN_ENV", Value: tokenEnv},
			{Name: "SITH_EXEC_START_PATH", Value: startPath},
			{Name: "SITH_EXEC_RELEASE_PATH", Value: releasePath},
		},
	}

	adapter, err := New(
		WithLoadingRules(testLoadingRules(t, *config)),
		WithProbeTimeout(100*time.Millisecond),
		WithMaxConcurrency(4),
		withDynamicFactory(func(_ *rest.Config) (dynamic.Interface, error) { return fakeClient(), nil }),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	for run := 1; run <= 3; run++ {
		discovery, err := adapter.Discover(context.Background())
		if err != nil {
			t.Fatalf("Discover() run %d error = %v", run, err)
		}
		if !slices.Equal(discovery.Unreachable, []string{"blocked"}) {
			t.Fatalf("Discover() run %d unreachable = %v, want [blocked]", run, discovery.Unreachable)
		}
	}
	if got := peerRequests.Load(); got != 3 {
		t.Fatalf("peer requests = %d, want one request per discovery", got)
	}
	if got := blockedRequests.Load(); got != 0 {
		t.Fatalf("blocked requests before helper release = %d, want 0", got)
	}
	started, err := os.ReadFile(startPath)
	if err != nil {
		t.Fatalf("read helper start marker: %v", err)
	}
	if got := string(started); got != "started\n" {
		t.Fatalf("helper start markers = %q, want one process", got)
	}

	if err := os.WriteFile(releasePath, []byte("release"), 0o600); err != nil {
		t.Fatalf("release exec helper: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		adapter.gate.mu.Lock()
		active := adapter.gate.byScope["blocked"]
		adapter.gate.mu.Unlock()
		if active == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("retained operations = %d after helper release, want 0", active)
		}
		time.Sleep(10 * time.Millisecond)
	}
	discovery, err := adapter.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover() after helper release error = %v", err)
	}
	if len(discovery.Unreachable) != 0 {
		t.Fatalf("Discover() after helper release unreachable = %v, want none", discovery.Unreachable)
	}
	if blockedRequests.Load() == 0 {
		t.Fatal("blocked context made no authenticated request after helper release")
	}
	discoveryJSON, err := json.Marshal(discovery)
	if err != nil {
		t.Fatalf("marshal discovery: %v", err)
	}
	assertNoSecretMaterial(t, "fleet discovery", discoveryJSON, []string{token})
	assertSandboxExcludesSecrets(t, sandbox, []string{token})
}

func runExecCredentialHelper() {
	var input struct {
		APIVersion string `json:"apiVersion"`
	}
	if err := json.Unmarshal([]byte(os.Getenv("KUBERNETES_EXEC_INFO")), &input); err != nil || input.APIVersion == "" {
		os.Exit(2)
	}
	tokenEnv := os.Getenv("SITH_EXEC_TOKEN_ENV")
	if tokenEnv == "" || os.Getenv(tokenEnv) == "" {
		os.Exit(2)
	}
	if startPath := os.Getenv("SITH_EXEC_START_PATH"); startPath != "" {
		marker, err := os.OpenFile(startPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			os.Exit(2)
		}
		if _, err := marker.WriteString("started\n"); err != nil || marker.Close() != nil {
			os.Exit(2)
		}
		releasePath := os.Getenv("SITH_EXEC_RELEASE_PATH")
		deadline := time.Now().Add(30 * time.Second)
		for {
			if _, err := os.Stat(releasePath); err == nil {
				break
			} else if !errors.Is(err, fs.ErrNotExist) || time.Now().After(deadline) {
				os.Exit(2)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	status := map[string]any{"token": os.Getenv(tokenEnv)}
	certificateEnv, keyEnv := os.Getenv("SITH_EXEC_CERTIFICATE_ENV"), os.Getenv("SITH_EXEC_KEY_ENV")
	if certificateEnv != "" || keyEnv != "" {
		if certificateEnv == "" || keyEnv == "" || os.Getenv(certificateEnv) == "" || os.Getenv(keyEnv) == "" {
			os.Exit(2)
		}
		status["clientCertificateData"] = os.Getenv(certificateEnv)
		status["clientKeyData"] = os.Getenv(keyEnv)
	}
	credential := map[string]any{
		"apiVersion": input.APIVersion,
		"kind":       "ExecCredential",
		"status":     status,
	}
	if json.NewEncoder(os.Stdout).Encode(credential) != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

func assertNoSecretMaterial(t *testing.T, subject string, payload []byte, secrets []string) {
	t.Helper()
	for index, secret := range secrets {
		if secret != "" && strings.Contains(string(payload), secret) {
			t.Errorf("%s contains secret material %d", subject, index)
		}
	}
}

func assertSandboxExcludesSecrets(t *testing.T, root string, secrets []string) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		payload, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		assertNoSecretMaterial(t, "sandbox file "+filepath.Base(path), payload, secrets)
		return nil
	})
	if err != nil {
		t.Fatalf("scan credential sandbox: %v", err)
	}
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
	return fakeClientWithKinds(listKinds, objects...)
}

func fakeClientWithKinds(
	listKinds map[schema.GroupVersionResource]string,
	objects ...runtime.Object,
) *dynamicfake.FakeDynamicClient {
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), listKinds, objects...)
}

func receiveWatchEvent(
	ctx context.Context,
	t *testing.T,
	events <-chan connector.WatchEvent,
) connector.WatchEvent {
	t.Helper()
	select {
	case event, open := <-events:
		if !open {
			t.Fatal("watch event channel closed")
		}
		return event
	case <-ctx.Done():
		t.Fatalf("wait for watch event: %v", ctx.Err())
		return connector.WatchEvent{}
	}
}

func waitForWatchAction(ctx context.Context, t *testing.T, client *dynamicfake.FakeDynamicClient) {
	t.Helper()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		for _, action := range client.Actions() {
			if action.GetVerb() == "watch" {
				return
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for watch action: %v", ctx.Err())
		case <-ticker.C:
		}
	}
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
