// SPDX-License-Identifier: Apache-2.0

package hubserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

func TestConsoleInventoryUsesPurposeProofPEPAndMinimalProjection(t *testing.T) {
	now := time.Date(2026, 7, 17, 19, 0, 0, 0, time.UTC)
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
	inventory := consoleTestInventory(t, consoleFleetQuerierFunc(func(
		_ context.Context, scope tenancy.Scope, query fleet.Query, freshness time.Duration, gotNow time.Time,
	) (fleet.QueryResult, error) {
		queryCalls++
		if scope.WorkspaceID() != "workspace-a" || scope.Subject() != "user:alice" || freshness != 5*time.Minute || gotNow.IsZero() ||
			query.Limit != consoleInventoryReadLimit || len(query.Kinds) != 1 || query.Kinds[0] != fleet.FactInventory ||
			query.Selector.ResourceKind != "Deployment" || query.Selector.Name != "payments" || query.Selector.Namespace != "payments" ||
			query.Selector.NamePrefix != "" || len(query.Selector.Labels) != 0 {
			t.Fatalf("inventory scope/query/freshness/now = %#v/%#v/%s/%s", scope, query, freshness, gotNow)
		}
		return fleet.QueryResult{
			Facts: []fleet.Fact{{
				Evidence: fleet.Evidence{
					Ref:  fleet.ResourceRef{SourceKind: hubfleet.SourceKind, Scope: "cluster-a", Kind: "Deployment", Namespace: "payments", Name: "payments"},
					Kind: fleet.FactInventory, Observed: json.RawMessage(`{"resource":"Deployment","replicas":4,"available_replicas":3,"generation":9}`),
					ObservedAt: now.Add(-6 * time.Minute), Source: "cluster-a",
					Provenance: fleet.Provenance{Adapter: hubfleet.SourceKind, ProtocolV: "1.0.0"},
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
		Correlator: noopConsoleCorrelator(t), Inventory: inventory, PEP: enforcer, Now: func() time.Time { return now },
		Random: bytes.NewReader(bytes.Repeat([]byte{0x73}, 128)),
	})
	if err != nil {
		t.Fatal(err)
	}

	page, fleetProof := authenticatedConsolePage(t, handler, session, "workspace-a")
	correlationProof := consolePageProof(t, page.Body.String(), "sith-correlation-csrf")
	inventoryProof := consolePageProof(t, page.Body.String(), "sith-inventory-csrf")
	if inventoryProof == "" || inventoryProof == fleetProof || inventoryProof == correlationProof || strings.Contains(page.Body.String(), session) {
		t.Fatalf("purpose proof separation/session leak = %q/%q/%q", fleetProof, correlationProof, inventoryProof)
	}
	response := httptest.NewRecorder()
	handler.ServeInventory(response, validConsoleInventoryRequest(session, inventoryProof))
	if response.Code != http.StatusOK || queryCalls != 1 || len(audits) != 1 || audits[0].Verb != pep.VerbFleetInventorySearch {
		t.Fatalf("status/query calls/audits/body = %d/%d/%#v/%q", response.Code, queryCalls, audits, response.Body.String())
	}
	assertConsoleSecurityHeaders(t, response.Header())
	for _, forbidden := range []string{`"observed"`, `"image_digests"`, `"attributes"`, `"provenance"`, `"workspace"`, `"source_kind"`, `"native_id"`, `"deep_link"`} {
		if strings.Contains(response.Body.String(), forbidden) {
			t.Fatalf("inventory response leaked %q: %s", forbidden, response.Body.String())
		}
	}
	var payload consoleInventoryResponse
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Records) != 1 || payload.Records[0].Scope != "cluster-a" || payload.Records[0].Replicas == nil || *payload.Records[0].Replicas != 4 ||
		payload.Records[0].AvailableReplicas == nil || *payload.Records[0].AvailableReplicas != 3 || payload.Records[0].Generation != 9 ||
		!payload.Records[0].Stale || payload.Records[0].StaleFor != "6m0s" || payload.Assessment.Complete {
		t.Fatalf("projected inventory = %#v", payload)
	}
}

func TestConsoleInventoryRejectsUnsafeRequestsBeforeQuery(t *testing.T) {
	now := time.Date(2026, 7, 17, 20, 0, 0, 0, time.UTC)
	consoleNow := now
	verifier, privateKey := fleetTestVerifier(t, now)
	session := signHubTestToken(t, hubValidClaims(now), privateKey)
	queryCalls := 0
	enforcer := fleetTestPEP(t, pep.AllowReadHook{})
	inventory := consoleTestInventory(t, consoleFleetQuerierFunc(func(context.Context, tenancy.Scope, fleet.Query, time.Duration, time.Time) (fleet.QueryResult, error) {
		queryCalls++
		return fleet.QueryResult{}, nil
	}), enforcer)
	newHandler := func(fill byte) *ConsoleHandler {
		handler, err := NewConsoleHandler(ConsoleHandlerConfig{
			Verifier: verifier,
			Reader: fleetReaderFunc(func(context.Context, tenancy.Scope, time.Duration, time.Time) (fleet.FleetResult, error) {
				return fleet.FleetResult{}, nil
			}),
			Correlator: noopConsoleCorrelator(t), Inventory: inventory, PEP: enforcer, Now: func() time.Time { return consoleNow },
			Random: bytes.NewReader(bytes.Repeat([]byte{fill}, 128)),
		})
		if err != nil {
			t.Fatal(err)
		}
		return handler
	}
	handler := newHandler(0x35)
	page, fleetProof := authenticatedConsolePage(t, handler, session, "workspace-a")
	inventoryProof := consolePageProof(t, page.Body.String(), "sith-inventory-csrf")
	correlationProof := consolePageProof(t, page.Body.String(), "sith-correlation-csrf")

	tests := []struct {
		name    string
		request func() *http.Request
	}{
		{name: "fleet proof", request: func() *http.Request { return validConsoleInventoryRequest(session, fleetProof) }},
		{name: "correlation proof", request: func() *http.Request { return validConsoleInventoryRequest(session, correlationProof) }},
		{name: "missing proof", request: func() *http.Request {
			request := validConsoleInventoryRequest(session, inventoryProof)
			request.Header.Del(consoleCSRFHeader)
			return request
		}},
		{name: "duplicate proof", request: func() *http.Request {
			request := validConsoleInventoryRequest(session, inventoryProof)
			request.Header.Add(consoleCSRFHeader, inventoryProof)
			return request
		}},
		{name: "cross site", request: func() *http.Request {
			request := validConsoleInventoryRequest(session, inventoryProof)
			request.Header.Set("Sec-Fetch-Site", "cross-site")
			return request
		}},
		{name: "foreign workspace", request: func() *http.Request {
			request := consoleTestRequest(http.MethodGet, "/v1/workspaces/workspace-b/console/inventory?kind=Deployment", "workspace-b", session)
			request.Header.Set(consoleCSRFHeader, inventoryProof)
			request.Header.Set("Sec-Fetch-Site", "same-origin")
			return request
		}},
		{name: "bearer fallback", request: func() *http.Request {
			request := validConsoleInventoryRequest(session, inventoryProof)
			request.Header.Set("Authorization", "Bearer "+session)
			return request
		}},
		{name: "duplicate cookie", request: func() *http.Request {
			request := validConsoleInventoryRequest(session, inventoryProof)
			request.AddCookie(&http.Cookie{Name: browserOIDCSessionCookie, Value: session})
			return request
		}},
		{name: "unknown query", request: func() *http.Request { return consoleInventoryRequest(session, inventoryProof, "kind=Pod&selector=all") }},
		{name: "malformed query", request: func() *http.Request { return consoleInventoryRequest(session, inventoryProof, "kind=%zz") }},
		{name: "duplicate query", request: func() *http.Request {
			return consoleInventoryRequest(session, inventoryProof, "kind=Deployment&kind=Pod")
		}},
		{name: "Secret", request: func() *http.Request { return consoleInventoryRequest(session, inventoryProof, "kind=Secret") }},
		{name: "unsupported kind", request: func() *http.Request { return consoleInventoryRequest(session, inventoryProof, "kind=Service") }},
		{name: "blank name", request: func() *http.Request { return consoleInventoryRequest(session, inventoryProof, "kind=Pod&name=") }},
		{name: "blank namespace", request: func() *http.Request { return consoleInventoryRequest(session, inventoryProof, "kind=Pod&namespace=") }},
		{name: "noncanonical order", request: func() *http.Request {
			return consoleInventoryRequest(session, inventoryProof, "namespace=payments&kind=Pod")
		}},
		{name: "untrimmed name", request: func() *http.Request { return consoleInventoryRequest(session, inventoryProof, "kind=Pod&name=+api") }},
		{name: "oversized kind", request: func() *http.Request {
			return consoleInventoryRequest(session, inventoryProof, "kind="+strings.Repeat("a", 129))
		}},
		{name: "fragment", request: func() *http.Request {
			request := validConsoleInventoryRequest(session, inventoryProof)
			request.URL.Fragment = "private"
			return request
		}},
		{name: "path", request: func() *http.Request {
			request := validConsoleInventoryRequest(session, inventoryProof)
			request.URL.Path += "/"
			return request
		}},
		{name: "method", request: func() *http.Request {
			request := validConsoleInventoryRequest(session, inventoryProof)
			request.Method = http.MethodPost
			return request
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeInventory(response, test.request())
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
	handler.ServeInventory(expired, validConsoleInventoryRequest(session, inventoryProof))
	if expired.Code != http.StatusForbidden || queryCalls != 0 {
		t.Fatalf("expired proof status/query calls = %d/%d", expired.Code, queryCalls)
	}
	consoleNow = now
	restarted := newHandler(0x79)
	replayed := httptest.NewRecorder()
	restarted.ServeInventory(replayed, validConsoleInventoryRequest(session, inventoryProof))
	if replayed.Code != http.StatusForbidden || queryCalls != 0 {
		t.Fatalf("restarted proof status/query calls = %d/%d", replayed.Code, queryCalls)
	}
}

func TestConsoleInventoryHidesServiceErrors(t *testing.T) {
	now := time.Date(2026, 7, 17, 20, 30, 0, 0, time.UTC)
	verifier, privateKey := fleetTestVerifier(t, now)
	session := signHubTestToken(t, hubValidClaims(now), privateKey)
	enforcer := fleetTestPEP(t, pep.AllowReadHook{})
	inventory := consoleTestInventory(t, consoleFleetQuerierFunc(func(context.Context, tenancy.Scope, fleet.Query, time.Duration, time.Time) (fleet.QueryResult, error) {
		return fleet.QueryResult{}, errors.New("database password=secret topology=private")
	}), enforcer)
	handler, err := NewConsoleHandler(ConsoleHandlerConfig{
		Verifier: verifier,
		Reader: fleetReaderFunc(func(context.Context, tenancy.Scope, time.Duration, time.Time) (fleet.FleetResult, error) {
			return fleet.FleetResult{}, nil
		}),
		Correlator: noopConsoleCorrelator(t), Inventory: inventory, PEP: enforcer, Now: func() time.Time { return now },
		Random: bytes.NewReader(bytes.Repeat([]byte{0x65}, 128)),
	})
	if err != nil {
		t.Fatal(err)
	}
	page, _ := authenticatedConsolePage(t, handler, session, "workspace-a")
	proof := consolePageProof(t, page.Body.String(), "sith-inventory-csrf")
	response := httptest.NewRecorder()
	handler.ServeInventory(response, validConsoleInventoryRequest(session, proof))
	if response.Code != http.StatusServiceUnavailable || response.Body.String() != "{\"error\":\"inventory_unavailable\"}\n" || strings.Contains(response.Body.String(), "secret") {
		t.Fatalf("status/body = %d/%q", response.Code, response.Body.String())
	}
}

func TestProjectConsoleInventoryFailsClosedOnStoredShape(t *testing.T) {
	now := time.Date(2026, 7, 17, 21, 0, 0, 0, time.UTC)
	request := hubfleet.InventorySearchRequest{ResourceKind: "Pod", Namespace: "payments", Limit: consoleInventoryReadLimit}
	valid := fleet.QueryResult{
		Facts: []fleet.Fact{{
			Evidence: fleet.Evidence{
				Ref:        fleet.ResourceRef{SourceKind: hubfleet.SourceKind, Scope: "cluster-a", Kind: "Pod", Namespace: "payments", Name: "api-0"},
				Kind:       fleet.FactInventory,
				Observed:   json.RawMessage(`{"resource":"Pod","ready":1,"generation":3,"image_digests":["sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"]}`),
				ObservedAt: now, Source: "cluster-a", Provenance: fleet.Provenance{Adapter: hubfleet.SourceKind, ProtocolV: "1.0.0"},
			},
			Workspace: "workspace-a",
		}},
		Coverage: fleet.Coverage{Requested: 1, Reachable: 1},
	}
	projected, err := projectConsoleInventory(valid, "workspace-a", request)
	if err != nil || len(projected.Records) != 1 || projected.Records[0].Ready == nil || !*projected.Records[0].Ready || !projected.Assessment.Complete {
		t.Fatalf("valid projection = %#v, %v", projected, err)
	}
	encoded, err := json.Marshal(projected)
	if err != nil || strings.Contains(string(encoded), "sha256:") || strings.Contains(string(encoded), "image_digests") {
		t.Fatalf("projected Pod leaked digest: %s, %v", encoded, err)
	}

	tests := []struct {
		name   string
		mutate func(*fleet.QueryResult)
	}{
		{name: "foreign workspace", mutate: func(result *fleet.QueryResult) { result.Facts[0].Workspace = "workspace-b" }},
		{name: "unexpected field", mutate: func(result *fleet.QueryResult) {
			result.Facts[0].Observed = json.RawMessage(`{"resource":"Pod","ready":1,"generation":3,"token":"secret"}`)
		}},
		{name: "duplicate field", mutate: func(result *fleet.QueryResult) {
			result.Facts[0].Observed = json.RawMessage(`{"resource":"Pod","ready":1,"ready":0,"generation":3}`)
		}},
		{name: "wrong resource", mutate: func(result *fleet.QueryResult) {
			result.Facts[0].Observed = json.RawMessage(`{"resource":"Deployment","ready":1,"generation":3}`)
		}},
		{name: "fractional count", mutate: func(result *fleet.QueryResult) {
			result.Facts[0].Observed = json.RawMessage(`{"resource":"Pod","ready":0.5,"generation":3}`)
		}},
		{name: "bad digest", mutate: func(result *fleet.QueryResult) {
			result.Facts[0].Observed = json.RawMessage(`{"resource":"Pod","ready":1,"generation":3,"image_digests":["latest"]}`)
		}},
		{name: "source mismatch", mutate: func(result *fleet.QueryResult) { result.Facts[0].Source = "cluster-b" }},
		{name: "attributes", mutate: func(result *fleet.QueryResult) { result.Facts[0].Ref.Attributes = map[string]string{"token": "secret"} }},
		{name: "provenance", mutate: func(result *fleet.QueryResult) { result.Facts[0].Provenance.NativeID = "private" }},
		{name: "display", mutate: func(result *fleet.QueryResult) {
			result.Facts[0].Display = []fleet.DisplayField{{Name: "token", Value: "secret"}}
		}},
		{name: "selector mismatch", mutate: func(result *fleet.QueryResult) { result.Facts[0].Ref.Namespace = "other" }},
		{name: "stale mismatch", mutate: func(result *fleet.QueryResult) { result.Facts[0].StaleFor = "collection failed" }},
		{name: "untrusted stale text", mutate: func(result *fleet.QueryResult) {
			result.Facts[0].Stale = true
			result.Facts[0].StaleFor = "credential=must-not-leak"
		}},
		{name: "duplicate fact", mutate: func(result *fleet.QueryResult) { result.Facts = append(result.Facts, result.Facts[0]) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := valid
			candidate.Facts = append([]fleet.Fact(nil), valid.Facts...)
			test.mutate(&candidate)
			if _, err := projectConsoleInventory(candidate, "workspace-a", request); err == nil {
				t.Fatal("unsafe stored shape was projected")
			}
		})
	}

	tooMany := valid
	tooMany.Facts = make([]fleet.Fact, consoleInventoryMaxRecords+1)
	if _, err := projectConsoleInventory(tooMany, "workspace-a", request); err == nil {
		t.Fatal("sentinel result above response bound was projected")
	}
	inconsistent := valid
	inconsistent.Coverage = fleet.Coverage{Requested: 1, Reachable: 2}
	projected, err = projectConsoleInventory(inconsistent, "workspace-a", request)
	if err != nil || !projected.Assessment.Inconsistent || projected.Assessment.Complete {
		t.Fatalf("inconsistent coverage was not preserved honestly: %#v, %v", projected, err)
	}
}

func FuzzProjectConsoleInventoryObserved(f *testing.F) {
	f.Add([]byte(`{"resource":"Pod","ready":1,"generation":3}`))
	f.Add([]byte(`{"resource":"Pod","ready":1,"ready":0,"generation":3}`))
	f.Add([]byte(`{"resource":"Pod","ready":1,"generation":3,"image_digests":["sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"]}`))
	f.Fuzz(func(t *testing.T, observed []byte) {
		now := time.Date(2026, 7, 17, 21, 30, 0, 0, time.UTC)
		result := fleet.QueryResult{
			Facts: []fleet.Fact{{
				Evidence: fleet.Evidence{
					Ref:  fleet.ResourceRef{SourceKind: hubfleet.SourceKind, Scope: "cluster-a", Kind: "Pod", Namespace: "payments", Name: "api-0"},
					Kind: fleet.FactInventory, Observed: observed, ObservedAt: now, Source: "cluster-a",
					Provenance: fleet.Provenance{Adapter: hubfleet.SourceKind, ProtocolV: "1.0.0"},
				},
				Workspace: "workspace-a",
			}},
			Coverage: fleet.Coverage{Requested: 1, Reachable: 1},
		}
		projected, err := projectConsoleInventory(result, "workspace-a", hubfleet.InventorySearchRequest{ResourceKind: "Pod", Namespace: "payments", Limit: consoleInventoryReadLimit})
		if err != nil {
			return
		}
		encoded, err := json.Marshal(projected)
		if err != nil || len(projected.Records) != 1 || strings.Contains(string(encoded), "image_digests") || strings.Contains(string(encoded), `"observed"`) {
			t.Fatalf("successful projection violated the closed response: %#v, %s, %v", projected, encoded, err)
		}
	})
}

func validConsoleInventoryRequest(session, proof string) *http.Request {
	return consoleInventoryRequest(session, proof, "kind=Deployment&name=payments&namespace=payments")
}

func consoleInventoryRequest(session, proof, rawQuery string) *http.Request {
	request := consoleTestRequest(http.MethodGet, "/v1/workspaces/workspace-a/console/inventory?"+rawQuery, "workspace-a", session)
	request.Header.Set(consoleCSRFHeader, proof)
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	return request
}
