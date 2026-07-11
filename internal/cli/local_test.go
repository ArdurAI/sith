// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/ArdurAI/sith/internal/fleet"
	"github.com/ArdurAI/sith/internal/localops"
)

func TestLocalCommandsRequireExplicitContextBeforeClientAccess(t *testing.T) {
	client := &fakeLocalClient{}
	tests := [][]string{
		{"yaml", "pod/api"},
		{"describe", "pod/api"},
		{"logs", "api"},
		{"exec", "api", "--", "true"},
		{"port-forward", "pod/api", "8080:80"},
		{"edit", "configmap/settings", "--file", filepath.Join(t.TempDir(), "missing")},
	}
	for _, args := range tests {
		_, stderr, exitCode := runLocalCLI(t, args, client, "")
		if exitCode == 0 || !strings.Contains(stderr, "--context is required") {
			t.Fatalf("args %v exit/stderr = %d/%q", args, exitCode, stderr)
		}
	}
	if client.calls() != 0 {
		t.Fatalf("local client calls = %d, want validation before access", client.calls())
	}
}

func TestYAMLAndLogsPassOneExactContext(t *testing.T) {
	client := &fakeLocalClient{
		view: localops.ObjectView{YAML: []byte("apiVersion: v1\nkind: Pod\n")},
		logs: "cluster-alpha\n",
	}
	stdout, stderr, exitCode := runLocalCLI(t, []string{
		"yaml", "pod/api", "--context", "alpha", "-n", "apps",
	}, client, "")
	if exitCode != 0 || stderr != "" || !strings.Contains(stdout, "kind: Pod") {
		t.Fatalf("yaml exit/stdout/stderr = %d/%q/%q", exitCode, stdout, stderr)
	}
	if client.lastTarget.Context != "alpha" || client.lastTarget.Namespace != "apps" {
		t.Fatalf("yaml target = %#v", client.lastTarget)
	}

	stdout, stderr, exitCode = runLocalCLI(t, []string{
		"logs", "api", "--context", "beta", "-n", "apps", "-f", "--tail", "15", "--since", "2m",
	}, client, "")
	if exitCode != 0 || stderr != "" || stdout != "cluster-alpha\n" {
		t.Fatalf("logs exit/stdout/stderr = %d/%q/%q", exitCode, stdout, stderr)
	}
	if client.lastTarget.Context != "beta" || !client.logOptions.Follow ||
		client.logOptions.TailLines == nil || *client.logOptions.TailLines != 15 || client.logOptions.Since.String() != "2m0s" {
		t.Fatalf("logs target/options = %#v/%#v", client.lastTarget, client.logOptions)
	}
}

func TestExecRequiresDelimiterAndPreservesArguments(t *testing.T) {
	client := &fakeLocalClient{}
	_, stderr, exitCode := runLocalCLI(t, []string{"exec", "api", "--context", "alpha", "printf"}, client, "")
	if exitCode == 0 || !strings.Contains(stderr, "followed by --") || client.execOptions.Command != nil {
		t.Fatalf("missing delimiter exit/stderr/command = %d/%q/%v", exitCode, stderr, client.execOptions.Command)
	}
	stdout, stderr, exitCode := runLocalCLI(t, []string{
		"exec", "api", "--context", "alpha", "-n", "apps", "--", "printf", "%s", "$(not-a-shell)",
	}, client, "")
	if exitCode != 0 || stderr != "" || stdout != "remote output\n" {
		t.Fatalf("exec exit/stdout/stderr = %d/%q/%q", exitCode, stdout, stderr)
	}
	if !slices.Equal(client.execOptions.Command, []string{"printf", "%s", "$(not-a-shell)"}) {
		t.Fatalf("exec command = %q", client.execOptions.Command)
	}
}

func TestEditAlwaysPreviewsDiffBeforeApply(t *testing.T) {
	directory := t.TempDir()
	filename := filepath.Join(directory, "settings.yaml")
	manifest := []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: settings\n  namespace: apps\n  resourceVersion: '7'\ndata:\n  mode: new\n")
	if err := os.WriteFile(filename, manifest, 0o600); err != nil {
		t.Fatalf("write edit fixture: %v", err)
	}
	client := &fakeLocalClient{
		preview: localops.ApplyPreview{
			CurrentYAML: []byte("apiVersion: v1\nkind: ConfigMap\ndata:\n  mode: old\n"),
			DryRunYAML:  []byte("apiVersion: v1\nkind: ConfigMap\ndata:\n  mode: new\n"),
		},
		applied: fleet.Evidence{Ref: fleet.ResourceRef{Scope: "alpha", Kind: "ConfigMap", Name: "settings"}},
	}
	stdout, stderr, exitCode := runLocalCLI(t, []string{
		"edit", "configmap/settings", "--context", "alpha", "-n", "apps", "--file", filename, "--yes",
	}, client, "")
	if exitCode != 0 || stderr != "" {
		t.Fatalf("edit exit/stdout/stderr = %d/%q/%q", exitCode, stdout, stderr)
	}
	if !strings.Contains(stdout, "server dry-run") || !strings.Contains(stdout, "-  mode: old") ||
		!strings.Contains(stdout, "+  mode: new") || !strings.Contains(stdout, "edited in context alpha") {
		t.Fatalf("edit stdout = %q", stdout)
	}
	if !slices.Equal(client.order, []string{"preview", "apply"}) || !bytes.Equal(client.manifest, manifest) {
		t.Fatalf("edit order/manifest = %v/%q", client.order, client.manifest)
	}
}

