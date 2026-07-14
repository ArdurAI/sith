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
)

const fleetRoutePrefix = "/v1/workspaces/"

// FleetRefresher performs one bounded refresh for the verified workspace scope.
type FleetRefresher interface {
	Collect(context.Context, tenancy.Scope) (fleet.Coverage, error)
}

// FleetHandlerConfig supplies the authenticated dependencies for the fixed hub fleet API.
type FleetHandlerConfig struct {
	Verifier  Verifier
	Collector FleetRefresher
	Reader    hubfleet.FleetReader
	PEP       *pep.Enforcer
}

// NewFleetHandler constructs the fixed, authenticated hub read surface.
//
// A caller can only address a workspace contained in its verified session. The handler accepts no
// user-supplied transport target, selector, credentials, or freshness override.
func NewFleetHandler(config FleetHandlerConfig) (http.Handler, error) {
	if config.Verifier == nil || config.Collector == nil || config.Reader == nil || config.PEP == nil {
		return nil, fmt.Errorf("construct hub fleet handler: verifier, collector, reader, and policy enforcer are required")
	}

	handler := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		setNoStore(response.Header())
		workspaceID, operation, ok := parseFleetRoute(request.URL)
		if !ok {
			writeFleetError(response, http.StatusNotFound, "not_found")
			return
		}
		scope, err := ScopeFromContext(request.Context(), workspaceID)
		if err != nil {
			writeFleetError(response, http.StatusForbidden, "forbidden")
			return
		}

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
		default:
			writeFleetError(response, http.StatusNotFound, "not_found")
		}
	})

	return Authenticate(config.Verifier, handler)
}

type fleetOperation uint8

const (
	fleetOperationRead fleetOperation = iota + 1
	fleetOperationRefresh
)

func parseFleetRoute(requestURL *url.URL) (tenancy.WorkspaceID, fleetOperation, bool) {
	if requestURL == nil || requestURL.RawQuery != "" {
		return "", 0, false
	}
	escapedPath := requestURL.EscapedPath()
	if !strings.HasPrefix(escapedPath, fleetRoutePrefix) {
		return "", 0, false
	}
	workspaceSegment, resource, found := strings.Cut(strings.TrimPrefix(escapedPath, fleetRoutePrefix), "/")
	if !found || workspaceSegment == "" || strings.Contains(resource, "/") {
		return "", 0, false
	}
	workspace, err := url.PathUnescape(workspaceSegment)
	if err != nil || url.PathEscape(workspace) != workspaceSegment {
		return "", 0, false
	}
	workspaceID := tenancy.WorkspaceID(workspace)
	if tenancy.ValidateWorkspaceID(workspaceID) != nil {
		return "", 0, false
	}
	switch resource {
	case "fleet":
		return workspaceID, fleetOperationRead, true
	case "fleet:refresh":
		return workspaceID, fleetOperationRefresh, true
	default:
		return "", 0, false
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
