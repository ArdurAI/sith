// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"

	"sigs.k8s.io/yaml"

	"github.com/ArdurAI/sith/internal/fleet"
)

type staticSource struct {
	result fleet.FleetResult
	called bool
}

type failingSource struct{}

func (failingSource) Kind() string {
	return "failing"
}

func (failingSource) Fleet(_ context.Context) (fleet.FleetResult, error) {
	return fleet.FleetResult{}, errors.New("source unavailable")
}

func (*staticSource) Kind() string {
	return "memory"
}

func (source *staticSource) Fleet(_ context.Context) (fleet.FleetResult, error) {
	source.called = true
	return source.result, nil
}

func TestVersionText(t *testing.T) {
	stdout, _, exitCode := runCLI(t, []string{"version"}, fleet.StubSource{})
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0", exitCode)
	}
	if !strings.Contains(stdout, "sith ") || !strings.Contains(stdout, runtime.GOOS+"/"+runtime.GOARCH) {
		t.Fatalf("stdout = %q, want version and platform", stdout)
	}
}

func TestVersionJSON(t *testing.T) {
	stdout, _, exitCode := runCLI(t, []string{"version", "-o", "json"}, fleet.StubSource{})
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0", exitCode)
	}

	var got map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("unmarshal stdout %q: %v", stdout, err)
	}
	for _, key := range []string{"version", "commit", "date", "go", "platform"} {
		if _, ok := got[key]; !ok {
			t.Errorf("JSON missing key %q: %#v", key, got)
		}
	}
}

func TestVersionAndClustersYAML(t *testing.T) {
	stdout, stderr, exitCode := runCLI(t, []string{"version", "-o", "yaml"}, fleet.StubSource{})
	if exitCode != 0 {
		t.Fatalf("version exit/stderr = %d/%q", exitCode, stderr)
	}
	var version map[string]any
	if err := yaml.Unmarshal([]byte(stdout), &version); err != nil || version["platform"] == nil {
		t.Fatalf("version YAML = %q, error = %v", stdout, err)
	}
	stdout, stderr, exitCode = runCLI(t, []string{"clusters", "-o", "yaml"}, fleet.StubSource{})
	if exitCode != 0 {
		t.Fatalf("clusters exit/stderr = %d/%q", exitCode, stderr)
	}
	var result fleet.FleetResult
	if err := yaml.Unmarshal([]byte(stdout), &result); err != nil || result.Clusters == nil {
		t.Fatalf("clusters YAML = %q, result = %#v, error = %v", stdout, result, err)
	}
}

func TestClustersEmptyText(t *testing.T) {
	stdout, _, exitCode := runCLI(t, []string{"clusters"}, fleet.StubSource{})
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0", exitCode)
	}
	const want = "No clusters found (source: stub — F2.1/#38 not yet implemented).\n"
	if stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
}

func TestClustersEmptyJSON(t *testing.T) {
	stdout, _, exitCode := runCLI(t, []string{"clusters", "-o", "json"}, fleet.StubSource{})
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0", exitCode)
	}

	var got fleet.FleetResult
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("unmarshal stdout %q: %v", stdout, err)
	}
	if got.Clusters == nil || len(got.Clusters) != 0 {
		t.Fatalf("clusters = %#v, want allocated empty slice", got.Clusters)
	}
}

func TestClustersEmptyNonStubNamesSource(t *testing.T) {
	source := &staticSource{result: fleet.FleetResult{}}

	stdout, _, exitCode := runCLI(t, []string{"clusters"}, source)
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0", exitCode)
	}
	if stdout != "No clusters found (source: memory).\n" {
		t.Fatalf("stdout = %q, want dynamic source name", stdout)
	}
}

func TestClustersNormalizesNilSliceInJSON(t *testing.T) {
	source := &staticSource{result: fleet.FleetResult{}}

	stdout, _, exitCode := runCLI(t, []string{"clusters", "-o", "json"}, source)
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0", exitCode)
	}
	const want = `{"clusters":[],"coverage":{"requested":0,"reachable":0}}` + "\n"
	if stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
}

