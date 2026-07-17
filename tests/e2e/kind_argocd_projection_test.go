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
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/ArdurAI/sith/internal/connector/argocd"
	"github.com/ArdurAI/sith/internal/fleet"
)

func TestKindArgoApplicationProjection(t *testing.T) {
	kindBinary := environmentOr("KIND_BIN", "kind")
	if _, err := exec.LookPath(kindBinary); err != nil {
		t.Fatalf("find kind binary %q: %v", kindBinary, err)
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Fatalf("find docker: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	clusterName := fmt.Sprintf("sith-argocd-e2e-%d", time.Now().UnixNano())
	image := environmentOr("KIND_NODE_IMAGE", defaultKindNodeImage)
	runCommand(ctx, t, "", kindBinary, "create", "cluster", "--name", clusterName, "--image", image, "--wait", "180s")
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cleanupCancel()
		command := exec.CommandContext(cleanupCtx, kindBinary, "delete", "cluster", "--name", clusterName)
		if output, err := command.CombinedOutput(); err != nil {
			t.Errorf("delete kind cluster %s: %v\n%s", clusterName, err, output)
		}
	})

	kubeconfigPath := filepath.Join(t.TempDir(), "kubeconfig")
	kubeconfig := runCommand(ctx, t, "", kindBinary, "get", "kubeconfig", "--name", clusterName)
	if err := os.WriteFile(kubeconfigPath, []byte(kubeconfig), 0o600); err != nil {
		t.Fatalf("write kind kubeconfig: %v", err)
	}
	contextName := "kind-" + clusterName
	client := dynamicClientForContext(t, kubeconfigPath, contextName)

	crdResource := client.Resource(schema.GroupVersionResource{
		Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions",
	})
	crd := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apiextensions.k8s.io/v1",
		"kind":       "CustomResourceDefinition",
		"metadata":   map[string]any{"name": "applications.argoproj.io"},
		"spec": map[string]any{
			"group": "argoproj.io", "scope": "Namespaced",
			"names": map[string]any{
				"plural": "applications", "singular": "application", "kind": "Application", "listKind": "ApplicationList",
			},
			"versions": []any{map[string]any{
				"name": "v1alpha1", "served": true, "storage": true,
				"schema": map[string]any{"openAPIV3Schema": map[string]any{
					"type": "object", "x-kubernetes-preserve-unknown-fields": true,
				}},
			}},
		},
	}}
	if _, err := crdResource.Create(ctx, crd, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create minimal Argo Application CRD: %v", err)
	}
	waitForCRDEstablished(ctx, t, crdResource, "applications.argoproj.io")

	applications := client.Resource(schema.GroupVersionResource{
		Group: "argoproj.io", Version: "v1alpha1", Resource: "applications",
	}).Namespace("default")
	application := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "argoproj.io/v1alpha1",
		"kind":       "Application",
		"metadata":   map[string]any{"name": "payments", "namespace": "default"},
		"spec": map[string]any{
			"project": "default",
			"source": map[string]any{
				"repoURL": "https://kind-token:kind-password@git.example/ardur/payments.git?token=do-not-retain",
				"path":    "clusters/kind/payments", "targetRevision": "main",
				"helm": map[string]any{"parameters": []any{map[string]any{"name": "secret", "value": "raw-kind-secret"}}},
			},
			"destination": map[string]any{"name": "in-cluster", "namespace": "payments"},
		},
		"status": map[string]any{
			"health": map[string]any{"status": "Healthy"},
			"sync":   map[string]any{"status": "OutOfSync", "revision": "abc123"},
			"history": []any{map[string]any{
				"id": int64(1), "revision": "abc123", "deployedAt": "2026-07-16T20:00:00Z",
			}},
		},
	}}
	if _, err := applications.Create(ctx, application, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create real kind Argo Application: %v", err)
	}
	observed, err := applications.Get(ctx, "payments", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("read real kind Argo Application: %v", err)
	}
	facts, err := argocd.ProjectApplication(argocd.Projection{
		Workspace: fleet.LocalWorkspace, Scope: contextName, ObservedAt: time.Now().UTC(), Application: *observed,
	})
	if err != nil {
		t.Fatalf("project real kind Argo Application: %v", err)
	}
	if len(facts) != 4 {
		t.Fatalf("real kind Argo facts = %#v, want desired, health, drift, and timeline", facts)
	}
	graph, err := fleet.NewGraph(fleet.LocalWorkspace, facts)
	if err != nil || len(graph.Nodes) != 1 || graph.Nodes[0].Entity.Cluster != contextName ||
		graph.Nodes[0].Entity.Namespace != "default" || graph.Nodes[0].Entity.Kind != "Application" ||
		graph.Nodes[0].Entity.Name != "payments" || len(graph.Nodes[0].Facts) != len(facts) {
		t.Fatalf("real kind Argo graph = %#v, error = %v", graph, err)
	}
	encoded, err := json.Marshal(graph)
	if err != nil {
		t.Fatalf("marshal real kind Argo graph: %v", err)
	}
	for _, secret := range []string{"kind-token", "kind-password", "do-not-retain", "raw-kind-secret"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("real kind Argo graph retained secret marker %q: %s", secret, encoded)
		}
	}
}

func waitForCRDEstablished(
	ctx context.Context,
	t *testing.T,
	resource interface {
		Get(context.Context, string, metav1.GetOptions, ...string) (*unstructured.Unstructured, error)
	},
	name string,
) {
	t.Helper()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		crd, err := resource.Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			t.Fatalf("read CRD %s: %v", name, err)
		}
		conditions, _, _ := unstructured.NestedSlice(crd.Object, "status", "conditions")
		for _, raw := range conditions {
			condition, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if condition["type"] == "Established" && condition["status"] == "True" {
				return
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for CRD %s: %v", name, ctx.Err())
		case <-ticker.C:
		}
	}
}
