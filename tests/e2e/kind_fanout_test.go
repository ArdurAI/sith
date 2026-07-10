// SPDX-License-Identifier: Apache-2.0
//go:build e2e && kind

package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/ArdurAI/sith/internal/connector/kubeconfig"
	"github.com/ArdurAI/sith/internal/fleet"
	"github.com/ArdurAI/sith/internal/fleetcache"
	"github.com/ArdurAI/sith/internal/hydrate"
)

const defaultKindNodeImage = "kindest/node:v1.36.1@sha256:3489c7674813ba5d8b1a9977baea8a6e553784dab7b84759d1014dbd78f7ebd5"

func TestKindFleetFanout(t *testing.T) {
	kindBinary := environmentOr("KIND_BIN", "kind")
	if _, err := exec.LookPath(kindBinary); err != nil {
		t.Fatalf("find kind binary %q: %v", kindBinary, err)
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Fatalf("find docker: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	version := runCommand(ctx, t, "", kindBinary, "version")
	if !strings.Contains(version, "v0.32.0") {
		t.Fatalf("kind version = %q, want v0.32.0", version)
	}

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	clusterNames := []string{"sith-e2e-a-" + suffix, "sith-e2e-b-" + suffix}
	image := environmentOr("KIND_NODE_IMAGE", defaultKindNodeImage)
	created := make([]string, 0, len(clusterNames))
	t.Cleanup(func() {
		for _, name := range created {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 2*time.Minute)
			command := exec.CommandContext(cleanupCtx, kindBinary, "delete", "cluster", "--name", name)
			output, err := command.CombinedOutput()
			cleanupCancel()
			if err != nil {
				t.Errorf("delete kind cluster %s: %v\n%s", name, err, output)
			}
		}
	})

	for _, name := range clusterNames {
		created = append(created, name)
		runCommand(ctx, t, "", kindBinary, "create", "cluster", "--name", name, "--image", image, "--wait", "180s")
	}

	kubeconfigPath := mergedKindKubeconfig(ctx, t, kindBinary, clusterNames)
	seedKindResources(ctx, t, kubeconfigPath, clusterNames)
	adapter, err := kubeconfig.New(
		kubeconfig.WithExplicitPath(kubeconfigPath),
		kubeconfig.WithProbeTimeout(5*time.Second),
		kubeconfig.WithRequestTimeout(15*time.Second),
	)
	if err != nil {
		t.Fatalf("construct kubeconfig adapter: %v", err)
	}

	discovery, err := adapter.Discover(ctx)
	if err != nil {
		t.Fatalf("discover real kind contexts: %v", err)
	}
	deadContext := "kind-sith-e2e-unreachable"
	if len(discovery.Scopes) != 3 || !slices.Equal(discovery.Unreachable, []string{deadContext}) {
		t.Fatalf("discovery = %#v, want two reachable kind contexts and %q unreachable", discovery, deadContext)
	}

	result, err := adapter.Query(ctx, fleet.Query{
		Kinds: []fleet.FactKind{fleet.FactInventory},
		Selector: fleet.Selector{
			ResourceKind: "Namespace",
			NamePrefix:   "kube-",
		},
	})
	if err != nil {
		t.Fatalf("query namespaces across kind contexts: %v", err)
	}
	if result.Coverage.Requested != 3 || result.Coverage.Reachable != 2 ||
		!slices.Equal(result.Coverage.Unreachable, []string{deadContext}) {
		t.Fatalf("query coverage = %#v, want two of three reachable", result.Coverage)
	}
	liveScopes := map[string]bool{
		"kind-" + clusterNames[0]: false,
		"kind-" + clusterNames[1]: false,
	}
	for _, fact := range result.Facts {
		if fact.Ref.Kind == "Namespace" && strings.HasPrefix(fact.Ref.Name, "kube-") {
			liveScopes[fact.Ref.Scope] = true
		}
	}
	for scope, seen := range liveScopes {
		if !seen {
			t.Errorf("query did not return a source-stamped namespace from %s", scope)
		}
	}

	root := repositoryRoot(t)
	binary := filepath.Join(t.TempDir(), "sith")
	runCommand(ctx, t, root, "go", "build", "-trimpath", "-o", binary, "./cmd/sith")
	command := exec.CommandContext(ctx, binary, "clusters", "--output", "json")
	command.Env = append(os.Environ(), "KUBECONFIG="+kubeconfigPath, "XDG_CONFIG_HOME="+t.TempDir())
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("run sith clusters against kind: %v\n%s", err, output)
	}
	var fleetResult fleet.FleetResult
	if err := json.Unmarshal(output, &fleetResult); err != nil {
		t.Fatalf("decode sith clusters output %q: %v", output, err)
	}
	if len(fleetResult.Clusters) != 3 || fleetResult.Coverage.Requested != 3 ||
		fleetResult.Coverage.Reachable != 2 || !slices.Equal(fleetResult.Coverage.Unreachable, []string{deadContext}) {
		t.Fatalf("sith clusters = %#v, want two live and one unreachable context", fleetResult)
	}

	getOutput, getStderr, err := runSith(ctx, binary, kubeconfigPath, "get", "pods", "-A", "--all-clusters", "--output", "json")
	if err != nil {
		t.Fatalf("run sith get against kind: %v\nstdout=%s\nstderr=%s", err, getOutput, getStderr)
	}
	var getSnapshot fleetcache.Snapshot
	if err := json.Unmarshal(getOutput, &getSnapshot); err != nil {
		t.Fatalf("decode sith get output %q: %v", getOutput, err)
	}
	if getSnapshot.Coverage.Requested != 3 || getSnapshot.Coverage.Reachable != 2 ||
		!slices.Equal(getSnapshot.Coverage.Unreachable, []string{deadContext}) {
		t.Fatalf("get coverage = %#v, want two of three", getSnapshot.Coverage)
	}
	assertCachedRecord(t, getSnapshot, "kind-"+clusterNames[0], "sith-vuln-sample", false)
	assertCachedRecord(t, getSnapshot, "kind-"+clusterNames[1], "sith-worker-sample", false)
	if !strings.Contains(getStderr, "warning: covered 2/3 clusters") {
		t.Fatalf("get stderr = %q, want partial coverage warning", getStderr)
	}

	genericOutput, genericStderr, err := runSith(
		ctx, binary, kubeconfigPath, "get", "configmaps", "-A", "--all-clusters", "--output", "json",
	)
	if err != nil {
		t.Fatalf("run generic sith get against kind: %v\nstdout=%s\nstderr=%s", err, genericOutput, genericStderr)
	}
	var genericSnapshot fleetcache.Snapshot
	if err := json.Unmarshal(genericOutput, &genericSnapshot); err != nil {
		t.Fatalf("decode generic sith get output %q: %v", genericOutput, err)
	}
	genericScopes := map[string]bool{
		"kind-" + clusterNames[0]: false,
		"kind-" + clusterNames[1]: false,
	}
	for _, record := range genericSnapshot.Records {
		if record.Kind == "ConfigMap" && record.Name == "sith-generic-sample" {
			genericScopes[record.Cluster] = true
		}
	}
	for scope, seen := range genericScopes {
		if !seen {
			t.Errorf("generic lens did not return a ConfigMap from %s", scope)
		}
	}
	if genericSnapshot.Coverage.Reachable != 2 || !strings.Contains(genericStderr, "warning: covered 2/3 clusters") {
		t.Fatalf("generic coverage/stderr = %#v/%q, want partial two-of-three", genericSnapshot.Coverage, genericStderr)
	}

	searchOutput, searchStderr, err := runSith(ctx, binary, kubeconfigPath, "search", "image:*log4j*", "--output", "json")
	if err != nil {
		t.Fatalf("run sith search against kind: %v\nstdout=%s\nstderr=%s", err, searchOutput, searchStderr)
	}
	var searchSnapshot fleetcache.Snapshot
	if err := json.Unmarshal(searchOutput, &searchSnapshot); err != nil {
		t.Fatalf("decode sith search output: %v", err)
	}
	if len(searchSnapshot.Records) != 1 || searchSnapshot.Records[0].Name != "sith-vuln-sample" ||
		searchSnapshot.Records[0].Cluster != "kind-"+clusterNames[0] {
		t.Fatalf("search records = %#v", searchSnapshot.Records)
	}

	correlateOutput, correlateStderr, err := runSith(
		ctx, binary, kubeconfigPath, "correlate", "deploy/sith-payments", "status!=Healthy", "--output", "json",
	)
	if err != nil {
		t.Fatalf("run sith correlate against kind: %v\nstdout=%s\nstderr=%s", err, correlateOutput, correlateStderr)
	}
	var correlateSnapshot fleetcache.Snapshot
	if err := json.Unmarshal(correlateOutput, &correlateSnapshot); err != nil {
		t.Fatalf("decode sith correlate output: %v", err)
	}
	if len(correlateSnapshot.Records) != 1 || correlateSnapshot.Records[0].Cluster != "kind-"+clusterNames[1] ||
		correlateSnapshot.Records[0].Status == "Healthy" {
		t.Fatalf("correlation records = %#v, want unhealthy beta deployment", correlateSnapshot.Records)
	}

	store := fleetcache.New()
	hydrator, err := hydrate.New(adapter, store)
	if err != nil {
		t.Fatalf("construct real hydrator: %v", err)
	}
	if err := hydrator.SyncOnce(ctx); err != nil {
		t.Fatalf("initial real hydration: %v", err)
	}
	initialCache := store.Query(fleetcache.Query{Kind: "Pod"})
	assertCachedRecord(t, initialCache, "kind-"+clusterNames[1], "sith-worker-sample", false)

	runCommand(ctx, t, "", kindBinary, "delete", "cluster", "--name", clusterNames[1])
	created = created[:1]
	if err := hydrator.SyncOnce(ctx); err != nil {
		t.Fatalf("degraded real hydration: %v", err)
	}
	degraded := store.Query(fleetcache.Query{Kind: "Pod"})
	assertCachedRecord(t, degraded, "kind-"+clusterNames[1], "sith-worker-sample", true)
	if degraded.Coverage.Reachable != 1 || !slices.Contains(degraded.Coverage.Unreachable, "kind-"+clusterNames[1]) ||
		!slices.Contains(degraded.Coverage.Unreachable, deadContext) {
		t.Fatalf("degraded coverage = %#v, want beta and dead context unreachable", degraded.Coverage)
	}
}

