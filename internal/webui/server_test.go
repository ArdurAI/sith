// SPDX-License-Identifier: Apache-2.0

package webui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ArdurAI/sith/internal/connector"
	"github.com/ArdurAI/sith/internal/fleet"
	"github.com/ArdurAI/sith/internal/fleetcache"
	"github.com/ArdurAI/sith/internal/localops"
)

const testOrigin = "http://127.0.0.1:7407"

func TestValidateLoopbackAddress(t *testing.T) {
	t.Parallel()
	for _, address := range []string{"127.0.0.1", "::1", "localhost"} {
		if err := ValidateLoopbackAddress(address); err != nil {
			t.Errorf("ValidateLoopbackAddress(%q) error = %v", address, err)
		}
	}
	for _, address := range []string{"0.0.0.0", "::", "192.0.2.10", "example.com", ""} {
		if err := ValidateLoopbackAddress(address); err == nil {
			t.Errorf("ValidateLoopbackAddress(%q) error = nil", address)
		}
	}
}

func TestHandlerEnforcesHostOriginCapabilityAndSecurityHeaders(t *testing.T) {
	t.Parallel()
	application := testApplication(t)
	handler := testHandler(t, application)

	index := serve(handler, http.MethodGet, "/", "", nil)
	if index.Code != http.StatusOK || !strings.Contains(index.Body.String(), application.token) ||
		strings.Contains(index.Body.String(), "__SITH_CSRF_TOKEN__") {
		t.Fatalf("index status/body = %d/%q", index.Code, index.Body.String())
	}
	for _, header := range []string{"Content-Security-Policy", "Permissions-Policy", "X-Frame-Options", "X-Content-Type-Options"} {
		if index.Header().Get(header) == "" {
			t.Errorf("index missing %s", header)
		}
	}
	asset := serve(handler, http.MethodGet, "/assets/app.js", "", nil)
	if asset.Code != http.StatusOK || strings.Contains(asset.Body.String(), "https://") {
		t.Fatalf("asset status/external reference = %d/%t", asset.Code, strings.Contains(asset.Body.String(), "https://"))
	}

	missing := serve(handler, http.MethodGet, "/api/v1/meta", "", nil)
	if missing.Code != http.StatusForbidden {
		t.Fatalf("missing capability status = %d", missing.Code)
	}
	request := httptest.NewRequest(http.MethodGet, testOrigin+"/api/v1/meta", nil)
	request.Host = "attacker.invalid"
	request.Header.Set(csrfHeader, application.token)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("wrong host status = %d", recorder.Code)
	}
	wrongOriginHeaders := http.Header{"Origin": []string{"https://attacker.invalid"}}
	wrongOrigin := serve(handler, http.MethodGet, "/api/v1/meta", application.token, wrongOriginHeaders)
	if wrongOrigin.Code != http.StatusForbidden {
		t.Fatalf("wrong origin status = %d", wrongOrigin.Code)
	}
	meta := serve(handler, http.MethodGet, "/api/v1/meta", application.token, nil)
	if meta.Code != http.StatusOK {
		t.Fatalf("meta status/body = %d/%s", meta.Code, meta.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(meta.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode meta: %v", err)
	}
	if payload["mode"] != "local" || payload["account_required"] != false || payload["telemetry"] != false {
		t.Fatalf("meta = %#v", payload)
	}
}

func TestHandlerAllowsOnlyTheExplicitDesktopOrigin(t *testing.T) {
	t.Parallel()
	application := testApplication(t)
	handler, err := application.Handler(DesktopOrigin)
	if err != nil {
		t.Fatalf("Handler(%q) error = %v", DesktopOrigin, err)
	}
	request := httptest.NewRequest(http.MethodGet, DesktopOrigin+"/api/v1/meta", nil)
	request.Host = "wails"
	request.Header.Set(csrfHeader, application.token)
	request.Header.Set("Origin", DesktopOrigin)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("desktop meta status/body = %d/%s", recorder.Code, recorder.Body.String())
	}
	for _, origin := range []string{"https://wails.localhost", "http://example.com", "wails://attacker"} {
		if _, err := application.Handler(origin); err == nil {
			t.Errorf("Handler(%q) error = nil", origin)
		}
	}
}

