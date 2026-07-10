// SPDX-License-Identifier: Apache-2.0

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaults(t *testing.T) {
	t.Parallel()

	want := Config{LogLevel: "info", LogFormat: "text"}
	if got := Defaults(); got != want {
		t.Fatalf("Defaults() = %#v, want %#v", got, want)
	}
}

func TestLoadFromFile(t *testing.T) {
	clearConfigEnvironment(t)
	path := writeConfig(t, "log_level: debug\nlog_format: json\n")

	got, err := Load(path, Overrides{})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.LogLevel != "debug" || got.LogFormat != "json" {
		t.Fatalf("Load() = %#v, want file values", got)
	}
}

func TestEnvOverridesFile(t *testing.T) {
	clearConfigEnvironment(t)
	path := writeConfig(t, "log_level: debug\nlog_format: json\n")
	t.Setenv("SITH_LOG_LEVEL", "warn")
	t.Setenv("SITH_LOG_FORMAT", "text")

	got, err := Load(path, Overrides{})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.LogLevel != "warn" || got.LogFormat != "text" {
		t.Fatalf("Load() = %#v, want environment values", got)
	}
}

func TestOverridesBeatEnv(t *testing.T) {
	clearConfigEnvironment(t)
	t.Setenv("SITH_LOG_LEVEL", "warn")
	t.Setenv("SITH_LOG_FORMAT", "json")

	got, err := Load("", Overrides{LogLevel: "error", LogFormat: "text"})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.LogLevel != "error" || got.LogFormat != "text" {
		t.Fatalf("Load() = %#v, want flag overrides", got)
	}
}

func TestMissingDefaultFileIsOK(t *testing.T) {
	clearConfigEnvironment(t)

	got, err := Load("", Overrides{})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got != Defaults() {
		t.Fatalf("Load() = %#v, want %#v", got, Defaults())
	}
}

func TestExplicitMissingPathErrors(t *testing.T) {
	clearConfigEnvironment(t)

	_, err := Load(filepath.Join(t.TempDir(), "missing.yaml"), Overrides{})
	if err == nil {
		t.Fatal("Load() error = nil, want an error")
	}
}

func TestInvalidLevelRejected(t *testing.T) {
	t.Parallel()

	for _, level := range []string{"", "verbose", "INFO"} {
		level := level
		t.Run(level, func(t *testing.T) {
			t.Parallel()
			if err := (Config{LogLevel: level, LogFormat: "text"}).Validate(); err == nil {
				t.Fatalf("Validate() error = nil for log level %q", level)
			}
		})
	}
}

func TestInvalidFormatRejected(t *testing.T) {
	t.Parallel()

	for _, format := range []string{"", "console", "JSON"} {
		format := format
		t.Run(format, func(t *testing.T) {
			t.Parallel()
			if err := (Config{LogLevel: "info", LogFormat: format}).Validate(); err == nil {
				t.Fatalf("Validate() error = nil for log format %q", format)
			}
		})
	}
}

func TestUnknownFieldRejected(t *testing.T) {
	clearConfigEnvironment(t)
	path := writeConfig(t, "log_level: info\ntelemetry: true\n")

	if _, err := Load(path, Overrides{}); err == nil {
		t.Fatal("Load() error = nil, want unknown field rejection")
	}
}

func TestMultipleDocumentsRejected(t *testing.T) {
	clearConfigEnvironment(t)
	path := writeConfig(t, "log_level: info\n---\nlog_format: json\n")

	if _, err := Load(path, Overrides{}); err == nil {
		t.Fatal("Load() error = nil, want multiple document rejection")
	}
}

func TestOversizedFileRejected(t *testing.T) {
	clearConfigEnvironment(t)
	path := writeConfig(t, strings.Repeat("x", maxConfigBytes+1))

	if _, err := Load(path, Overrides{}); err == nil {
		t.Fatal("Load() error = nil, want oversized file rejection")
	}
}

func TestKubeconfigEnvironmentApplied(t *testing.T) {
	clearConfigEnvironment(t)
	t.Setenv("SITH_KUBECONFIG", "/tmp/fleet-kubeconfig")

	got, err := Load("", Overrides{})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.KubeconfigPath != "/tmp/fleet-kubeconfig" {
		t.Fatalf("KubeconfigPath = %q", got.KubeconfigPath)
	}
}

func clearConfigEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("SITH_LOG_LEVEL", "")
	t.Setenv("SITH_LOG_FORMAT", "")
	t.Setenv("SITH_KUBECONFIG", "")
}

func writeConfig(t *testing.T, contents string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	return path
}
