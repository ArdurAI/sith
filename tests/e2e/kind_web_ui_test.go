// SPDX-License-Identifier: Apache-2.0
//go:build e2e && kind

package e2e_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ArdurAI/sith/internal/fleetcache"
	"github.com/ArdurAI/sith/internal/localops"
)

var (
	webUIAddressPattern = regexp.MustCompile(`^sith ui listening on (http://127\.0\.0\.1:[0-9]+)$`)
	webUITokenPattern   = regexp.MustCompile(`name="sith-csrf-token" content="([^"]+)"`)
)

type webUIProcess struct {
	command *exec.Cmd
	stderr  *bytes.Buffer
	once    sync.Once
}

type webUIObject struct {
	Target localops.Target `json:"target"`
	YAML   string          `json:"yaml"`
}

type webUIExecResult struct {
	Stdout string `json:"stdout"`
	Stderr string `json:"stderr"`
}

type webUIPreview struct {
	Diff         string `json:"diff"`
	PreviewToken string `json:"preview_token"`
}

type webUIForward struct {
	ID     string                   `json:"id"`
	Target localops.Target          `json:"target"`
	Ports  []localops.ForwardedPort `json:"ports"`
}

func exerciseWebUI(
	ctx context.Context,
	t *testing.T,
	binary, kubeconfigPath string,
	clusters []string,
) {
	t.Helper()
	_, refusedStderr, err := runSith(ctx, binary, kubeconfigPath, "ui", "--no-open", "--address", "0.0.0.0")
	if err == nil || !strings.Contains(refusedStderr, "is not loopback") {
		t.Fatalf("external web UI bind error/stderr = %v/%q, want loopback refusal", err, refusedStderr)
	}

	process, origin := startWebUI(ctx, t, binary, kubeconfigPath)
	t.Cleanup(func() { process.stop(t) })
	client := &http.Client{Timeout: 20 * time.Second}

	index := webUIRequest(ctx, t, client, http.MethodGet, origin, "", "", nil)
	if index.StatusCode != http.StatusOK || !strings.Contains(string(index.Body), "Sith — Fleet IDE") {
		t.Fatalf("web UI index status/body = %d/%q", index.StatusCode, index.Body)
	}
	match := webUITokenPattern.FindSubmatch(index.Body)
	if len(match) != 2 {
		t.Fatalf("web UI index did not contain a local session capability")
	}
	token := string(match[1])

	missing := webUIRequest(ctx, t, client, http.MethodGet, origin, "/api/v1/meta", "", nil)
	if missing.StatusCode != http.StatusForbidden {
		t.Fatalf("web UI API without capability status = %d, want 403", missing.StatusCode)
	}
	meta := webUIRequest(ctx, t, client, http.MethodGet, origin, "/api/v1/meta", token, nil)
	var metadata map[string]any
	decodeWebUIJSON(t, meta, http.StatusOK, &metadata)
	if metadata["mode"] != "local" || metadata["account_required"] != false || metadata["telemetry"] != false {
		t.Fatalf("web UI metadata = %#v", metadata)
	}

	contexts := []string{"kind-" + clusters[0], "kind-" + clusters[1]}
	snapshot := waitForWebUISnapshot(ctx, t, client, origin, token, contexts)
	if snapshot.Coverage.Requested != 3 || snapshot.Coverage.Reachable != 2 ||
		!slices.Equal(snapshot.Coverage.Unreachable, []string{"kind-sith-e2e-unreachable"}) {
		t.Fatalf("web UI snapshot coverage = %#v, want two of three reachable", snapshot.Coverage)
	}

	searchPath := "/api/v1/snapshot?kind=Pod&q=" + url.QueryEscape("image:*log4j*")
	search := webUIRequest(ctx, t, client, http.MethodGet, origin, searchPath, token, nil)
	var searchSnapshot fleetcache.Snapshot
	decodeWebUIJSON(t, search, http.StatusOK, &searchSnapshot)
	if len(searchSnapshot.Records) != 1 || searchSnapshot.Records[0].Name != "sith-vuln-sample" ||
		searchSnapshot.Records[0].Cluster != contexts[0] {
		t.Fatalf("web UI search records = %#v", searchSnapshot.Records)
	}

	correlationPath := "/api/v1/snapshot?correlate=true&q=" +
		url.QueryEscape("deploy/sith-payments status!=Healthy")
	correlation := webUIRequest(ctx, t, client, http.MethodGet, origin, correlationPath, token, nil)
	var correlationSnapshot fleetcache.Snapshot
	decodeWebUIJSON(t, correlation, http.StatusOK, &correlationSnapshot)
	if len(correlationSnapshot.Records) != 1 || correlationSnapshot.Records[0].Cluster != contexts[1] ||
		correlationSnapshot.Records[0].Status == "Healthy" {
		t.Fatalf("web UI correlation records = %#v", correlationSnapshot.Records)
	}

	objectPath := webUITargetPath("/api/v1/object", localops.Target{
		Context: contexts[0], Namespace: "default", Kind: "Pod", Name: "sith-local-ops",
	})
	object := webUIRequest(ctx, t, client, http.MethodGet, origin, objectPath, token, nil)
	var viewed webUIObject
	decodeWebUIJSON(t, object, http.StatusOK, &viewed)
	if viewed.Target.Context != contexts[0] || !strings.Contains(viewed.YAML, contexts[0]) ||
		strings.Contains(viewed.YAML, contexts[1]) {
		t.Fatalf("web UI exact object target/yaml = %#v/%q", viewed.Target, viewed.YAML)
	}

	secretPath := webUITargetPath("/api/v1/object", localops.Target{
		Context: contexts[0], Namespace: "default", Kind: "Secret", Name: "sith-local-secret",
	})
	secret := webUIRequest(ctx, t, client, http.MethodGet, origin, secretPath, token, nil)
	var masked webUIObject
	decodeWebUIJSON(t, secret, http.StatusOK, &masked)
	secretValue := base64.StdEncoding.EncodeToString([]byte("token-" + contexts[0]))
	if !strings.Contains(masked.YAML, "<redacted>") || strings.Contains(masked.YAML, secretValue) {
		t.Fatalf("web UI masked secret leaked value: %q", masked.YAML)
	}

	logsPath := webUITargetPath("/api/v1/logs", localops.Target{
		Context: contexts[0], Namespace: "default", Kind: "Pod", Name: "sith-local-ops",
	}) + "&tail=20"
	logs := webUIRequest(ctx, t, client, http.MethodGet, origin, logsPath, token, nil)
	if logs.StatusCode != http.StatusOK || !strings.Contains(string(logs.Body), "cluster="+contexts[0]+" ready") ||
		strings.Contains(string(logs.Body), contexts[1]) {
		t.Fatalf("web UI logs status/body = %d/%q", logs.StatusCode, logs.Body)
	}

	execPayload := map[string]any{
		"target":  localops.Target{Context: contexts[0], Namespace: "default", Kind: "Pod", Name: "sith-local-ops"},
		"command": []string{"/fixture", "echo", contexts[0]},
	}
	executed := webUIRequest(ctx, t, client, http.MethodPost, origin, "/api/v1/exec", token, marshalWebUIJSON(t, execPayload))
	var execution webUIExecResult
	decodeWebUIJSON(t, executed, http.StatusOK, &execution)
	if strings.TrimSpace(execution.Stdout) != contexts[0] || execution.Stderr != "" {
		t.Fatalf("web UI exec result = %#v", execution)
	}

	exerciseWebUIEdit(ctx, t, client, origin, token, contexts[0])
	exerciseWebUIForward(ctx, t, client, origin, token, contexts[0])

	refresh := webUIRequest(ctx, t, client, http.MethodPost, origin, "/api/v1/sync", token, []byte(`{}`))
	if refresh.StatusCode != http.StatusAccepted {
		t.Fatalf("web UI explicit refresh status/body = %d/%q", refresh.StatusCode, refresh.Body)
	}
	process.stop(t)
}

