// SPDX-License-Identifier: Apache-2.0
//go:build e2e

// Package e2e_test verifies the compiled Sith process boundary.
package e2e_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestBinarySmoke(t *testing.T) {
	root := repositoryRoot(t)
	binary := filepath.Join(t.TempDir(), "sith")
	kubeconfig := filepath.Join(t.TempDir(), "kubeconfig")
	if err := os.WriteFile(kubeconfig, []byte("apiVersion: v1\nkind: Config\n"), 0o600); err != nil {
		t.Fatalf("write empty kubeconfig: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	build := exec.CommandContext(ctx, "go", "build", "-trimpath", "-o", binary, "./cmd/sith")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build binary: %v\n%s", err, output)
	}
	egress := newEgressGuard(t)

	tests := []struct {
		name      string
		args      []string
		contains  string
		validJSON bool
		wantError bool
	}{
		{name: "version text", args: []string{"version"}, contains: "sith dev"},
		{name: "version JSON", args: []string{"version", "-o", "json"}, validJSON: true},
		{name: "clusters text", args: []string{"clusters"}, contains: "No clusters found (source: local-kubeconfig)."},
		{name: "clusters JSON", args: []string{"clusters", "-o", "json"}, validJSON: true},
		{name: "get no egress", args: []string{"get", "pods", "-A", "--all-clusters", "-o", "json"}, contains: "no kubeconfig contexts discovered", wantError: true},
		{name: "search no egress", args: []string{"search", "status:Running", "-o", "json"}, contains: "no kubeconfig contexts discovered", wantError: true},
		{name: "correlate no egress", args: []string{"correlate", "deploy/payments", "status!=Healthy", "-o", "json"}, contains: "no kubeconfig contexts discovered", wantError: true},
		{name: "hub stub", args: []string{"hub"}, contains: "phase-1+"},
		{name: "no arguments", contains: "Usage:"},
		{name: "help", args: []string{"--help"}, contains: "Usage:"},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			command := exec.CommandContext(ctx, binary, test.args...)
			command.Env = egress.environment(t.TempDir(), kubeconfig)
			output, err := command.CombinedOutput()
			if (err != nil) != test.wantError {
				t.Fatalf("run %v error = %v, want error %t\n%s", test.args, err, test.wantError, output)
			}
			if test.contains != "" && !strings.Contains(string(output), test.contains) {
				t.Fatalf("output = %q, want %q", output, test.contains)
			}
			if test.validJSON && !json.Valid(output) {
				t.Fatalf("output is not valid JSON: %q", output)
			}
		})
	}
	smokeWebUI(ctx, t, binary, egress.environment(t.TempDir(), kubeconfig))
	egress.assertUnused(t)
}

func smokeWebUI(ctx context.Context, t *testing.T, binary string, environment []string) {
	t.Helper()
	command := exec.CommandContext(ctx, binary, "ui", "--no-open", "--address", "127.0.0.1", "--port", "0")
	command.Env = environment
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatalf("web UI stdout: %v", err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatalf("start web UI: %v", err)
	}
	stopped := false
	defer func() {
		if !stopped {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	}()
	lineReady := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		if scanner.Scan() {
			lineReady <- scanner.Text()
		}
		close(lineReady)
	}()
	var line string
	select {
	case line = <-lineReady:
	case <-time.After(10 * time.Second):
		t.Fatalf("web UI did not report startup: %s", stderr.String())
	case <-ctx.Done():
		t.Fatalf("web UI startup context: %v", ctx.Err())
	}
	const prefix = "sith ui listening on "
	if !strings.HasPrefix(line, prefix) {
		t.Fatalf("web UI startup line = %q", line)
	}
	origin := strings.TrimPrefix(line, prefix)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, origin+"/", nil)
	if err != nil {
		t.Fatalf("construct web UI request: %v", err)
	}
	response, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
	if err != nil {
		t.Fatalf("request web UI: %v", err)
	}
	body, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("read web UI index: %v / %v", readErr, closeErr)
	}
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), "Sith — Fleet IDE") {
		t.Fatalf("web UI status/body = %d/%q", response.StatusCode, body)
	}
	if err := command.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("interrupt web UI: %v", err)
	}
	if err := command.Wait(); err != nil {
		t.Fatalf("wait for web UI: %v\n%s", err, stderr.String())
	}
	stopped = true
}

