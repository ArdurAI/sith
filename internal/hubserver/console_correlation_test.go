// SPDX-License-Identifier: Apache-2.0

package hubserver

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ArdurAI/sith/internal/fleet"
	"github.com/ArdurAI/sith/internal/hubfleet"
	"github.com/ArdurAI/sith/internal/pep"
	"github.com/ArdurAI/sith/internal/tenancy"
)

type consoleFleetQuerierFunc func(context.Context, tenancy.Scope, fleet.Query, time.Duration, time.Time) (fleet.QueryResult, error)

func (function consoleFleetQuerierFunc) QueryFleet(
	ctx context.Context,
	scope tenancy.Scope,
	query fleet.Query,
	freshness time.Duration,
	now time.Time,
) (fleet.QueryResult, error) {
	return function(ctx, scope, query, freshness, now)
}

func noopConsoleCorrelator(t *testing.T) *hubfleet.Correlator {
	t.Helper()
	return consoleTestCorrelator(t, consoleFleetQuerierFunc(func(context.Context, tenancy.Scope, fleet.Query, time.Duration, time.Time) (fleet.QueryResult, error) {
		return fleet.QueryResult{}, nil
	}), fleetTestPEP(t, pep.AllowReadHook{}))
}

func consoleTestCorrelator(t *testing.T, querier hubfleet.FleetQuerier, enforcer *pep.Enforcer) *hubfleet.Correlator {
	t.Helper()
	correlator, err := hubfleet.NewCorrelator(hubfleet.CorrelatorConfig{Querier: querier, PEP: enforcer})
	if err != nil {
		t.Fatal(err)
	}
	return correlator
}

func noopConsoleInventory(t *testing.T) *hubfleet.InventorySearcher {
	t.Helper()
	return consoleTestInventory(t, consoleFleetQuerierFunc(func(context.Context, tenancy.Scope, fleet.Query, time.Duration, time.Time) (fleet.QueryResult, error) {
		return fleet.QueryResult{}, nil
	}), fleetTestPEP(t, pep.AllowReadHook{}))
}

func consoleTestInventory(t *testing.T, querier hubfleet.FleetQuerier, enforcer *pep.Enforcer) *hubfleet.InventorySearcher {
	t.Helper()
	searcher, err := hubfleet.NewInventorySearcher(hubfleet.InventorySearcherConfig{Querier: querier, PEP: enforcer})
	if err != nil {
		t.Fatal(err)
	}
	return searcher
}

