// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/ArdurAI/sith/internal/fleet"
)

func TestResolveLaunchMode(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		requested string
		goos      string
		want      launchMode
		wantError bool
	}{
		{name: "macOS auto", requested: "auto", goos: "darwin", want: launchModeDesktop},
		{name: "Linux auto", requested: "auto", goos: "linux", want: launchModeUI},
		{name: "Windows auto", requested: "auto", goos: "windows", want: launchModeUI},
		{name: "macOS desktop", requested: "desktop", goos: "darwin", want: launchModeDesktop},
		{name: "Linux desktop", requested: "desktop", goos: "linux", wantError: true},
		{name: "explicit UI", requested: "ui", goos: "darwin", want: launchModeUI},
		{name: "unknown", requested: "browser", goos: "darwin", wantError: true},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := resolveLaunchMode(test.requested, test.goos)
			if test.wantError {
				if err == nil {
					t.Fatalf("resolveLaunchMode(%q, %q) = %q, want error", test.requested, test.goos, got)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("resolveLaunchMode(%q, %q) = %q, %v, want %q", test.requested, test.goos, got, err, test.want)
			}
		})
	}
}

func TestLaunchCommandIsRegistered(t *testing.T) {
	stdout, stderr, exitCode := runCLI(t, []string{"--help"}, fleet.StubSource{})
	if exitCode != 0 || stderr != "" || !strings.Contains(stdout, "launch") ||
		!strings.Contains(stdout, "Open the local fleet IDE") {
		t.Fatalf("root help exit/stdout/stderr = %d/%q/%q", exitCode, stdout, stderr)
	}
}

func TestLaunchRejectsUnknownModeBeforeBackendSelection(t *testing.T) {
	stdout, stderr, exitCode := runCLI(t, []string{"launch", "--mode", "browser"}, fleet.StubSource{})
	if exitCode == 0 || stdout != "" || !strings.Contains(stderr, "invalid launch mode") {
		t.Fatalf("launch exit/stdout/stderr = %d/%q/%q", exitCode, stdout, stderr)
	}
}

func TestLaunchUIRequiresLocalBackend(t *testing.T) {
	stdout, stderr, exitCode := runCLI(t, []string{"launch", "--mode", "ui", "--no-open"}, fleet.StubSource{})
	if exitCode == 0 || stdout != "" || !strings.Contains(stderr, "requires a Kubernetes reader") {
		t.Fatalf("launch exit/stdout/stderr = %d/%q/%q", exitCode, stdout, stderr)
	}
}

func TestLaunchUIStartsOnLoopback(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	stdout, stderr, exitCode := runUICLI(ctx, t, []string{
		"launch", "--mode", "ui", "--port", "0", "--no-open",
	}, &cacheReader{}, &fakeLocalClient{})
	if exitCode != 0 || stderr != "" || !strings.Contains(stdout, "sith ui listening on http://127.0.0.1:") {
		t.Fatalf("launch exit/stdout/stderr = %d/%q/%q", exitCode, stdout, stderr)
	}
}

func TestSelectUIBackendAcceptsExplicitDirectoryWithoutDefaultBackend(t *testing.T) {
	directory := writeLaunchKubeconfig(t)
	reader, local, err := selectUIBackend(nil, nil, directory)
	if err != nil || reader == nil || local == nil {
		t.Fatalf("selectUIBackend() = %#v, %#v, %v, want explicit directory backend", reader, local, err)
	}
}

func TestLaunchUIStartsFromExplicitDirectoryWithoutDefaultBackend(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("SITH_KUBECONFIG", "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var stdout, stderr bytes.Buffer
	exitCode := executeBackendContext(ctx, []string{
		"launch", "--mode", "ui", "--port", "0", "--no-open",
		"--kubeconfig-dir", writeLaunchKubeconfig(t),
	}, backend{source: fleet.StubSource{}}, &stdout, &stderr)
	if exitCode != 0 || stderr.Len() != 0 ||
		!strings.Contains(stdout.String(), "sith ui listening on http://127.0.0.1:") {
		t.Fatalf("launch exit/stdout/stderr = %d/%q/%q", exitCode, stdout.String(), stderr.String())
	}
}

func writeLaunchKubeconfig(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	config := []byte(`apiVersion: v1
kind: Config
clusters:
  - name: demo
    cluster:
      server: https://demo.invalid
users:
  - name: demo
    user: {}
contexts:
  - name: demo
    context:
      cluster: demo
      user: demo
current-context: demo
`)
	if err := os.WriteFile(filepath.Join(directory, "config.yaml"), config, 0o600); err != nil {
		t.Fatal(err)
	}
	return directory
}

func TestDesktopLaunchRejectsUIOnlyFlags(t *testing.T) {
	command := &cobra.Command{Use: "launch"}
	options := &uiOptions{address: "127.0.0.1"}
	bindUIFlags(command, options)
	if err := command.ParseFlags([]string{"--no-open"}); err != nil {
		t.Fatal(err)
	}
	if err := rejectDesktopUIFlags(command); err == nil || !strings.Contains(err.Error(), "--mode ui") {
		t.Fatalf("rejectDesktopUIFlags() error = %v, want explicit UI-mode guidance", err)
	}
}

func TestDesktopLaunchAcceptsSharedKubeconfigDirectoryFlag(t *testing.T) {
	command := &cobra.Command{Use: "launch"}
	options := &uiOptions{address: "127.0.0.1"}
	bindUIFlags(command, options)
	if err := command.ParseFlags([]string{"--kubeconfig-dir", t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	if err := rejectDesktopUIFlags(command); err != nil {
		t.Fatalf("rejectDesktopUIFlags() error = %v, want shared directory flag accepted", err)
	}
}
