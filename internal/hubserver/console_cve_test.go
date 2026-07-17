// SPDX-License-Identifier: Apache-2.0

package hubserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

func TestConsoleCVEUsesPurposeProofPEPAndMinimalProjection(t *testing.T) {
	now := time.Date(2026, 7, 17, 22, 0, 0, 0, time.UTC)
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
	digest := "sha256:" + strings.Repeat("a", 64)
	queryCalls := 0
	cve := consoleTestCVE(t, consoleFleetQuerierFunc(func(
		_ context.Context, scope tenancy.Scope, query fleet.Query, freshness time.Duration, gotNow time.Time,
	) (fleet.QueryResult, error) {
		queryCalls++
		if scope.WorkspaceID() != "workspace-a" || scope.Subject() != "user:alice" || freshness != 5*time.Minute || gotNow.IsZero() ||
			query.Limit != consoleCVEReadLimit || len(query.Kinds) != 1 || query.Kinds[0] != fleet.FactCVE ||
			query.Selector.ResourceKind != "Image" || query.Selector.CVE != "CVE-2026-0001" || query.Selector.Image != "" {
			t.Fatalf("CVE scope/query/freshness/now = %#v/%#v/%s/%s", scope, query, freshness, gotNow)
		}
		return consoleCVEQueryResult(now, digest), nil
	}), enforcer)
	handler, err := NewConsoleHandler(ConsoleHandlerConfig{
		Verifier: verifier,
		Reader: fleetReaderFunc(func(context.Context, tenancy.Scope, time.Duration, time.Time) (fleet.FleetResult, error) {
			return fleet.FleetResult{}, nil
		}),
		Correlator: noopConsoleCorrelator(t), Inventory: noopConsoleInventory(t), CVE: cve, PEP: enforcer, Now: func() time.Time { return now },
		Random: bytes.NewReader(bytes.Repeat([]byte{0x74}, 192)),
	})
	if err != nil {
		t.Fatal(err)
	}

	page, fleetProof := authenticatedConsolePage(t, handler, session, "workspace-a")
	cveProof := consolePageProof(t, page.Body.String(), "sith-cve-csrf")
	correlationProof := consolePageProof(t, page.Body.String(), "sith-correlation-csrf")
	inventoryProof := consolePageProof(t, page.Body.String(), "sith-inventory-csrf")
	if cveProof == "" || cveProof == fleetProof || cveProof == correlationProof || cveProof == inventoryProof || strings.Contains(page.Body.String(), session) {
		t.Fatalf("purpose proof separation/session leak = %q/%q/%q/%q", fleetProof, correlationProof, inventoryProof, cveProof)
	}
	response := httptest.NewRecorder()
	handler.ServeCVEIdentifier(response, validConsoleCVERequest(session, cveProof))
	if response.Code != http.StatusOK || queryCalls != 1 || len(audits) != 1 || audits[0].Verb != pep.VerbFleetCVEIdentifierSearch {
		t.Fatalf("status/query calls/audits/body = %d/%d/%#v/%q", response.Code, queryCalls, audits, response.Body.String())
	}
	assertConsoleSecurityHeaders(t, response.Header())
	for _, forbidden := range []string{`"observed"`, `"ids"`, `"attributes"`, `"provenance"`, `"workspace"`, `"source_kind"`, `"native_id"`, `"deep_link"`} {
		if strings.Contains(response.Body.String(), forbidden) {
			t.Fatalf("CVE response leaked %q: %s", forbidden, response.Body.String())
		}
	}
	var payload consoleCVEResponse
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Records) != 1 || payload.Records[0].Scope != "cluster-a" || payload.Records[0].ImageDigest != digest ||
		payload.Records[0].Identifier != "CVE-2026-0001" || payload.Records[0].Severity != "high" ||
		!payload.Records[0].ObservedAt.Equal(now.Add(-6*time.Minute)) || !payload.Records[0].Stale || payload.Records[0].StaleFor != "6m0s" ||
		payload.Assessment.Complete || !containsCoverageGap(payload.Assessment.Gaps, fleet.CoverageGapStale) ||
		!containsCoverageGap(payload.Assessment.Gaps, fleet.CoverageGapUnreachable) ||
		len(payload.Assessment.Stale) != 1 || payload.Assessment.Stale[0] != "cluster-a" ||
		len(payload.Assessment.Unreachable) != 1 || payload.Assessment.Unreachable[0] != "cluster-c" {
		t.Fatalf("projected CVE evidence = %#v", payload)
	}
}

