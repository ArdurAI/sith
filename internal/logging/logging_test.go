// SPDX-License-Identifier: Apache-2.0

package logging

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestNewTextHandler(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	logger, err := New(&output, "info", "text")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	logger.Info("ready", "clusters", 0)
	if got := output.String(); !strings.Contains(got, "level=INFO") || !strings.Contains(got, "msg=ready") {
		t.Fatalf("text log = %q, want level and message", got)
	}
	if strings.HasPrefix(strings.TrimSpace(output.String()), "{") {
		t.Fatalf("text log unexpectedly looks like JSON: %q", output.String())
	}
}

func TestNewJSONHandler(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	logger, err := New(&output, "info", "json")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	logger.Info("ready", "clusters", 0)
	var entry map[string]any
	if err := json.Unmarshal(output.Bytes(), &entry); err != nil {
		t.Fatalf("unmarshal log line %q: %v", output.String(), err)
	}
	if entry["msg"] != "ready" || entry["level"] != "INFO" {
		t.Fatalf("JSON log = %#v, want ready/INFO", entry)
	}
}

func TestLevelFiltering(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	logger, err := New(&output, "warn", "text")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	logger.Info("hidden")
	logger.Warn("visible")
	if strings.Contains(output.String(), "hidden") || !strings.Contains(output.String(), "visible") {
		t.Fatalf("filtered output = %q", output.String())
	}
}

func TestInvalidLevelOrFormatErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		level  string
		format string
	}{
		{name: "level", level: "verbose", format: "text"},
		{name: "format", level: "info", format: "console"},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := New(&bytes.Buffer{}, test.level, test.format); err == nil {
				t.Fatal("New() error = nil, want an error")
			}
		})
	}
}