func exerciseWebUIDirectoryImport(
	ctx context.Context,
	t *testing.T,
	binary, kubeconfigDirectory string,
	clusters []string,
) {
	t.Helper()
	process, origin := startWebUIFromDirectory(ctx, t, binary, kubeconfigDirectory)
	t.Cleanup(func() { process.stop(t) })
	client := &http.Client{Timeout: 20 * time.Second}
	index := webUIRequest(ctx, t, client, http.MethodGet, origin, "", "", nil)
	match := webUITokenPattern.FindSubmatch(index.Body)
	if index.StatusCode != http.StatusOK || len(match) != 2 {
		process.stop(t)
		t.Fatalf("directory-import web UI index status/body = %d/%q", index.StatusCode, index.Body)
	}
	token := string(match[1])
	deadline := time.NewTimer(45 * time.Second)
	defer deadline.Stop()
	var snapshot fleetcache.Snapshot
	for {
		response := webUIRequest(ctx, t, client, http.MethodGet, origin, "/api/v1/snapshot?kind=Pod", token, nil)
		decodeWebUIJSON(t, response, http.StatusOK, &snapshot)
		if snapshot.Coverage.Requested == 2 && snapshot.Coverage.Reachable == 2 && len(snapshot.Scopes) == 2 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for directory-import UI: %v", ctx.Err())
		case <-deadline.C:
			t.Fatalf("directory-import UI did not hydrate both contexts: %#v", snapshot)
		case <-time.After(250 * time.Millisecond):
		}
	}
	if strings.Contains(mustMarshalWebUISnapshot(t, snapshot), kubeconfigDirectory) {
		t.Fatalf("directory-import snapshot exposed absolute directory: %#v", snapshot)
	}
	byOrigin := make(map[string]string, len(snapshot.Scopes))
	for _, scope := range snapshot.Scopes {
		if scope.DisplayName == "" || scope.Origin == "" || scope.Name == scope.DisplayName {
			t.Fatalf("directory-import scope metadata = %#v", scope)
		}
		byOrigin[scope.Origin] = scope.Name
	}
	if len(byOrigin) != 2 || byOrigin["first.yaml"] == "" || byOrigin["nested/second.yaml"] == "" {
		t.Fatalf("directory-import source groups = %#v", byOrigin)
	}
	selected := byOrigin["first.yaml"]
	filtered := webUIRequest(
		ctx, t, client, http.MethodGet, origin,
		"/api/v1/snapshot?kind=Pod&scopes="+url.QueryEscape(selected), token, nil,
	)
	var oneSource fleetcache.Snapshot
	decodeWebUIJSON(t, filtered, http.StatusOK, &oneSource)
	if len(oneSource.Scopes) != 1 || oneSource.Scopes[0].Name != selected || oneSource.Coverage.Requested != 1 {
		t.Fatalf("directory-import source selection = %#v", oneSource)
	}
	for _, record := range oneSource.Records {
		if record.Cluster != selected {
			t.Fatalf("directory-import selection returned another source: %#v", oneSource.Records)
		}
	}
	objectPath := webUITargetPath("/api/v1/object", localops.Target{
		Context: selected, Namespace: "default", Kind: "Pod", Name: "sith-local-ops",
	})
	object := webUIRequest(ctx, t, client, http.MethodGet, origin, objectPath, token, nil)
	var viewed webUIObject
	decodeWebUIJSON(t, object, http.StatusOK, &viewed)
	if viewed.Target.Context != selected || !strings.Contains(viewed.YAML, "sith-local-ops") ||
		!strings.Contains(viewed.YAML, "kind-"+clusters[0]) {
		t.Fatalf("directory-import object read = %#v/%q", viewed.Target, viewed.YAML)
	}
	process.stop(t)
}

