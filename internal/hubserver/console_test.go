// SPDX-License-Identifier: Apache-2.0

package hubserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"html/template"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/ArdurAI/sith/internal/fleet"
	"github.com/ArdurAI/sith/internal/hubfleet"
	"github.com/ArdurAI/sith/internal/pep"
	"github.com/ArdurAI/sith/internal/tenancy"
)

func TestConsoleHandlerReadsOnlySignedWorkspaceWithCSRFAndPEP(t *testing.T) {
	now := time.Date(2026, 7, 17, 9, 0, 0, 0, time.UTC)
	verifier, privateKey := fleetTestVerifier(t, now)
	session := signHubTestToken(t, hubValidClaims(now), privateKey)
	var audits []pep.AuditEvent
	enforcer, err := pep.NewEnforcer(pep.Config{
		Hook: pep.AllowReadHook{},
		Auditor: pep.AuditFunc(func(_ context.Context, event pep.AuditEvent) error {
			audits = append(audits, event)
			return nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	readerCalled := 0
	var observed []hubfleet.FleetReadOutcome
	handler, err := NewConsoleHandler(ConsoleHandlerConfig{
		Verifier:   verifier,
		Correlator: noopConsoleCorrelator(t),
		Inventory:  noopConsoleInventory(t),
		CVE:        noopConsoleCVE(t),
		Reader: fleetReaderFunc(func(_ context.Context, scope tenancy.Scope, freshness time.Duration, gotNow time.Time) (fleet.FleetResult, error) {
			readerCalled++
			if scope.WorkspaceID() != "workspace-a" || scope.Subject() != "user:alice" || freshness != 5*time.Minute || gotNow.IsZero() {
				t.Fatalf("scope/freshness/now = %#v/%s/%s", scope, freshness, gotNow)
			}
			return fleet.FleetResult{
				Clusters: []fleet.Cluster{
					{Name: "spoke-a", SourceKind: hubfleet.SourceKind, Reachable: true, ObservedAt: now.Add(-time.Minute)},
					{Name: "spoke-b", SourceKind: hubfleet.SourceKind, Reachable: true, ObservedAt: now.Add(-10 * time.Minute)},
					{Name: "spoke-c", SourceKind: hubfleet.SourceKind, Reachable: false},
				},
				Coverage: fleet.Coverage{Requested: 4, Reachable: 2, Unreachable: []string{"spoke-c"}, Stale: []string{"spoke-b"}},
			}, nil
		}),
		PEP: enforcer, ReadObserver: fleetReadObserverFunc(func(outcome hubfleet.FleetReadOutcome) {
			observed = append(observed, outcome)
		}),
		Now: func() time.Time { return now }, Random: bytes.NewReader(bytes.Repeat([]byte{0x5a}, 192)),
	})
	if err != nil {
		t.Fatal(err)
	}

	pageResponse, csrfToken := authenticatedConsolePage(t, handler, session, "workspace-a")
	if pageResponse.Code != http.StatusOK || strings.Contains(pageResponse.Body.String(), session) || csrfToken == "" {
		t.Fatalf("page status/token/body = %d/%q/%q", pageResponse.Code, csrfToken, pageResponse.Body.String())
	}
	for _, forbidden := range []string{"<style", "onclick=", "localStorage", "sessionStorage", "http://", "https://"} {
		if strings.Contains(pageResponse.Body.String(), forbidden) {
			t.Fatalf("page contains forbidden inline or external surface %q", forbidden)
		}
	}
	for _, required := range []string{`id="coverage-details"`, `id="inventory-form"`, `id="inventory-gaps"`, `name="sith-inventory-csrf"`, `id="cve-form"`, `id="cve-gaps"`, `name="sith-cve-csrf"`} {
		if !strings.Contains(pageResponse.Body.String(), required) {
			t.Fatalf("page omits required console boundary %q", required)
		}
	}
	assertConsoleSecurityHeaders(t, pageResponse.Header())

	request := consoleTestRequest(http.MethodGet, "/v1/workspaces/workspace-a/console/fleet", "workspace-a", session)
	request.Header.Set(consoleCSRFHeader, csrfToken)
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	response := httptest.NewRecorder()
	handler.ServeFleet(response, request)
	if response.Code != http.StatusOK || readerCalled != 1 || len(audits) != 1 || audits[0].Verb != pep.VerbFleetRead {
		t.Fatalf("status/reader/audits = %d/%d/%#v body=%q", response.Code, readerCalled, audits, response.Body.String())
	}
	if len(observed) != 1 || observed[0] != hubfleet.FleetReadOutcomeDegraded {
		t.Fatalf("console fleet read observations = %q, want degraded", observed)
	}
	assertConsoleSecurityHeaders(t, response.Header())
	var payload consoleFleetResponse
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Fleet.Clusters) != 3 || payload.Assessment.Complete || payload.Assessment.Unaccounted != 1 ||
		!containsCoverageGap(payload.Assessment.Gaps, fleet.CoverageGapStale) ||
		!containsCoverageGap(payload.Assessment.Gaps, fleet.CoverageGapUnreachable) ||
		!containsCoverageGap(payload.Assessment.Gaps, fleet.CoverageGapUnaccounted) {
		t.Fatalf("console fleet response = %#v", payload)
	}
}

func TestConsoleRoutesResolveWorkspaceThroughServeMux(t *testing.T) {
	now := time.Date(2026, 7, 17, 9, 30, 0, 0, time.UTC)
	verifier, privateKey := fleetTestVerifier(t, now)
	session := signHubTestToken(t, hubValidClaims(now), privateKey)
	readerCalled := 0
	handler, err := NewConsoleHandler(ConsoleHandlerConfig{
		Verifier:   verifier,
		Correlator: noopConsoleCorrelator(t),
		Inventory:  noopConsoleInventory(t),
		CVE:        noopConsoleCVE(t),
		Reader: fleetReaderFunc(func(_ context.Context, scope tenancy.Scope, _ time.Duration, _ time.Time) (fleet.FleetResult, error) {
			readerCalled++
			if scope.WorkspaceID() != "workspace-a" {
				t.Fatalf("ServeMux scope = %#v", scope)
			}
			return fleet.FleetResult{Coverage: fleet.Coverage{Requested: 1, Reachable: 1}}, nil
		}),
		PEP: fleetTestPEP(t, pep.AllowReadHook{}), Now: func() time.Time { return now },
		Random: bytes.NewReader(bytes.Repeat([]byte{0x6a}, 192)),
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("GET /v1/workspaces/{workspace}/console", http.HandlerFunc(handler.ServePage))
	mux.Handle("GET /v1/workspaces/{workspace}/console/fleet", http.HandlerFunc(handler.ServeFleet))
	mux.Handle("GET /v1/console/assets/console.css", http.HandlerFunc(handler.ServeCSS))
	mux.Handle("GET /v1/console/assets/console.js", http.HandlerFunc(handler.ServeJavaScript))

	pageRequest := httptest.NewRequest(http.MethodGet, "https://hub.sith.test/v1/workspaces/workspace-a/console", nil)
	pageRequest.AddCookie(&http.Cookie{Name: browserOIDCSessionCookie, Value: session})
	pageResponse := httptest.NewRecorder()
	mux.ServeHTTP(pageResponse, pageRequest)
	if pageResponse.Code != http.StatusOK {
		t.Fatalf("ServeMux page status/body = %d/%q", pageResponse.Code, pageResponse.Body.String())
	}
	const marker = `<meta name="sith-csrf" content="`
	_, after, found := strings.Cut(pageResponse.Body.String(), marker)
	if !found {
		t.Fatalf("ServeMux page omitted CSRF token: %q", pageResponse.Body.String())
	}
	csrfToken, _, found := strings.Cut(after, `">`)
	if !found {
		t.Fatal("ServeMux page emitted malformed CSRF token")
	}

	fleetRequest := httptest.NewRequest(http.MethodGet, "https://hub.sith.test/v1/workspaces/workspace-a/console/fleet", nil)
	fleetRequest.AddCookie(&http.Cookie{Name: browserOIDCSessionCookie, Value: session})
	fleetRequest.Header.Set(consoleCSRFHeader, csrfToken)
	fleetRequest.Header.Set("Sec-Fetch-Site", "same-origin")
	fleetResponse := httptest.NewRecorder()
	mux.ServeHTTP(fleetResponse, fleetRequest)
	if fleetResponse.Code != http.StatusOK || readerCalled != 1 {
		t.Fatalf("ServeMux fleet status/reader/body = %d/%d/%q", fleetResponse.Code, readerCalled, fleetResponse.Body.String())
	}

	mutation := httptest.NewRequest(http.MethodPost, "https://hub.sith.test/v1/workspaces/workspace-a/console/fleet", nil)
	mutationResponse := httptest.NewRecorder()
	mux.ServeHTTP(mutationResponse, mutation)
	if mutationResponse.Code != http.StatusMethodNotAllowed || readerCalled != 1 {
		t.Fatalf("ServeMux mutation status/reader = %d/%d", mutationResponse.Code, readerCalled)
	}
}

func TestConsoleHandlerFailsClosedBeforeReader(t *testing.T) {
	verifierNow := time.Date(2026, 7, 17, 10, 0, 0, 0, time.UTC)
	consoleNow := verifierNow
	verifier, privateKey := fleetTestVerifier(t, verifierNow)
	session := signHubTestToken(t, hubValidClaims(verifierNow), privateKey)
	readerCalled := 0
	newHandler := func(fill byte) *ConsoleHandler {
		handler, err := NewConsoleHandler(ConsoleHandlerConfig{
			Verifier:   verifier,
			Correlator: noopConsoleCorrelator(t),
			Inventory:  noopConsoleInventory(t),
			CVE:        noopConsoleCVE(t),
			Reader: fleetReaderFunc(func(context.Context, tenancy.Scope, time.Duration, time.Time) (fleet.FleetResult, error) {
				readerCalled++
				return fleet.FleetResult{Coverage: fleet.Coverage{}}, nil
			}),
			PEP: fleetTestPEP(t, pep.AllowReadHook{}), Now: func() time.Time { return consoleNow },
			Random: bytes.NewReader(bytes.Repeat([]byte{fill}, 192)),
		})
		if err != nil {
			t.Fatal(err)
		}
		return handler
	}
	handler := newHandler(0x31)
	_, csrfToken := authenticatedConsolePage(t, handler, session, "workspace-a")

	tests := []struct {
		name      string
		request   func() *http.Request
		wantCode  int
		wantError string
	}{
		{name: "missing session", request: func() *http.Request {
			return consoleTestRequest(http.MethodGet, "/v1/workspaces/workspace-a/console/fleet", "workspace-a", "")
		}, wantCode: http.StatusUnauthorized, wantError: "unauthorized"},
		{name: "authorization fallback refused", request: func() *http.Request {
			request := validConsoleFleetRequest(session, csrfToken)
			request.Header.Set("Authorization", "Bearer "+session)
			return request
		}, wantCode: http.StatusUnauthorized, wantError: "unauthorized"},
		{name: "duplicate session cookie", request: func() *http.Request {
			request := validConsoleFleetRequest(session, csrfToken)
			request.AddCookie(&http.Cookie{Name: browserOIDCSessionCookie, Value: session})
			return request
		}, wantCode: http.StatusUnauthorized, wantError: "unauthorized"},
		{name: "foreign workspace", request: func() *http.Request {
			request := consoleTestRequest(http.MethodGet, "/v1/workspaces/workspace-b/console/fleet", "workspace-b", session)
			request.Header.Set(consoleCSRFHeader, csrfToken)
			return request
		}, wantCode: http.StatusForbidden, wantError: "forbidden"},
		{name: "missing CSRF", request: func() *http.Request {
			request := validConsoleFleetRequest(session, csrfToken)
			request.Header.Del(consoleCSRFHeader)
			return request
		}, wantCode: http.StatusForbidden, wantError: "forbidden"},
		{name: "wrong CSRF", request: func() *http.Request {
			request := validConsoleFleetRequest(session, csrfToken)
			request.Header.Set(consoleCSRFHeader, strings.Repeat("x", len(csrfToken)))
			return request
		}, wantCode: http.StatusForbidden, wantError: "forbidden"},
		{name: "cross-site metadata", request: func() *http.Request {
			request := validConsoleFleetRequest(session, csrfToken)
			request.Header.Set("Sec-Fetch-Site", "cross-site")
			return request
		}, wantCode: http.StatusForbidden, wantError: "forbidden"},
		{name: "query rejected", request: func() *http.Request {
			request := consoleTestRequest(http.MethodGet, "/v1/workspaces/workspace-a/console/fleet?refresh=true", "workspace-a", session)
			request.Header.Set(consoleCSRFHeader, csrfToken)
			return request
		}, wantCode: http.StatusNotFound, wantError: "not_found"},
		{name: "method rejected", request: func() *http.Request {
			request := consoleTestRequest(http.MethodPost, "/v1/workspaces/workspace-a/console/fleet", "workspace-a", session)
			request.Header.Set(consoleCSRFHeader, csrfToken)
			return request
		}, wantCode: http.StatusNotFound, wantError: "not_found"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeFleet(response, test.request())
			if response.Code != test.wantCode || !strings.Contains(response.Body.String(), test.wantError) {
				t.Fatalf("status/body = %d/%q", response.Code, response.Body.String())
			}
		})
	}
	if readerCalled != 0 {
		t.Fatalf("invalid requests reached reader %d times", readerCalled)
	}

	consoleNow = consoleNow.Add(defaultConsoleCSRFLifetime + time.Second)
	expired := validConsoleFleetRequest(session, csrfToken)
	expiredResponse := httptest.NewRecorder()
	handler.ServeFleet(expiredResponse, expired)
	if expiredResponse.Code != http.StatusForbidden || readerCalled != 0 {
		t.Fatalf("expired CSRF status/reader = %d/%d", expiredResponse.Code, readerCalled)
	}

	consoleNow = verifierNow
	restarted := newHandler(0x77)
	replay := validConsoleFleetRequest(session, csrfToken)
	replayResponse := httptest.NewRecorder()
	restarted.ServeFleet(replayResponse, replay)
	if replayResponse.Code != http.StatusForbidden || readerCalled != 0 {
		t.Fatalf("post-restart replay status/reader = %d/%d", replayResponse.Code, readerCalled)
	}
}

func TestConsoleHandlerHidesReaderErrorsAndServesFixedAssets(t *testing.T) {
	now := time.Date(2026, 7, 17, 11, 0, 0, 0, time.UTC)
	verifier, privateKey := fleetTestVerifier(t, now)
	session := signHubTestToken(t, hubValidClaims(now), privateKey)
	var observed []hubfleet.FleetReadOutcome
	handler, err := NewConsoleHandler(ConsoleHandlerConfig{
		Verifier:   verifier,
		Correlator: noopConsoleCorrelator(t),
		Inventory:  noopConsoleInventory(t),
		CVE:        noopConsoleCVE(t),
		Reader: fleetReaderFunc(func(context.Context, tenancy.Scope, time.Duration, time.Time) (fleet.FleetResult, error) {
			return fleet.FleetResult{}, errors.New("database=secret topology=private")
		}),
		PEP: fleetTestPEP(t, pep.AllowReadHook{}), ReadObserver: fleetReadObserverFunc(func(outcome hubfleet.FleetReadOutcome) {
			observed = append(observed, outcome)
		}),
		Now: func() time.Time { return now }, Random: bytes.NewReader(bytes.Repeat([]byte{0x41}, 192)),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, csrfToken := authenticatedConsolePage(t, handler, session, "workspace-a")
	response := httptest.NewRecorder()
	handler.ServeFleet(response, validConsoleFleetRequest(session, csrfToken))
	if response.Code != http.StatusServiceUnavailable || response.Body.String() != "{\"error\":\"fleet_unavailable\"}\n" || strings.Contains(response.Body.String(), "secret") {
		t.Fatalf("status/body = %d/%q", response.Code, response.Body.String())
	}
	if len(observed) != 1 || observed[0] != hubfleet.FleetReadOutcomeError {
		t.Fatalf("console error observations = %q, want error", observed)
	}

	for _, asset := range []struct {
		name        string
		serve       func(http.ResponseWriter, *http.Request)
		contentType string
		required    string
	}{
		{name: "CSS", serve: handler.ServeCSS, contentType: "text/css; charset=utf-8", required: "prefers-reduced-motion"},
		{name: "JavaScript", serve: handler.ServeJavaScript, contentType: "text/javascript; charset=utf-8", required: "Inventory read complete."},
	} {
		t.Run(asset.name, func(t *testing.T) {
			path := "/v1/console/assets/console.css"
			if asset.name == "JavaScript" {
				path = "/v1/console/assets/console.js"
			}
			request := httptest.NewRequest(http.MethodGet, "https://hub.sith.test"+path, nil)
			response := httptest.NewRecorder()
			asset.serve(response, request)
			body := response.Body.String()
			if response.Code != http.StatusOK || response.Header().Get("Content-Type") != asset.contentType || !strings.Contains(body, asset.required) {
				t.Fatalf("status/type/body = %d/%q/%q", response.Code, response.Header().Get("Content-Type"), body)
			}
			for _, forbidden := range []string{"localStorage", "sessionStorage", "innerHTML", "setInterval", "fleet:refresh", "/api/v1/exec", "/api/v1/edit"} {
				if strings.Contains(body, forbidden) {
					t.Fatalf("asset contains forbidden capability %q", forbidden)
				}
			}
		})
	}
}

func TestNewConsoleHandlerRejectsUnsafeConfiguration(t *testing.T) {
	for _, config := range []ConsoleHandlerConfig{
		{},
		{Verifier: authVerifierFunc(func(context.Context, string) (tenancy.Principal, error) { return tenancy.Principal{}, nil })},
		{Verifier: authVerifierFunc(func(context.Context, string) (tenancy.Principal, error) { return tenancy.Principal{}, nil }), Reader: fleetReaderFunc(nil), PEP: nil},
		{Verifier: authVerifierFunc(func(context.Context, string) (tenancy.Principal, error) { return tenancy.Principal{}, nil }), Reader: fleetReaderFunc(func(context.Context, tenancy.Scope, time.Duration, time.Time) (fleet.FleetResult, error) {
			return fleet.FleetResult{}, nil
		}), PEP: fleetTestPEP(t, pep.AllowReadHook{})},
		{Verifier: authVerifierFunc(func(context.Context, string) (tenancy.Principal, error) { return tenancy.Principal{}, nil }), Reader: fleetReaderFunc(func(context.Context, tenancy.Scope, time.Duration, time.Time) (fleet.FleetResult, error) {
			return fleet.FleetResult{}, nil
		}), Correlator: noopConsoleCorrelator(t), Inventory: noopConsoleInventory(t), CVE: noopConsoleCVE(t), PEP: fleetTestPEP(t, pep.AllowReadHook{}), CSRFLifetime: 30 * time.Second},
	} {
		if _, err := NewConsoleHandler(config); err == nil {
			t.Fatalf("NewConsoleHandler accepted unsafe config %#v", config)
		}
	}
	verifier, _ := fleetTestVerifier(t, time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC))
	if _, err := NewConsoleHandler(ConsoleHandlerConfig{
		Verifier:   verifier,
		Correlator: noopConsoleCorrelator(t),
		Inventory:  noopConsoleInventory(t),
		CVE:        noopConsoleCVE(t),
		Reader: fleetReaderFunc(func(context.Context, tenancy.Scope, time.Duration, time.Time) (fleet.FleetResult, error) {
			return fleet.FleetResult{}, nil
		}),
		PEP: fleetTestPEP(t, pep.AllowReadHook{}), Random: bytes.NewReader(nil),
	}); err == nil {
		t.Fatal("NewConsoleHandler accepted failed random source")
	}
}

func TestConsolePageAndAssetFailuresRemainGeneric(t *testing.T) {
	now := time.Date(2026, 7, 17, 13, 0, 0, 0, time.UTC)
	verifier, privateKey := fleetTestVerifier(t, now)
	session := signHubTestToken(t, hubValidClaims(now), privateKey)
	reader := fleetReaderFunc(func(context.Context, tenancy.Scope, time.Duration, time.Time) (fleet.FleetResult, error) {
		return fleet.FleetResult{}, nil
	})

	shortRandom, err := NewConsoleHandler(ConsoleHandlerConfig{
		Verifier: verifier, Reader: reader, Correlator: noopConsoleCorrelator(t), Inventory: noopConsoleInventory(t), CVE: noopConsoleCVE(t), PEP: fleetTestPEP(t, pep.AllowReadHook{}), Now: func() time.Time { return now },
		Random: bytes.NewReader(bytes.Repeat([]byte{0x51}, consoleCSRFKeyBytes)),
	})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	shortRandom.ServePage(response, consoleTestRequest(http.MethodGet, "/v1/workspaces/workspace-a/console", "workspace-a", session))
	if response.Code != http.StatusServiceUnavailable || response.Body.String() != "{\"error\":\"console_unavailable\"}\n" {
		t.Fatalf("random failure status/body = %d/%q", response.Code, response.Body.String())
	}

	handler, err := NewConsoleHandler(ConsoleHandlerConfig{
		Verifier: verifier, Reader: reader, Correlator: noopConsoleCorrelator(t), Inventory: noopConsoleInventory(t), CVE: noopConsoleCVE(t), PEP: fleetTestPEP(t, pep.AllowReadHook{}), Now: func() time.Time { return now },
		Random: bytes.NewReader(bytes.Repeat([]byte{0x61}, 192)),
	})
	if err != nil {
		t.Fatal(err)
	}
	handler.page = template.Must(template.New("console.html").Parse(`{{call .Workspace}}`))
	response = httptest.NewRecorder()
	handler.ServePage(response, consoleTestRequest(http.MethodGet, "/v1/workspaces/workspace-a/console", "workspace-a", session))
	if response.Code != http.StatusServiceUnavailable || response.Body.String() != "{\"error\":\"console_unavailable\"}\n" {
		t.Fatalf("template failure status/body = %d/%q", response.Code, response.Body.String())
	}

	handler.assets = fstest.MapFS{}
	assetResponse := httptest.NewRecorder()
	handler.ServeCSS(assetResponse, httptest.NewRequest(http.MethodGet, "https://hub.sith.test/v1/console/assets/console.css", nil))
	if assetResponse.Code != http.StatusNotFound || assetResponse.Body.String() != "{\"error\":\"not_found\"}\n" {
		t.Fatalf("missing asset status/body = %d/%q", assetResponse.Code, assetResponse.Body.String())
	}
	wrongPathResponse := httptest.NewRecorder()
	handler.ServeJavaScript(wrongPathResponse, httptest.NewRequest(http.MethodGet, "https://hub.sith.test/v1/console/assets/other.js", nil))
	if wrongPathResponse.Code != http.StatusNotFound {
		t.Fatalf("wrong asset path status = %d", wrongPathResponse.Code)
	}

	invalidPage := httptest.NewRecorder()
	handler.ServePage(invalidPage, consoleTestRequest(http.MethodGet, "/v1/workspaces/workspace-a/console?next=evil", "workspace-a", session))
	if invalidPage.Code != http.StatusNotFound {
		t.Fatalf("queried page status = %d", invalidPage.Code)
	}

	var nilHandler *ConsoleHandler
	nilResponse := httptest.NewRecorder()
	nilHandler.ServePage(nilResponse, consoleTestRequest(http.MethodGet, "/v1/workspaces/workspace-a/console", "workspace-a", session))
	if nilResponse.Code != http.StatusUnauthorized {
		t.Fatalf("nil handler page status = %d", nilResponse.Code)
	}
	if _, ok := exactConsoleSession(nil); ok {
		t.Fatal("nil request produced a console session")
	}
	if _, ok := canonicalConsoleRequest(nil, "/console"); ok {
		t.Fatal("nil request produced a console workspace")
	}
}

func authenticatedConsolePage(t *testing.T, handler *ConsoleHandler, session, workspace string) (*httptest.ResponseRecorder, string) {
	t.Helper()
	request := consoleTestRequest(http.MethodGet, "/v1/workspaces/"+workspace+"/console", workspace, session)
	response := httptest.NewRecorder()
	handler.ServePage(response, request)
	const marker = `<meta name="sith-csrf" content="`
	_, after, found := strings.Cut(response.Body.String(), marker)
	if !found {
		t.Fatalf("console page omitted CSRF token: %q", response.Body.String())
	}
	token, _, found := strings.Cut(after, `">`)
	if !found {
		t.Fatalf("console page emitted malformed CSRF token")
	}
	return response, token
}

func validConsoleFleetRequest(session, csrfToken string) *http.Request {
	request := consoleTestRequest(http.MethodGet, "/v1/workspaces/workspace-a/console/fleet", "workspace-a", session)
	request.Header.Set(consoleCSRFHeader, csrfToken)
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	return request
}

func consoleTestRequest(method, target, workspace, session string) *http.Request {
	request := httptest.NewRequest(method, "https://hub.sith.test"+target, nil)
	request.SetPathValue("workspace", workspace)
	if session != "" {
		request.AddCookie(&http.Cookie{Name: browserOIDCSessionCookie, Value: session})
	}
	return request
}

func assertConsoleSecurityHeaders(t *testing.T, header http.Header) {
	t.Helper()
	if header.Get("Cache-Control") != "no-store" || header.Get("Pragma") != "no-cache" || header.Get("X-Frame-Options") != "DENY" ||
		header.Get("Referrer-Policy") != "no-referrer" || !strings.Contains(header.Get("Content-Security-Policy"), "default-src 'none'") ||
		!strings.Contains(header.Get("Content-Security-Policy"), "connect-src 'self'") {
		t.Fatalf("console security headers = %#v", header)
	}
}

func containsCoverageGap(gaps []fleet.CoverageGap, wanted fleet.CoverageGap) bool {
	for _, gap := range gaps {
		if gap == wanted {
			return true
		}
	}
	return false
}
