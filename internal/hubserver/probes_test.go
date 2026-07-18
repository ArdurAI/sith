// SPDX-License-Identifier: Apache-2.0

package hubserver

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type probeChecker func(context.Context) error

func (checker probeChecker) Ping(ctx context.Context) error { return checker(ctx) }

func TestProbeHandlerSeparatesLivenessFromDatabaseReadiness(t *testing.T) {
	checker := probeChecker(func(context.Context) error { return errors.New("database endpoint secret") })
	handler, err := NewProbeHandler(checker)
	if err != nil {
		t.Fatal(err)
	}

	live := serveProbe(t, handler.ServeLiveness, http.MethodGet, LivenessPath)
	assertProbeResponse(t, live, http.StatusNoContent)
	ready := serveProbe(t, handler.ServeReadiness, http.MethodGet, ReadinessPath)
	assertProbeResponse(t, ready, http.StatusServiceUnavailable)
	if ready.Body.String() != "" {
		t.Fatalf("readiness body leaked dependency state: %q", ready.Body.String())
	}
}

func TestProbeHandlerReportsReadyAndBoundsDependencyCheck(t *testing.T) {
	t.Run("ready", func(t *testing.T) {
		handler, err := NewProbeHandler(probeChecker(func(context.Context) error { return nil }))
		if err != nil {
			t.Fatal(err)
		}
		assertProbeResponse(t, serveProbe(t, handler.ServeReadiness, http.MethodGet, ReadinessPath), http.StatusNoContent)
	})

	t.Run("timeout", func(t *testing.T) {
		var deadline time.Time
		handler, err := NewProbeHandler(probeChecker(func(ctx context.Context) error {
			var ok bool
			deadline, ok = ctx.Deadline()
			if !ok {
				t.Fatal("readiness checker received no deadline")
			}
			<-ctx.Done()
			return ctx.Err()
		}))
		if err != nil {
			t.Fatal(err)
		}
		handler.timeout = 5 * time.Millisecond
		started := time.Now()
		response := serveProbe(t, handler.ServeReadiness, http.MethodGet, ReadinessPath)
		assertProbeResponse(t, response, http.StatusServiceUnavailable)
		if deadline.IsZero() || time.Since(started) > 250*time.Millisecond {
			t.Fatalf("readiness deadline/elapsed = %s/%s", deadline, time.Since(started))
		}
	})

	t.Run("caller cancellation", func(t *testing.T) {
		checker := probeChecker(func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		})
		handler, err := NewProbeHandler(checker)
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodGet, ReadinessPath, nil)
		ctx, cancel := context.WithCancel(request.Context())
		cancel()
		response := httptest.NewRecorder()
		handler.ServeReadiness(response, request.WithContext(ctx))
		assertProbeResponse(t, response, http.StatusServiceUnavailable)
	})
}

func TestProbeHandlerFailsClosedOnPanicAndInvalidRequests(t *testing.T) {
	handler, err := NewProbeHandler(probeChecker(func(context.Context) error { panic("database endpoint secret") }))
	if err != nil {
		t.Fatal(err)
	}
	assertProbeResponse(t, serveProbe(t, handler.ServeReadiness, http.MethodGet, ReadinessPath), http.StatusServiceUnavailable)

	for _, test := range []struct {
		name   string
		serve  func(http.ResponseWriter, *http.Request)
		method string
		target string
	}{
		{name: "liveness post", serve: handler.ServeLiveness, method: http.MethodPost, target: LivenessPath},
		{name: "liveness query", serve: handler.ServeLiveness, method: http.MethodGet, target: LivenessPath + "?detail=1"},
		{name: "liveness path", serve: handler.ServeLiveness, method: http.MethodGet, target: LivenessPath + "/"},
		{name: "readiness head", serve: handler.ServeReadiness, method: http.MethodHead, target: ReadinessPath},
		{name: "readiness query", serve: handler.ServeReadiness, method: http.MethodGet, target: ReadinessPath + "?detail=1"},
		{name: "readiness encoded path", serve: handler.ServeReadiness, method: http.MethodGet, target: "/%72eadyz"},
	} {
		t.Run(test.name, func(t *testing.T) {
			assertProbeResponse(t, serveProbe(t, test.serve, test.method, test.target), http.StatusNotFound)
		})
	}

	if _, err := NewProbeHandler(nil); err == nil {
		t.Fatal("NewProbeHandler accepted a missing readiness checker")
	}
	var nilHandler *ProbeHandler
	assertProbeResponse(t, serveProbe(t, nilHandler.ServeLiveness, http.MethodGet, LivenessPath), http.StatusNotFound)
}

func serveProbe(t *testing.T, serve func(http.ResponseWriter, *http.Request), method, target string) *httptest.ResponseRecorder {
	t.Helper()
	response := httptest.NewRecorder()
	serve(response, httptest.NewRequest(method, target, nil))
	return response
}

func assertProbeResponse(t *testing.T, response *httptest.ResponseRecorder, wantStatus int) {
	t.Helper()
	if response.Code != wantStatus || response.Body.Len() != 0 || response.Header().Get("Content-Length") != "0" ||
		response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("X-Content-Type-Options") != "nosniff" || response.Header().Get("Content-Type") != "" {
		t.Fatalf("probe response = status %d headers %#v body %q, want body-free %d", response.Code, response.Header(), response.Body.String(), wantStatus)
	}
}