func TestConsoleCorrelationUsesPurposeProofPEPAndMinimalProjection(t *testing.T) {
	now := time.Date(2026, 7, 17, 15, 0, 0, 0, time.UTC)
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
	queryCalls := 0
	correlator := consoleTestCorrelator(t, consoleFleetQuerierFunc(func(
		_ context.Context, scope tenancy.Scope, query fleet.Query, freshness time.Duration, gotNow time.Time,
	) (fleet.QueryResult, error) {
		queryCalls++
		if scope.WorkspaceID() != "workspace-a" || scope.Subject() != "user:alice" || freshness != 5*time.Minute || gotNow.IsZero() ||
			query.Limit != consoleCorrelationReadLimit || len(query.Kinds) != 1 || query.Kinds[0] != fleet.FactHealth ||
			query.Selector.ResourceKind != "Deployment" || query.Selector.Name != "payments" ||
			query.Selector.Namespace != "payments" || query.Selector.HealthNot != "Healthy" {
			t.Fatalf("correlation scope/query/freshness/now = %#v/%#v/%s/%s", scope, query, freshness, gotNow)
		}
		return fleet.QueryResult{
			Facts: []fleet.Fact{{
				Evidence: fleet.Evidence{
					Ref: fleet.ResourceRef{SourceKind: hubfleet.SourceKind, Scope: "cluster-a", Kind: "Deployment", Namespace: "payments", Name: "payments",
						Attributes: map[string]string{"credential": "must-not-leak"}},
					Kind: fleet.FactHealth, Observed: json.RawMessage(`{"status":"Degraded"}`), ObservedAt: now.Add(-6 * time.Minute),
					Source: "cluster-a", Provenance: fleet.Provenance{NativeID: "private-native-id", DeepLink: "https://private.example"},
				},
				Workspace: "workspace-a", Stale: true, StaleFor: "6m0s",
			}},
			Coverage: fleet.Coverage{Requested: 3, Reachable: 2, Unreachable: []string{"cluster-c"}, Stale: []string{"cluster-a"}},
		}, nil
	}), enforcer)
	handler, err := NewConsoleHandler(ConsoleHandlerConfig{
		Verifier: verifier,
		Reader: fleetReaderFunc(func(context.Context, tenancy.Scope, time.Duration, time.Time) (fleet.FleetResult, error) {
			return fleet.FleetResult{}, nil
		}),
		Correlator: correlator, Inventory: noopConsoleInventory(t), PEP: enforcer, Now: func() time.Time { return now },
		Random: bytes.NewReader(bytes.Repeat([]byte{0x72}, 128)),
	})
	if err != nil {
		t.Fatal(err)
	}

	page, fleetProof := authenticatedConsolePage(t, handler, session, "workspace-a")
	correlationProof := consolePageProof(t, page.Body.String(), "sith-correlation-csrf")
	if correlationProof == "" || correlationProof == fleetProof || strings.Contains(page.Body.String(), session) {
		t.Fatalf("purpose proof separation/session leak = %q/%q", fleetProof, correlationProof)
	}
	request := validConsoleCorrelationRequest(session, correlationProof)
	response := httptest.NewRecorder()
	handler.ServeCorrelation(response, request)
	if response.Code != http.StatusOK || queryCalls != 1 || len(audits) != 1 || audits[0].Verb != pep.VerbFleetCorrelate {
		t.Fatalf("status/query calls/audits/body = %d/%d/%#v/%q", response.Code, queryCalls, audits, response.Body.String())
	}
	assertConsoleSecurityHeaders(t, response.Header())
	for _, forbidden := range []string{"must-not-leak", "private-native-id", "private.example", `"observed"`, `"attributes"`, `"provenance"`, `"workspace"`, `"source_kind"`, `"deep_link"`} {
		if strings.Contains(response.Body.String(), forbidden) {
			t.Fatalf("correlation response leaked %q: %s", forbidden, response.Body.String())
		}
	}
	var payload consoleCorrelationResponse
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Matches) != 1 || payload.Matches[0].Scope != "cluster-a" || payload.Matches[0].Health != "Degraded" ||
		!payload.Matches[0].Stale || payload.Matches[0].StaleFor != "6m0s" || payload.Assessment.Complete || payload.Assessment.Unaccounted != 0 {
		t.Fatalf("projected correlation = %#v", payload)
	}
}

