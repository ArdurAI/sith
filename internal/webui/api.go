// SPDX-License-Identifier: Apache-2.0

package webui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/pmezard/go-difflib/difflib"

	"github.com/ArdurAI/sith/internal/fleetcache"
	"github.com/ArdurAI/sith/internal/hydrate"
	"github.com/ArdurAI/sith/internal/localops"
)

const (
	maxAPIRequestBytes = 10 << 20
	maxExecOutputBytes = 1 << 20
	maxExecArguments   = 64
	maxForwardPorts    = 16
)

func (application *Application) registerAPI(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/meta", application.handleMeta)
	mux.HandleFunc("GET /api/v1/snapshot", application.handleSnapshot)
	mux.HandleFunc("POST /api/v1/sync", application.handleSync)
	mux.HandleFunc("GET /api/v1/object", application.handleObject)
	mux.HandleFunc("GET /api/v1/logs", application.handleLogs)
	mux.HandleFunc("POST /api/v1/exec", application.handleExec)
	mux.HandleFunc("POST /api/v1/edit/preview", application.handleEditPreview)
	mux.HandleFunc("POST /api/v1/edit/apply", application.handleEditApply)
	mux.HandleFunc("GET /api/v1/port-forwards", application.handleListForwards)
	mux.HandleFunc("POST /api/v1/port-forwards", application.handleStartForward)
	mux.HandleFunc("DELETE /api/v1/port-forwards/{id}", application.handleDeleteForward)
}

func (application *Application) handleMeta(response http.ResponseWriter, _ *http.Request) {
	writeJSON(response, http.StatusOK, map[string]any{
		"mode": application.mode, "account_required": false, "telemetry": false,
		"lenses":     hydrate.TierOneKinds(),
		"operations": []string{"describe", "yaml", "logs", "exec", "port-forward", "edit"},
	})
}

func (application *Application) handleSnapshot(response http.ResponseWriter, request *http.Request) {
	query := fleetcache.Query{
		Kind:  request.URL.Query().Get("kind"),
		Limit: 5000,
	}
	if scopes := splitNonEmpty(request.URL.Query().Get("scopes"), ","); len(scopes) > 0 {
		query.Scopes = scopes
	}
	expression := strings.TrimSpace(request.URL.Query().Get("q"))
	if expression != "" {
		var parsed fleetcache.Query
		var err error
		if request.URL.Query().Get("correlate") == "true" {
			parsed, err = fleetcache.ParseCorrelation(expression)
		} else {
			parsed, err = fleetcache.ParseSearch(expression)
		}
		if err != nil {
			writeAPIError(response, http.StatusBadRequest, err.Error())
			return
		}
		if request.URL.Query().Get("all") != "true" && parsed.Kind == "" {
			parsed.Kind = query.Kind
		}
		if len(query.Scopes) > 0 {
			parsed.Scopes = query.Scopes
		}
		parsed.Limit = query.Limit
		query = parsed
	}
	writeJSON(response, http.StatusOK, application.store.Query(query))
}

type syncRequest struct {
	Kinds []string `json:"kinds"`
}

func (application *Application) handleSync(response http.ResponseWriter, request *http.Request) {
	payload := syncRequest{}
	if request.ContentLength != 0 {
		if err := decodeJSON(response, request, 64<<10, &payload); err != nil {
			writeAPIError(response, http.StatusBadRequest, err.Error())
			return
		}
	}
	kinds := append([]string(nil), payload.Kinds...)
	if !application.refreshing.CompareAndSwap(false, true) {
		writeJSON(response, http.StatusAccepted, map[string]string{"status": "refresh already running"})
		return
	}
	go func() {
		defer application.refreshing.Store(false)
		if len(kinds) == 0 {
			_ = application.syncer.SyncOnce(application.ctx)
			return
		}
		_ = application.syncer.SyncKinds(application.ctx, kinds...)
	}()
	writeJSON(response, http.StatusAccepted, map[string]string{"status": "refresh scheduled"})
}

type objectResponse struct {
	Target localops.Target   `json:"target"`
	YAML   string            `json:"yaml"`
	Events []json.RawMessage `json:"events"`
}

func (application *Application) handleObject(response http.ResponseWriter, request *http.Request) {
	target, err := targetFromQuery(request)
	if err != nil {
		writeAPIError(response, http.StatusBadRequest, err.Error())
		return
	}
	if request.URL.Query().Get("describe") == "true" {
		description, err := application.local.Describe(request.Context(), target)
		if err != nil {
			writeOperationError(response, err)
			return
		}
		events := make([]json.RawMessage, 0, len(description.Events))
		for _, event := range description.Events {
			events = append(events, append(json.RawMessage(nil), event.Observed...))
		}
		writeJSON(response, http.StatusOK, objectResponse{Target: target, YAML: string(description.Object.YAML), Events: events})
		return
	}
	reveal := request.URL.Query().Get("reveal_secrets") == "true"
	view, err := application.local.View(request.Context(), target, reveal)
	if err != nil {
		writeOperationError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, objectResponse{Target: target, YAML: string(view.YAML), Events: []json.RawMessage{}})
}