func TestDesktopFolderBridgeIsOptInAndDoesNotExposePaths(t *testing.T) {
	t.Parallel()
	index, err := fs.ReadFile(embeddedAssets, "assets/index.html")
	if err != nil {
		t.Fatal(err)
	}
	script, err := fs.ReadFile(embeddedAssets, "assets/app.js")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(index), `id="import-folder-button" class="quiet-action" type="button" hidden`) {
		t.Fatal("desktop import control is not hidden by default")
	}
	if !strings.Contains(string(script), "window.go?.cli?.DesktopBridge?.ChooseKubeconfigDirectory") ||
		!strings.Contains(string(script), "if (await directoryPicker()) window.location.reload()") {
		t.Fatal("desktop bridge is not opt-in or does not reload after a successful source swap")
	}
	if strings.Contains(string(script), "selectedDirectory") || strings.Contains(string(script), "kubeconfigDir") {
		t.Fatal("desktop bridge must not retain a selected local path in the UI")
	}
}

func TestSnapshotReadsCacheOnlyAndRefreshIsExplicit(t *testing.T) {
	t.Parallel()
	syncer := &webSyncer{}
	store := populatedWebStore(t)
	application, err := New(t.Context(), store, syncer, &webLocalClient{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = application.Close() })
	handler := testHandler(t, application)
	snapshot := serve(handler, http.MethodGet, "/api/v1/snapshot?kind=Pod&q=status:Running", application.token, nil)
	if snapshot.Code != http.StatusOK || syncer.calls.Load() != 0 {
		t.Fatalf("snapshot status/sync calls = %d/%d", snapshot.Code, syncer.calls.Load())
	}
	var decoded fleetcache.Snapshot
	if err := json.Unmarshal(snapshot.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Records) != 1 || decoded.Records[0].Cluster != "alpha" {
		t.Fatalf("snapshot = %#v", decoded)
	}
	refresh := serveBody(handler, http.MethodPost, "/api/v1/sync", application.token, `{}`)
	if refresh.Code != http.StatusAccepted {
		t.Fatalf("refresh status/body = %d/%s", refresh.Code, refresh.Body.String())
	}
	deadline := time.Now().Add(time.Second)
	for syncer.calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if syncer.calls.Load() != 1 {
		t.Fatalf("sync calls = %d", syncer.calls.Load())
	}
}

func TestSnapshotExposesOnlySafeImportMetadata(t *testing.T) {
	t.Parallel()
	store := populatedWebStore(t)
	store.SetDiscovery(fleet.LocalWorkspace, connector.Discovery{
		Scopes: []connector.Scope{{
			Name: "import-123/context/prod", DisplayName: "prod", Origin: "team-a.yaml", Reachable: true, ObservedAt: time.Now().UTC(),
		}},
		Diagnostics: []connector.Diagnostic{{Source: "broken.yaml", Message: "invalid kubeconfig"}},
	})
	application, err := New(t.Context(), store, &webSyncer{}, &webLocalClient{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = application.Close() })
	response := serve(testHandler(t, application), http.MethodGet, "/api/v1/snapshot", application.token, nil)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "team-a.yaml") ||
		!strings.Contains(response.Body.String(), "broken.yaml") || strings.Contains(response.Body.String(), "/Users/") {
		t.Fatalf("snapshot status/body = %d/%s", response.Code, response.Body.String())
	}
}

func TestRefreshRequestsAreSingleFlight(t *testing.T) {
	t.Parallel()
	syncer := &blockingWebSyncer{started: make(chan struct{}), release: make(chan struct{})}
	application, err := New(t.Context(), populatedWebStore(t), syncer, &webLocalClient{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = application.Close() })
	handler := testHandler(t, application)
	first := serveBody(handler, http.MethodPost, "/api/v1/sync", application.token, `{}`)
	if first.Code != http.StatusAccepted {
		t.Fatalf("first refresh status/body = %d/%s", first.Code, first.Body.String())
	}
	select {
	case <-syncer.started:
	case <-time.After(time.Second):
		t.Fatal("first refresh did not start")
	}
	second := serveBody(handler, http.MethodPost, "/api/v1/sync", application.token, `{}`)
	if second.Code != http.StatusAccepted || !strings.Contains(second.Body.String(), "already running") ||
		syncer.calls.Load() != 1 {
		t.Fatalf("coalesced refresh status/body/calls = %d/%s/%d", second.Code, second.Body.String(), syncer.calls.Load())
	}
	close(syncer.release)
}