func TestClustersSourceErrorIsConcise(t *testing.T) {
	_, stderr, exitCode := runCLI(t, []string{"clusters"}, failingSource{})
	if exitCode == 0 {
		t.Fatal("exit code = 0, want non-zero")
	}
	if !strings.Contains(stderr, "source unavailable") {
		t.Fatalf("stderr = %q, want source error", stderr)
	}
	if strings.Contains(stderr, "Usage:") {
		t.Fatalf("stderr = %q, want no usage noise", stderr)
	}
}

func TestClustersUsesInjectedSource(t *testing.T) {
	source := &staticSource{result: fleet.FleetResult{
		Clusters: []fleet.Cluster{
			{
				Name:       "prod-us",
				Context:    "prod-us-admin",
				SourceKind: "memory",
				Reachable:  true,
				ObservedAt: time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC),
			},
			{Name: "lab", Context: "lab", SourceKind: "memory"},
		},
	}}

	stdout, _, exitCode := runCLI(t, []string{"clusters"}, source)
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0", exitCode)
	}
	if !source.called {
		t.Fatal("injected source was not called")
	}
	for _, want := range []string{"NAME", "prod-us", "prod-us-admin", "lab", "memory"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout = %q, want it to contain %q", stdout, want)
		}
	}
}

func TestUIRequiresLocalBackend(t *testing.T) {
	stdout, stderr, exitCode := runCLI(t, []string{"ui", "--no-open"}, fleet.StubSource{})
	if exitCode == 0 || stdout != "" || !strings.Contains(stderr, "requires a Kubernetes reader") {
		t.Fatalf("exit/stdout/stderr = %d/%q/%q", exitCode, stdout, stderr)
	}
}

func TestHubStub(t *testing.T) {
	stdout, _, exitCode := runCLI(t, []string{"hub"}, fleet.StubSource{})
	if exitCode != 0 || stdout != "sith hub: not yet implemented — hub mode is phase-1+ (E1–E10).\n" {
		t.Fatalf("exit/stdout = %d/%q", exitCode, stdout)
	}
}

func TestRootHelpExitsZero(t *testing.T) {
	stdout, stderr, exitCode := runCLI(t, []string{"--help"}, fleet.StubSource{})
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0; stderr = %q", exitCode, stderr)
	}
	if !strings.Contains(stdout, "Usage:") || !strings.Contains(stdout, "sith [flags]") {
		t.Fatalf("stdout = %q, want usage", stdout)
	}
}

func TestRootNoArgsExitsZero(t *testing.T) {
	stdout, stderr, exitCode := runCLI(t, nil, fleet.StubSource{})
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0; stderr = %q", exitCode, stderr)
	}
	if !strings.Contains(stdout, "Usage:") {
		t.Fatalf("stdout = %q, want usage", stdout)
	}
}

func TestUnknownCommandNonZero(t *testing.T) {
	_, stderr, exitCode := runCLI(t, []string{"bogus"}, fleet.StubSource{})
	if exitCode == 0 {
		t.Fatal("exit code = 0, want non-zero")
	}
	if !strings.Contains(stderr, "unknown command") {
		t.Fatalf("stderr = %q, want unknown command error", stderr)
	}
}

func TestInvalidLogLevelFlagFails(t *testing.T) {
	_, stderr, exitCode := runCLI(t, []string{"clusters", "--log-level", "nope"}, fleet.StubSource{})
	if exitCode == 0 {
		t.Fatal("exit code = 0, want non-zero")
	}
	if !strings.Contains(stderr, "invalid log level") {
		t.Fatalf("stderr = %q, want invalid log level error", stderr)
	}
}

func runCLI(t *testing.T, args []string, source fleet.Source) (stdout, stderr string, exitCode int) {
	t.Helper()

	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("SITH_LOG_LEVEL", "")
	t.Setenv("SITH_LOG_FORMAT", "")
	t.Setenv("SITH_KUBECONFIG", "")

	var stdoutBuffer bytes.Buffer
	var stderrBuffer bytes.Buffer
	exitCode = execute(args, source, &stdoutBuffer, &stderrBuffer)
	return stdoutBuffer.String(), stderrBuffer.String(), exitCode
}