func TestConsoleCVERejectsUnsafeRequestsBeforeQuery(t *testing.T) {
	now := time.Date(2026, 7, 17, 22, 30, 0, 0, time.UTC)
	consoleNow := now
	verifier, privateKey := fleetTestVerifier(t, now)
	session := signHubTestToken(t, hubValidClaims(now), privateKey)
	queryCalls := 0
	enforcer := fleetTestPEP(t, pep.AllowReadHook{})
	cve := consoleTestCVE(t, consoleFleetQuerierFunc(func(context.Context, tenancy.Scope, fleet.Query, time.Duration, time.Time) (fleet.QueryResult, error) {
		queryCalls++
		return fleet.QueryResult{}, nil
	}), enforcer)
	newHandler := func(fill byte) *ConsoleHandler {
		handler, err := NewConsoleHandler(ConsoleHandlerConfig{
			Verifier: verifier,
			Reader: fleetReaderFunc(func(context.Context, tenancy.Scope, time.Duration, time.Time) (fleet.FleetResult, error) {
				return fleet.FleetResult{}, nil
			}),
			Correlator: noopConsoleCorrelator(t), Inventory: noopConsoleInventory(t), CVE: cve, PEP: enforcer, Now: func() time.Time { return consoleNow },
			Random: bytes.NewReader(bytes.Repeat([]byte{fill}, 192)),
		})
		if err != nil {
			t.Fatal(err)
		}
		return handler
	}
	handler := newHandler(0x36)
	page, fleetProof := authenticatedConsolePage(t, handler, session, "workspace-a")
	cveProof := consolePageProof(t, page.Body.String(), "sith-cve-csrf")
	inventoryProof := consolePageProof(t, page.Body.String(), "sith-inventory-csrf")
	correlationProof := consolePageProof(t, page.Body.String(), "sith-correlation-csrf")

	tests := []struct {
		name    string
		request func() *http.Request
	}{
		{name: "fleet proof", request: func() *http.Request { return validConsoleCVERequest(session, fleetProof) }},
		{name: "inventory proof", request: func() *http.Request { return validConsoleCVERequest(session, inventoryProof) }},
		{name: "correlation proof", request: func() *http.Request { return validConsoleCVERequest(session, correlationProof) }},
		{name: "missing proof", request: func() *http.Request {
			request := validConsoleCVERequest(session, cveProof)
			request.Header.Del(consoleCSRFHeader)
			return request
		}},
		{name: "duplicate proof", request: func() *http.Request {
			request := validConsoleCVERequest(session, cveProof)
			request.Header.Add(consoleCSRFHeader, cveProof)
			return request
		}},
		{name: "cross site", request: func() *http.Request {
			request := validConsoleCVERequest(session, cveProof)
			request.Header.Set("Sec-Fetch-Site", "cross-site")
			return request
		}},
		{name: "foreign workspace", request: func() *http.Request {
			request := consoleTestRequest(http.MethodGet, "/v1/workspaces/workspace-b/console/cves?identifier=CVE-2026-0001", "workspace-b", session)
			request.Header.Set(consoleCSRFHeader, cveProof)
			request.Header.Set("Sec-Fetch-Site", "same-origin")
			return request
		}},
		{name: "bearer fallback", request: func() *http.Request {
			request := validConsoleCVERequest(session, cveProof)
			request.Header.Set("Authorization", "Bearer "+session)
			return request
		}},
		{name: "duplicate cookie", request: func() *http.Request {
			request := validConsoleCVERequest(session, cveProof)
			request.AddCookie(&http.Cookie{Name: browserOIDCSessionCookie, Value: session})
			return request
		}},
		{name: "lowercase", request: func() *http.Request { return consoleCVERequest(session, cveProof, "identifier=cve-2026-0001") }},
		{name: "wildcard", request: func() *http.Request { return consoleCVERequest(session, cveProof, "identifier=CVE-2026-0001%2A") }},
		{name: "duplicate query", request: func() *http.Request {
			return consoleCVERequest(session, cveProof, "identifier=CVE-2026-0001&identifier=CVE-2026-0002")
		}},
		{name: "unknown query", request: func() *http.Request { return consoleCVERequest(session, cveProof, "identifier=CVE-2026-0001&limit=1") }},
		{name: "malformed query", request: func() *http.Request { return consoleCVERequest(session, cveProof, "identifier=%zz") }},
		{name: "noncanonical encoding", request: func() *http.Request { return consoleCVERequest(session, cveProof, "identifier=CVE%2D2026%2D0001") }},
		{name: "oversized identifier", request: func() *http.Request {
			return consoleCVERequest(session, cveProof, "identifier=CVE-2026-"+strings.Repeat("1", 56))
		}},
		{name: "fragment", request: func() *http.Request {
			request := validConsoleCVERequest(session, cveProof)
			request.URL.Fragment = "private"
			return request
		}},
		{name: "path", request: func() *http.Request {
			request := validConsoleCVERequest(session, cveProof)
			request.URL.Path += "/"
			return request
		}},
		{name: "method", request: func() *http.Request {
			request := validConsoleCVERequest(session, cveProof)
			request.Method = http.MethodPost
			return request
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeCVEIdentifier(response, test.request())
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
	handler.ServeCVEIdentifier(expired, validConsoleCVERequest(session, cveProof))
	if expired.Code != http.StatusForbidden || queryCalls != 0 {
		t.Fatalf("expired proof status/query calls = %d/%d", expired.Code, queryCalls)
	}
	consoleNow = now
	restarted := newHandler(0x78)
	replayed := httptest.NewRecorder()
	restarted.ServeCVEIdentifier(replayed, validConsoleCVERequest(session, cveProof))
	if replayed.Code != http.StatusForbidden || queryCalls != 0 {
		t.Fatalf("restarted proof status/query calls = %d/%d", replayed.Code, queryCalls)
	}
}

func TestConsoleCVEHidesServiceErrors(t *testing.T) {
	now := time.Date(2026, 7, 17, 23, 0, 0, 0, time.UTC)
	verifier, privateKey := fleetTestVerifier(t, now)
	session := signHubTestToken(t, hubValidClaims(now), privateKey)
	enforcer := fleetTestPEP(t, pep.AllowReadHook{})
	cve := consoleTestCVE(t, consoleFleetQuerierFunc(func(context.Context, tenancy.Scope, fleet.Query, time.Duration, time.Time) (fleet.QueryResult, error) {
		return fleet.QueryResult{}, errors.New("database password=secret topology=private")
	}), enforcer)
	handler, err := NewConsoleHandler(ConsoleHandlerConfig{
		Verifier: verifier,
		Reader: fleetReaderFunc(func(context.Context, tenancy.Scope, time.Duration, time.Time) (fleet.FleetResult, error) {
			return fleet.FleetResult{}, nil
		}),
		Correlator: noopConsoleCorrelator(t), Inventory: noopConsoleInventory(t), CVE: cve, PEP: enforcer, Now: func() time.Time { return now },
		Random: bytes.NewReader(bytes.Repeat([]byte{0x66}, 192)),
	})
	if err != nil {
		t.Fatal(err)
	}
	page, _ := authenticatedConsolePage(t, handler, session, "workspace-a")
	proof := consolePageProof(t, page.Body.String(), "sith-cve-csrf")
	response := httptest.NewRecorder()
	handler.ServeCVEIdentifier(response, validConsoleCVERequest(session, proof))
	if response.Code != http.StatusServiceUnavailable || response.Body.String() != "{\"error\":\"cve_evidence_unavailable\"}\n" || strings.Contains(response.Body.String(), "secret") {
		t.Fatalf("status/body = %d/%q", response.Code, response.Body.String())
	}
}

func TestProjectConsoleCVEFailsClosedOnStoredShape(t *testing.T) {
	now := time.Date(2026, 7, 17, 23, 30, 0, 0, time.UTC)
	digest := "sha256:" + strings.Repeat("a", 64)
	request := hubfleet.CVEIdentifierSearchRequest{Identifier: "CVE-2026-0001", Limit: consoleCVEReadLimit}
	valid := consoleCVEQueryResult(now, digest)
	projected, err := projectConsoleCVEIdentifier(valid, "workspace-a", request)
	if err != nil || len(projected.Records) != 1 || projected.Records[0].ImageDigest != digest || projected.Records[0].Severity != "high" {
		t.Fatalf("valid projection = %#v, %v", projected, err)
	}

	tests := []struct {
		name   string
		mutate func(*fleet.QueryResult)
	}{
		{name: "foreign workspace", mutate: func(result *fleet.QueryResult) { result.Facts[0].Workspace = "workspace-b" }},
		{name: "wrong kind", mutate: func(result *fleet.QueryResult) { result.Facts[0].Kind = fleet.FactInventory }},
		{name: "source mismatch", mutate: func(result *fleet.QueryResult) { result.Facts[0].Source = "cluster-b" }},
		{name: "tag instead of digest", mutate: func(result *fleet.QueryResult) { result.Facts[0].Ref.Name = "registry.example/api:latest" }},
		{name: "attributes", mutate: func(result *fleet.QueryResult) { result.Facts[0].Ref.Attributes = map[string]string{"token": "secret"} }},
		{name: "provenance", mutate: func(result *fleet.QueryResult) { result.Facts[0].Provenance.NativeID = "private" }},
		{name: "display", mutate: func(result *fleet.QueryResult) {
			result.Facts[0].Display = []fleet.DisplayField{{Name: "token", Value: "secret"}}
		}},
		{name: "selector mismatch", mutate: func(result *fleet.QueryResult) {
			result.Facts[0].Observed = json.RawMessage(`{"image":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","ids":["CVE-2026-0002"],"severity":"high"}`)
		}},
		{name: "digest mismatch", mutate: func(result *fleet.QueryResult) {
			result.Facts[0].Observed = json.RawMessage(`{"image":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","ids":["CVE-2026-0001"],"severity":"high"}`)
		}},
		{name: "unknown field", mutate: func(result *fleet.QueryResult) {
			result.Facts[0].Observed = json.RawMessage(`{"image":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","ids":["CVE-2026-0001"],"severity":"high","token":"secret"}`)
		}},
		{name: "duplicate field", mutate: func(result *fleet.QueryResult) {
			result.Facts[0].Observed = json.RawMessage(`{"image":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","ids":["CVE-2026-0001"],"ids":["CVE-2026-0002"],"severity":"high"}`)
		}},
		{name: "untrusted stale text", mutate: func(result *fleet.QueryResult) { result.Facts[0].StaleFor = "credential=must-not-leak" }},
		{name: "duplicate fact", mutate: func(result *fleet.QueryResult) { result.Facts = append(result.Facts, result.Facts[0]) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := valid
			candidate.Facts = append([]fleet.Fact(nil), valid.Facts...)
			test.mutate(&candidate)
			if _, err := projectConsoleCVEIdentifier(candidate, "workspace-a", request); err == nil {
				t.Fatal("unsafe stored shape was projected")
			}
		})
	}

	tooMany := valid
	tooMany.Facts = make([]fleet.Fact, consoleCVEMaxRecords+1)
	for index := range tooMany.Facts {
		distinctDigest := fmt.Sprintf("sha256:%064x", index+1)
		fact := valid.Facts[0]
		fact.Ref.Scope = fmt.Sprintf("cluster-%03d", index+1)
		fact.Ref.Name = distinctDigest
		fact.Source = fact.Ref.Scope
		fact.Observed = json.RawMessage(`{"image":"` + distinctDigest + `","ids":["CVE-2026-0001"],"severity":"high"}`)
		tooMany.Facts[index] = fact
	}
	if _, err := projectConsoleCVEIdentifier(tooMany, "workspace-a", request); err == nil {
		t.Fatal("sentinel result above response bound was projected")
	}
	inconsistent := valid
	inconsistent.Coverage = fleet.Coverage{Requested: 1, Reachable: 2}
	projected, err = projectConsoleCVEIdentifier(inconsistent, "workspace-a", request)
	if err != nil || !projected.Assessment.Inconsistent || projected.Assessment.Complete {
		t.Fatalf("inconsistent coverage was not preserved honestly: %#v, %v", projected, err)
	}
}

func FuzzProjectConsoleCVEObserved(f *testing.F) {
	f.Add([]byte(`{"image":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","ids":["CVE-2026-0001"],"severity":"high"}`))
	f.Add([]byte(`{"image":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","ids":["CVE-2026-0001"],"ids":["CVE-2026-0002"],"severity":"high"}`))
	f.Fuzz(func(t *testing.T, observed []byte) {
		now := time.Date(2026, 7, 17, 23, 45, 0, 0, time.UTC)
		digest := "sha256:" + strings.Repeat("a", 64)
		result := consoleCVEQueryResult(now, digest)
		result.Facts[0].Observed = observed
		projected, err := projectConsoleCVEIdentifier(result, "workspace-a", hubfleet.CVEIdentifierSearchRequest{Identifier: "CVE-2026-0001", Limit: consoleCVEReadLimit})
		if err != nil {
			return
		}
		encoded, err := json.Marshal(projected)
		if err != nil || len(projected.Records) != 1 || strings.Contains(string(encoded), `"observed"`) || strings.Contains(string(encoded), `"ids"`) {
			t.Fatalf("successful projection violated closed response: %#v, %s, %v", projected, encoded, err)
		}
	})
}

func consoleCVEQueryResult(now time.Time, digest string) fleet.QueryResult {
	return fleet.QueryResult{
		Facts: []fleet.Fact{{
			Evidence: fleet.Evidence{
				Ref:  fleet.ResourceRef{SourceKind: hubfleet.SourceKind, Scope: "cluster-a", Kind: "Image", Name: digest},
				Kind: fleet.FactCVE, Observed: json.RawMessage(`{"image":"` + digest + `","ids":["CVE-2026-0001","CVE-2026-0002"],"severity":"high"}`),
				ObservedAt: now.Add(-6 * time.Minute), Source: "cluster-a",
				Provenance: fleet.Provenance{Adapter: hubfleet.SourceKind, ProtocolV: "1.0.0"},
			},
			Workspace: "workspace-a", Stale: true, StaleFor: "6m0s",
		}},
		Coverage: fleet.Coverage{Requested: 3, Reachable: 2, Unreachable: []string{"cluster-c"}, Stale: []string{"cluster-a"}},
	}
}

func validConsoleCVERequest(session, proof string) *http.Request {
	return consoleCVERequest(session, proof, "identifier=CVE-2026-0001")
}

func consoleCVERequest(session, proof, rawQuery string) *http.Request {
	request := consoleTestRequest(http.MethodGet, "/v1/workspaces/workspace-a/console/cves?"+rawQuery, "workspace-a", session)
	request.Header.Set(consoleCSRFHeader, proof)
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	return request
}