func (application *Application) handleLogs(response http.ResponseWriter, request *http.Request) {
	target, err := targetFromQuery(request)
	if err != nil {
		writeAPIError(response, http.StatusBadRequest, err.Error())
		return
	}
	target.Kind = "Pod"
	tail := int64(200)
	if value := request.URL.Query().Get("tail"); value != "" {
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsed < -1 || parsed > 10000 {
			writeAPIError(response, http.StatusBadRequest, "tail must be between -1 and 10000")
			return
		}
		tail = parsed
	}
	since := time.Duration(0)
	if value := request.URL.Query().Get("since"); value != "" {
		since, err = time.ParseDuration(value)
		if err != nil || since < 0 {
			writeAPIError(response, http.StatusBadRequest, "since must be a non-negative duration")
			return
		}
	}
	stream, err := application.local.Logs(request.Context(), target, localops.LogOptions{
		Container: request.URL.Query().Get("container"), Follow: request.URL.Query().Get("follow") == "true",
		Previous: request.URL.Query().Get("previous") == "true", Timestamps: true, TailLines: &tail, Since: since,
	})
	if err != nil {
		writeOperationError(response, err)
		return
	}
	defer func() { _ = stream.Close() }()
	response.Header().Set("Content-Type", "text/plain; charset=utf-8")
	response.WriteHeader(http.StatusOK)
	flusher, _ := response.(http.Flusher)
	buffer := make([]byte, 16<<10)
	for {
		count, readErr := stream.Read(buffer)
		if count > 0 {
			if _, err := response.Write(buffer[:count]); err != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if readErr != nil {
			return
		}
	}
}

type execRequest struct {
	Target    localops.Target `json:"target"`
	Container string          `json:"container,omitempty"`
	Command   []string        `json:"command"`
}

type execResponse struct {
	Stdout    string `json:"stdout"`
	Stderr    string `json:"stderr"`
	Truncated bool   `json:"truncated"`
}

func (application *Application) handleExec(response http.ResponseWriter, request *http.Request) {
	payload := execRequest{}
	if err := decodeJSON(response, request, maxAPIRequestBytes, &payload); err != nil {
		writeAPIError(response, http.StatusBadRequest, err.Error())
		return
	}
	if len(payload.Command) == 0 || len(payload.Command) > maxExecArguments {
		writeAPIError(response, http.StatusBadRequest, "command must contain between 1 and 64 arguments")
		return
	}
	for _, argument := range payload.Command {
		if len(argument) > 4096 {
			writeAPIError(response, http.StatusBadRequest, "each command argument must be at most 4096 bytes")
			return
		}
	}
	payload.Target.Kind = "Pod"
	stdout, stderr := &boundedWriter{limit: maxExecOutputBytes}, &boundedWriter{limit: maxExecOutputBytes}
	err := application.local.Exec(request.Context(), payload.Target, localops.ExecOptions{
		Container: payload.Container, Command: append([]string(nil), payload.Command...),
	}, localops.Streams{Stdout: stdout, Stderr: stderr})
	if err != nil {
		writeOperationError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, execResponse{
		Stdout: stdout.String(), Stderr: stderr.String(), Truncated: stdout.truncated || stderr.truncated,
	})
}

type editRequest struct {
	Target       localops.Target `json:"target"`
	Manifest     string          `json:"manifest"`
	PreviewToken string          `json:"preview_token,omitempty"`
}

type editPreviewResponse struct {
	Current      string `json:"current"`
	DryRun       string `json:"dry_run"`
	Diff         string `json:"diff"`
	PreviewToken string `json:"preview_token"`
}

func (application *Application) handleEditPreview(response http.ResponseWriter, request *http.Request) {
	payload, ok := decodeEditRequest(response, request)
	if !ok {
		return
	}
	preview, err := application.local.PreviewApply(request.Context(), payload.Target, []byte(payload.Manifest))
	if err != nil {
		writeOperationError(response, err)
		return
	}
	diff, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
		A: difflib.SplitLines(string(preview.CurrentYAML)), B: difflib.SplitLines(string(preview.DryRunYAML)),
		FromFile: payload.Target.Kind + "/" + payload.Target.Name + " (current)",
		ToFile:   payload.Target.Kind + "/" + payload.Target.Name + " (server dry-run)", Context: 3,
	})
	if err != nil {
		writeAPIError(response, http.StatusInternalServerError, "build server dry-run diff: "+err.Error())
		return
	}
	token, err := application.previews.issue(payload.Target, payload.Manifest)
	if err != nil {
		writeAPIError(response, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(response, http.StatusOK, editPreviewResponse{
		Current: string(preview.CurrentYAML), DryRun: string(preview.DryRunYAML), Diff: diff, PreviewToken: token,
	})
}

func (application *Application) handleEditApply(response http.ResponseWriter, request *http.Request) {
	payload, ok := decodeEditRequest(response, request)
	if !ok {
		return
	}
	if err := application.previews.consume(payload.PreviewToken, payload.Target, payload.Manifest); err != nil {
		writeAPIError(response, http.StatusConflict, err.Error())
		return
	}
	evidence, err := application.local.Apply(request.Context(), payload.Target, []byte(payload.Manifest))
	if err != nil {
		writeOperationError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, evidence)
}

func decodeEditRequest(response http.ResponseWriter, request *http.Request) (editRequest, bool) {
	payload := editRequest{}
	if err := decodeJSON(response, request, maxAPIRequestBytes, &payload); err != nil {
		writeAPIError(response, http.StatusBadRequest, err.Error())
		return editRequest{}, false
	}
	if len(payload.Manifest) == 0 || len(payload.Manifest) > maxAPIRequestBytes {
		writeAPIError(response, http.StatusBadRequest, "manifest must contain between 1 byte and 10 MiB")
		return editRequest{}, false
	}
	return payload, true
}

type startForwardRequest struct {
	Target localops.Target `json:"target"`
	Ports  []string        `json:"ports"`
}

func (application *Application) handleListForwards(response http.ResponseWriter, _ *http.Request) {
	writeJSON(response, http.StatusOK, application.forwards.list())
}

func (application *Application) handleStartForward(response http.ResponseWriter, request *http.Request) {
	payload := startForwardRequest{}
	if err := decodeJSON(response, request, 64<<10, &payload); err != nil {
		writeAPIError(response, http.StatusBadRequest, err.Error())
		return
	}
	if len(payload.Ports) == 0 || len(payload.Ports) > maxForwardPorts {
		writeAPIError(response, http.StatusBadRequest, "ports must contain between 1 and 16 mappings")
		return
	}
	if err := application.forwards.reserve(); err != nil {
		writeAPIError(response, http.StatusTooManyRequests, err.Error())
		return
	}
	reserved := true
	defer func() {
		if reserved {
			application.forwards.releaseReservation()
		}
	}()
	session, err := application.local.PortForward(application.ctx, localops.ForwardRequest{
		Target: payload.Target, Ports: append([]string(nil), payload.Ports...),
	})
	if err != nil {
		writeOperationError(response, err)
		return
	}
	timer := time.NewTimer(20 * time.Second)
	defer timer.Stop()
	select {
	case <-session.Ready():
		forward, err := application.forwards.add(payload.Target, session)
		if err != nil {
			_ = session.Close()
			writeOperationError(response, err)
			return
		}
		reserved = false
		writeJSON(response, http.StatusCreated, forward)
	case err := <-session.Done():
		_ = session.Close()
		if err == nil {
			err = fmt.Errorf("port-forward ended before becoming ready")
		}
		writeOperationError(response, err)
	case <-timer.C:
		_ = session.Close()
		writeAPIError(response, http.StatusGatewayTimeout, "port-forward did not become ready within 20 seconds")
	case <-request.Context().Done():
		_ = session.Close()
	}
}

func (application *Application) handleDeleteForward(response http.ResponseWriter, request *http.Request) {
	if err := application.forwards.close(request.PathValue("id")); err != nil {
		writeAPIError(response, http.StatusNotFound, err.Error())
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func targetFromQuery(request *http.Request) (localops.Target, error) {
	target := localops.Target{
		Context: request.URL.Query().Get("context"), Namespace: request.URL.Query().Get("namespace"),
		Kind: request.URL.Query().Get("kind"), Name: request.URL.Query().Get("name"),
	}
	if err := target.Validate(); err != nil {
		return localops.Target{}, err
	}
	return target, nil
}

func splitNonEmpty(value, separator string) []string {
	parts := strings.Split(value, separator)
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func writeOperationError(response http.ResponseWriter, err error) {
	writeAPIError(response, http.StatusUnprocessableEntity, err.Error())
}

type boundedWriter struct {
	buffer    bytes.Buffer
	limit     int
	truncated bool
}

func (writer *boundedWriter) Write(payload []byte) (int, error) {
	original := len(payload)
	remaining := writer.limit - writer.buffer.Len()
	if remaining <= 0 {
		writer.truncated = writer.truncated || original > 0
		return original, nil
	}
	if len(payload) > remaining {
		payload = payload[:remaining]
		writer.truncated = true
	}
	_, _ = writer.buffer.Write(payload)
	return original, nil
}

func (writer *boundedWriter) String() string { return writer.buffer.String() }

var _ io.Writer = (*boundedWriter)(nil)
