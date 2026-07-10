// SPDX-License-Identifier: Apache-2.0
//go:build e2e

// Package e2e_test verifies the compiled Sith process boundary.
package e2e_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestBinarySmoke(t *testing.T) {
	root := repositoryRoot(t)
	binary := filepath.Join(t.TempDir(), "sith")
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	build := exec.CommandContext(ctx, "go", "build", "-trimpath", "-o", binary, "./cmd/sith")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build binary: %v\n%s", err, output)
	}

	tests := []struct {
		name      string
		args      []string
		contains  string
		validJSON bool
	}{
		{name: "version text", args: []string{"version"}, contains: "sith dev"},
		{name: "version JSON", args: []string{"version", "-o", "json"}, validJSON: true},
		{name: "clusters text", args: []string{"clusters"}, contains: "No clusters found"},
		{name: "clusters JSON", args: []string{"clusters", "-o", "json"}, validJSON: true},
		{name: "ui stub", args: []string{"ui"}, contains: "not yet implemented"},
		{name: "hub stub", args: []string{"hub"}, contains: "phase-1+"},
		{name: "no arguments", contains: "Usage:"},
		{name: "help", args: []string{"--help"}, contains: "Usage:"},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			command := exec.CommandContext(ctx, binary, test.args...)
			command.Env = append(os.Environ(), "XDG_CONFIG_HOME="+t.TempDir())
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("run %v: %v\n%s", test.args, err, output)
			}
			if test.contains != "" && !strings.Contains(string(output), test.contains) {
				t.Fatalf("output = %q, want %q", output, test.contains)
			}
			if test.validJSON && !json.Valid(output) {
				t.Fatalf("output is not valid JSON: %q", output)
			}
		})
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