func TestLocalOperationAPIUsesExactTargetAndPreviewBeforeApply(t *testing.T) {
	t.Parallel()
	client := &webLocalClient{
		view:    localops.ObjectView{YAML: []byte("kind: Pod\nmetadata:\n  name: api\n")},
		preview: localops.ApplyPreview{CurrentYAML: []byte("mode: old\n"), DryRunYAML: []byte("mode: new\n")},
	}
	application := testApplicationWithLocal(t, client)
	handler := testHandler(t, application)
	query := "context=alpha&namespace=apps&kind=Pod&name=api"
	object := serve(handler, http.MethodGet, "/api/v1/object?"+query, application.token, nil)
	if object.Code != http.StatusOK || !strings.Contains(object.Body.String(), "kind: Pod") || client.target.Context != "alpha" {
		t.Fatalf("object status/body/target = %d/%s/%#v", object.Code, object.Body.String(), client.target)
	}
	logs := serve(handler, http.MethodGet, "/api/v1/logs?"+query, application.token, nil)
	if logs.Code != http.StatusOK || logs.Body.String() != "alpha logs\n" {
		t.Fatalf("logs status/body = %d/%q", logs.Code, logs.Body.String())
	}
	execBody := `{"target":{"context":"alpha","namespace":"apps","kind":"Pod","name":"api"},"command":["printf","%s","$(literal)"]}`
	executed := serveBody(handler, http.MethodPost, "/api/v1/exec", application.token, execBody)
	if executed.Code != http.StatusOK || !slices.Equal(client.command, []string{"printf", "%s", "$(literal)"}) {
		t.Fatalf("exec status/body/argv = %d/%s/%q", executed.Code, executed.Body.String(), client.command)
	}
	editPayload := editRequest{
		Target:   localops.Target{Context: "alpha", Namespace: "apps", Kind: "ConfigMap", Name: "settings"},
		Manifest: "mode: new\n",
	}
	editBody, _ := json.Marshal(editPayload)
	preview := serveBody(handler, http.MethodPost, "/api/v1/edit/preview", application.token, string(editBody))
	if preview.Code != http.StatusOK || !strings.Contains(preview.Body.String(), "server dry-run") {
		t.Fatalf("preview status/body = %d/%s", preview.Code, preview.Body.String())
	}
	withoutPreview := serveBody(handler, http.MethodPost, "/api/v1/edit/apply", application.token, string(editBody))
	if withoutPreview.Code != http.StatusConflict {
		t.Fatalf("apply without preview status/body = %d/%s", withoutPreview.Code, withoutPreview.Body.String())
	}
	var grant editPreviewResponse
	if err := json.Unmarshal(preview.Body.Bytes(), &grant); err != nil || grant.PreviewToken == "" {
		t.Fatalf("decode preview grant: %#v/%v", grant, err)
	}
	editPayload.PreviewToken = grant.PreviewToken
	applyBody, _ := json.Marshal(editPayload)
	applied := serveBody(handler, http.MethodPost, "/api/v1/edit/apply", application.token, string(applyBody))
	if applied.Code != http.StatusOK || !slices.Equal(client.order, []string{"view", "logs", "exec", "preview", "apply"}) {
		t.Fatalf("apply status/order = %d/%v", applied.Code, client.order)
	}
}

func TestPortForwardAPIOwnsAndClosesSession(t *testing.T) {
	t.Parallel()
	client := &webLocalClient{}
	application := testApplicationWithLocal(t, client)
	handler := testHandler(t, application)
	body := `{"target":{"context":"alpha","namespace":"apps","kind":"Service","name":"api"},"ports":[":http"]}`
	started := serveBody(handler, http.MethodPost, "/api/v1/port-forwards", application.token, body)
	if started.Code != http.StatusCreated {
		t.Fatalf("start status/body = %d/%s", started.Code, started.Body.String())
	}
	var forward forwardView
	if err := json.Unmarshal(started.Body.Bytes(), &forward); err != nil {
		t.Fatal(err)
	}
	listed := serve(handler, http.MethodGet, "/api/v1/port-forwards", application.token, nil)
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), forward.ID) {
		t.Fatalf("list status/body = %d/%s", listed.Code, listed.Body.String())
	}
	closed := serve(handler, http.MethodDelete, "/api/v1/port-forwards/"+forward.ID, application.token, nil)
	if closed.Code != http.StatusNoContent || client.session == nil || !client.session.closed.Load() {
		t.Fatalf("close status/session = %d/%#v", closed.Code, client.session)
	}
}

