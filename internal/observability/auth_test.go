// SPDX-License-Identifier: Apache-2.0

package observability

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/ArdurAI/sith/internal/hubserver"
)

func TestSlogAuthObserverEmitsOnlyValidatedFixedFields(t *testing.T) {
	var output bytes.Buffer
	observer, err := NewSlogAuthObserver(slog.New(slog.NewJSONHandler(&output, nil)))
	if err != nil {
		t.Fatal(err)
	}
	observer.ObserveAuth(hubserver.AuthEvent{Outcome: hubserver.AuthOutcomeRefused})
	observer.ObserveAuth(hubserver.AuthEvent{Outcome: hubserver.AuthOutcomeAccepted})
	observer.ObserveAuth(hubserver.AuthEvent{Outcome: "token=secret"})

	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("authentication log lines = %q", output.String())
	}
	var record map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &record); err != nil {
		t.Fatal(err)
	}
	for field := range record {
		if !map[string]bool{"time": true, "level": true, "msg": true, "surface": true, "auth_outcome": true}[field] {
			t.Fatalf("authentication log contains unexpected field %q: %#v", field, record)
		}
	}
	if record["level"] != "WARN" || record["msg"] != "authentication refused" ||
		record["surface"] != "hub-auth" || record["auth_outcome"] != string(hubserver.AuthOutcomeRefused) {
		t.Fatalf("authentication record = %#v", record)
	}
	for _, forbidden := range []string{"token=secret", "workspace-a", "trace", "header", "principal", "verifier"} {
		if strings.Contains(lines[0], forbidden) {
			t.Fatalf("authentication record leaked %q: %s", forbidden, lines[0])
		}
	}
}

func TestSlogAuthObserverTextHandlerAndNilLogger(t *testing.T) {
	var output bytes.Buffer
	observer, err := NewSlogAuthObserver(slog.New(slog.NewTextHandler(&output, nil)))
	if err != nil {
		t.Fatal(err)
	}
	observer.ObserveAuth(hubserver.AuthEvent{Outcome: hubserver.AuthOutcomeRefused})
	line := output.String()
	for _, field := range []string{"level=WARN", "msg=\"authentication refused\"", "surface=hub-auth", "auth_outcome=refused"} {
		if !strings.Contains(line, field) {
			t.Fatalf("text authentication log missing %q: %q", field, line)
		}
	}
	for _, forbidden := range []string{"token", "workspace", "trace", "header", "principal", "verifier"} {
		if strings.Contains(line, forbidden) {
			t.Fatalf("text authentication record leaked %q: %q", forbidden, line)
		}
	}
	if _, err := NewSlogAuthObserver(nil); err == nil {
		t.Fatal("NewSlogAuthObserver() accepted nil logger")
	}
}
