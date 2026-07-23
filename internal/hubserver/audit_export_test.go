// SPDX-License-Identifier: Apache-2.0

package hubserver

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"go/parser"
	"go/token"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ArdurAI/sith/internal/auditrecord"
	"github.com/ArdurAI/sith/internal/pep"
	"github.com/ArdurAI/sith/internal/tenancy"
)

type policyAuditExporterFunc func(context.Context, tenancy.Scope) (auditrecord.Export, error)

func (function policyAuditExporterFunc) ExportPolicyAuditChain(ctx context.Context, scope tenancy.Scope) (auditrecord.Export, error) {
	return function(ctx, scope)
}

func (function policyAuditExporterFunc) ExportPolicyAuditPage(
	context.Context,
	tenancy.Scope,
	auditrecord.PageRequest,
) (auditrecord.Page, error) {
	return auditrecord.Page{}, errors.New("paged export is unavailable")
}

type policyAuditExporterStub struct {
	complete func(context.Context, tenancy.Scope) (auditrecord.Export, error)
	page     func(context.Context, tenancy.Scope, auditrecord.PageRequest) (auditrecord.Page, error)
}

func (stub policyAuditExporterStub) ExportPolicyAuditChain(ctx context.Context, scope tenancy.Scope) (auditrecord.Export, error) {
	if stub.complete == nil {
		return auditrecord.Export{}, errors.New("complete export is unavailable")
	}
	return stub.complete(ctx, scope)
}

func (stub policyAuditExporterStub) ExportPolicyAuditPage(
	ctx context.Context,
	scope tenancy.Scope,
	request auditrecord.PageRequest,
) (auditrecord.Page, error) {
	if stub.page == nil {
		return auditrecord.Page{}, errors.New("paged export is unavailable")
	}
	return stub.page(ctx, scope, request)
}

