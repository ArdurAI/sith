// SPDX-License-Identifier: Apache-2.0

package observability

import (
	"fmt"
	"log/slog"

	"github.com/ArdurAI/sith/internal/tracing"
)

// NewSlogTraceObserver constructs the local structured trace recorder used by the hub runtime.
// It serializes only the already-validated trace contract; it owns no network, listener, queue,
// persistence, exporter, or SDK integration.
func NewSlogTraceObserver(logger *slog.Logger) (tracing.Observer, error) {
	if logger == nil {
		return nil, fmt.Errorf("construct trace observer: logger is required")
	}
	return slogTraceObserver{logger: logger}, nil
}

type slogTraceObserver struct {
	logger *slog.Logger
}

func (observer slogTraceObserver) ObserveTrace(event tracing.Event) {
	if observer.logger == nil || event.Validate() != nil {
		return
	}
	observer.logger.Info(
		"trace stage",
		"trace_id", event.TraceID,
		"trace_stage", event.Stage,
		"trace_outcome", event.Outcome,
		"duration_ms", event.Duration.Milliseconds(),
	)
}
