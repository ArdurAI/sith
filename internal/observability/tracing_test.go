// SPDX-License-Identifier: Apache-2.0

package observability

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/ArdurAI/sith/internal/tracing"
)

func TestSlogTraceObserverEmitsOnlyValidatedTraceFields(t *testing.T) {
	var output bytes.Buffer
	observer, err := NewSlogTraceObserver(slog.New(slog.NewJSONHandler(&output, nil)))
	if err != nil {
		t.Fatal(err)
	}
	observer.ObserveTrace(tracing.Event{
		TraceID: "0123456789abcdef0123456789abcdef", Stage: tracing.StagePEPDecision,
		Outcome: tracing.OutcomeSuccess, Duration: 125 * time.Millisecond,
	})
	observer.ObserveTrace(tracing.Event{
		TraceID: "workspace-a/token=secret", Stage: tracing.StagePEPDecision,
		Outcome: tracing.OutcomeSuccess, Duration: time.Millisecond,
	})
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("trace lines = %q", output.String())
	}
	var record map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &record); err != nil {
		t.Fatal(err)
	}
	for field := range record {
		if !map[string]bool{"time": true, "level": true, "msg": true, "trace_id": true, "trace_stage": true, "trace_outcome": true, "duration_ms": true}[field] {
			t.Fatalf("trace log contains unexpected field %q: %#v", field, record)
		}
	}
	if record["msg"] != "trace stage" || record["trace_id"] != "0123456789abcdef0123456789abcdef" ||
		record["trace_stage"] != string(tracing.StagePEPDecision) || record["trace_outcome"] != string(tracing.OutcomeSuccess) ||
		record["duration_ms"] != float64(125) {
		t.Fatalf("trace record = %#v", record)
	}
	for _, forbidden := range []string{"workspace-a", "token=secret", "actor", "spoke", "endpoint", "arguments_digest"} {
		if strings.Contains(lines[0], forbidden) {
			t.Fatalf("trace log leaked %q: %s", forbidden, lines[0])
		}
	}
}

func TestNewSlogTraceObserverRejectsNilLogger(t *testing.T) {
	if _, err := NewSlogTraceObserver(nil); err == nil {
		t.Fatal("NewSlogTraceObserver() accepted nil logger")
	}
}
