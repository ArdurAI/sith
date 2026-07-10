// SPDX-License-Identifier: Apache-2.0
//go:build e2e && kind

package e2e_test

import (
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

	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/ArdurAI/sith/internal/connector/kubeconfig"
	"github.com/ArdurAI/sith/internal/fleet"
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