func TestConsoleCorrelationRejectsUnsafeRequestsBeforeQuery(t *testing.T) {
	now := time.Date(2026, 7, 17, 16, 0, 0, 0, time.UTC)
	consoleNow := now
	verifier, privateKey := fleetTestVerifier(t, now)
	session := signHubTestToken(t, hubValidClaims(now), privateKey)
	queryCalls := 0
	enforcer := fleetTestPEP(t, pep.AllowReadHook{})
	correlator := consoleTestCorrelator(t, consoleFleetQuerierFunc(func(context.Context, tenancy.Scope, fleet.Query, time.Duration, time.Time) (fleet.QueryResult, error) {
		queryCalls++
		return fleet.QueryResult{}, nil
	}), enforcer)
	newHandler := func(fill byte) *ConsoleHandler {
		handler, err := NewConsoleHandler(ConsoleHandlerConfig{
			Verifier: verifier,
			Reader: fleetReaderFunc(func(context.Context, tenancy.Scope, time.Duration, time.Time) (fleet.FleetResult, error) {
				return fleet.FleetResult{}, nil
			}),
			Correlator: correlator, Inventory: noopConsoleInventory(t), PEP: enforcer, Now: func() time.Time { return consoleNow },
			Random: bytes.NewReader(bytes.Repeat([]byte{fill}, 128)),
		})
		if err != nil {
			t.Fatal(err)
		}
		return handler
	}
	handler := newHandler(0x31)
	page, fleetProof := authenticatedConsolePage(t, handler, session, "workspace-a")
	correlationProof := consolePageProof(t, page.Body.String(), "sith-correlation-csrf")

	tests := []struct {
		name    string
		request func() *http.Request
	}{
		{name: "fleet proof is not reusable", request: func() *http.Request { return validConsoleCorrelationRequest(session, fleetProof) }},
		{name: "missing proof", request: func() *http.Request {
			request := validConsoleCorrelationRequest(session, correlationProof)
			request.Header.Del(consoleCSRFHeader)
			return request
		}},
		{name: "duplicate proof", request: func() *http.Request {
			request := validConsoleCorrelationRequest(session, correlationProof)
			request.Header.Add(consoleCSRFHeader, correlationProof)
			return request
		}},
		{name: "cross site", request: func() *http.Request {
			request := validConsoleCorrelationRequest(session, correlationProof)
			request.Header.Set("Sec-Fetch-Site", "cross-site")
			return request
		}},
		{name: "bearer fallback", request: func() *http.Request {
			request := validConsoleCorrelationRequest(session, correlationProof)
			request.Header.Set("Authorization", "Bearer "+session)
			return request
		}},
		{name: "duplicate cookie", request: func() *http.Request {
			request := validConsoleCorrelationRequest(session, correlationProof)
			request.AddCookie(&http.Cookie{Name: browserOIDCSessionCookie, Value: session})
			return request
		}},
		{name: "unknown query", request: func() *http.Request {
			return consoleCorrelationRequest(session, correlationProof, "kind=Deployment&name=payments&selector=all")
		}},
		{name: "duplicate query", request: func() *http.Request {
			return consoleCorrelationRequest(session, correlationProof, "kind=Deployment&kind=Pod&name=payments")
		}},
		{name: "condition override", request: func() *http.Request {
			return consoleCorrelationRequest(session, correlationProof, "health_not=Unknown&kind=Deployment&name=payments")
		}},
		{name: "Secret kind", request: func() *http.Request {
			return consoleCorrelationRequest(session, correlationProof, "kind=Secret&name=payments")
		}},
		{name: "blank namespace", request: func() *http.Request {
			return consoleCorrelationRequest(session, correlationProof, "kind=Deployment&name=payments&namespace=")
		}},
		{name: "noncanonical order", request: func() *http.Request {
			return consoleCorrelationRequest(session, correlationProof, "name=payments&kind=Deployment")
		}},
		{name: "untrimmed name", request: func() *http.Request {
			return consoleCorrelationRequest(session, correlationProof, "kind=Deployment&name=+payments")
		}},
		{name: "method", request: func() *http.Request {
			request := validConsoleCorrelationRequest(session, correlationProof)
			request.Method = http.MethodPost
			return request
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeCorrelation(response, test.request())
			if response.Code == http.StatusOK {
				t.Fatalf("unsafe request succeeded: %q", response.Body.String())
			}
		})
	}
	if queryCalls != 0 {
		t.Fatalf("unsafe requests reached query %d times", queryCalls)
	}

	consoleNow = now.Add(defaultConsoleCSRFLifetime + time.Second)
	expired := httptest.NewRecorder()
	handler.ServeCorrelation(expired, validConsoleCorrelationRequest(session, correlationProof))
	if expired.Code != http.StatusForbidden || queryCalls != 0 {
		t.Fatalf("expired proof status/query calls = %d/%d", expired.Code, queryCalls)
	}
	consoleNow = now
	restarted := newHandler(0x77)
	replayed := httptest.NewRecorder()
	restarted.ServeCorrelation(replayed, validConsoleCorrelationRequest(session, correlationProof))
	if replayed.Code != http.StatusForbidden || queryCalls != 0 {
		t.Fatalf("restarted proof status/query calls = %d/%d", replayed.Code, queryCalls)
	}
}