func exerciseWebUIEdit(
	ctx context.Context,
	t *testing.T,
	client *http.Client,
	origin, token, contextName string,
) {
	t.Helper()
	target := localops.Target{Context: contextName, Namespace: "default", Kind: "ConfigMap", Name: "sith-local-edit"}
	path := webUITargetPath("/api/v1/object", target)
	current := webUIRequest(ctx, t, client, http.MethodGet, origin, path, token, nil)
	var object webUIObject
	decodeWebUIJSON(t, current, http.StatusOK, &object)
	manifest := strings.Replace(object.YAML, "mode: verified-1", "mode: verified-web", 1)
	if manifest == object.YAML {
		t.Fatalf("web UI edit source did not contain prior server-applied value: %q", object.YAML)
	}
	payload := map[string]any{"target": target, "manifest": manifest}
	previewed := webUIRequest(
		ctx, t, client, http.MethodPost, origin, "/api/v1/edit/preview", token, marshalWebUIJSON(t, payload),
	)
	var preview webUIPreview
	decodeWebUIJSON(t, previewed, http.StatusOK, &preview)
	if preview.PreviewToken == "" || !strings.Contains(preview.Diff, "server dry-run") {
		t.Fatalf("web UI edit preview = %#v", preview)
	}

	withoutGrant := webUIRequest(
		ctx, t, client, http.MethodPost, origin, "/api/v1/edit/apply", token, marshalWebUIJSON(t, payload),
	)
	if withoutGrant.StatusCode != http.StatusConflict {
		t.Fatalf("web UI apply without preview status = %d, want 409", withoutGrant.StatusCode)
	}
	payload["preview_token"] = preview.PreviewToken
	applied := webUIRequest(
		ctx, t, client, http.MethodPost, origin, "/api/v1/edit/apply", token, marshalWebUIJSON(t, payload),
	)
	if applied.StatusCode != http.StatusOK {
		t.Fatalf("web UI previewed apply status/body = %d/%q", applied.StatusCode, applied.Body)
	}
	after := webUIRequest(ctx, t, client, http.MethodGet, origin, path, token, nil)
	var verified webUIObject
	decodeWebUIJSON(t, after, http.StatusOK, &verified)
	if !strings.Contains(verified.YAML, "mode: verified-web") {
		t.Fatalf("web UI applied object = %q", verified.YAML)
	}
}

