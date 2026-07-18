// SPDX-License-Identifier: Apache-2.0

package hubserver

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ArdurAI/sith/internal/fleet"
	"github.com/ArdurAI/sith/internal/hubauth"
	"github.com/ArdurAI/sith/internal/hubfleet"
	"github.com/ArdurAI/sith/internal/pep"
	"github.com/ArdurAI/sith/internal/tenancy"
	"github.com/ArdurAI/sith/internal/tracing"
)

type fleetRefresherFunc func(context.Context, tenancy.Scope) (fleet.Coverage, error)

func (function fleetRefresherFunc) Collect(ctx context.Context, scope tenancy.Scope) (fleet.Coverage, error) {
	return function(ctx, scope)
}

type fleetReaderFunc func(context.Context, tenancy.Scope, time.Duration, time.Time) (fleet.FleetResult, error)

func (function fleetReaderFunc) ReadFleet(
	ctx context.Context,
	scope tenancy.Scope,
	freshness time.Duration,
	now time.Time,
) (fleet.FleetResult, error) {
	return function(ctx, scope, freshness, now)
}

type fleetImageSearcherFunc func(context.Context, tenancy.Scope, hubfleet.ImageSearchRequest) (fleet.QueryResult, error)

func (function fleetImageSearcherFunc) Search(
	ctx context.Context,
	scope tenancy.Scope,
	request hubfleet.ImageSearchRequest,
) (fleet.QueryResult, error) {
	return function(ctx, scope, request)
}

type fleetCVEIdentifierSearcherFunc func(context.Context, tenancy.Scope, hubfleet.CVEIdentifierSearchRequest) (fleet.QueryResult, error)

func (function fleetCVEIdentifierSearcherFunc) SearchByIdentifier(
	ctx context.Context,
	scope tenancy.Scope,
	request hubfleet.CVEIdentifierSearchRequest,
) (fleet.QueryResult, error) {
	return function(ctx, scope, request)
}

var _ FleetRefresher = fleetRefresherFunc(nil)
var _ hubfleet.FleetReader = fleetReaderFunc(nil)
var _ FleetImageSearcher = fleetImageSearcherFunc(nil)
var _ FleetCVESearcher = fleetImageSearcherFunc(nil)
var _ FleetCVEIdentifierSearcher = fleetCVEIdentifierSearcherFunc(nil)

type fleetReadObserverFunc func(hubfleet.FleetReadOutcome)

func (function fleetReadObserverFunc) ObserveFleetRead(outcome hubfleet.FleetReadOutcome) {
	function(outcome)
}

