// SPDX-License-Identifier: Apache-2.0
//go:build e2e && kind

package e2e_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

func buildKindFixture(
	ctx context.Context,
	t *testing.T,
	root, kindBinary string,
	clusters []string,
	suffix string,
) string {
	t.Helper()
	contextDirectory := t.TempDir()
	fixtureBinary := filepath.Join(contextDirectory, "fixture")
	build := exec.CommandContext(ctx, "go", "build", "-trimpath", "-o", fixtureBinary, "./tests/e2e/fixture")
	build.Dir = root
	build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+runtime.GOARCH)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build Linux fixture: %v\n%s", err, output)
	}
	image := "sith-local-ops:" + suffix
	runCommand(
		ctx,
		t,
		root,
		"docker",
		"build",
		"--tag",
		image,
		"--file",
		filepath.Join(root, "tests", "e2e", "fixture", "Dockerfile"),
		contextDirectory,
	)
	for _, cluster := range clusters {
		runCommand(ctx, t, "", kindBinary, "load", "docker-image", "--name", cluster, image)
	}
	return image
}

func seedLocalOperationResources(
	ctx context.Context,
	t *testing.T,
	kubeconfigPath string,
	clusters []string,
	image string,
) {
	t.Helper()
	for _, cluster := range clusters {
		contextName := "kind-" + cluster
		client := dynamicClientForContext(t, kubeconfigPath, contextName)
		pod := &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "Pod",
			"metadata": map[string]any{
				"name": "sith-local-ops", "namespace": "default", "labels": map[string]any{"app": "sith-local-ops"},
			},
			"spec": map[string]any{"containers": []any{map[string]any{
				"name": "fixture", "image": image, "imagePullPolicy": "Never",
				"env":   []any{map[string]any{"name": "SITH_FIXTURE_CLUSTER", "value": contextName}},
				"ports": []any{map[string]any{"name": "http", "containerPort": int64(8080), "protocol": "TCP"}},
			}}},
		}}
		createdPod, err := client.Resource(schema.GroupVersionResource{Version: "v1", Resource: "pods"}).
			Namespace("default").Create(ctx, pod, metav1.CreateOptions{})
		if err != nil {
			t.Fatalf("create local-operation pod in %s: %v", contextName, err)
		}
		service := &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "Service",
			"metadata":   map[string]any{"name": "sith-local-ops", "namespace": "default"},
			"spec": map[string]any{
				"selector": map[string]any{"app": "sith-local-ops"},
				"ports":    []any{map[string]any{"name": "web", "port": int64(80), "targetPort": "http", "protocol": "TCP"}},
			},
		}}
		if _, err := client.Resource(schema.GroupVersionResource{Version: "v1", Resource: "services"}).
			Namespace("default").Create(ctx, service, metav1.CreateOptions{}); err != nil {
			t.Fatalf("create local-operation service in %s: %v", contextName, err)
		}
		configMap := &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "v1", "kind": "ConfigMap",
			"metadata": map[string]any{"name": "sith-local-edit", "namespace": "default"},
			"data":     map[string]any{"cluster": contextName, "mode": "old"},
		}}
		if _, err := client.Resource(schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}).
			Namespace("default").Create(ctx, configMap, metav1.CreateOptions{}); err != nil {
			t.Fatalf("create local-operation configmap in %s: %v", contextName, err)
		}
		secretValue := base64.StdEncoding.EncodeToString([]byte("token-" + contextName))
		secret := &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "v1", "kind": "Secret",
			"metadata": map[string]any{"name": "sith-local-secret", "namespace": "default"},
			"type":     "Opaque", "data": map[string]any{"token": secretValue},
		}}
		if _, err := client.Resource(schema.GroupVersionResource{Version: "v1", Resource: "secrets"}).
			Namespace("default").Create(ctx, secret, metav1.CreateOptions{}); err != nil {
			t.Fatalf("create local-operation secret in %s: %v", contextName, err)
		}
		event := &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "v1", "kind": "Event",
			"metadata": map[string]any{"name": "sith-local-ready", "namespace": "default"},
			"involvedObject": map[string]any{
				"apiVersion": "v1", "kind": "Pod", "namespace": "default",
				"name": "sith-local-ops", "uid": string(createdPod.GetUID()),
			},
			"type": "Normal", "reason": "FixtureReady", "message": "fixture event from " + contextName,
		}}
		if _, err := client.Resource(schema.GroupVersionResource{Version: "v1", Resource: "events"}).
			Namespace("default").Create(ctx, event, metav1.CreateOptions{}); err != nil {
			t.Fatalf("create local-operation event in %s: %v", contextName, err)
		}
		waitForReadyPod(ctx, t, client, contextName)
	}
}

