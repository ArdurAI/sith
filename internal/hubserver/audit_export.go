// SPDX-License-Identifier: Apache-2.0

package hubserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/ArdurAI/sith/internal/auditrecord"
	"github.com/ArdurAI/sith/internal/pep"
	"github.com/ArdurAI/sith/internal/tenancy"
	"github.com/ArdurAI/sith/internal/tracing"
)

const (
	auditExportRoutePrefix    = "/v1/workspaces/"
	auditExportResource       = "audit/export"
	auditExportFilename       = "sith-policy-audit.json"
	maxConcurrentAuditExports = 4
)

// PolicyAuditExporter reads one complete, verified retained chain after authorization succeeds.
type PolicyAuditExporter interface {
	ExportPolicyAuditChain(context.Context, tenancy.Scope) (auditrecord.Export, error)
}

// AuditExportHandlerConfig supplies the authenticated export boundary.
type AuditExportHandlerConfig struct {
	Verifier     Verifier
	AuthObserver AuthObserver
	Exporter     PolicyAuditExporter
	PEP          *pep.Enforcer
}

// NewAuditExportHandler constructs the exact bearer-authenticated audit download route. It bounds
// process concurrency before policy/database work; the exporter itself bounds retained entries and
// completes its database transaction before returning a value for HTTP encoding.
func NewAuditExportHandler(config AuditExportHandlerConfig) (http.Handler, error) {
	if config.Verifier == nil || config.Exporter == nil || config.PEP == nil {
		return nil, fmt.Errorf("construct audit export handler: verifier, exporter, and policy enforcer are required")
	}
	slots := make(chan struct{}, maxConcurrentAuditExports)
	handler := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		setNoStore(response.Header())
		response.Header().Set("X-Content-Type-Options", "nosniff")
		workspaceID, ok := parseAuditExportRoute(request.URL)
		if !ok {
			writeAuditExportError(response, http.StatusNotFound, "not_found")
			return
		}
		if request.Method != http.MethodGet {
			response.Header().Set("Allow", http.MethodGet)
			writeAuditExportError(response, http.StatusMethodNotAllowed, "method_not_allowed")
			return
		}
		if requestHasBody(request) {
			writeAuditExportError(response, http.StatusNotFound, "not_found")
			return
		}
		scope, err := ScopeFromContext(request.Context(), workspaceID)
		if err != nil {
			writeAuditExportError(response, http.StatusForbidden, "forbidden")
			return
		}
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		default:
			response.Header().Set("Retry-After", "1")
			writeAuditExportError(response, http.StatusServiceUnavailable, "audit_export_unavailable")
			return
		}

		traceContext, _, err := tracing.Ensure(request.Context())
		if err != nil {
			writeAuditExportError(response, http.StatusServiceUnavailable, "audit_export_unavailable")
			return
		}
		if err := config.PEP.AuthorizeAuditExport(traceContext, scope); err != nil {
			if errors.Is(err, pep.ErrDenied) {
				writeAuditExportError(response, http.StatusForbidden, "forbidden")
				return
			}
			writeAuditExportError(response, http.StatusServiceUnavailable, "audit_export_unavailable")
			return
		}
		exported, err := config.Exporter.ExportPolicyAuditChain(traceContext, scope)
		if err != nil {
			writeAuditExportError(response, http.StatusServiceUnavailable, "audit_export_unavailable")
			return
		}
		if err := exported.VerifyForWorkspace(scope.WorkspaceID()); err != nil {
			writeAuditExportError(response, http.StatusServiceUnavailable, "audit_export_unavailable")
			return
		}
		encoded, err := json.Marshal(exported)
		if err != nil {
			writeAuditExportError(response, http.StatusServiceUnavailable, "audit_export_unavailable")
			return
		}
		encoded = append(encoded, '\n')
		response.Header().Set("Content-Disposition", `attachment; filename="`+auditExportFilename+`"`)
		response.Header().Set("Content-Type", "application/json")
		response.Header().Set("Content-Length", strconv.Itoa(len(encoded)))
		response.WriteHeader(http.StatusOK)
		_, _ = response.Write(encoded)
	})
	return AuthenticateWithObserver(config.Verifier, config.AuthObserver, handler)
}

func requestHasBody(request *http.Request) bool {
	return request == nil || request.ContentLength != 0 || len(request.TransferEncoding) != 0 ||
		(request.Body != nil && request.Body != http.NoBody)
}

func parseAuditExportRoute(requestURL *url.URL) (tenancy.WorkspaceID, bool) {
	if requestURL == nil || requestURL.RawQuery != "" || requestURL.ForceQuery || requestURL.Fragment != "" {
		return "", false
	}
	escapedPath := requestURL.EscapedPath()
	if !strings.HasPrefix(escapedPath, auditExportRoutePrefix) {
		return "", false
	}
	workspaceSegment, resource, found := strings.Cut(strings.TrimPrefix(escapedPath, auditExportRoutePrefix), "/")
	if !found || workspaceSegment == "" || resource != auditExportResource {
		return "", false
	}
	workspace, err := url.PathUnescape(workspaceSegment)
	if err != nil || url.PathEscape(workspace) != workspaceSegment {
		return "", false
	}
	workspaceID := tenancy.WorkspaceID(workspace)
	if tenancy.ValidateWorkspaceID(workspaceID) != nil {
		return "", false
	}
	return workspaceID, true
}

func writeAuditExportError(response http.ResponseWriter, status int, code string) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(struct {
		Error string `json:"error"`
	}{Error: code})
}
