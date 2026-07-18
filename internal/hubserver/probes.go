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

// ProbeHandler exposes fixed, body-free liveness and readiness responses. It deliberately does
// not authenticate because the kubelet has no Hub identity, and it never reveals dependency or
// error details.
type ProbeHandler struct {
	checker ReadinessChecker
	timeout time.Duration
}

// NewProbeHandler constructs the Hub probe boundary over its required application database.
func NewProbeHandler(checker ReadinessChecker) (*ProbeHandler, error) {
	if checker == nil {
		return nil, fmt.Errorf("construct hub probes: readiness checker is required")
	}
	return &ProbeHandler{checker: checker, timeout: readinessTimeout}, nil
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
	if !dependencyReady(ctx, handler.checker) {
		writeProbeStatus(response, http.StatusServiceUnavailable)
		return
	}
	writeProbeStatus(response, http.StatusNoContent)
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

func writeProbeStatus(response http.ResponseWriter, status int) {
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Length", "0")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	response.WriteHeader(status)
}