func TestFleetHandlerRefreshUsesOnlySignedWorkspaceScope(t *testing.T) {
	now := time.Date(2026, 7, 14, 7, 0, 0, 0, time.UTC)
	verifier, privateKey := fleetTestVerifier(t, now)
	called := false
	var observed []hubfleet.FleetReadOutcome
	handler, err := NewFleetHandler(FleetHandlerConfig{
		Verifier: verifier,
		Collector: fleetRefresherFunc(func(_ context.Context, scope tenancy.Scope) (fleet.Coverage, error) {
			called = true
			if got, want := scope.WorkspaceID(), tenancy.WorkspaceID("workspace-a"); got != want {
				t.Fatalf("workspace scope = %q, want %q", got, want)
			}
			if got, want := scope.Subject(), "user:alice"; got != want {
				t.Fatalf("scope subject = %q, want %q", got, want)
			}
			return fleet.Coverage{Requested: 2, Reachable: 2}, nil
		}),
		Reader: fleetReaderFunc(func(context.Context, tenancy.Scope, time.Duration, time.Time) (fleet.FleetResult, error) {
			t.Fatal("refresh reached fleet reader")
			return fleet.FleetResult{}, nil
		}),
		ImageSearcher: fleetImageSearcherFunc(func(context.Context, tenancy.Scope, hubfleet.ImageSearchRequest) (fleet.QueryResult, error) {
			t.Fatal("refresh reached image searcher")
			return fleet.QueryResult{}, nil
		}),
		PEP: fleetTestPEP(t, pep.AllowReadHook{}),
		ReadObserver: fleetReadObserverFunc(func(outcome hubfleet.FleetReadOutcome) {
			observed = append(observed, outcome)
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	request := authenticatedFleetRequest(t, http.MethodPost, "/v1/workspaces/workspace-a/fleet:refresh", privateKey, now)
	request.Header.Set("X-Workspace", "workspace-b")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
	}
	if !called {
		t.Fatal("refresh collector was not called")
	}
	if len(observed) != 0 {
		t.Fatalf("refresh emitted fleet-read observations = %q", observed)
	}
	if response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("Pragma") != "no-cache" {
		t.Fatalf("refresh response caching headers = Cache-Control %q, Pragma %q", response.Header().Get("Cache-Control"), response.Header().Get("Pragma"))
	}
	var coverage fleet.Coverage
	if err := json.NewDecoder(response.Body).Decode(&coverage); err != nil {
		t.Fatal(err)
	}
	if coverage.Requested != 2 || coverage.Reachable != 2 || len(coverage.Unreachable) != 0 || len(coverage.Stale) != 0 {
		t.Fatalf("coverage = %+v", coverage)
	}
}

func TestFleetHandlerMintsLocalTraceAfterSignedScope(t *testing.T) {
	now := time.Date(2026, 7, 14, 7, 0, 0, 0, time.UTC)
	verifier, privateKey := fleetTestVerifier(t, now)
	var auditEvents []pep.AuditEvent
	var traceEvents []tracing.Event
	enforcer, err := pep.NewEnforcer(pep.Config{
		Hook: pep.AllowReadHook{},
		Auditor: pep.AuditFunc(func(_ context.Context, event pep.AuditEvent) error {
			auditEvents = append(auditEvents, event)
			return nil
		}),
		TraceObserver: tracing.ObserverFunc(func(event tracing.Event) { traceEvents = append(traceEvents, event) }),
	})
	if err != nil {
		t.Fatal(err)
	}
	var collectorTrace tracing.ID
	handler, err := NewFleetHandler(FleetHandlerConfig{
		Verifier: verifier,
		Collector: fleetRefresherFunc(func(ctx context.Context, scope tenancy.Scope) (fleet.Coverage, error) {
			var ok bool
			collectorTrace, ok = tracing.FromContext(ctx)
			if !ok {
				t.Fatal("authenticated collector received no trace context")
			}
			if err := enforcer.AuthorizeRead(ctx, scope, pep.NewReadInput(pep.VerbSpokeSnapshotRefresh, nil)); err != nil {
				t.Fatalf("AuthorizeRead() error = %v", err)
			}
			return fleet.Coverage{Requested: 1, Reachable: 1}, nil
		}),
		Reader: fleetReaderFunc(func(context.Context, tenancy.Scope, time.Duration, time.Time) (fleet.FleetResult, error) {
			t.Fatal("refresh reached fleet reader")
			return fleet.FleetResult{}, nil
		}),
		ImageSearcher: fleetImageSearcherFunc(func(context.Context, tenancy.Scope, hubfleet.ImageSearchRequest) (fleet.QueryResult, error) {
			t.Fatal("refresh reached image searcher")
			return fleet.QueryResult{}, nil
		}),
		PEP: enforcer,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := authenticatedFleetRequest(t, http.MethodPost, "/v1/workspaces/workspace-a/fleet:refresh", privateKey, now)
	request.Header.Set("Traceparent", "00-0123456789abcdef0123456789abcdef-0123456789abcdef-01")
	request.Header.Set("X-B3-Traceid", "0123456789abcdef0123456789abcdef")
	request.Header.Set("X-Request-ID", "workspace-a/token=secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !collectorTrace.Valid() {
		t.Fatalf("status = %d trace = %q body = %q", response.Code, collectorTrace, response.Body.String())
	}
	if collectorTrace == "0123456789abcdef0123456789abcdef" || len(auditEvents) != 1 || auditEvents[0].TraceID != collectorTrace ||
		len(traceEvents) != 1 || traceEvents[0].TraceID != collectorTrace || traceEvents[0].Stage != tracing.StagePEPDecision {
		t.Fatalf("trace propagation = collector %q audits %#v events %#v", collectorTrace, auditEvents, traceEvents)
	}
	for _, name := range []string{"Traceparent", "Tracestate", "B3", "X-B3-Traceid", "X-Request-ID", "X-Correlation-ID", "X-Trace-ID"} {
		if value := response.Header().Get(name); value != "" {
			t.Fatalf("response echoed untrusted trace header %s: %q", name, value)
		}
	}
}

func TestFleetHandlerReadConstructsRequestScopedSource(t *testing.T) {
	now := time.Date(2026, 7, 14, 7, 0, 0, 0, time.UTC)
	verifier, privateKey := fleetTestVerifier(t, now)
	called := false
	var observed []hubfleet.FleetReadOutcome
	handler, err := NewFleetHandler(FleetHandlerConfig{
		Verifier: verifier,
		Collector: fleetRefresherFunc(func(context.Context, tenancy.Scope) (fleet.Coverage, error) {
			t.Fatal("fleet read reached collector")
			return fleet.Coverage{}, nil
		}),
		Reader: fleetReaderFunc(func(_ context.Context, scope tenancy.Scope, freshness time.Duration, gotNow time.Time) (fleet.FleetResult, error) {
			called = true
			if got, want := scope.WorkspaceID(), tenancy.WorkspaceID("workspace-a"); got != want {
				t.Fatalf("workspace scope = %q, want %q", got, want)
			}
			if freshness != 5*time.Minute {
				t.Fatalf("freshness = %s, want default 5m", freshness)
			}
			if gotNow.IsZero() {
				t.Fatal("fleet source supplied a zero observation time")
			}
			return fleet.FleetResult{Clusters: []fleet.Cluster{{Name: "spoke-a", SourceKind: hubfleet.SourceKind, Reachable: true}}, Coverage: fleet.Coverage{Requested: 1, Reachable: 1}}, nil
		}),
		ImageSearcher: fleetImageSearcherFunc(func(context.Context, tenancy.Scope, hubfleet.ImageSearchRequest) (fleet.QueryResult, error) {
			t.Fatal("fleet read reached image searcher")
			return fleet.QueryResult{}, nil
		}),
		PEP: fleetTestPEP(t, pep.AllowReadHook{}),
		ReadObserver: fleetReadObserverFunc(func(outcome hubfleet.FleetReadOutcome) {
			observed = append(observed, outcome)
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, authenticatedFleetRequest(t, http.MethodGet, "/v1/workspaces/workspace-a/fleet", privateKey, now))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
	}
	if !called {
		t.Fatal("fleet reader was not called")
	}
	if len(observed) != 1 || observed[0] != hubfleet.FleetReadOutcomeComplete {
		t.Fatalf("fleet read observations = %q, want complete", observed)
	}
	var result fleet.FleetResult
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if len(result.Clusters) != 1 || result.Clusters[0].Name != "spoke-a" || !result.Coverage.Complete() {
		t.Fatalf("fleet result = %+v", result)
	}
}

func TestFleetHandlerRejectsForeignWorkspaceBeforeDependencies(t *testing.T) {
	now := time.Date(2026, 7, 14, 7, 0, 0, 0, time.UTC)
	verifier, privateKey := fleetTestVerifier(t, now)
	handler, err := NewFleetHandler(FleetHandlerConfig{
		Verifier: verifier,
		Collector: fleetRefresherFunc(func(context.Context, tenancy.Scope) (fleet.Coverage, error) {
			t.Fatal("foreign workspace reached collector")
			return fleet.Coverage{}, nil
		}),
		Reader: fleetReaderFunc(func(context.Context, tenancy.Scope, time.Duration, time.Time) (fleet.FleetResult, error) {
			t.Fatal("foreign workspace reached reader")
			return fleet.FleetResult{}, nil
		}),
		ImageSearcher: fleetImageSearcherFunc(func(context.Context, tenancy.Scope, hubfleet.ImageSearchRequest) (fleet.QueryResult, error) {
			t.Fatal("foreign workspace reached image searcher")
			return fleet.QueryResult{}, nil
		}),
		PEP: fleetTestPEP(t, pep.AllowReadHook{}),
	})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, authenticatedFleetRequest(t, http.MethodGet, "/v1/workspaces/workspace-b/fleet", privateKey, now))
	if response.Code != http.StatusForbidden || response.Body.String() != "{\"error\":\"forbidden\"}\n" {
		t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
	}
}

func TestFleetHandlerRejectsUnsupportedRoutesMethodsAndQueries(t *testing.T) {
	now := time.Date(2026, 7, 14, 7, 0, 0, 0, time.UTC)
	verifier, privateKey := fleetTestVerifier(t, now)
	handler, err := NewFleetHandler(FleetHandlerConfig{
		Verifier: verifier,
		Collector: fleetRefresherFunc(func(context.Context, tenancy.Scope) (fleet.Coverage, error) {
			t.Fatal("invalid request reached collector")
			return fleet.Coverage{}, nil
		}),
		Reader: fleetReaderFunc(func(context.Context, tenancy.Scope, time.Duration, time.Time) (fleet.FleetResult, error) {
			t.Fatal("invalid request reached reader")
			return fleet.FleetResult{}, nil
		}),
		ImageSearcher: fleetImageSearcherFunc(func(context.Context, tenancy.Scope, hubfleet.ImageSearchRequest) (fleet.QueryResult, error) {
			t.Fatal("invalid request reached image searcher")
			return fleet.QueryResult{}, nil
		}),
		PEP: fleetTestPEP(t, pep.AllowReadHook{}),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name      string
		method    string
		target    string
		wantCode  int
		wantBody  string
		wantAllow string
	}{
		{name: "unknown route", method: http.MethodGet, target: "/v1/workspaces/workspace-a/fleet/extra", wantCode: http.StatusNotFound, wantBody: "{\"error\":\"not_found\"}\n"},
		{name: "query rejected", method: http.MethodGet, target: "/v1/workspaces/workspace-a/fleet?freshness=1s", wantCode: http.StatusNotFound, wantBody: "{\"error\":\"not_found\"}\n"},
		{name: "noncanonical image", method: http.MethodGet, target: "/v1/workspaces/workspace-a/fleet/images/registry.example%2Fpayments%3Alatest", wantCode: http.StatusNotFound, wantBody: "{\"error\":\"not_found\"}\n"},
		{name: "CVE suffix rejects extra path", method: http.MethodGet, target: "/v1/workspaces/workspace-a/fleet/images/sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/cves/extra", wantCode: http.StatusNotFound, wantBody: "{\"error\":\"not_found\"}\n"},
		{name: "CVE identifier rejects lower case", method: http.MethodGet, target: "/v1/workspaces/workspace-a/fleet/cves/cve-2026-0001", wantCode: http.StatusNotFound, wantBody: "{\"error\":\"not_found\"}\n"},
		{name: "CVE identifier rejects glob", method: http.MethodGet, target: "/v1/workspaces/workspace-a/fleet/cves/CVE-2026-0001*", wantCode: http.StatusNotFound, wantBody: "{\"error\":\"not_found\"}\n"},
		{name: "CVE identifier rejects extra path", method: http.MethodGet, target: "/v1/workspaces/workspace-a/fleet/cves/CVE-2026-0001/extra", wantCode: http.StatusNotFound, wantBody: "{\"error\":\"not_found\"}\n"},
		{name: "read method", method: http.MethodPost, target: "/v1/workspaces/workspace-a/fleet", wantCode: http.StatusMethodNotAllowed, wantBody: "{\"error\":\"method_not_allowed\"}\n", wantAllow: http.MethodGet},
		{name: "refresh method", method: http.MethodGet, target: "/v1/workspaces/workspace-a/fleet:refresh", wantCode: http.StatusMethodNotAllowed, wantBody: "{\"error\":\"method_not_allowed\"}\n", wantAllow: http.MethodPost},
		{name: "image method", method: http.MethodPost, target: "/v1/workspaces/workspace-a/fleet/images/sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", wantCode: http.StatusMethodNotAllowed, wantBody: "{\"error\":\"method_not_allowed\"}\n", wantAllow: http.MethodGet},
		{name: "CVE method", method: http.MethodPost, target: "/v1/workspaces/workspace-a/fleet/images/sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/cves", wantCode: http.StatusMethodNotAllowed, wantBody: "{\"error\":\"method_not_allowed\"}\n", wantAllow: http.MethodGet},
		{name: "CVE identifier method", method: http.MethodPost, target: "/v1/workspaces/workspace-a/fleet/cves/CVE-2026-0001", wantCode: http.StatusMethodNotAllowed, wantBody: "{\"error\":\"method_not_allowed\"}\n", wantAllow: http.MethodGet},
		{name: "CVE identifier unavailable", method: http.MethodGet, target: "/v1/workspaces/workspace-a/fleet/cves/CVE-2026-0001", wantCode: http.StatusServiceUnavailable, wantBody: "{\"error\":\"cve_identifier_search_unavailable\"}\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, authenticatedFleetRequest(t, test.method, test.target, privateKey, now))
			if response.Code != test.wantCode || response.Body.String() != test.wantBody || response.Header().Get("Allow") != test.wantAllow {
				t.Fatalf("status = %d, body = %q, allow = %q", response.Code, response.Body.String(), response.Header().Get("Allow"))
			}
		})
	}
}

func TestFleetHandlerHidesDependencyErrors(t *testing.T) {
	now := time.Date(2026, 7, 14, 7, 0, 0, 0, time.UTC)
	verifier, privateKey := fleetTestVerifier(t, now)
	handler, err := NewFleetHandler(FleetHandlerConfig{
		Verifier: verifier,
		Collector: fleetRefresherFunc(func(context.Context, tenancy.Scope) (fleet.Coverage, error) {
			return fleet.Coverage{}, errors.New("secret token leaked")
		}),
		Reader: fleetReaderFunc(func(context.Context, tenancy.Scope, time.Duration, time.Time) (fleet.FleetResult, error) {
			return fleet.FleetResult{}, errors.New("database topology leaked")
		}),
		ImageSearcher: fleetImageSearcherFunc(func(context.Context, tenancy.Scope, hubfleet.ImageSearchRequest) (fleet.QueryResult, error) {
			return fleet.QueryResult{}, errors.New("registry credential leaked")
		}),
		PEP: fleetTestPEP(t, pep.AllowReadHook{}),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		method string
		target string
		body   string
	}{
		{method: http.MethodPost, target: "/v1/workspaces/workspace-a/fleet:refresh", body: "{\"error\":\"refresh_unavailable\"}\n"},
		{method: http.MethodGet, target: "/v1/workspaces/workspace-a/fleet", body: "{\"error\":\"fleet_unavailable\"}\n"},
		{method: http.MethodGet, target: "/v1/workspaces/workspace-a/fleet/images/sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", body: "{\"error\":\"image_search_unavailable\"}\n"},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, authenticatedFleetRequest(t, test.method, test.target, privateKey, now))
		if response.Code != http.StatusServiceUnavailable || response.Body.String() != test.body || strings.Contains(response.Body.String(), "leaked") {
			t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
		}
	}
}

func TestFleetHandlerSearchesOneExactImageDigestInSignedWorkspace(t *testing.T) {
	now := time.Date(2026, 7, 14, 7, 0, 0, 0, time.UTC)
	verifier, privateKey := fleetTestVerifier(t, now)
	digest := "sha256:" + strings.Repeat("a", 64)
	called := false
	handler, err := NewFleetHandler(FleetHandlerConfig{
		Verifier: verifier,
		Collector: fleetRefresherFunc(func(context.Context, tenancy.Scope) (fleet.Coverage, error) {
			t.Fatal("image search reached collector")
			return fleet.Coverage{}, nil
		}),
		Reader: fleetReaderFunc(func(context.Context, tenancy.Scope, time.Duration, time.Time) (fleet.FleetResult, error) {
			t.Fatal("image search reached fleet reader")
			return fleet.FleetResult{}, nil
		}),
		ImageSearcher: fleetImageSearcherFunc(func(_ context.Context, scope tenancy.Scope, request hubfleet.ImageSearchRequest) (fleet.QueryResult, error) {
			called = true
			if scope.WorkspaceID() != "workspace-a" || request.Digest != digest || request.Limit != 0 {
				t.Fatalf("image search scope/request = %#v/%#v", scope, request)
			}
			return fleet.QueryResult{Facts: []fleet.Fact{{Workspace: "workspace-a", Evidence: fleet.Evidence{Ref: fleet.ResourceRef{Scope: "spoke-a", Kind: "Pod", Name: "payments"}}}}, Coverage: fleet.Coverage{Requested: 2, Reachable: 2}}, nil
		}),
		PEP: fleetTestPEP(t, pep.AllowReadHook{}),
	})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, authenticatedFleetRequest(t, http.MethodGet, "/v1/workspaces/workspace-a/fleet/images/"+digest, privateKey, now))
	if response.Code != http.StatusOK || !called {
		t.Fatalf("status = %d, called = %t, body = %q", response.Code, called, response.Body.String())
	}
	var result fleet.QueryResult
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil || len(result.Facts) != 1 || result.Facts[0].Ref.Scope != "spoke-a" || !result.Coverage.Complete() {
		t.Fatalf("image result = %#v, error = %v", result, err)
	}
}

func TestFleetHandlerSearchesCVEsForOneExactImageDigestInSignedWorkspace(t *testing.T) {
	now := time.Date(2026, 7, 14, 19, 0, 0, 0, time.UTC)
	verifier, privateKey := fleetTestVerifier(t, now)
	digest := "sha256:" + strings.Repeat("a", 64)
	called := false
	handler, err := NewFleetHandler(FleetHandlerConfig{
		Verifier: verifier,
		Collector: fleetRefresherFunc(func(context.Context, tenancy.Scope) (fleet.Coverage, error) {
			t.Fatal("CVE search reached collector")
			return fleet.Coverage{}, nil
		}),
		Reader: fleetReaderFunc(func(context.Context, tenancy.Scope, time.Duration, time.Time) (fleet.FleetResult, error) {
			t.Fatal("CVE search reached fleet reader")
			return fleet.FleetResult{}, nil
		}),
		ImageSearcher: fleetImageSearcherFunc(func(context.Context, tenancy.Scope, hubfleet.ImageSearchRequest) (fleet.QueryResult, error) {
			t.Fatal("CVE search reached image searcher")
			return fleet.QueryResult{}, nil
		}),
		CVESearcher: fleetImageSearcherFunc(func(_ context.Context, scope tenancy.Scope, request hubfleet.ImageSearchRequest) (fleet.QueryResult, error) {
			called = true
			if scope.WorkspaceID() != "workspace-a" || request.Digest != digest || request.Limit != 0 {
				t.Fatalf("CVE search scope/request = %#v/%#v", scope, request)
			}
			return fleet.QueryResult{Facts: []fleet.Fact{{Workspace: "workspace-a", Evidence: fleet.Evidence{Ref: fleet.ResourceRef{Scope: "spoke-a", Kind: "Image", Name: digest}}}}, Coverage: fleet.Coverage{Requested: 2, Reachable: 2}}, nil
		}),
		PEP: fleetTestPEP(t, pep.AllowReadHook{}),
	})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, authenticatedFleetRequest(t, http.MethodGet, "/v1/workspaces/workspace-a/fleet/images/"+digest+"/cves", privateKey, now))
	if response.Code != http.StatusOK || !called || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status/called/cache = %d/%t/%q", response.Code, called, response.Header().Get("Cache-Control"))
	}
	var result fleet.QueryResult
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil || len(result.Facts) != 1 || result.Facts[0].Ref.Kind != "Image" || !result.Coverage.Complete() {
		t.Fatalf("CVE result = %#v, error = %v", result, err)
	}
}

func TestFleetHandlerSearchesOneExactCVEIdentifierInSignedWorkspace(t *testing.T) {
	now := time.Date(2026, 7, 14, 20, 0, 0, 0, time.UTC)
	verifier, privateKey := fleetTestVerifier(t, now)
	called := false
	handler, err := NewFleetHandler(FleetHandlerConfig{
		Verifier: verifier,
		Collector: fleetRefresherFunc(func(context.Context, tenancy.Scope) (fleet.Coverage, error) {
			t.Fatal("CVE identifier search reached collector")
			return fleet.Coverage{}, nil
		}),
		Reader: fleetReaderFunc(func(context.Context, tenancy.Scope, time.Duration, time.Time) (fleet.FleetResult, error) {
			t.Fatal("CVE identifier search reached fleet reader")
			return fleet.FleetResult{}, nil
		}),
		ImageSearcher: fleetImageSearcherFunc(func(context.Context, tenancy.Scope, hubfleet.ImageSearchRequest) (fleet.QueryResult, error) {
			t.Fatal("CVE identifier search reached image searcher")
			return fleet.QueryResult{}, nil
		}),
		CVEIdentifierSearcher: fleetCVEIdentifierSearcherFunc(func(_ context.Context, scope tenancy.Scope, request hubfleet.CVEIdentifierSearchRequest) (fleet.QueryResult, error) {
			called = true
			if scope.WorkspaceID() != "workspace-a" || request.Identifier != "CVE-2026-0001" || request.Limit != 0 {
				t.Fatalf("CVE identifier search scope/request = %#v/%#v", scope, request)
			}
			return fleet.QueryResult{Facts: []fleet.Fact{{Workspace: "workspace-a", Evidence: fleet.Evidence{Ref: fleet.ResourceRef{Scope: "spoke-a", Kind: "Image", Name: "sha256:" + strings.Repeat("a", 64)}}}}, Coverage: fleet.Coverage{Requested: 2, Reachable: 2}}, nil
		}),
		PEP: fleetTestPEP(t, pep.AllowReadHook{}),
	})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, authenticatedFleetRequest(t, http.MethodGet, "/v1/workspaces/workspace-a/fleet/cves/CVE-2026-0001", privateKey, now))
	if response.Code != http.StatusOK || !called || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status/called/cache = %d/%t/%q", response.Code, called, response.Header().Get("Cache-Control"))
	}
	var result fleet.QueryResult
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil || len(result.Facts) != 1 || result.Facts[0].Ref.Kind != "Image" || !result.Coverage.Complete() {
		t.Fatalf("CVE identifier result = %#v, error = %v", result, err)
	}
}

func TestNewFleetHandlerRejectsMissingDependencies(t *testing.T) {
	if _, err := NewFleetHandler(FleetHandlerConfig{}); err == nil {
		t.Fatal("NewFleetHandler accepted missing dependencies")
	}
}

func TestFleetHandlerForwardsAuthRefusalsToConfiguredObserver(t *testing.T) {
	now := time.Date(2026, 7, 14, 13, 0, 0, 0, time.UTC)
	verifier, _ := fleetTestVerifier(t, now)
	var events []AuthEvent
	handler, err := NewFleetHandler(FleetHandlerConfig{
		Verifier:     verifier,
		AuthObserver: AuthObserverFunc(func(event AuthEvent) { events = append(events, event) }),
		Collector: fleetRefresherFunc(func(context.Context, tenancy.Scope) (fleet.Coverage, error) {
			t.Fatal("unauthenticated request reached collector")
			return fleet.Coverage{}, nil
		}),
		Reader: fleetReaderFunc(func(context.Context, tenancy.Scope, time.Duration, time.Time) (fleet.FleetResult, error) {
			t.Fatal("unauthenticated request reached reader")
			return fleet.FleetResult{}, nil
		}),
		ImageSearcher: fleetImageSearcherFunc(func(context.Context, tenancy.Scope, hubfleet.ImageSearchRequest) (fleet.QueryResult, error) {
			t.Fatal("unauthenticated request reached image searcher")
			return fleet.QueryResult{}, nil
		}),
		PEP: fleetTestPEP(t, pep.AllowReadHook{}),
	})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "https://hub.sith.test/v1/workspaces/workspace-a/fleet", nil))
	if response.Code != http.StatusUnauthorized || len(events) != 1 || events[0] != (AuthEvent{Outcome: AuthOutcomeRefused}) {
		t.Fatalf("status = %d events = %#v", response.Code, events)
	}
}

func fleetTestVerifier(t *testing.T, now time.Time) (*hubauth.JWTVerifier, ed25519.PrivateKey) {
	t.Helper()
	publicKey, privateKey := hubTestKeyPair()
	verifier, err := hubauth.NewJWTVerifier(hubauth.JWTConfig{
		Issuer: hubTestIssuer, Audience: hubTestAudience, Keys: map[string]ed25519.PublicKey{hubTestKeyID: publicKey}, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	return verifier, privateKey
}

func fleetTestPEP(t *testing.T, hook pep.PolicyHook) *pep.Enforcer {
	t.Helper()
	enforcer, err := pep.NewEnforcer(pep.Config{
		Hook: hook,
		Auditor: pep.AuditFunc(func(context.Context, pep.AuditEvent) error {
			return nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	return enforcer
}

func authenticatedFleetRequest(t *testing.T, method, target string, privateKey ed25519.PrivateKey, now time.Time) *http.Request {
	t.Helper()
	request := httptest.NewRequest(method, "https://hub.sith.test"+target, nil)
	request.Header.Set("Authorization", "Bearer "+signHubTestToken(t, hubValidClaims(now), privateKey))
	return request
}