func seedKindResources(ctx context.Context, t *testing.T, kubeconfigPath string, clusters []string) {
	t.Helper()
	rawConfig, err := clientcmd.LoadFromFile(kubeconfigPath)
	if err != nil {
		t.Fatalf("load merged kubeconfig: %v", err)
	}
	for index, cluster := range clusters {
		contextName := "kind-" + cluster
		clientConfig := clientcmd.NewNonInteractiveClientConfig(
			*rawConfig, contextName, &clientcmd.ConfigOverrides{}, &clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfigPath},
		)
		restConfig, err := clientConfig.ClientConfig()
		if err != nil {
			t.Fatalf("build client config for %s: %v", contextName, err)
		}
		client, err := dynamic.NewForConfig(restConfig)
		if err != nil {
			t.Fatalf("build dynamic client for %s: %v", contextName, err)
		}
		podName, podImage := "sith-vuln-sample", "registry.example/log4j-demo:v1"
		if index == 1 {
			podName, podImage = "sith-worker-sample", "registry.example/worker:v1"
		}
		pod := &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "Pod",
			"metadata":   map[string]any{"name": podName, "namespace": "default", "labels": map[string]any{"app": podName}},
			"spec": map[string]any{
				"containers": []any{map[string]any{"name": "app", "image": podImage}},
			},
		}}
		if _, err := client.Resource(schema.GroupVersionResource{Version: "v1", Resource: "pods"}).
			Namespace("default").Create(ctx, pod, metav1.CreateOptions{}); err != nil {
			t.Fatalf("create pod in %s: %v", contextName, err)
		}
		configMap := &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata":   map[string]any{"name": "sith-generic-sample", "namespace": "default"},
			"data":       map[string]any{"cluster": contextName},
		}}
		if _, err := client.Resource(schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}).
			Namespace("default").Create(ctx, configMap, metav1.CreateOptions{}); err != nil {
			t.Fatalf("create configmap in %s: %v", contextName, err)
		}
		replicas := int64(index)
		deployment := &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "apps/v1",
			"kind":       "Deployment",
			"metadata":   map[string]any{"name": "sith-payments", "namespace": "default"},
			"spec": map[string]any{
				"replicas": replicas,
				"selector": map[string]any{"matchLabels": map[string]any{"app": "sith-payments"}},
				"template": map[string]any{
					"metadata": map[string]any{"labels": map[string]any{"app": "sith-payments"}},
					"spec": map[string]any{"containers": []any{map[string]any{
						"name": "app", "image": "registry.example/does-not-exist:v1",
					}}},
				},
			},
		}}
		if _, err := client.Resource(schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}).
			Namespace("default").Create(ctx, deployment, metav1.CreateOptions{}); err != nil {
			t.Fatalf("create deployment in %s: %v", contextName, err)
		}
	}
}

