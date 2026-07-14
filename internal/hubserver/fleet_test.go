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

var _ FleetRefresher = fleetRefresherFunc(nil)
var _ hubfleet.FleetReader = fleetReaderFunc(nil)
var _ FleetImageSearcher = fleetImageSearcherFunc(nil)

func TestFleetHandlerRefreshUsesOnlySignedWorkspaceScope(t *testing.T) {
	now := time.Date(2026, 7, 14, 7, 0, 0, 0, time.UTC)
	verifier, privateKey := fleetTestVerifier(t, now)
	called := false
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

func TestFleetHandlerReadConstructsRequestScopedSource(t *testing.T) {
	now := time.Date(2026, 7, 14, 7, 0, 0, 0, time.UTC)
	verifier, privateKey := fleetTestVerifier(t, now)
	called := false
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
		{name: "read method", method: http.MethodPost, target: "/v1/workspaces/workspace-a/fleet", wantCode: http.StatusMethodNotAllowed, wantBody: "{\"error\":\"method_not_allowed\"}\n", wantAllow: http.MethodGet},
		{name: "refresh method", method: http.MethodGet, target: "/v1/workspaces/workspace-a/fleet:refresh", wantCode: http.StatusMethodNotAllowed, wantBody: "{\"error\":\"method_not_allowed\"}\n", wantAllow: http.MethodPost},
		{name: "image method", method: http.MethodPost, target: "/v1/workspaces/workspace-a/fleet/images/sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", wantCode: http.StatusMethodNotAllowed, wantBody: "{\"error\":\"method_not_allowed\"}\n", wantAllow: http.MethodGet},
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

func TestNewFleetHandlerRejectsMissingDependencies(t *testing.T) {
	if _, err := NewFleetHandler(FleetHandlerConfig{}); err == nil {
		t.Fatal("NewFleetHandler accepted missing dependencies")
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
