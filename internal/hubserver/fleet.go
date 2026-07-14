// SPDX-License-Identifier: Apache-2.0

package hubserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/ArdurAI/sith/internal/fleet"
	"github.com/ArdurAI/sith/internal/hubfleet"
	"github.com/ArdurAI/sith/internal/pep"
	"github.com/ArdurAI/sith/internal/tenancy"
	"github.com/ArdurAI/sith/internal/tracing"
)

const fleetRoutePrefix = "/v1/workspaces/"

// FleetRefresher performs one bounded refresh for the verified workspace scope.
type FleetRefresher interface {
	Collect(context.Context, tenancy.Scope) (fleet.Coverage, error)
}

// FleetImageSearcher resolves one exact immutable image digest in the signed workspace.
type FleetImageSearcher interface {
	Search(context.Context, tenancy.Scope, hubfleet.ImageSearchRequest) (fleet.QueryResult, error)
}

// FleetCVESearcher resolves one exact immutable image's normalized CVE observations.
type FleetCVESearcher interface {
	Search(context.Context, tenancy.Scope, hubfleet.ImageSearchRequest) (fleet.QueryResult, error)
}

// FleetHandlerConfig supplies the authenticated dependencies for the fixed hub fleet API.
type FleetHandlerConfig struct {
	Verifier      Verifier
	AuthObserver  AuthObserver
	Collector     FleetRefresher
	Reader        hubfleet.FleetReader
	ImageSearcher FleetImageSearcher
	CVESearcher   FleetCVESearcher
	PEP           *pep.Enforcer
}

// NewFleetHandler constructs the fixed, authenticated hub read surface.
//
// A caller can only address a workspace contained in its verified session. The handler accepts no
// user-supplied transport target, selector, credentials, or freshness override.
func NewFleetHandler(config FleetHandlerConfig) (http.Handler, error) {
	if config.Verifier == nil || config.Collector == nil || config.Reader == nil || config.ImageSearcher == nil || config.PEP == nil {
		return nil, fmt.Errorf("construct hub fleet handler: verifier, collector, reader, image searcher, and policy enforcer are required")
	}

	handler := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		setNoStore(response.Header())
		workspaceID, operation, imageDigest, ok := parseFleetRoute(request.URL)
		if !ok {
			writeFleetError(response, http.StatusNotFound, "not_found")
			return
		}
		scope, err := ScopeFromContext(request.Context(), workspaceID)
		if err != nil {
			writeFleetError(response, http.StatusForbidden, "forbidden")
			return
		}
		traceContext, _, err := tracing.Ensure(request.Context())
		if err != nil {
			writeFleetError(response, http.StatusServiceUnavailable, "trace_unavailable")
			return
		}
		request = request.WithContext(traceContext)

		switch operation {
		case fleetOperationRead:
			if request.Method != http.MethodGet {
				response.Header().Set("Allow", http.MethodGet)
				writeFleetError(response, http.StatusMethodNotAllowed, "method_not_allowed")
				return
			}
			source, err := hubfleet.NewSource(hubfleet.SourceConfig{
				Reader: config.Reader,
				Scope:  scope,
				PEP:    config.PEP,
			})
			if err != nil {
				writeFleetError(response, http.StatusForbidden, "forbidden")
				return
			}
			result, err := source.Fleet(request.Context())
			if err != nil {
				writeFleetError(response, http.StatusServiceUnavailable, "fleet_unavailable")
				return
			}
			writeFleetJSON(response, http.StatusOK, result)
		case fleetOperationRefresh:
			if request.Method != http.MethodPost {
				response.Header().Set("Allow", http.MethodPost)
				writeFleetError(response, http.StatusMethodNotAllowed, "method_not_allowed")
				return
			}
			coverage, err := config.Collector.Collect(request.Context(), scope)
			if err != nil {
				writeFleetError(response, http.StatusServiceUnavailable, "refresh_unavailable")
				return
			}
			writeFleetJSON(response, http.StatusOK, coverage)
		case fleetOperationImageSearch:
			if request.Method != http.MethodGet {
				response.Header().Set("Allow", http.MethodGet)
				writeFleetError(response, http.StatusMethodNotAllowed, "method_not_allowed")
				return
			}
			result, err := config.ImageSearcher.Search(request.Context(), scope, hubfleet.ImageSearchRequest{Digest: imageDigest})
			if err != nil {
				writeFleetError(response, http.StatusServiceUnavailable, "image_search_unavailable")
				return
			}
			writeFleetJSON(response, http.StatusOK, result)
		case fleetOperationCVESearch:
			if request.Method != http.MethodGet {
				response.Header().Set("Allow", http.MethodGet)
				writeFleetError(response, http.StatusMethodNotAllowed, "method_not_allowed")
				return
			}
			if config.CVESearcher == nil {
				writeFleetError(response, http.StatusServiceUnavailable, "cve_search_unavailable")
				return
			}
			result, err := config.CVESearcher.Search(request.Context(), scope, hubfleet.ImageSearchRequest{Digest: imageDigest})
			if err != nil {
				writeFleetError(response, http.StatusServiceUnavailable, "cve_search_unavailable")
				return
			}
			writeFleetJSON(response, http.StatusOK, result)
		default:
			writeFleetError(response, http.StatusNotFound, "not_found")
		}
	})

	return AuthenticateWithObserver(config.Verifier, config.AuthObserver, handler)
}

type fleetOperation uint8

const (
	fleetOperationRead fleetOperation = iota + 1
	fleetOperationRefresh
	fleetOperationImageSearch
	fleetOperationCVESearch
)

func parseFleetRoute(requestURL *url.URL) (tenancy.WorkspaceID, fleetOperation, string, bool) {
	if requestURL == nil || requestURL.RawQuery != "" {
		return "", 0, "", false
	}
	escapedPath := requestURL.EscapedPath()
	if !strings.HasPrefix(escapedPath, fleetRoutePrefix) {
		return "", 0, "", false
	}
	workspaceSegment, resource, found := strings.Cut(strings.TrimPrefix(escapedPath, fleetRoutePrefix), "/")
	if !found || workspaceSegment == "" {
		return "", 0, "", false
	}
	workspace, err := url.PathUnescape(workspaceSegment)
	if err != nil || url.PathEscape(workspace) != workspaceSegment {
		return "", 0, "", false
	}
	workspaceID := tenancy.WorkspaceID(workspace)
	if tenancy.ValidateWorkspaceID(workspaceID) != nil {
		return "", 0, "", false
	}
	switch resource {
	case "fleet":
		return workspaceID, fleetOperationRead, "", true
	case "fleet:refresh":
		return workspaceID, fleetOperationRefresh, "", true
	default:
		digestSegment, found := strings.CutPrefix(resource, "fleet/images/")
		if !found || digestSegment == "" {
			return "", 0, "", false
		}
		operation := fleetOperationImageSearch
		if strings.HasSuffix(digestSegment, "/cves") {
			operation = fleetOperationCVESearch
			digestSegment = strings.TrimSuffix(digestSegment, "/cves")
		}
		if digestSegment == "" || strings.Contains(digestSegment, "/") {
			return "", 0, "", false
		}
		digest, err := url.PathUnescape(digestSegment)
		if err != nil || url.PathEscape(digest) != digestSegment || fleet.ValidateImageDigest(digest) != nil {
			return "", 0, "", false
		}
		return workspaceID, operation, digest, true
	}
}

func writeFleetJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}

func writeFleetError(response http.ResponseWriter, status int, code string) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(struct {
		Error string `json:"error"`
	}{Error: code})
}
