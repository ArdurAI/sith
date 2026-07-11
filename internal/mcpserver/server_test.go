// SPDX-License-Identifier: Apache-2.0

package mcpserver

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ArdurAI/sith/internal/connector"
	"github.com/ArdurAI/sith/internal/fleet"
	"github.com/ArdurAI/sith/internal/fleetcache"
)

func TestMCPToolsAreReadOnlyScopedAndAudited(t *testing.T) {
	t.Parallel()
	store := mcpStore(t)
	auditor := &recordingAuditor{}
	server, endpoint := startMCPTestServer(t, store, Config{Version: "test", Auditor: auditor})
	session := connectMCPClient(t, endpoint, nil)
	defer func() { _ = session.Close() }()

	listed, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	wantNames := []string{"fleet.correlate", "fleet.cve-search", "fleet.health", "fleet.inventory"}
	names := make([]string, 0, len(listed.Tools))
	for _, tool := range listed.Tools {
		names = append(names, tool.Name)
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint || !tool.Annotations.IdempotentHint ||
			tool.Annotations.DestructiveHint == nil || *tool.Annotations.DestructiveHint ||
			tool.Annotations.OpenWorldHint == nil || *tool.Annotations.OpenWorldHint {
			t.Errorf("tool %s annotations = %#v", tool.Name, tool.Annotations)
		}
	}
	slices.Sort(names)
	if !slices.Equal(names, wantNames) {
		t.Fatalf("tool names = %v", names)
	}

	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "fleet.inventory", Arguments: map[string]any{"kind": "Pod"},
	})
	if err != nil {
		t.Fatal(err)
	}
	output := decodeToolOutput(t, result)
	if output.Workspace != fleet.LocalWorkspace || len(output.Snapshot.Records) != 1 ||
		output.Snapshot.Records[0].Name != "local-api" {
		t.Fatalf("inventory output = %#v", output)
	}
	for _, record := range output.Snapshot.Records {
		if record.Workspace != fleet.LocalWorkspace {
			t.Fatalf("cross-workspace record leaked: %#v", record)
		}
	}
	if len(output.Snapshot.Scopes) != 1 || output.Snapshot.Scopes[0].Name != "alpha" {
		t.Fatalf("cross-workspace scope metadata leaked: %#v", output.Snapshot.Scopes)
	}
	events := auditor.snapshot()
	if len(events) != 1 || !events[0].Allowed || events[0].Tool != "fleet.inventory" ||
		events[0].Workspace != fleet.LocalWorkspace || events[0].Records != 1 {
		t.Fatalf("audit events = %#v", events)
	}
	result, err = session.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "fleet.inventory", Arguments: map[string]any{"kind": "Pod", "scopes": []string{"beta"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	guessed := decodeToolOutput(t, result)
	if len(guessed.Snapshot.Records) != 0 || len(guessed.Snapshot.Scopes) != 1 ||
		guessed.Snapshot.Scopes[0].Name != "beta" || guessed.Snapshot.Scopes[0].Reachable {
		t.Fatalf("guessed cross-workspace scope leaked metadata: %#v", guessed)
	}
	server.Close()
}

func TestMCPCVESearchReturnsCanonicalFindings(t *testing.T) {
	t.Parallel()
	store := mcpStore(t)
	_, endpoint := startMCPTestServer(t, store, Config{Version: "test"})
	session := connectMCPClient(t, endpoint, nil)
	defer func() { _ = session.Close() }()
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "fleet.cve-search", Arguments: map[string]any{
			"image": "*payments*", "cve": "CVE-2026-1234",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	output := decodeToolOutput(t, result)
	if len(output.Snapshot.Records) != 1 || !slices.Equal(output.Snapshot.Records[0].CVEs, []string{"CVE-2026-1234"}) {
		t.Fatalf("CVE output = %#v", output)
	}
}

func TestMCPTokenIsRequiredAtHTTPAndExecutionBoundaries(t *testing.T) {
	t.Parallel()
	store := mcpStore(t)
	auditor := &recordingAuditor{}
	testServer := httptest.NewUnstartedServer(nil)
	endpoint := "http://" + testServer.Listener.Addr().String() + "/mcp"
	token := strings.Repeat("t", 43)
	handler, err := New(store, Config{
		Audience: endpoint, Version: "test", Token: token,
		Expiration: time.Now().Add(time.Hour), Auditor: auditor,
	})
	if err != nil {
		t.Fatal(err)
	}
	testServer.Config.Handler = handler
	testServer.Start()
	t.Cleanup(testServer.Close)

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, nil)
	if _, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{
		Endpoint: endpoint, HTTPClient: &http.Client{Transport: httpTransport(nil)},
	}, nil); err == nil {
		t.Fatal("unauthenticated MCP connection succeeded")
	}
	session := connectMCPClient(t, endpoint, map[string]string{"Authorization": "Bearer " + token})
	defer func() { _ = session.Close() }()
	if _, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "fleet.health", Arguments: map[string]any{}}); err != nil {
		t.Fatal(err)
	}

	app := &application{
		store: store, audience: endpoint, token: token, expiration: time.Now().Add(time.Hour),
		auditor: auditor, now: time.Now,
	}
	if _, _, err := app.execute(context.Background(), "fleet.inventory", fleetcache.Query{}); err == nil {
		t.Fatal("execution without authenticated context succeeded")
	}
	events := auditor.snapshot()
	if len(events) < 2 || events[len(events)-1].Allowed || events[len(events)-1].Err == "" {
		t.Fatalf("audit events = %#v", events)
	}
}