func TestAuditExportHandlerAuthorizesBeforePortableDownload(t *testing.T) {
	now := time.Date(2026, time.July, 18, 9, 30, 0, 0, time.UTC)
	verifier, privateKey := fleetTestVerifier(t, now)
	var events []pep.AuditEvent
	enforcer, err := pep.NewEnforcer(pep.Config{
		Hook: pep.AllowReadHook{},
		Auditor: pep.AuditFunc(func(_ context.Context, event pep.AuditEvent) error {
			events = append(events, event)
			return nil
		}),
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	want := testPolicyAuditExport(now)
	handler, err := NewAuditExportHandler(AuditExportHandlerConfig{
		Verifier: verifier,
		Exporter: policyAuditExporterFunc(func(_ context.Context, scope tenancy.Scope) (auditrecord.Export, error) {
			if len(events) != 1 || events[0].WorkspaceID != scope.WorkspaceID() ||
				events[0].Action != tenancy.ActionExportAudit || events[0].Verb != pep.VerbAuditExport ||
				events[0].Verdict != pep.VerdictAllow {
				t.Fatalf("exporter observed policy events = %#v", events)
			}
			return want, nil
		}),
		PEP: enforcer,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := authenticatedAuditExportRequest(t, http.MethodGet, "/v1/workspaces/workspace-a/audit/export", privateKey, now, tenancy.RoleAdmin)
	request.Header.Set("X-Workspace", "workspace-b")
	request.Header.Set("X-Role", "reader")
	request.Header.Set("Traceparent", "00-0123456789abcdef0123456789abcdef-0123456789abcdef-01")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status/body = %d/%q", response.Code, response.Body.String())
	}
	if response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("Pragma") != "no-cache" ||
		response.Header().Get("X-Content-Type-Options") != "nosniff" || response.Header().Get("Content-Type") != "application/json" ||
		response.Header().Get("Content-Disposition") != `attachment; filename="sith-policy-audit.json"` ||
		response.Header().Get("Content-Length") != strconv.Itoa(response.Body.Len()) {
		t.Fatalf("unsafe export headers = %#v", response.Header())
	}
	var got auditrecord.Export
	if err := json.NewDecoder(response.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Schema != want.Schema || got.WorkspaceID != "workspace-a" || got.Chain != want.Chain || len(got.Entries) != 1 ||
		got.Entries[0] != want.Entries[0] {
		t.Fatalf("export = %#v, want %#v", got, want)
	}
	if len(events) != 1 || events[0].Actor != "user:alice" || events[0].Role != tenancy.RoleAdmin ||
		events[0].TraceID == "0123456789abcdef0123456789abcdef" {
		t.Fatalf("policy events = %#v", events)
	}
}

func TestAuditExportHandlerAuthorizesSnapshotBoundPages(t *testing.T) {
	now := time.Date(2026, time.July, 18, 9, 30, 0, 0, time.UTC)
	verifier, privateKey := fleetTestVerifier(t, now)
	var events []pep.AuditEvent
	enforcer, err := pep.NewEnforcer(pep.Config{
		Hook: pep.AllowReadHook{},
		Auditor: pep.AuditFunc(func(_ context.Context, event pep.AuditEvent) error {
			events = append(events, event)
			return nil
		}),
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	initial := testPolicyAuditPage(now, 1, "sha256:"+strings.Repeat("0", 64))
	continuation := testPolicyAuditPage(now.Add(time.Microsecond), 513, "sha256:"+strings.Repeat("b", 64))
	cursor, err := auditrecord.EncodePageCursor(
		"workspace-a", continuation.Snapshot.HeadSequence, continuation.Snapshot.HeadHash,
		continuation.StartSequence, continuation.PreviousHash,
	)
	if err != nil {
		t.Fatal(err)
	}
	pageCalls := 0
	handler, err := NewAuditExportHandler(AuditExportHandlerConfig{
		Verifier: verifier,
		Exporter: policyAuditExporterStub{page: func(_ context.Context, scope tenancy.Scope, request auditrecord.PageRequest) (auditrecord.Page, error) {
			pageCalls++
			if len(events) != pageCalls || scope.WorkspaceID() != "workspace-a" {
				t.Fatalf("page exporter observed events/scope = %#v/%#v", events, scope)
			}
			if pageCalls == 1 {
				if !request.Initial() {
					t.Fatal("initial page route produced a continuation request")
				}
				return initial, nil
			}
			if request.Initial() || request.HeadSequence() != 513 || request.NextSequence() != 513 ||
				request.PreviousHash() != continuation.PreviousHash {
				t.Fatalf("continuation request = %#v", request)
			}
			return continuation, nil
		}},
		PEP: enforcer,
	})
	if err != nil {
		t.Fatal(err)
	}

	for index, target := range []string{
		"/v1/workspaces/workspace-a/audit/export/pages",
		"/v1/workspaces/workspace-a/audit/export/pages?cursor=" + cursor,
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, authenticatedAuditExportRequest(t, http.MethodGet, target, privateKey, now, tenancy.RoleAdmin))
		if response.Code != http.StatusOK || response.Header().Get("Content-Disposition") !=
			`attachment; filename="sith-policy-audit-page.json"` || response.Header().Get("Content-Length") != strconv.Itoa(response.Body.Len()) {
			t.Fatalf("page %d status/headers/body = %d/%#v/%q", index, response.Code, response.Header(), response.Body.String())
		}
		var page auditrecord.Page
		if err := json.NewDecoder(response.Body).Decode(&page); err != nil || page.Verify() != nil {
			t.Fatalf("page %d decode/verify = %#v/%v", index, page, err)
		}
	}
	if pageCalls != 2 || len(events) != 2 {
		t.Fatalf("page calls/events = %d/%#v", pageCalls, events)
	}
	for _, event := range events {
		if event.Action != tenancy.ActionExportAudit || event.Verb != pep.VerbAuditExport ||
			event.Verdict != pep.VerdictAllow {
			t.Fatalf("page policy event = %#v", event)
		}
	}
}

func TestAuditExportHandlerAuditsCanonicalButInvalidCursor(t *testing.T) {
	now := time.Date(2026, time.July, 18, 9, 30, 0, 0, time.UTC)
	verifier, privateKey := fleetTestVerifier(t, now)
	events := 0
	enforcer, err := pep.NewEnforcer(pep.Config{
		Hook: pep.AllowReadHook{}, Auditor: pep.AuditFunc(func(context.Context, pep.AuditEvent) error {
			events++
			return nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := mustAuditExportHandler(t, verifier, policyAuditExporterStub{page: func(
		context.Context, tenancy.Scope, auditrecord.PageRequest,
	) (auditrecord.Page, error) {
		t.Fatal("invalid cursor reached exporter")
		return auditrecord.Page{}, nil
	}}, enforcer)
	response := httptest.NewRecorder()
	target := "/v1/workspaces/workspace-a/audit/export/pages?cursor=" + strings.Repeat("A", auditrecord.PageCursorChars)
	handler.ServeHTTP(response, authenticatedAuditExportRequest(t, http.MethodGet, target, privateKey, now, tenancy.RoleAdmin))
	if response.Code != http.StatusServiceUnavailable || events != 1 ||
		response.Body.String() != "{\"error\":\"audit_export_unavailable\"}\n" {
		t.Fatalf("status/events/body = %d/%d/%q", response.Code, events, response.Body.String())
	}
}

func TestAuditExportHandlerFailsClosedBeforeDisclosure(t *testing.T) {
	now := time.Date(2026, time.July, 18, 9, 30, 0, 0, time.UTC)
	verifier, privateKey := fleetTestVerifier(t, now)

	t.Run("non-admin role", func(t *testing.T) {
		var events []pep.AuditEvent
		enforcer, err := pep.NewEnforcer(pep.Config{
			Hook: pep.AllowReadHook{}, Auditor: pep.AuditFunc(func(_ context.Context, event pep.AuditEvent) error {
				events = append(events, event)
				return nil
			}),
		})
		if err != nil {
			t.Fatal(err)
		}
		handler := mustAuditExportHandler(t, verifier, policyAuditExporterFunc(func(context.Context, tenancy.Scope) (auditrecord.Export, error) {
			t.Fatal("non-admin request reached exporter")
			return auditrecord.Export{}, nil
		}), enforcer)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, authenticatedAuditExportRequest(t, http.MethodGet, "/v1/workspaces/workspace-a/audit/export", privateKey, now, tenancy.RoleReader))
		if response.Code != http.StatusForbidden || response.Body.String() != "{\"error\":\"forbidden\"}\n" ||
			len(events) != 1 || events[0].Verdict != pep.VerdictDeny || events[0].ReasonCode != "role-denied" {
			t.Fatalf("status/body/events = %d/%q/%#v", response.Code, response.Body.String(), events)
		}
	})

	t.Run("foreign workspace", func(t *testing.T) {
		handler := mustAuditExportHandler(t, verifier, policyAuditExporterFunc(func(context.Context, tenancy.Scope) (auditrecord.Export, error) {
			t.Fatal("foreign request reached exporter")
			return auditrecord.Export{}, nil
		}), fleetTestPEP(t, pep.AllowReadHook{}))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, authenticatedAuditExportRequest(t, http.MethodGet, "/v1/workspaces/workspace-b/audit/export", privateKey, now, tenancy.RoleAdmin))
		if response.Code != http.StatusForbidden {
			t.Fatalf("status/body = %d/%q", response.Code, response.Body.String())
		}
	})

	t.Run("policy audit failure", func(t *testing.T) {
		enforcer, err := pep.NewEnforcer(pep.Config{
			Hook: pep.AllowReadHook{}, Auditor: pep.AuditFunc(func(context.Context, pep.AuditEvent) error { return errors.New("token=secret") }),
		})
		if err != nil {
			t.Fatal(err)
		}
		handler := mustAuditExportHandler(t, verifier, policyAuditExporterFunc(func(context.Context, tenancy.Scope) (auditrecord.Export, error) {
			t.Fatal("audit failure reached exporter")
			return auditrecord.Export{}, nil
		}), enforcer)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, authenticatedAuditExportRequest(t, http.MethodGet, "/v1/workspaces/workspace-a/audit/export", privateKey, now, tenancy.RoleAdmin))
		if response.Code != http.StatusServiceUnavailable || strings.Contains(response.Body.String(), "secret") {
			t.Fatalf("status/body = %d/%q", response.Code, response.Body.String())
		}
	})

	t.Run("export failure", func(t *testing.T) {
		handler := mustAuditExportHandler(t, verifier, policyAuditExporterFunc(func(context.Context, tenancy.Scope) (auditrecord.Export, error) {
			return auditrecord.Export{}, errors.New("database-url=secret")
		}), fleetTestPEP(t, pep.AllowReadHook{}))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, authenticatedAuditExportRequest(t, http.MethodGet, "/v1/workspaces/workspace-a/audit/export", privateKey, now, tenancy.RoleAdmin))
		if response.Code != http.StatusServiceUnavailable || strings.Contains(response.Body.String(), "secret") {
			t.Fatalf("status/body = %d/%q", response.Code, response.Body.String())
		}
	})

	t.Run("foreign export object", func(t *testing.T) {
		handler := mustAuditExportHandler(t, verifier, policyAuditExporterFunc(func(context.Context, tenancy.Scope) (auditrecord.Export, error) {
			foreign := testPolicyAuditExport(now)
			foreign.WorkspaceID = "workspace-b"
			return foreign, nil
		}), fleetTestPEP(t, pep.AllowReadHook{}))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, authenticatedAuditExportRequest(t, http.MethodGet, "/v1/workspaces/workspace-a/audit/export", privateKey, now, tenancy.RoleAdmin))
		if response.Code != http.StatusServiceUnavailable || strings.Contains(response.Body.String(), "workspace-b") {
			t.Fatalf("status/body = %d/%q", response.Code, response.Body.String())
		}
	})
}

func TestAuditExportHandlerRejectsMalformedSurfaceBeforePolicy(t *testing.T) {
	now := time.Date(2026, time.July, 18, 9, 30, 0, 0, time.UTC)
	verifier, privateKey := fleetTestVerifier(t, now)
	policyCalls := 0
	enforcer, err := pep.NewEnforcer(pep.Config{
		Hook: pep.HookFunc(func(context.Context, pep.Request) (pep.Decision, error) {
			policyCalls++
			return pep.Decision{Verdict: pep.VerdictAllow, ReasonCode: "phase-1-audit-export"}, nil
		}),
		Auditor: pep.AuditFunc(func(context.Context, pep.AuditEvent) error { return nil }),
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := mustAuditExportHandler(t, verifier, policyAuditExporterFunc(func(context.Context, tenancy.Scope) (auditrecord.Export, error) {
		t.Fatal("malformed request reached exporter")
		return auditrecord.Export{}, nil
	}), enforcer)
	for _, test := range []struct {
		name   string
		method string
		target string
		status int
	}{
		{name: "method", method: http.MethodPost, target: "/v1/workspaces/workspace-a/audit/export", status: http.StatusMethodNotAllowed},
		{name: "query", method: http.MethodGet, target: "/v1/workspaces/workspace-a/audit/export?after=1", status: http.StatusNotFound},
		{name: "page extra query", method: http.MethodGet, target: "/v1/workspaces/workspace-a/audit/export/pages?cursor=a&limit=1", status: http.StatusNotFound},
		{name: "page escaped cursor", method: http.MethodGet, target: "/v1/workspaces/workspace-a/audit/export/pages?cursor=%41", status: http.StatusNotFound},
		{name: "page trailing slash", method: http.MethodGet, target: "/v1/workspaces/workspace-a/audit/export/pages/", status: http.StatusNotFound},
		{name: "trailing slash", method: http.MethodGet, target: "/v1/workspaces/workspace-a/audit/export/", status: http.StatusNotFound},
		{name: "encoded workspace", method: http.MethodGet, target: "/v1/workspaces/workspace%2Da/audit/export", status: http.StatusNotFound},
		{name: "extra resource", method: http.MethodGet, target: "/v1/workspaces/workspace-a/audit/export/all", status: http.StatusNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, authenticatedAuditExportRequest(t, test.method, test.target, privateKey, now, tenancy.RoleAdmin))
			if response.Code != test.status {
				t.Fatalf("status/body = %d/%q, want %d", response.Code, response.Body.String(), test.status)
			}
		})
	}
	for _, body := range []struct {
		name             string
		contentLength    int64
		transferEncoding []string
		body             io.ReadCloser
	}{
		{name: "content", contentLength: 12, body: io.NopCloser(strings.NewReader("token=secret"))},
		{name: "unknown length", contentLength: -1, body: io.NopCloser(strings.NewReader("token=secret"))},
		{name: "chunked", transferEncoding: []string{"chunked"}, body: io.NopCloser(strings.NewReader("token=secret"))},
		{name: "zero-length custom body", body: io.NopCloser(strings.NewReader(""))},
	} {
		t.Run(body.name, func(t *testing.T) {
			request := authenticatedAuditExportRequest(t, http.MethodGet, "/v1/workspaces/workspace-a/audit/export", privateKey, now, tenancy.RoleAdmin)
			request.ContentLength = body.contentLength
			request.TransferEncoding = body.transferEncoding
			request.Body = body.body
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusNotFound {
				t.Fatalf("body-framed status/body = %d/%q", response.Code, response.Body.String())
			}
		})
	}
	cookieRequest := authenticatedAuditExportRequest(
		t, http.MethodGet, "/v1/workspaces/workspace-a/audit/export/pages", privateKey, now, tenancy.RoleAdmin,
	)
	cookieRequest.AddCookie(&http.Cookie{Name: "__Host-sith-session", Value: "ignored-browser-session"})
	cookieResponse := httptest.NewRecorder()
	handler.ServeHTTP(cookieResponse, cookieRequest)
	if cookieResponse.Code != http.StatusNotFound {
		t.Fatalf("page cookie status/body = %d/%q", cookieResponse.Code, cookieResponse.Body.String())
	}
	if policyCalls != 0 {
		t.Fatalf("malformed requests reached policy %d times", policyCalls)
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "https://hub.sith.test/v1/workspaces/workspace-a/audit/export", nil))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status/body = %d/%q", response.Code, response.Body.String())
	}
}

func TestAuditExportHandlerBoundsConcurrentDatabaseWork(t *testing.T) {
	now := time.Date(2026, time.July, 18, 9, 30, 0, 0, time.UTC)
	verifier, privateKey := fleetTestVerifier(t, now)
	entered := make(chan struct{}, maxConcurrentAuditExports)
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseAll := func() { releaseOnce.Do(func() { close(release) }) }
	handler := mustAuditExportHandler(t, verifier, policyAuditExporterFunc(func(context.Context, tenancy.Scope) (auditrecord.Export, error) {
		entered <- struct{}{}
		<-release
		return testPolicyAuditExport(now), nil
	}), fleetTestPEP(t, pep.AllowReadHook{}))

	var workers sync.WaitGroup
	t.Cleanup(func() {
		releaseAll()
		workers.Wait()
	})
	for range maxConcurrentAuditExports {
		workers.Add(1)
		go func() {
			defer workers.Done()
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, authenticatedAuditExportRequest(t, http.MethodGet, "/v1/workspaces/workspace-a/audit/export", privateKey, now, tenancy.RoleAdmin))
			if response.Code != http.StatusOK {
				t.Errorf("worker status/body = %d/%q", response.Code, response.Body.String())
			}
		}()
	}
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for range maxConcurrentAuditExports {
		select {
		case <-entered:
		case <-deadline.C:
			t.Fatal("timed out waiting for audit exports to enter")
		}
	}
	overflow := httptest.NewRecorder()
	handler.ServeHTTP(overflow, authenticatedAuditExportRequest(t, http.MethodGet, "/v1/workspaces/workspace-a/audit/export", privateKey, now, tenancy.RoleAdmin))
	if overflow.Code != http.StatusServiceUnavailable || overflow.Header().Get("Retry-After") != "1" ||
		overflow.Body.String() != "{\"error\":\"audit_export_unavailable\"}\n" {
		t.Fatalf("overflow status/headers/body = %d/%#v/%q", overflow.Code, overflow.Header(), overflow.Body.String())
	}
	releaseAll()
	workers.Wait()
}

func TestNewAuditExportHandlerRejectsMissingDependencies(t *testing.T) {
	verifier, _ := fleetTestVerifier(t, time.Now())
	exporter := policyAuditExporterFunc(func(context.Context, tenancy.Scope) (auditrecord.Export, error) { return auditrecord.Export{}, nil })
	enforcer := fleetTestPEP(t, pep.AllowReadHook{})
	for _, config := range []AuditExportHandlerConfig{
		{},
		{Verifier: verifier, Exporter: exporter},
		{Verifier: verifier, PEP: enforcer},
		{Exporter: exporter, PEP: enforcer},
	} {
		if _, err := NewAuditExportHandler(config); err == nil {
			t.Fatalf("unsafe config accepted: %#v", config)
		}
	}
}

func TestAuditExportHandlerHasNoStorageConnectorOrExecutionCapability(t *testing.T) {
	t.Parallel()
	parsed, err := parser.ParseFile(token.NewFileSet(), "audit_export.go", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	for _, imported := range parsed.Imports {
		path := strings.Trim(imported.Path.Value, `"`)
		for _, forbidden := range []string{
			"os", "os/exec", "path", "path/filepath", "/hubdb", "/hubfleet", "/connector", "/localops",
			"k8s.io/", "google.golang.org/grpc",
		} {
			if path == forbidden || strings.Contains(path, forbidden) {
				t.Fatalf("audit export handler imports forbidden capability %q", path)
			}
		}
	}
}

func mustAuditExportHandler(t *testing.T, verifier Verifier, exporter PolicyAuditExporter, enforcer *pep.Enforcer) http.Handler {
	t.Helper()
	handler, err := NewAuditExportHandler(AuditExportHandlerConfig{Verifier: verifier, Exporter: exporter, PEP: enforcer})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func authenticatedAuditExportRequest(
	t *testing.T,
	method, target string,
	privateKey ed25519.PrivateKey,
	now time.Time,
	role tenancy.Role,
) *http.Request {
	t.Helper()
	claims := hubValidClaims(now)
	claims.Memberships["workspace-a"] = role
	request := httptest.NewRequest(method, "https://hub.sith.test"+target, nil)
	request.Header.Set("Authorization", "Bearer "+signHubTestToken(t, claims, privateKey))
	return request
}

func testPolicyAuditExport(now time.Time) auditrecord.Export {
	exported := auditrecord.Export{
		Schema: auditrecord.SchemaV1, WorkspaceID: "workspace-a",
		Chain: auditrecord.Chain{HashAlgorithm: auditrecord.HashAlgorithm, HeadSequence: 1},
		Entries: []auditrecord.Entry{{
			Sequence: 1, FormatVersion: 1, RecordedAt: now, TraceID: strings.Repeat("1", 32), Actor: "user:alice",
			Role: "admin", Action: "export-audit", Verb: "audit.export", Verdict: "allow",
			ReasonCode: "phase-1-audit-export", EventKind: "policy-decision", PreviousHash: "sha256:" + strings.Repeat("0", 64),
		}},
	}
	hash, err := auditrecord.RecomputeEntryHash("workspace-a", exported.Entries[0])
	if err != nil {
		panic(err)
	}
	exported.Entries[0].EntryHash = hash
	exported.Chain.HeadHash = hash
	return exported
}

func testPolicyAuditPage(now time.Time, sequence int64, previousHash string) auditrecord.Page {
	entry := auditrecord.Entry{
		Sequence: sequence, FormatVersion: 1, RecordedAt: now, TraceID: strings.Repeat("1", 32), Actor: "user:alice",
		Role: "admin", Action: "export-audit", Verb: "audit.export", Verdict: "allow",
		ReasonCode: "phase-1-audit-export", EventKind: "policy-decision", PreviousHash: previousHash,
	}
	hash, err := auditrecord.RecomputeEntryHash("workspace-a", entry)
	if err != nil {
		panic(err)
	}
	entry.EntryHash = hash
	return auditrecord.Page{
		Schema: auditrecord.PageSchemaV1, WorkspaceID: "workspace-a",
		Snapshot:      auditrecord.Chain{HashAlgorithm: auditrecord.HashAlgorithm, HeadSequence: sequence, HeadHash: hash},
		StartSequence: sequence, PreviousHash: previousHash, Entries: []auditrecord.Entry{entry},
	}
}