func TestPortForwardAPILimitsActiveSessions(t *testing.T) {
	t.Parallel()
	client := &webLocalClient{}
	application := testApplicationWithLocal(t, client)
	handler := testHandler(t, application)
	var first forwardView
	for index := range maxActiveForwards {
		body := fmt.Sprintf(
			`{"target":{"context":"alpha","namespace":"apps","kind":"Service","name":"api-%d"},"ports":[":http"]}`,
			index,
		)
		started := serveBody(handler, http.MethodPost, "/api/v1/port-forwards", application.token, body)
		if started.Code != http.StatusCreated {
			t.Fatalf("start forward %d status/body = %d/%s", index, started.Code, started.Body.String())
		}
		if index == 0 {
			if err := json.Unmarshal(started.Body.Bytes(), &first); err != nil {
				t.Fatal(err)
			}
		}
	}
	overLimit := serveBody(
		handler,
		http.MethodPost,
		"/api/v1/port-forwards",
		application.token,
		`{"target":{"context":"alpha","namespace":"apps","kind":"Service","name":"overflow"},"ports":[":http"]}`,
	)
	if overLimit.Code != http.StatusTooManyRequests || !strings.Contains(overLimit.Body.String(), "at most 16") ||
		len(client.order) != maxActiveForwards {
		t.Fatalf("over-limit status/body/operations = %d/%s/%d", overLimit.Code, overLimit.Body.String(), len(client.order))
	}
	closed := serve(handler, http.MethodDelete, "/api/v1/port-forwards/"+first.ID, application.token, nil)
	if closed.Code != http.StatusNoContent {
		t.Fatalf("close first forward status = %d", closed.Code)
	}
	replacement := serveBody(
		handler,
		http.MethodPost,
		"/api/v1/port-forwards",
		application.token,
		`{"target":{"context":"alpha","namespace":"apps","kind":"Service","name":"replacement"},"ports":[":http"]}`,
	)
	if replacement.Code != http.StatusCreated {
		t.Fatalf("replacement forward status/body = %d/%s", replacement.Code, replacement.Body.String())
	}
}

func testApplication(t *testing.T) *Application {
	t.Helper()
	return testApplicationWithLocal(t, &webLocalClient{})
}

func testApplicationWithLocal(t *testing.T, local localops.Client) *Application {
	t.Helper()
	application, err := New(t.Context(), populatedWebStore(t), &webSyncer{}, local)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = application.Close() })
	return application
}

func testHandler(t *testing.T, application *Application) http.Handler {
	t.Helper()
	handler, err := application.Handler(testOrigin)
	if err != nil {
		t.Fatalf("Handler() error = %v", err)
	}
	return handler
}

func serve(handler http.Handler, method, path, token string, headers http.Header) *httptest.ResponseRecorder {
	return serveRequest(handler, method, path, token, headers, nil)
}

func serveBody(handler http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	return serveRequest(handler, method, path, token, nil, strings.NewReader(body))
}