func TestEditSurfacesServerDryRunRejectionWithoutApply(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "edit.yaml")
	if err := os.WriteFile(filename, []byte("invalid: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	client := &fakeLocalClient{previewErr: errors.New("admission webhook denied this exact edit")}
	_, stderr, exitCode := runLocalCLI(t, []string{
		"edit", "configmap/settings", "--context", "alpha", "--file", filename, "--yes",
	}, client, "")
	if exitCode == 0 || !strings.Contains(stderr, "admission webhook denied this exact edit") {
		t.Fatalf("edit rejection exit/stderr = %d/%q", exitCode, stderr)
	}
	if !slices.Equal(client.order, []string{"preview"}) {
		t.Fatalf("edit rejection order = %v", client.order)
	}
}

func runLocalCLI(
	t *testing.T,
	args []string,
	client localops.Client,
	input string,
) (stdout, stderr string, exitCode int) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("SITH_LOG_LEVEL", "")
	t.Setenv("SITH_LOG_FORMAT", "")
	var stdoutBuffer, stderrBuffer bytes.Buffer
	exitCode = executeBackend(args, backend{
		source: fleet.StubSource{}, local: client, tuiInput: strings.NewReader(input),
	}, &stdoutBuffer, &stderrBuffer)
	return stdoutBuffer.String(), stderrBuffer.String(), exitCode
}

type fakeLocalClient struct {
	mu          sync.Mutex
	callCount   int
	lastTarget  localops.Target
	view        localops.ObjectView
	logs        string
	logOptions  localops.LogOptions
	execOptions localops.ExecOptions
	preview     localops.ApplyPreview
	previewErr  error
	applied     fleet.Evidence
	manifest    []byte
	order       []string
}

func (client *fakeLocalClient) record(target localops.Target) {
	client.mu.Lock()
	defer client.mu.Unlock()
	client.callCount++
	client.lastTarget = target
}

func (client *fakeLocalClient) calls() int {
	client.mu.Lock()
	defer client.mu.Unlock()
	return client.callCount
}

func (client *fakeLocalClient) View(_ context.Context, target localops.Target, _ bool) (localops.ObjectView, error) {
	client.record(target)
	return client.view, nil
}

func (client *fakeLocalClient) Describe(_ context.Context, target localops.Target) (localops.Description, error) {
	client.record(target)
	return localops.Description{Object: client.view}, nil
}

func (client *fakeLocalClient) Logs(
	_ context.Context,
	target localops.Target,
	options localops.LogOptions,
) (io.ReadCloser, error) {
	client.record(target)
	client.logOptions = options
	return io.NopCloser(strings.NewReader(client.logs)), nil
}

func (client *fakeLocalClient) Exec(
	_ context.Context,
	target localops.Target,
	options localops.ExecOptions,
	streams localops.Streams,
) error {
	client.record(target)
	client.execOptions = options
	_, err := io.WriteString(streams.Stdout, "remote output\n")
	return err
}

func (client *fakeLocalClient) PortForward(
	_ context.Context,
	request localops.ForwardRequest,
) (localops.ForwardSession, error) {
	client.record(request.Target)
	ready := make(chan struct{})
	close(ready)
	done := make(chan error, 1)
	done <- nil
	return &fakeForwardSession{ready: ready, done: done}, nil
}

func (client *fakeLocalClient) PreviewApply(
	_ context.Context,
	target localops.Target,
	manifest []byte,
) (localops.ApplyPreview, error) {
	client.record(target)
	client.order = append(client.order, "preview")
	client.manifest = append([]byte(nil), manifest...)
	return client.preview, client.previewErr
}

func (client *fakeLocalClient) Apply(
	_ context.Context,
	target localops.Target,
	manifest []byte,
) (fleet.Evidence, error) {
	client.record(target)
	client.order = append(client.order, "apply")
	client.manifest = append([]byte(nil), manifest...)
	return client.applied, nil
}

type fakeForwardSession struct {
	ready <-chan struct{}
	done  <-chan error
}

func (session *fakeForwardSession) Ready() <-chan struct{} { return session.ready }
func (session *fakeForwardSession) Done() <-chan error     { return session.done }
func (*fakeForwardSession) Ports() ([]localops.ForwardedPort, error) {
	return []localops.ForwardedPort{{Local: 8080, Remote: 80}}, nil
}
func (*fakeForwardSession) Close() error { return nil }