func waitForReadyPod(ctx context.Context, t *testing.T, client dynamic.Interface, contextName string) {
	t.Helper()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(90 * time.Second)
	defer deadline.Stop()
	for {
		pod, err := client.Resource(schema.GroupVersionResource{Version: "v1", Resource: "pods"}).
			Namespace("default").Get(ctx, "sith-local-ops", metav1.GetOptions{})
		if err != nil {
			t.Fatalf("read local-operation pod in %s: %v", contextName, err)
		}
		conditions, _, _ := unstructured.NestedSlice(pod.Object, "status", "conditions")
		for _, raw := range conditions {
			condition, _ := raw.(map[string]any)
			if condition["type"] == "Ready" && condition["status"] == "True" {
				return
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for local-operation pod in %s: %v", contextName, ctx.Err())
		case <-deadline.C:
			t.Fatalf("local-operation pod in %s did not become ready: %#v", contextName, pod.Object["status"])
		case <-ticker.C:
		}
	}
}

func exerciseLocalOperations(
	ctx context.Context,
	t *testing.T,
	binary, kubeconfigPath string,
	clusters []string,
) {
	t.Helper()
	contexts := []string{"kind-" + clusters[0], "kind-" + clusters[1]}
	for index, contextName := range contexts {
		otherContext := contexts[1-index]
		logOutput, logStderr, err := runSith(
			ctx, binary, kubeconfigPath, "logs", "sith-local-ops", "--context", contextName, "--tail", "20",
		)
		if err != nil || !strings.Contains(string(logOutput), "cluster="+contextName+" ready") ||
			strings.Contains(string(logOutput), otherContext) {
			t.Fatalf("logs for %s error/stdout/stderr = %v/%q/%q", contextName, err, logOutput, logStderr)
		}

		execOutput, execStderr, err := runSith(
			ctx, binary, kubeconfigPath, "exec", "sith-local-ops", "--context", contextName,
			"--", "/fixture", "echo", contextName,
		)
		if err != nil || strings.TrimSpace(string(execOutput)) != contextName {
			t.Fatalf("exec for %s error/stdout/stderr = %v/%q/%q", contextName, err, execOutput, execStderr)
		}

		yamlOutput, yamlStderr, err := runSith(
			ctx, binary, kubeconfigPath, "yaml", "pod/sith-local-ops", "--context", contextName,
		)
		if err != nil || !strings.Contains(string(yamlOutput), "SITH_FIXTURE_CLUSTER") ||
			!strings.Contains(string(yamlOutput), contextName) {
			t.Fatalf("yaml for %s error/stdout/stderr = %v/%q/%q", contextName, err, yamlOutput, yamlStderr)
		}

		secretValue := base64.StdEncoding.EncodeToString([]byte("token-" + contextName))
		masked, maskedStderr, err := runSith(
			ctx, binary, kubeconfigPath, "yaml", "secret/sith-local-secret", "--context", contextName,
		)
		if err != nil || !strings.Contains(string(masked), "<redacted>") || strings.Contains(string(masked), secretValue) {
			t.Fatalf("masked secret for %s error/stdout/stderr = %v/%q/%q", contextName, err, masked, maskedStderr)
		}
		revealed, revealedStderr, err := runSith(
			ctx, binary, kubeconfigPath, "yaml", "secret/sith-local-secret", "--context", contextName, "--show-secrets",
		)
		if err != nil || !strings.Contains(string(revealed), secretValue) {
			t.Fatalf("revealed secret for %s error/stdout/stderr = %v/%q/%q", contextName, err, revealed, revealedStderr)
		}

		described, describeStderr, err := runSith(
			ctx, binary, kubeconfigPath, "describe", "pod/sith-local-ops", "--context", contextName,
		)
		if err != nil || !strings.Contains(string(described), "FixtureReady") ||
			!strings.Contains(string(described), "fixture event from "+contextName) {
			t.Fatalf("describe for %s error/stdout/stderr = %v/%q/%q", contextName, err, described, describeStderr)
		}

		editSource, editSourceStderr, err := runSith(
			ctx, binary, kubeconfigPath, "yaml", "configmap/sith-local-edit", "--context", contextName,
		)
		if err != nil {
			t.Fatalf("read edit source for %s: %v/%s", contextName, err, editSourceStderr)
		}
		edited := strings.Replace(string(editSource), "mode: old", "mode: verified-"+strconv.Itoa(index+1), 1)
		if edited == string(editSource) {
			t.Fatalf("edit source for %s did not contain expected mode: %q", contextName, editSource)
		}
		editFile := filepath.Join(t.TempDir(), "edit.yaml")
		if err := os.WriteFile(editFile, []byte(edited), 0o600); err != nil {
			t.Fatalf("write edit file for %s: %v", contextName, err)
		}
		editOutput, editStderr, err := runSith(
			ctx, binary, kubeconfigPath, "edit", "configmap/sith-local-edit", "--context", contextName,
			"--file", editFile, "--yes",
		)
		if err != nil || !strings.Contains(string(editOutput), "server dry-run") ||
			!strings.Contains(string(editOutput), "edited in context "+contextName) {
			t.Fatalf("edit for %s error/stdout/stderr = %v/%q/%q", contextName, err, editOutput, editStderr)
		}
		afterEdit, afterEditStderr, err := runSith(
			ctx, binary, kubeconfigPath, "yaml", "configmap/sith-local-edit", "--context", contextName,
		)
		if err != nil || !strings.Contains(string(afterEdit), "mode: verified-"+strconv.Itoa(index+1)) {
			t.Fatalf("verify edit for %s error/stdout/stderr = %v/%q/%q", contextName, err, afterEdit, afterEditStderr)
		}

		body := exercisePortForward(ctx, t, binary, kubeconfigPath, contextName)
		if !strings.Contains(body, "cluster="+contextName) || strings.Contains(body, otherContext) {
			t.Fatalf("port-forward response for %s = %q", contextName, body)
		}
	}
}

var readyPortPattern = regexp.MustCompile("port-forward ready ([0-9]+) -> 8080")

func exercisePortForward(
	ctx context.Context,
	t *testing.T,
	binary, kubeconfigPath, contextName string,
) string {
	t.Helper()
	command := exec.CommandContext(
		ctx,
		binary,
		"port-forward",
		"service/sith-local-ops",
		"--context",
		contextName,
		":web",
	)
	command.Env = append(os.Environ(),
		"KUBECONFIG="+kubeconfigPath,
		"XDG_CONFIG_HOME="+filepath.Join(filepath.Dir(kubeconfigPath), "config-home"),
	)
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatalf("port-forward stdout for %s: %v", contextName, err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatalf("start port-forward for %s: %v", contextName, err)
	}
	lines := make(chan string, 32)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
		close(lines)
	}()
	var localPort int
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	for localPort == 0 {
		select {
		case line, open := <-lines:
			if !open {
				_ = command.Wait()
				t.Fatalf("port-forward for %s ended before ready: %s", contextName, stderr.String())
			}
			match := readyPortPattern.FindStringSubmatch(line)
			if len(match) == 2 {
				localPort, err = strconv.Atoi(match[1])
				if err != nil {
					t.Fatalf("parse local port %q: %v", match[1], err)
				}
			}
		case <-timer.C:
			_ = command.Process.Kill()
			_ = command.Wait()
			t.Fatalf("port-forward for %s timed out: %s", contextName, stderr.String())
		case <-ctx.Done():
			_ = command.Process.Kill()
			_ = command.Wait()
			t.Fatalf("port-forward for %s context ended: %v", contextName, ctx.Err())
		}
	}
	client := &http.Client{Timeout: 10 * time.Second}
	response, err := client.Get("http://127.0.0.1:" + strconv.Itoa(localPort) + "/")
	if err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		t.Fatalf("request forwarded endpoint for %s: %v", contextName, err)
	}
	body, readErr := io.ReadAll(io.LimitReader(response.Body, 4096))
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("read forwarded response for %s: %v", contextName, errors.Join(readErr, closeErr))
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("forwarded response for %s status = %s, body=%q", contextName, response.Status, body)
	}
	if err := command.Process.Signal(os.Interrupt); err != nil {
		_ = command.Process.Kill()
	}
	_ = command.Wait()
	return string(body)
}