func TestProjectConsoleCorrelationFailsClosedOnStoredShape(t *testing.T) {
	now := time.Date(2026, 7, 17, 17, 0, 0, 0, time.UTC)
	request := hubfleet.CorrelationRequest{ResourceKind: "Deployment", Name: "payments", Namespace: "payments", HealthNot: "Healthy", Limit: consoleCorrelationReadLimit}
	valid := fleet.QueryResult{
		Facts: []fleet.Fact{{
			Evidence: fleet.Evidence{
				Ref:  fleet.ResourceRef{SourceKind: hubfleet.SourceKind, Scope: "cluster-a", Kind: "Deployment", Namespace: "payments", Name: "payments"},
				Kind: fleet.FactHealth, Observed: json.RawMessage(`{"status":"Unknown"}`), ObservedAt: now, Source: "cluster-a",
			},
			Workspace: "workspace-a",
		}},
		Coverage: fleet.Coverage{Requested: 1, Reachable: 1},
	}
	if projected, err := projectConsoleCorrelation(valid, "workspace-a", request); err != nil || len(projected.Matches) != 1 || !projected.Assessment.Complete {
		t.Fatalf("valid projection = %#v, %v", projected, err)
	}

	tests := []struct {
		name   string
		mutate func(*fleet.QueryResult)
	}{
		{name: "foreign workspace", mutate: func(result *fleet.QueryResult) { result.Facts[0].Workspace = "workspace-b" }},
		{name: "unexpected observation field", mutate: func(result *fleet.QueryResult) {
			result.Facts[0].Observed = json.RawMessage(`{"status":"Unknown","token":"secret"}`)
		}},
		{name: "unsupported health", mutate: func(result *fleet.QueryResult) { result.Facts[0].Observed = json.RawMessage(`{"status":"Broken"}`) }},
		{name: "healthy mismatch", mutate: func(result *fleet.QueryResult) { result.Facts[0].Observed = json.RawMessage(`{"status":"Healthy"}`) }},
		{name: "source mismatch", mutate: func(result *fleet.QueryResult) { result.Facts[0].Source = "cluster-b" }},
		{name: "stale mismatch", mutate: func(result *fleet.QueryResult) { result.Facts[0].StaleFor = "collection failed" }},
		{name: "untrusted stale text", mutate: func(result *fleet.QueryResult) {
			result.Facts[0].Stale = true
			result.Facts[0].StaleFor = "credential=must-not-leak"
		}},
		{name: "duplicate fact", mutate: func(result *fleet.QueryResult) { result.Facts = append(result.Facts, result.Facts[0]) }},
		{name: "oversized coverage", mutate: func(result *fleet.QueryResult) {
			result.Coverage.Stale = make([]string, consoleCorrelationMaxCoverageScopes+1)
			for index := range result.Coverage.Stale {
				result.Coverage.Stale[index] = "cluster"
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := valid
			candidate.Facts = append([]fleet.Fact(nil), valid.Facts...)
			test.mutate(&candidate)
			if _, err := projectConsoleCorrelation(candidate, "workspace-a", request); err == nil {
				t.Fatal("unsafe stored shape was projected")
			}
		})
	}

	tooMany := valid
	tooMany.Facts = make([]fleet.Fact, consoleCorrelationMaxMatches+1)
	if _, err := projectConsoleCorrelation(tooMany, "workspace-a", request); err == nil {
		t.Fatal("sentinel result above response bound was projected")
	}
	inconsistent := valid
	inconsistent.Coverage = fleet.Coverage{Requested: 1, Reachable: 2}
	projected, err := projectConsoleCorrelation(inconsistent, "workspace-a", request)
	if err != nil || !projected.Assessment.Inconsistent || projected.Assessment.Complete {
		t.Fatalf("inconsistent coverage was not preserved honestly: %#v, %v", projected, err)
	}
}

func validConsoleCorrelationRequest(session, proof string) *http.Request {
	return consoleCorrelationRequest(session, proof, "kind=Deployment&name=payments&namespace=payments")
}

func consoleCorrelationRequest(session, proof, rawQuery string) *http.Request {
	request := consoleTestRequest(http.MethodGet, "/v1/workspaces/workspace-a/console/correlate?"+rawQuery, "workspace-a", session)
	request.Header.Set(consoleCSRFHeader, proof)
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	return request
}

func consolePageProof(t *testing.T, page, name string) string {
	t.Helper()
	marker := `<meta name="` + name + `" content="`
	_, after, found := strings.Cut(page, marker)
	if !found {
		t.Fatalf("console page omitted %s proof", name)
	}
	proof, _, found := strings.Cut(after, `">`)
	if !found || proof == "" {
		t.Fatalf("console page emitted malformed %s proof", name)
	}
	return proof
}