func runSith(ctx context.Context, binary, kubeconfigPath string, args ...string) ([]byte, string, error) {
	command := exec.CommandContext(ctx, binary, args...)
	command.Env = append(os.Environ(),
		"KUBECONFIG="+kubeconfigPath,
		"XDG_CONFIG_HOME="+filepath.Join(filepath.Dir(kubeconfigPath), "config-home"),
	)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	return stdout.Bytes(), stderr.String(), err
}

func assertCachedRecord(t *testing.T, snapshot fleetcache.Snapshot, cluster, name string, stale bool) {
	t.Helper()
	for _, record := range snapshot.Records {
		if record.Cluster == cluster && record.Name == name {
			if record.Stale != stale {
				t.Fatalf("record %s/%s stale = %t, want %t", cluster, name, record.Stale, stale)
			}
			return
		}
	}
	t.Fatalf("record %s/%s missing from %#v", cluster, name, snapshot.Records)
}

func mergedKindKubeconfig(ctx context.Context, t *testing.T, kindBinary string, clusters []string) string {
	t.Helper()
	merged := clientcmdapi.NewConfig()
	for _, cluster := range clusters {
		data := runCommandBytes(ctx, t, "", kindBinary, "get", "kubeconfig", "--name", cluster)
		config, err := clientcmd.Load(data)
		if err != nil {
			t.Fatalf("decode kind kubeconfig for %s: %v", cluster, err)
		}
		mergeConfigMaps(merged, config)
		if merged.CurrentContext == "" {
			merged.CurrentContext = config.CurrentContext
		}
	}

	const deadContext = "kind-sith-e2e-unreachable"
	merged.Clusters[deadContext] = &clientcmdapi.Cluster{Server: "https://127.0.0.1:1"}
	merged.AuthInfos[deadContext] = &clientcmdapi.AuthInfo{}
	merged.Contexts[deadContext] = &clientcmdapi.Context{Cluster: deadContext, AuthInfo: deadContext}

	path := filepath.Join(t.TempDir(), "kubeconfig")
	if err := clientcmd.WriteToFile(*merged, path); err != nil {
		t.Fatalf("write merged kind kubeconfig: %v", err)
	}
	return path
}

func mergeConfigMaps(destination, source *clientcmdapi.Config) {
	for name, cluster := range source.Clusters {
		destination.Clusters[name] = cluster
	}
	for name, authInfo := range source.AuthInfos {
		destination.AuthInfos[name] = authInfo
	}
	for name, contextConfig := range source.Contexts {
		destination.Contexts[name] = contextConfig
	}
}

func environmentOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func runCommand(ctx context.Context, t *testing.T, directory, name string, args ...string) string {
	t.Helper()
	return string(runCommandBytes(ctx, t, directory, name, args...))
}

func runCommandBytes(ctx context.Context, t *testing.T, directory, name string, args ...string) []byte {
	t.Helper()
	command := exec.CommandContext(ctx, name, args...)
	command.Dir = directory
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("run %s %s: %v\n%s", name, strings.Join(args, " "), err, output)
	}
	return output
}
