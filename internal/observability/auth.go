// SPDX-License-Identifier: Apache-2.0

package observability

import (
	"fmt"
	"log/slog"

	"github.com/ArdurAI/sith/internal/hubserver"
)

// NewSlogAuthObserver constructs a synchronous structured authentication-refusal recorder for a
// controlled embedding. The Hub runtime uses the process-supervised auditdelivery observer so an
// arbitrary local slog handler cannot delay a governed response. This adapter serializes only the
// closed AuthEvent contract; it owns no listener, exporter, queue, persistence, telemetry backend,
// or network path.
func NewSlogAuthObserver(logger *slog.Logger) (hubserver.AuthObserver, error) {
	if logger == nil {
		return nil, fmt.Errorf("construct authentication observer: logger is required")
	}
	return slogAuthObserver{logger: logger}, nil
}

type slogAuthObserver struct {
	logger *slog.Logger
}

func (observer slogAuthObserver) ObserveAuth(event hubserver.AuthEvent) {
	if observer.logger == nil || event.Validate() != nil || event.Outcome != hubserver.AuthOutcomeRefused {
		return
	}
	observer.logger.Warn(
		"authentication refused",
		"surface", "hub-auth",
		"auth_outcome", event.Outcome,
	)
}