func TestMCPRejectsHostAndCrossOriginRequests(t *testing.T) {
	t.Parallel()
	_, endpoint := startMCPTestServer(t, mcpStore(t), Config{Version: "test"})
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, endpoint, strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Host = "attacker.invalid"
	response, err := (&http.Client{Transport: httpTransport(nil)}).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("host rejection status = %d", response.StatusCode)
	}

	request, err = http.NewRequestWithContext(t.Context(), http.MethodPost, endpoint, strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://attacker.invalid")
	response, err = (&http.Client{Transport: httpTransport(nil)}).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("origin rejection status/body = %d/%q", response.StatusCode, body)
	}
}

func startMCPTestServer(t *testing.T, store *fleetcache.Store, config Config) (*httptest.Server, string) {
	t.Helper()
	server := httptest.NewUnstartedServer(nil)
	endpoint := "http://" + server.Listener.Addr().String() + "/mcp"
	config.Audience = endpoint
	handler, err := New(store, config)
	if err != nil {
		t.Fatal(err)
	}
	server.Config.Handler = handler
	server.Start()
	t.Cleanup(server.Close)
	return server, endpoint
}

func connectMCPClient(t *testing.T, endpoint string, headers map[string]string) *mcp.ClientSession {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, nil)
	session, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{
		Endpoint:   endpoint,
		HTTPClient: &http.Client{Transport: httpTransport(headers), Timeout: 5 * time.Second},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return session
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (function roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func httpTransport(headers map[string]string) http.RoundTripper {
	base := &http.Transport{Proxy: nil}
	return roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		clone := request.Clone(request.Context())
		for name, value := range headers {
			clone.Header.Set(name, value)
		}
		return base.RoundTrip(clone)
	})
}

func decodeToolOutput(t *testing.T, result *mcp.CallToolResult) toolOutput {
	t.Helper()
	payload, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var output toolOutput
	if err := json.Unmarshal(payload, &output); err != nil {
		t.Fatal(err)
	}
	return output
}

func mcpStore(t *testing.T) *fleetcache.Store {
	t.Helper()
	now := time.Now().UTC()
	store := fleetcache.New()
	store.SetDiscovery(fleet.LocalWorkspace, connector.Discovery{Scopes: []connector.Scope{
		{Name: "alpha", Reachable: true, ObservedAt: now},
	}})
	store.SetDiscovery("other", connector.Discovery{Scopes: []connector.Scope{
		{Name: "beta", Reachable: true, ObservedAt: now},
	}})
	local := mcpPodFact(t, fleet.LocalWorkspace, "alpha", "local-api", now)
	other := mcpPodFact(t, "other", "beta", "other-api", now)
	if err := store.Replace("Pod", fleet.QueryResult{
		Facts: []fleet.Fact{local, other}, Coverage: fleet.Coverage{Requested: 2, Reachable: 2},
	}); err != nil {
		t.Fatal(err)
	}
	cvePayload, err := json.Marshal(fleet.CVEObservation{
		Image: "registry.example/payments:v4", IDs: []string{"CVE-2026-1234"}, Severity: "high",
	})
	if err != nil {
		t.Fatal(err)
	}
	cve := fleet.Fact{Evidence: fleet.Evidence{
		Ref:  fleet.ResourceRef{SourceKind: "scanner", Scope: "alpha", Kind: "Image", Name: "payments-v4"},
		Kind: fleet.FactCVE, Observed: cvePayload, ObservedAt: now, Source: "scanner",
	}, Workspace: fleet.LocalWorkspace}
	if err := store.Replace("CVE", fleet.QueryResult{
		Facts: []fleet.Fact{cve}, Coverage: fleet.Coverage{Requested: 2, Reachable: 2},
	}); err != nil {
		t.Fatal(err)
	}
	return store
}

func mcpPodFact(t *testing.T, workspace, scope, name string, observed time.Time) fleet.Fact {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"apiVersion": "v1", "kind": "Pod",
		"metadata": map[string]any{"name": name, "namespace": "apps"},
		"spec":     map[string]any{"containers": []any{map[string]any{"image": "registry.example/api:v1"}}},
		"status":   map[string]any{"phase": "Running"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return fleet.Fact{Evidence: fleet.Evidence{
		Ref:  fleet.ResourceRef{SourceKind: "test", Scope: scope, Kind: "Pod", Namespace: "apps", Name: name},
		Kind: fleet.FactInventory, Observed: payload, ObservedAt: observed, Source: scope,
	}, Workspace: workspace}
}

type recordingAuditor struct {
	mu     sync.Mutex
	events []AuditEvent
}

func (auditor *recordingAuditor) Record(_ context.Context, event AuditEvent) {
	auditor.mu.Lock()
	defer auditor.mu.Unlock()
	auditor.events = append(auditor.events, event)
}

func (auditor *recordingAuditor) snapshot() []AuditEvent {
	auditor.mu.Lock()
	defer auditor.mu.Unlock()
	return append([]AuditEvent(nil), auditor.events...)
}
