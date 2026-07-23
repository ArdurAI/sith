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
	auditExportPagesResource  = "audit/export/pages"
	auditExportFilename       = "sith-policy-audit.json"
	auditExportPageFilename   = "sith-policy-audit-page.json"
	maxConcurrentAuditExports = 4
)

// PolicyAuditExporter reads complete or snapshot-paged verified retained chains after
// authorization succeeds.
type PolicyAuditExporter interface {
	ExportPolicyAuditChain(context.Context, tenancy.Scope) (auditrecord.Export, error)
	ExportPolicyAuditPage(context.Context, tenancy.Scope, auditrecord.PageRequest) (auditrecord.Page, error)
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
		route, ok := parseAuditExportRoute(request.URL)
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
		if route.pages && len(request.Header.Values("Cookie")) != 0 {
			writeAuditExportError(response, http.StatusNotFound, "not_found")
			return
		}
		scope, err := ScopeFromContext(request.Context(), route.workspaceID)
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
		if route.pages {
			pageRequest, err := auditrecord.FirstPage(scope.WorkspaceID())
			if route.cursor != "" {
				pageRequest, err = auditrecord.ContinuePage(scope.WorkspaceID(), route.cursor)
			}
			if err != nil {
				writeAuditExportError(response, http.StatusServiceUnavailable, "audit_export_unavailable")
				return
			}
			page, err := config.Exporter.ExportPolicyAuditPage(traceContext, scope, pageRequest)
			if err != nil || page.VerifyForWorkspace(scope.WorkspaceID()) != nil || pageRequest.ValidatePage(page) != nil {
				writeAuditExportError(response, http.StatusServiceUnavailable, "audit_export_unavailable")
				return
			}
			writeAuditExportDocument(response, page, auditExportPageFilename)
			return
		}

		exported, err := config.Exporter.ExportPolicyAuditChain(traceContext, scope)
		if err != nil || exported.VerifyForWorkspace(scope.WorkspaceID()) != nil {
			writeAuditExportError(response, http.StatusServiceUnavailable, "audit_export_unavailable")
			return
		}
		writeAuditExportDocument(response, exported, auditExportFilename)
	})
	return AuthenticateWithObserver(config.Verifier, config.AuthObserver, handler)
}

type auditExportRoute struct {
	workspaceID tenancy.WorkspaceID
	pages       bool
	cursor      string
}

func requestHasBody(request *http.Request) bool {
	return request == nil || request.ContentLength != 0 || len(request.TransferEncoding) != 0 ||
		(request.Body != nil && request.Body != http.NoBody)
}

func parseAuditExportRoute(requestURL *url.URL) (auditExportRoute, bool) {
	if requestURL == nil || requestURL.ForceQuery || requestURL.Fragment != "" {
		return auditExportRoute{}, false
	}
	escapedPath := requestURL.EscapedPath()
	if !strings.HasPrefix(escapedPath, auditExportRoutePrefix) {
		return auditExportRoute{}, false
	}
	workspaceSegment, resource, found := strings.Cut(strings.TrimPrefix(escapedPath, auditExportRoutePrefix), "/")
	if !found || workspaceSegment == "" || (resource != auditExportResource && resource != auditExportPagesResource) {
		return auditExportRoute{}, false
	}
	workspace, err := url.PathUnescape(workspaceSegment)
	if err != nil || url.PathEscape(workspace) != workspaceSegment {
		return auditExportRoute{}, false
	}
	workspaceID := tenancy.WorkspaceID(workspace)
	if tenancy.ValidateWorkspaceID(workspaceID) != nil {
		return auditExportRoute{}, false
	}
	route := auditExportRoute{workspaceID: workspaceID, pages: resource == auditExportPagesResource}
	if !route.pages {
		return route, requestURL.RawQuery == ""
	}
	if requestURL.RawQuery == "" {
		return route, true
	}
	if len(requestURL.RawQuery) > len("cursor=")+auditrecord.PageCursorChars {
		return auditExportRoute{}, false
	}
	values, err := url.ParseQuery(requestURL.RawQuery)
	if err != nil || len(values) != 1 || len(values["cursor"]) != 1 || values.Get("cursor") == "" {
		return auditExportRoute{}, false
	}
	route.cursor = values.Get("cursor")
	if requestURL.RawQuery != "cursor="+route.cursor {
		return auditExportRoute{}, false
	}
	return route, true
}

func writeAuditExportDocument(response http.ResponseWriter, document any, filename string) {
	encoded, err := json.Marshal(document)
	if err != nil {
		writeAuditExportError(response, http.StatusServiceUnavailable, "audit_export_unavailable")
		return
	}
	encoded = append(encoded, '\n')
	response.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	response.Header().Set("Content-Type", "application/json")
	response.Header().Set("Content-Length", strconv.Itoa(len(encoded)))
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write(encoded)
}

func writeAuditExportError(response http.ResponseWriter, status int, code string) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(struct {
		Error string `json:"error"`
	}{Error: code})
}