func exerciseWebUIForward(
	ctx context.Context,
	t *testing.T,
	client *http.Client,
	origin, token, contextName string,
) {
	t.Helper()
	payload := map[string]any{
		"target": localops.Target{Context: contextName, Namespace: "default", Kind: "Service", Name: "sith-local-ops"},
		"ports":  []string{":web"},
	}
	started := webUIRequest(
		ctx, t, client, http.MethodPost, origin, "/api/v1/port-forwards", token, marshalWebUIJSON(t, payload),
	)
	var forward webUIForward
	decodeWebUIJSON(t, started, http.StatusCreated, &forward)
	if forward.ID == "" || forward.Target.Context != contextName || len(forward.Ports) != 1 ||
		forward.Ports[0].Local == 0 || forward.Ports[0].Remote != 8080 {
		t.Fatalf("web UI forward = %#v", forward)
	}
	forwarded := webUIRequest(
		ctx, t, client, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d", forward.Ports[0].Local), "", "", nil,
	)
	if forwarded.StatusCode != http.StatusOK || !strings.Contains(string(forwarded.Body), "cluster="+contextName) {
		t.Fatalf("web UI forwarded response status/body = %d/%q", forwarded.StatusCode, forwarded.Body)
	}
	listed := webUIRequest(ctx, t, client, http.MethodGet, origin, "/api/v1/port-forwards", token, nil)
	var forwards []webUIForward
	decodeWebUIJSON(t, listed, http.StatusOK, &forwards)
	if len(forwards) != 1 || forwards[0].ID != forward.ID {
		t.Fatalf("web UI forwards = %#v", forwards)
	}
	closed := webUIRequest(
		ctx, t, client, http.MethodDelete, origin, "/api/v1/port-forwards/"+url.PathEscape(forward.ID), token, nil,
	)
	if closed.StatusCode != http.StatusNoContent {
		t.Fatalf("web UI close forward status/body = %d/%q", closed.StatusCode, closed.Body)
	}
}

func startWebUI(ctx context.Context, t *testing.T, binary, kubeconfigPath string) (*webUIProcess, string) {
	t.Helper()
	command := exec.Command(binary, "ui", "--no-open", "--address", "127.0.0.1", "--port", "0")
	command.Env = append(os.Environ(),
		"KUBECONFIG="+kubeconfigPath,
		"XDG_CONFIG_HOME="+filepath.Join(filepath.Dir(kubeconfigPath), "config-home-web"),
	)
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatalf("web UI stdout: %v", err)
	}
	process := &webUIProcess{command: command, stderr: &bytes.Buffer{}}
	command.Stderr = process.stderr
	if err := command.Start(); err != nil {
		t.Fatalf("start web UI: %v", err)
	}
	lines := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		if scanner.Scan() {
			lines <- scanner.Text()
		}
		close(lines)
	}()
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	select {
	case line, open := <-lines:
		if !open {
			process.stop(t)
			t.Fatalf("web UI exited before reporting its address: %s", process.stderr.String())
		}
		match := webUIAddressPattern.FindStringSubmatch(line)
		if len(match) != 2 {
			process.stop(t)
			t.Fatalf("web UI address line = %q", line)
		}
		return process, match[1]
	case <-timer.C:
		process.stop(t)
		t.Fatalf("web UI did not report its address: %s", process.stderr.String())
	case <-ctx.Done():
		process.stop(t)
		t.Fatalf("web UI context ended before startup: %v", ctx.Err())
	}
	return nil, ""
}

