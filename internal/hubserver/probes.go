// SPDX-License-Identifier: Apache-2.0

package hubserver

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

const (
	// LivenessPath is the fixed dependency-free process probe route.
	LivenessPath = "/healthz"
	// ReadinessPath is the fixed application-database readiness route.
	ReadinessPath = "/readyz"

	readinessTimeout = time.Second
)

// ReadinessChecker verifies one required Hub dependency. Implementations must honor context
// cancellation and must not return sensitive details to the caller; ProbeHandler discards errors.
type ReadinessChecker interface {
	Ping(context.Context) error
}

// ReadinessOutcome is the closed self-observability result of one completed database readiness
// check. It deliberately does not distinguish dependency failure classes or carry request data.
type ReadinessOutcome string

const (
	// ReadinessOutcomeReady means the application database responded inside the server deadline.
	ReadinessOutcomeReady ReadinessOutcome = "ready"
	// ReadinessOutcomeUnavailable collapses errors, cancellation, timeout, and checker panic.
	ReadinessOutcomeUnavailable ReadinessOutcome = "unavailable"
)

// Valid reports whether the outcome belongs to the fixed readiness vocabulary.
func (outcome ReadinessOutcome) Valid() bool {
	return outcome == ReadinessOutcomeReady || outcome == ReadinessOutcomeUnavailable
}

// ReadinessObserver records one completed valid readiness dependency check. Implementations must
// not affect probe behavior; ProbeHandler recovers observer panics at the instrumentation seam.
type ReadinessObserver interface {
	ObserveReadiness(ReadinessOutcome, time.Duration)
}

// ProbeHandlerConfig supplies the required dependency and optional self-observability sink.
type ProbeHandlerConfig struct {
	Checker  ReadinessChecker
	Observer ReadinessObserver
}

// ProbeHandler exposes fixed, body-free liveness and readiness responses. It deliberately does
// not authenticate because the kubelet has no Hub identity, and it never reveals dependency or
// error details.
type ProbeHandler struct {
	checker  ReadinessChecker
	observer ReadinessObserver
	timeout  time.Duration
}

// NewProbeHandler constructs the Hub probe boundary over its required application database.
func NewProbeHandler(config ProbeHandlerConfig) (*ProbeHandler, error) {
	if config.Checker == nil {
		return nil, fmt.Errorf("construct hub probes: readiness checker is required")
	}
	return &ProbeHandler{checker: config.Checker, observer: config.Observer, timeout: readinessTimeout}, nil
}

// ServeLiveness reports only whether the Hub HTTP process can serve this exact request. Required
// dependencies intentionally do not participate so a dependency outage cannot cause a restart
// storm.
func (handler *ProbeHandler) ServeLiveness(response http.ResponseWriter, request *http.Request) {
	if handler == nil || !validProbeRequest(request, LivenessPath) {
		writeProbeStatus(response, http.StatusNotFound)
		return
	}
	writeProbeStatus(response, http.StatusNoContent)
}

// ServeReadiness reports whether the application database responds inside the fixed deadline.
// Errors and panics collapse to one body-free unavailable response.
func (handler *ProbeHandler) ServeReadiness(response http.ResponseWriter, request *http.Request) {
	if handler == nil || handler.checker == nil || handler.timeout <= 0 || handler.timeout > time.Second ||
		!validProbeRequest(request, ReadinessPath) {
		writeProbeStatus(response, http.StatusNotFound)
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), handler.timeout)
	defer cancel()
	started := time.Now()
	ready := dependencyReady(ctx, handler.checker)
	outcome := ReadinessOutcomeUnavailable
	status := http.StatusServiceUnavailable
	if ready {
		outcome = ReadinessOutcomeReady
		status = http.StatusNoContent
	}
	observeReadiness(handler.observer, outcome, time.Since(started))
	writeProbeStatus(response, status)
}

func validProbeRequest(request *http.Request, path string) bool {
	return request != nil && request.Method == http.MethodGet && request.URL != nil &&
		request.URL.Path == path && request.URL.EscapedPath() == path && request.URL.RawQuery == ""
}

func dependencyReady(ctx context.Context, checker ReadinessChecker) (ready bool) {
	defer func() {
		if recover() != nil {
			ready = false
		}
	}()
	return checker.Ping(ctx) == nil
}

func observeReadiness(observer ReadinessObserver, outcome ReadinessOutcome, duration time.Duration) {
	if observer == nil {
		return
	}
	defer func() {
		_ = recover()
	}()
	observer.ObserveReadiness(outcome, duration)
}

func writeProbeStatus(response http.ResponseWriter, status int) {
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Length", "0")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	response.WriteHeader(status)
}