func serveRequest(
	handler http.Handler,
	method, path, token string,
	headers http.Header,
	body io.Reader,
) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, testOrigin+path, body)
	request.Host = "127.0.0.1:7407"
	if token != "" {
		request.Header.Set(csrfHeader, token)
	}
	for name, values := range headers {
		request.Header[name] = append([]string(nil), values...)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func populatedWebStore(t *testing.T) *fleetcache.Store {
	t.Helper()
	now := time.Now().UTC()
	store := fleetcache.New()
	store.SetDiscovery(fleet.LocalWorkspace, connector.Discovery{Scopes: []connector.Scope{{Name: "alpha", Reachable: true, ObservedAt: now}}})
	observed := json.RawMessage(`{"apiVersion":"v1","kind":"Pod","metadata":{"name":"api","namespace":"apps"},"status":{"phase":"Running"}}`)
	err := store.Replace("Pod", fleet.QueryResult{
		Facts: []fleet.Fact{{Evidence: fleet.Evidence{
			Ref:  fleet.ResourceRef{SourceKind: "test", Scope: "alpha", Kind: "Pod", Namespace: "apps", Name: "api"},
			Kind: fleet.FactInventory, Observed: observed, ObservedAt: now,
		}, Workspace: fleet.LocalWorkspace}},
		Coverage: fleet.Coverage{Requested: 1, Reachable: 1},
	})
	if err != nil {
		t.Fatalf("populate store: %v", err)
	}
	return store
}

type webSyncer struct{ calls atomic.Int32 }

func (syncer *webSyncer) SyncOnce(context.Context) error {
	syncer.calls.Add(1)
	return nil
}
func (syncer *webSyncer) SyncKinds(context.Context, ...string) error {
	syncer.calls.Add(1)
	return nil
}

type blockingWebSyncer struct {
	calls   atomic.Int32
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (syncer *blockingWebSyncer) SyncOnce(ctx context.Context) error {
	return syncer.run(ctx)
}

func (syncer *blockingWebSyncer) SyncKinds(ctx context.Context, _ ...string) error {
	return syncer.run(ctx)
}

func (syncer *blockingWebSyncer) run(ctx context.Context) error {
	syncer.calls.Add(1)
	syncer.once.Do(func() { close(syncer.started) })
	select {
	case <-syncer.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type webLocalClient struct {
	mu      sync.Mutex
	target  localops.Target
	view    localops.ObjectView
	preview localops.ApplyPreview
	command []string
	order   []string
	session *webForwardSession
}

func (client *webLocalClient) record(target localops.Target, operation string) {
	client.mu.Lock()
	defer client.mu.Unlock()
	client.target = target
	client.order = append(client.order, operation)
}

func (client *webLocalClient) View(_ context.Context, target localops.Target, _ bool) (localops.ObjectView, error) {
	client.record(target, "view")
	return client.view, nil
}
func (client *webLocalClient) Describe(_ context.Context, target localops.Target) (localops.Description, error) {
	client.record(target, "describe")
	return localops.Description{Object: client.view}, nil
}
func (client *webLocalClient) Logs(_ context.Context, target localops.Target, _ localops.LogOptions) (io.ReadCloser, error) {
	client.record(target, "logs")
	return io.NopCloser(strings.NewReader("alpha logs\n")), nil
}
func (client *webLocalClient) Exec(_ context.Context, target localops.Target, options localops.ExecOptions, streams localops.Streams) error {
	client.record(target, "exec")
	client.command = append([]string(nil), options.Command...)
	_, _ = io.WriteString(streams.Stdout, "command output\n")
	return nil
}
func (client *webLocalClient) PortForward(_ context.Context, request localops.ForwardRequest) (localops.ForwardSession, error) {
	client.record(request.Target, "forward")
	ready := make(chan struct{})
	close(ready)
	client.session = &webForwardSession{ready: ready, done: make(chan error, 1)}
	return client.session, nil
}
func (client *webLocalClient) PreviewApply(_ context.Context, target localops.Target, _ []byte) (localops.ApplyPreview, error) {
	client.record(target, "preview")
	return client.preview, nil
}
func (client *webLocalClient) Apply(_ context.Context, target localops.Target, _ []byte) (fleet.Evidence, error) {
	client.record(target, "apply")
	return fleet.Evidence{Ref: fleet.ResourceRef{Scope: target.Context, Kind: target.Kind, Name: target.Name}}, nil
}

type webForwardSession struct {
	ready  <-chan struct{}
	done   chan error
	closed atomic.Bool
	once   sync.Once
}

func (session *webForwardSession) Ready() <-chan struct{} { return session.ready }
func (session *webForwardSession) Done() <-chan error     { return session.done }
func (*webForwardSession) Ports() ([]localops.ForwardedPort, error) {
	return []localops.ForwardedPort{{Local: 18080, Remote: 8080}}, nil
}
func (session *webForwardSession) Close() error {
	session.once.Do(func() {
		session.closed.Store(true)
		session.done <- nil
	})
	return nil
}