func startWebUIFromDirectory(ctx context.Context, t *testing.T, binary, kubeconfigDirectory string) (*webUIProcess, string) {
	t.Helper()
	command := exec.Command(binary, "ui", "--no-open", "--address", "127.0.0.1", "--port", "0", "--kubeconfig-dir", kubeconfigDirectory)
	command.Env = append(os.Environ(), "XDG_CONFIG_HOME="+filepath.Join(kubeconfigDirectory, "config-home-web"))
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatalf("directory-import web UI stdout: %v", err)
	}
	process := &webUIProcess{command: command, stderr: &bytes.Buffer{}}
	command.Stderr = process.stderr
	if err := command.Start(); err != nil {
		t.Fatalf("start directory-import web UI: %v", err)
	}
	lines := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		if scanner.Scan() {
			lines <- scanner.Text()
		}
		close(lines)
	}()
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	select {
	case line, open := <-lines:
		if !open {
			process.stop(t)
			t.Fatalf("directory-import web UI exited before reporting its address: %s", process.stderr.String())
		}
		match := webUIAddressPattern.FindStringSubmatch(line)
		if len(match) != 2 {
			process.stop(t)
			t.Fatalf("directory-import web UI address line = %q", line)
		}
		return process, match[1]
	case <-timer.C:
		process.stop(t)
		t.Fatalf("directory-import web UI did not report its address: %s", process.stderr.String())
	case <-ctx.Done():
		process.stop(t)
		t.Fatalf("directory-import web UI context ended before startup: %v", ctx.Err())
	}
	return nil, ""
}

func mustMarshalWebUISnapshot(t *testing.T, snapshot fleetcache.Snapshot) string {
	t.Helper()
	payload, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return string(payload)
}

func (process *webUIProcess) stop(t *testing.T) {
	t.Helper()
	process.once.Do(func() {
		if process.command.ProcessState == nil {
			if err := process.command.Process.Signal(os.Interrupt); err != nil && !errors.Is(err, os.ErrProcessDone) {
				t.Errorf("interrupt web UI: %v", err)
			}
		}
		if err := process.command.Wait(); err != nil && process.command.ProcessState != nil &&
			!process.command.ProcessState.Success() {
			t.Errorf("wait for web UI: %v\n%s", err, process.stderr.String())
		}
	})
}

func waitForWebUISnapshot(
	ctx context.Context,
	t *testing.T,
	client *http.Client,
	origin, token string,
	contexts []string,
) fleetcache.Snapshot {
	t.Helper()
	deadline := time.NewTimer(45 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		response := webUIRequest(ctx, t, client, http.MethodGet, origin, "/api/v1/snapshot?kind=Pod", token, nil)
		var snapshot fleetcache.Snapshot
		decodeWebUIJSON(t, response, http.StatusOK, &snapshot)
		seen := map[string]bool{contexts[0]: false, contexts[1]: false}
		for _, record := range snapshot.Records {
			if record.Name == "sith-local-ops" {
				seen[record.Cluster] = true
			}
		}
		if snapshot.Coverage.Reachable == 2 && seen[contexts[0]] && seen[contexts[1]] {
			return snapshot
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for web UI cache hydration: %v", ctx.Err())
		case <-deadline.C:
			t.Fatalf("web UI cache did not hydrate both contexts: %#v", snapshot)
		case <-ticker.C:
		}
	}
}

type webUIResponse struct {
	StatusCode int
	Body       []byte
}

func webUIRequest(
	ctx context.Context,
	t *testing.T,
	client *http.Client,
	method, origin, path, token string,
	body []byte,
) webUIResponse {
	t.Helper()
	request, err := http.NewRequestWithContext(ctx, method, origin+path, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("construct web UI request: %v", err)
	}
	if token != "" {
		request.Header.Set("X-Sith-CSRF", token)
		request.Header.Set("Origin", origin)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("run web UI request %s %s: %v", method, request.URL, err)
	}
	payload, readErr := io.ReadAll(io.LimitReader(response.Body, 12<<20))
	closeErr := response.Body.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		t.Fatalf("read web UI response %s %s: %v", method, request.URL, err)
	}
	return webUIResponse{StatusCode: response.StatusCode, Body: payload}
}

func webUITargetPath(base string, target localops.Target) string {
	values := url.Values{
		"context": []string{target.Context}, "namespace": []string{target.Namespace},
		"kind": []string{target.Kind}, "name": []string{target.Name},
	}
	return base + "?" + values.Encode()
}

func marshalWebUIJSON(t *testing.T, value any) []byte {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encode web UI JSON: %v", err)
	}
	return payload
}

func decodeWebUIJSON(t *testing.T, response webUIResponse, status int, destination any) {
	t.Helper()
	if response.StatusCode != status {
		t.Fatalf("web UI response status/body = %d/%q, want %d", response.StatusCode, response.Body, status)
	}
	if err := json.Unmarshal(response.Body, destination); err != nil {
		t.Fatalf("decode web UI response %q: %v", response.Body, err)
	}
}