type egressGuard struct {
	server   *httptest.Server
	mu       sync.Mutex
	requests []string
}

func newEgressGuard(t *testing.T) *egressGuard {
	t.Helper()
	guard := &egressGuard{}
	guard.server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		guard.mu.Lock()
		guard.requests = append(guard.requests, request.Method+" "+request.URL.String())
		guard.mu.Unlock()
		response.WriteHeader(http.StatusTeapot)
	}))
	t.Cleanup(guard.server.Close)
	proxyURL, err := url.Parse(guard.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
		Timeout:   2 * time.Second,
	}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://telemetry.invalid/control", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("verify egress sentinel: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusTeapot {
		t.Fatalf("egress sentinel status = %d", response.StatusCode)
	}
	guard.mu.Lock()
	guard.requests = nil
	guard.mu.Unlock()
	return guard
}

func (guard *egressGuard) environment(configRoot, kubeconfig string) []string {
	overrides := map[string]string{
		"ALL_PROXY": guard.server.URL, "HTTP_PROXY": guard.server.URL, "HTTPS_PROXY": guard.server.URL,
		"NO_PROXY": "", "all_proxy": guard.server.URL, "http_proxy": guard.server.URL,
		"https_proxy": guard.server.URL, "no_proxy": "", "XDG_CONFIG_HOME": configRoot, "KUBECONFIG": kubeconfig,
	}
	environment := make([]string, 0, len(os.Environ())+len(overrides))
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if _, replaced := overrides[name]; !replaced {
			environment = append(environment, entry)
		}
	}
	names := make([]string, 0, len(overrides))
	for name := range overrides {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		environment = append(environment, name+"="+overrides[name])
	}
	return environment
}

func (guard *egressGuard) assertUnused(t *testing.T) {
	t.Helper()
	guard.mu.Lock()
	defer guard.mu.Unlock()
	if len(guard.requests) != 0 {
		t.Fatalf("local-mode binary attempted non-cluster network egress: %v", guard.requests)
	}
}

func TestUnknownCommandFails(t *testing.T) {
	t.Parallel()

	root := repositoryRoot(t)
	binary := filepath.Join(t.TempDir(), "sith")
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	build := exec.CommandContext(ctx, "go", "build", "-trimpath", "-o", binary, "./cmd/sith")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build binary: %v\n%s", err, output)
	}

	command := exec.CommandContext(ctx, binary, "bogus")
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("bogus command succeeded: %s", output)
	}
	if !strings.Contains(string(output), "unknown command") {
		t.Fatalf("output = %q, want unknown command", output)
	}
}

func TestMakeBuildInjectsMetadata(t *testing.T) {
	root := repositoryRoot(t)
	binDir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	const (
		version = "v0.0.0-e2e"
		commit  = "abc1234"
		date    = "2026-07-10T19:00:00Z"
	)
	build := exec.CommandContext(
		ctx,
		"make",
		"build",
		"BIN_DIR="+binDir,
		"VERSION="+version,
		"COMMIT="+commit,
		"DATE="+date,
	)
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("make build: %v\n%s", err, output)
	}

	binary := filepath.Join(binDir, "sith")
	command := exec.CommandContext(ctx, binary, "version", "-o", "json")
	output, err := command.Output()
	if err != nil {
		t.Fatalf("run built binary: %v", err)
	}

	var got struct {
		Version string `json:"version"`
		Commit  string `json:"commit"`
		Date    string `json:"date"`
	}
	if err := json.Unmarshal(output, &got); err != nil {
		t.Fatalf("unmarshal version output %q: %v", output, err)
	}
	if got.Version != version || got.Commit != commit || got.Date != date {
		t.Fatalf("metadata = %#v, want version=%q commit=%q date=%q", got, version, commit, date)
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()

	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve current test file")
	}

	return filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", ".."))
}
