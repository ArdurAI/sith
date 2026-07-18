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

type readinessObservation struct {
	outcome  ReadinessOutcome
	duration time.Duration
}

type readinessObserverFunc func(ReadinessOutcome, time.Duration)

func (observer readinessObserverFunc) ObserveReadiness(outcome ReadinessOutcome, duration time.Duration) {
	observer(outcome, duration)
}

type recordingReadinessObserver struct {
	events []readinessObservation
}

func (observer *recordingReadinessObserver) ObserveReadiness(outcome ReadinessOutcome, duration time.Duration) {
	observer.events = append(observer.events, readinessObservation{outcome: outcome, duration: duration})
}

func TestProbeHandlerSeparatesLivenessFromDatabaseReadiness(t *testing.T) {
	checker := probeChecker(func(context.Context) error { return errors.New("database endpoint secret") })
	observer := &recordingReadinessObserver{}
	handler, err := NewProbeHandler(ProbeHandlerConfig{
		Checker: checker, Observer: observer,
	})
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
	if len(observer.events) != 1 || observer.events[0].outcome != ReadinessOutcomeUnavailable || observer.events[0].duration < 0 {
		t.Fatalf("readiness observations = %#v", observer.events)
	}
}

func TestProbeHandlerReportsReadyAndBoundsDependencyCheck(t *testing.T) {
	t.Run("ready", func(t *testing.T) {
		observer := &recordingReadinessObserver{}
		handler, err := NewProbeHandler(ProbeHandlerConfig{
			Checker: probeChecker(func(context.Context) error { return nil }), Observer: observer,
		})
		if err != nil {
			t.Fatal(err)
		}
		assertProbeResponse(t, serveProbe(t, handler.ServeReadiness, http.MethodGet, ReadinessPath), http.StatusNoContent)
		assertReadinessObservation(t, observer, ReadinessOutcomeReady)
	})

	t.Run("timeout", func(t *testing.T) {
		var deadline time.Time
		observer := &recordingReadinessObserver{}
		handler, err := NewProbeHandler(ProbeHandlerConfig{Checker: probeChecker(func(ctx context.Context) error {
			var ok bool
			deadline, ok = ctx.Deadline()
			if !ok {
				t.Fatal("readiness checker received no deadline")
			}
			<-ctx.Done()
			return ctx.Err()
		}), Observer: observer})
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
		assertReadinessObservation(t, observer, ReadinessOutcomeUnavailable)
	})

	t.Run("caller cancellation", func(t *testing.T) {
		checker := probeChecker(func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		})
		observer := &recordingReadinessObserver{}
		handler, err := NewProbeHandler(ProbeHandlerConfig{Checker: checker, Observer: observer})
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodGet, ReadinessPath, nil)
		ctx, cancel := context.WithCancel(request.Context())
		cancel()
		response := httptest.NewRecorder()
		handler.ServeReadiness(response, request.WithContext(ctx))
		assertProbeResponse(t, response, http.StatusServiceUnavailable)
		assertReadinessObservation(t, observer, ReadinessOutcomeUnavailable)
	})
}

func TestProbeHandlerFailsClosedOnPanicAndInvalidRequests(t *testing.T) {
	observations := make([]ReadinessOutcome, 0, 1)
	checkerCalls := 0
	handler, err := NewProbeHandler(ProbeHandlerConfig{
		Checker: probeChecker(func(context.Context) error {
			checkerCalls++
			panic("database endpoint secret")
		}),
		Observer: readinessObserverFunc(func(outcome ReadinessOutcome, _ time.Duration) {
			observations = append(observations, outcome)
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	assertProbeResponse(t, serveProbe(t, handler.ServeReadiness, http.MethodGet, ReadinessPath), http.StatusServiceUnavailable)
	if checkerCalls != 1 || len(observations) != 1 || observations[0] != ReadinessOutcomeUnavailable {
		t.Fatalf("checker calls/observations = %d/%v", checkerCalls, observations)
	}

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
	if checkerCalls != 1 || len(observations) != 1 {
		t.Fatalf("invalid requests reached checker/observer: calls=%d observations=%v", checkerCalls, observations)
	}

	if _, err := NewProbeHandler(ProbeHandlerConfig{}); err == nil {
		t.Fatal("NewProbeHandler accepted a missing readiness checker")
	}
	var nilHandler *ProbeHandler
	assertProbeResponse(t, serveProbe(t, nilHandler.ServeLiveness, http.MethodGet, LivenessPath), http.StatusNotFound)
}

func TestProbeHandlerRecoversReadinessObserverPanic(t *testing.T) {
	checkerCalls := 0
	handler, err := NewProbeHandler(ProbeHandlerConfig{
		Checker: probeChecker(func(context.Context) error {
			checkerCalls++
			return nil
		}),
		Observer: readinessObserverFunc(func(ReadinessOutcome, time.Duration) { panic("metrics fault") }),
	})
	if err != nil {
		t.Fatal(err)
	}
	assertProbeResponse(t, serveProbe(t, handler.ServeReadiness, http.MethodGet, ReadinessPath), http.StatusNoContent)
	if checkerCalls != 1 {
		t.Fatalf("readiness checker calls = %d, want 1", checkerCalls)
	}
}

func serveProbe(t *testing.T, serve func(http.ResponseWriter, *http.Request), method, target string) *httptest.ResponseRecorder {
	t.Helper()
	response := httptest.NewRecorder()
	serve(response, httptest.NewRequest(method, target, nil))
	return response
}

func assertReadinessObservation(t *testing.T, observer *recordingReadinessObserver, want ReadinessOutcome) {
	t.Helper()
	if len(observer.events) != 1 || observer.events[0].outcome != want || observer.events[0].duration < 0 {
		t.Fatalf("readiness observations = %#v, want one %q", observer.events, want)
	}
}

func assertProbeResponse(t *testing.T, response *httptest.ResponseRecorder, wantStatus int) {
	t.Helper()
	if response.Code != wantStatus || response.Body.Len() != 0 || response.Header().Get("Content-Length") != "0" ||
		response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("X-Content-Type-Options") != "nosniff" || response.Header().Get("Content-Type") != "" {
		t.Fatalf("probe response = status %d headers %#v body %q, want body-free %d", response.Code, response.Header(), response.Body.String(), wantStatus)
	}
}
