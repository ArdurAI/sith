// SPDX-License-Identifier: Apache-2.0

package hubserver

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/ArdurAI/sith/internal/fleet"
	"github.com/ArdurAI/sith/internal/hubfleet"
	"github.com/ArdurAI/sith/internal/pep"
	"github.com/ArdurAI/sith/internal/tenancy"
)

const (
	consoleCSRFHeader                   = "X-Sith-CSRF"
	consoleCSRFVersion                  = byte(1)
	consoleCSRFTimeBytes                = 10
	consoleCSRFRandomBytes              = 32
	consoleCSRFKeyBytes                 = 32
	consoleCSRFDigestBytes              = sha256.Size
	consoleCSRFPayloadBytes             = 1 + consoleCSRFTimeBytes + consoleCSRFRandomBytes
	consoleCSRFTokenBytes               = consoleCSRFPayloadBytes + consoleCSRFDigestBytes
	consoleFleetCSRFDomain              = "sith-hub-console-fleet/v1"
	consoleCorrelationDomain            = "sith-hub-console-correlation/v1"
	consoleInventoryDomain              = "sith-hub-console-inventory/v1"
	consoleCVEIdentifierDomain          = "sith-hub-console-cve-identifier/v1"
	consoleCorrelationReadLimit         = 257
	consoleCorrelationMaxMatches        = 256
	consoleCorrelationMaxCoverageScopes = 1_000
	consoleInventoryReadLimit           = 257
	consoleInventoryMaxRecords          = 256
	consoleCVEReadLimit                 = 257
	consoleCVEMaxRecords                = 256
	consoleMaximumSafeInteger           = int64(9_007_199_254_740_991)
	defaultConsoleCSRFLifetime          = 5 * time.Minute
	maximumConsoleCSRFLifetime          = 10 * time.Minute
)

//go:embed console_assets/*
var embeddedConsoleAssets embed.FS

// ConsoleHandlerConfig supplies only the authenticated read dependencies for the Hub console.
type ConsoleHandlerConfig struct {
	Verifier     Verifier
	AuthObserver AuthObserver
	Reader       hubfleet.FleetReader
	Correlator   *hubfleet.Correlator
	Inventory    *hubfleet.InventorySearcher
	CVE          *hubfleet.CVESearcher
	PEP          *pep.Enforcer
	CSRFLifetime time.Duration
	Now          func() time.Time
	Random       io.Reader
}

// ConsoleHandler owns the Hub-only cookie/session adapter and its embedded read-only frontend.
type ConsoleHandler struct {
	verifier     Verifier
	authObserver AuthObserver
	reader       hubfleet.FleetReader
	correlator   *hubfleet.Correlator
	inventory    *hubfleet.InventorySearcher
	cve          *hubfleet.CVESearcher
	pep          *pep.Enforcer
	csrfLifetime time.Duration
	now          func() time.Time
	random       io.Reader
	randomMu     sync.Mutex
	csrfKey      [consoleCSRFKeyBytes]byte
	page         *template.Template
	assets       fs.FS
}

type consolePageData struct {
	Workspace            string
	FleetCSRFToken       string
	CorrelationCSRFToken string
	InventoryCSRFToken   string
	CVECSRFToken         string
}

type consoleFleetResponse struct {
	Fleet      fleet.FleetResult        `json:"fleet"`
	Assessment fleet.CoverageAssessment `json:"assessment"`
}

type consoleHealthMatch struct {
	Scope        string    `json:"scope"`
	ResourceKind string    `json:"resource_kind"`
	Namespace    string    `json:"namespace,omitempty"`
	Name         string    `json:"name"`
	Health       string    `json:"health"`
	ObservedAt   time.Time `json:"observed_at"`
	Stale        bool      `json:"stale"`
	StaleFor     string    `json:"stale_for,omitempty"`
}

type consoleCorrelationResponse struct {
	Matches    []consoleHealthMatch     `json:"matches"`
	Coverage   fleet.Coverage           `json:"coverage"`
	Assessment fleet.CoverageAssessment `json:"assessment"`
}

type consoleInventoryRecord struct {
	Scope             string    `json:"scope"`
	ResourceKind      string    `json:"resource_kind"`
	Namespace         string    `json:"namespace,omitempty"`
	Name              string    `json:"name"`
	ObservedAt        time.Time `json:"observed_at"`
	Stale             bool      `json:"stale"`
	StaleFor          string    `json:"stale_for,omitempty"`
	Replicas          *int64    `json:"replicas,omitempty"`
	AvailableReplicas *int64    `json:"available_replicas,omitempty"`
	Ready             *bool     `json:"ready,omitempty"`
	Generation        int64     `json:"generation"`
}

type consoleInventoryResponse struct {
	Records    []consoleInventoryRecord `json:"records"`
	Coverage   fleet.Coverage           `json:"coverage"`
	Assessment fleet.CoverageAssessment `json:"assessment"`
}

type consoleCVERecord struct {
	Scope       string    `json:"scope"`
	ImageDigest string    `json:"image_digest"`
	Identifier  string    `json:"identifier"`
	Severity    string    `json:"severity"`
	ObservedAt  time.Time `json:"observed_at"`
	Stale       bool      `json:"stale"`
	StaleFor    string    `json:"stale_for,omitempty"`
}

type consoleCVEResponse struct {
	Records    []consoleCVERecord       `json:"records"`
	Coverage   fleet.Coverage           `json:"coverage"`
	Assessment fleet.CoverageAssessment `json:"assessment"`
}

// NewConsoleHandler constructs a separate cookie-authenticated read surface. It does not alter
// bearer API authentication and exposes no refresh, connector, local-operation, or write seam.
func NewConsoleHandler(config ConsoleHandlerConfig) (*ConsoleHandler, error) {
	if config.Verifier == nil || config.Reader == nil || config.Correlator == nil || config.Inventory == nil || config.CVE == nil || config.PEP == nil {
		return nil, fmt.Errorf("construct Hub console: verifier, reader, correlator, inventory searcher, CVE searcher, and policy enforcer are required")
	}
	if config.CSRFLifetime == 0 {
		config.CSRFLifetime = defaultConsoleCSRFLifetime
	}
	if config.CSRFLifetime < time.Minute || config.CSRFLifetime > maximumConsoleCSRFLifetime {
		return nil, fmt.Errorf("construct Hub console: CSRF lifetime must be between one and ten minutes")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.Random == nil {
		config.Random = rand.Reader
	}
	assets, err := fs.Sub(embeddedConsoleAssets, "console_assets")
	if err != nil {
		return nil, fmt.Errorf("construct Hub console: embedded assets are unavailable")
	}
	page, err := template.ParseFS(assets, "console.html")
	if err != nil {
		return nil, fmt.Errorf("construct Hub console: page template is invalid")
	}
	handler := &ConsoleHandler{
		verifier: config.Verifier, authObserver: config.AuthObserver, reader: config.Reader, correlator: config.Correlator, inventory: config.Inventory, cve: config.CVE, pep: config.PEP,
		csrfLifetime: config.CSRFLifetime, now: config.Now, random: config.Random, page: page, assets: assets,
	}
	if _, err := io.ReadFull(handler.random, handler.csrfKey[:]); err != nil {
		return nil, fmt.Errorf("construct Hub console: CSRF key generation failed")
	}
	return handler, nil
}

// ServePage returns the authenticated shell and purpose-separated session/workspace-bound proofs.
func (handler *ConsoleHandler) ServePage(response http.ResponseWriter, request *http.Request) {
	setConsoleSecurityHeaders(response.Header())
	workspaceID, ok := canonicalConsoleRequest(request, "/console")
	if !ok {
		writeConsoleError(response, http.StatusNotFound, "not_found")
		return
	}
	scope, rawSession, ok := handler.authorize(response, request, workspaceID)
	if !ok {
		return
	}
	fleetCSRFToken, err := handler.newCSRFToken(rawSession, scope.WorkspaceID(), consoleFleetCSRFDomain)
	if err != nil {
		writeConsoleError(response, http.StatusServiceUnavailable, "console_unavailable")
		return
	}
	correlationCSRFToken, err := handler.newCSRFToken(rawSession, scope.WorkspaceID(), consoleCorrelationDomain)
	if err != nil {
		writeConsoleError(response, http.StatusServiceUnavailable, "console_unavailable")
		return
	}
	inventoryCSRFToken, err := handler.newCSRFToken(rawSession, scope.WorkspaceID(), consoleInventoryDomain)
	if err != nil {
		writeConsoleError(response, http.StatusServiceUnavailable, "console_unavailable")
		return
	}
	cveCSRFToken, err := handler.newCSRFToken(rawSession, scope.WorkspaceID(), consoleCVEIdentifierDomain)
	if err != nil {
		writeConsoleError(response, http.StatusServiceUnavailable, "console_unavailable")
		return
	}
	var rendered bytes.Buffer
	if err := handler.page.Execute(&rendered, consolePageData{
		Workspace: string(scope.WorkspaceID()), FleetCSRFToken: fleetCSRFToken, CorrelationCSRFToken: correlationCSRFToken, InventoryCSRFToken: inventoryCSRFToken, CVECSRFToken: cveCSRFToken,
	}); err != nil {
		writeConsoleError(response, http.StatusServiceUnavailable, "console_unavailable")
		return
	}
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write(rendered.Bytes())
}

// ServeFleet returns one persisted tenant-scoped snapshot through the existing PEP read path.
func (handler *ConsoleHandler) ServeFleet(response http.ResponseWriter, request *http.Request) {
	setConsoleSecurityHeaders(response.Header())
	workspaceID, ok := canonicalConsoleRequest(request, "/console/fleet")
	if !ok {
		writeConsoleError(response, http.StatusNotFound, "not_found")
		return
	}
	scope, rawSession, ok := handler.authorize(response, request, workspaceID)
	if !ok {
		return
	}
	if !sameOriginFetch(request.Header.Values("Sec-Fetch-Site")) ||
		!handler.validCSRFToken(request.Header.Values(consoleCSRFHeader), rawSession, scope.WorkspaceID(), consoleFleetCSRFDomain) {
		writeConsoleError(response, http.StatusForbidden, "forbidden")
		return
	}
	source, err := hubfleet.NewSource(hubfleet.SourceConfig{Reader: handler.reader, Scope: scope, PEP: handler.pep})
	if err != nil {
		writeConsoleError(response, http.StatusForbidden, "forbidden")
		return
	}
	result, err := source.Fleet(request.Context())
	if err != nil {
		writeConsoleError(response, http.StatusServiceUnavailable, "fleet_unavailable")
		return
	}
	writeConsoleJSON(response, http.StatusOK, consoleFleetResponse{Fleet: result, Assessment: result.Coverage.Assessment()})
}

// ServeCorrelation answers one explicit exact-resource health question through the existing
// tenant-scoped PEP correlator, then projects stored facts to a deliberately minimal response.
func (handler *ConsoleHandler) ServeCorrelation(response http.ResponseWriter, request *http.Request) {
	setConsoleSecurityHeaders(response.Header())
	workspaceID, correlationRequest, ok := canonicalConsoleCorrelationRequest(request)
	if !ok {
		writeConsoleError(response, http.StatusNotFound, "not_found")
		return
	}
	scope, rawSession, ok := handler.authorize(response, request, workspaceID)
	if !ok {
		return
	}
	if !sameOriginFetch(request.Header.Values("Sec-Fetch-Site")) ||
		!handler.validCSRFToken(request.Header.Values(consoleCSRFHeader), rawSession, scope.WorkspaceID(), consoleCorrelationDomain) {
		writeConsoleError(response, http.StatusForbidden, "forbidden")
		return
	}
	result, err := handler.correlator.Correlate(request.Context(), scope, correlationRequest)
	if err != nil {
		writeConsoleError(response, http.StatusServiceUnavailable, "correlation_unavailable")
		return
	}
	projected, err := projectConsoleCorrelation(result, scope.WorkspaceID(), correlationRequest)
	if err != nil {
		writeConsoleError(response, http.StatusServiceUnavailable, "correlation_unavailable")
		return
	}
	writeConsoleJSON(response, http.StatusOK, projected)
}

// ServeInventory answers one explicit bounded inventory selection through the dedicated
// tenant-scoped PEP service, then projects stored facts to a closed browser response.
func (handler *ConsoleHandler) ServeInventory(response http.ResponseWriter, request *http.Request) {
	setConsoleSecurityHeaders(response.Header())
	workspaceID, inventoryRequest, ok := canonicalConsoleInventoryRequest(request)
	if !ok {
		writeConsoleError(response, http.StatusNotFound, "not_found")
		return
	}
	scope, rawSession, ok := handler.authorize(response, request, workspaceID)
	if !ok {
		return
	}
	if !sameOriginFetch(request.Header.Values("Sec-Fetch-Site")) ||
		!handler.validCSRFToken(request.Header.Values(consoleCSRFHeader), rawSession, scope.WorkspaceID(), consoleInventoryDomain) {
		writeConsoleError(response, http.StatusForbidden, "forbidden")
		return
	}
	result, err := handler.inventory.Search(request.Context(), scope, inventoryRequest)
	if err != nil {
		writeConsoleError(response, http.StatusServiceUnavailable, "inventory_unavailable")
		return
	}
	projected, err := projectConsoleInventory(result, scope.WorkspaceID(), inventoryRequest)
	if err != nil {
		writeConsoleError(response, http.StatusServiceUnavailable, "inventory_unavailable")
		return
	}
	writeConsoleJSON(response, http.StatusOK, projected)
}

// ServeCVEIdentifier answers one explicit canonical CVE question through the existing
// tenant-scoped PEP service, then projects immutable runtime-image evidence to a closed response.
func (handler *ConsoleHandler) ServeCVEIdentifier(response http.ResponseWriter, request *http.Request) {
	setConsoleSecurityHeaders(response.Header())
	workspaceID, cveRequest, ok := canonicalConsoleCVEIdentifierRequest(request)
	if !ok {
		writeConsoleError(response, http.StatusNotFound, "not_found")
		return
	}
	scope, rawSession, ok := handler.authorize(response, request, workspaceID)
	if !ok {
		return
	}
	if !sameOriginFetch(request.Header.Values("Sec-Fetch-Site")) ||
		!handler.validCSRFToken(request.Header.Values(consoleCSRFHeader), rawSession, scope.WorkspaceID(), consoleCVEIdentifierDomain) {
		writeConsoleError(response, http.StatusForbidden, "forbidden")
		return
	}
	result, err := handler.cve.SearchByIdentifier(request.Context(), scope, cveRequest)
	if err != nil {
		writeConsoleError(response, http.StatusServiceUnavailable, "cve_evidence_unavailable")
		return
	}
	projected, err := projectConsoleCVEIdentifier(result, scope.WorkspaceID(), cveRequest)
	if err != nil {
		writeConsoleError(response, http.StatusServiceUnavailable, "cve_evidence_unavailable")
		return
	}
	writeConsoleJSON(response, http.StatusOK, projected)
}

// ServeCSS returns the fixed embedded stylesheet and never inspects a session cookie.
func (handler *ConsoleHandler) ServeCSS(response http.ResponseWriter, request *http.Request) {
	handler.serveAsset(response, request, "/v1/console/assets/console.css", "console.css", "text/css; charset=utf-8")
}

// ServeJavaScript returns the fixed embedded renderer and never inspects a session cookie.
func (handler *ConsoleHandler) ServeJavaScript(response http.ResponseWriter, request *http.Request) {
	handler.serveAsset(response, request, "/v1/console/assets/console.js", "console.js", "text/javascript; charset=utf-8")
}

func (handler *ConsoleHandler) serveAsset(response http.ResponseWriter, request *http.Request, expectedPath, name, contentType string) {
	setConsoleSecurityHeaders(response.Header())
	if handler == nil || request == nil || request.Method != http.MethodGet || request.URL == nil || request.URL.RawQuery != "" ||
		request.URL.EscapedPath() != expectedPath {
		writeConsoleError(response, http.StatusNotFound, "not_found")
		return
	}
	payload, err := fs.ReadFile(handler.assets, name)
	if err != nil {
		writeConsoleError(response, http.StatusNotFound, "not_found")
		return
	}
	response.Header().Set("Content-Type", contentType)
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write(payload)
}

func (handler *ConsoleHandler) authorize(
	response http.ResponseWriter,
	request *http.Request,
	workspaceID tenancy.WorkspaceID,
) (tenancy.Scope, string, bool) {
	if handler == nil || handler.verifier == nil || request == nil || len(request.Header.Values("Authorization")) != 0 {
		refuseAuthentication(handlerAuthObserver(handler), response)
		return tenancy.Scope{}, "", false
	}
	rawSession, ok := exactConsoleSession(request)
	if !ok {
		refuseAuthentication(handler.authObserver, response)
		return tenancy.Scope{}, "", false
	}
	principal, err := handler.verifier.Verify(request.Context(), rawSession)
	if err != nil {
		refuseAuthentication(handler.authObserver, response)
		return tenancy.Scope{}, "", false
	}
	scope, err := principal.Scope(workspaceID)
	if err != nil {
		writeConsoleError(response, http.StatusForbidden, "forbidden")
		return tenancy.Scope{}, "", false
	}
	return scope, rawSession, true
}

func handlerAuthObserver(handler *ConsoleHandler) AuthObserver {
	if handler == nil {
		return nil
	}
	return handler.authObserver
}

func exactConsoleSession(request *http.Request) (string, bool) {
	if request == nil {
		return "", false
	}
	var session string
	count := 0
	for _, cookie := range request.Cookies() {
		if cookie.Name != browserOIDCSessionCookie {
			continue
		}
		count++
		session = cookie.Value
	}
	return session, count == 1 && session != "" && len(session) <= maxBearerTokenBytes && strings.TrimSpace(session) == session
}

func canonicalConsoleRequest(request *http.Request, suffix string) (tenancy.WorkspaceID, bool) {
	if request == nil || request.Method != http.MethodGet || request.URL == nil || request.URL.RawQuery != "" || request.URL.Fragment != "" {
		return "", false
	}
	workspace := request.PathValue("workspace")
	workspaceID := tenancy.WorkspaceID(workspace)
	if tenancy.ValidateWorkspaceID(workspaceID) != nil {
		return "", false
	}
	wantPath := "/v1/workspaces/" + url.PathEscape(workspace) + suffix
	return workspaceID, request.URL.EscapedPath() == wantPath
}

func canonicalConsoleCorrelationRequest(request *http.Request) (tenancy.WorkspaceID, hubfleet.CorrelationRequest, bool) {
	if request == nil || request.Method != http.MethodGet || request.URL == nil || request.URL.Fragment != "" ||
		request.URL.RawQuery == "" || len(request.URL.RawQuery) > 1_024 {
		return "", hubfleet.CorrelationRequest{}, false
	}
	workspaceID := tenancy.WorkspaceID(request.PathValue("workspace"))
	if tenancy.ValidateWorkspaceID(workspaceID) != nil {
		return "", hubfleet.CorrelationRequest{}, false
	}
	wantPath := "/v1/workspaces/" + url.PathEscape(string(workspaceID)) + "/console/correlate"
	if request.URL.EscapedPath() != wantPath {
		return "", hubfleet.CorrelationRequest{}, false
	}
	values, err := url.ParseQuery(request.URL.RawQuery)
	if err != nil || len(values) < 2 || len(values) > 3 || values.Encode() != request.URL.RawQuery {
		return "", hubfleet.CorrelationRequest{}, false
	}
	for key, entries := range values {
		if (key != "kind" && key != "name" && key != "namespace") || len(entries) != 1 {
			return "", hubfleet.CorrelationRequest{}, false
		}
	}
	if len(values["kind"]) != 1 || len(values["name"]) != 1 ||
		(len(values["namespace"]) == 1 && values.Get("namespace") == "") {
		return "", hubfleet.CorrelationRequest{}, false
	}
	correlationRequest := hubfleet.CorrelationRequest{
		ResourceKind: values.Get("kind"), Name: values.Get("name"), Namespace: values.Get("namespace"),
		HealthNot: "Healthy", Limit: consoleCorrelationReadLimit,
	}
	if correlationRequest.Validate() != nil {
		return "", hubfleet.CorrelationRequest{}, false
	}
	return workspaceID, correlationRequest, true
}

func canonicalConsoleInventoryRequest(request *http.Request) (tenancy.WorkspaceID, hubfleet.InventorySearchRequest, bool) {
	if request == nil || request.Method != http.MethodGet || request.URL == nil || request.URL.Fragment != "" ||
		request.URL.RawQuery == "" || len(request.URL.RawQuery) > 1_024 {
		return "", hubfleet.InventorySearchRequest{}, false
	}
	workspaceID := tenancy.WorkspaceID(request.PathValue("workspace"))
	if tenancy.ValidateWorkspaceID(workspaceID) != nil {
		return "", hubfleet.InventorySearchRequest{}, false
	}
	wantPath := "/v1/workspaces/" + url.PathEscape(string(workspaceID)) + "/console/inventory"
	if request.URL.EscapedPath() != wantPath {
		return "", hubfleet.InventorySearchRequest{}, false
	}
	values, err := url.ParseQuery(request.URL.RawQuery)
	if err != nil || len(values) < 1 || len(values) > 3 || values.Encode() != request.URL.RawQuery {
		return "", hubfleet.InventorySearchRequest{}, false
	}
	for key, entries := range values {
		if (key != "kind" && key != "name" && key != "namespace") || len(entries) != 1 {
			return "", hubfleet.InventorySearchRequest{}, false
		}
	}
	if len(values["kind"]) != 1 || values.Get("kind") == "" ||
		(len(values["name"]) == 1 && values.Get("name") == "") ||
		(len(values["namespace"]) == 1 && values.Get("namespace") == "") {
		return "", hubfleet.InventorySearchRequest{}, false
	}
	inventoryRequest := hubfleet.InventorySearchRequest{
		ResourceKind: values.Get("kind"), Namespace: values.Get("namespace"), Name: values.Get("name"), Limit: consoleInventoryReadLimit,
	}
	if inventoryRequest.Validate() != nil {
		return "", hubfleet.InventorySearchRequest{}, false
	}
	return workspaceID, inventoryRequest, true
}

func canonicalConsoleCVEIdentifierRequest(request *http.Request) (tenancy.WorkspaceID, hubfleet.CVEIdentifierSearchRequest, bool) {
	if request == nil || request.Method != http.MethodGet || request.URL == nil || request.URL.Fragment != "" ||
		request.URL.RawQuery == "" || len(request.URL.RawQuery) > 96 {
		return "", hubfleet.CVEIdentifierSearchRequest{}, false
	}
	workspaceID := tenancy.WorkspaceID(request.PathValue("workspace"))
	if tenancy.ValidateWorkspaceID(workspaceID) != nil {
		return "", hubfleet.CVEIdentifierSearchRequest{}, false
	}
	wantPath := "/v1/workspaces/" + url.PathEscape(string(workspaceID)) + "/console/cves"
	if request.URL.EscapedPath() != wantPath {
		return "", hubfleet.CVEIdentifierSearchRequest{}, false
	}
	values, err := url.ParseQuery(request.URL.RawQuery)
	if err != nil || len(values) != 1 || len(values["identifier"]) != 1 || values.Encode() != request.URL.RawQuery {
		return "", hubfleet.CVEIdentifierSearchRequest{}, false
	}
	identifier := values.Get("identifier")
	if len(identifier) > 64 {
		return "", hubfleet.CVEIdentifierSearchRequest{}, false
	}
	canonical, err := fleet.NormalizeCVEIdentifier(identifier)
	if err != nil || canonical != identifier {
		return "", hubfleet.CVEIdentifierSearchRequest{}, false
	}
	return workspaceID, hubfleet.CVEIdentifierSearchRequest{Identifier: identifier, Limit: consoleCVEReadLimit}, true
}

func projectConsoleCorrelation(
	result fleet.QueryResult,
	workspaceID tenancy.WorkspaceID,
	request hubfleet.CorrelationRequest,
) (consoleCorrelationResponse, error) {
	if workspaceID == "" || len(result.Facts) > consoleCorrelationMaxMatches {
		return consoleCorrelationResponse{}, fmt.Errorf("project console correlation: invalid result bounds")
	}
	coverage, err := projectConsoleCoverage(result.Coverage)
	if err != nil {
		return consoleCorrelationResponse{}, err
	}
	matches := make([]consoleHealthMatch, 0, len(result.Facts))
	seen := make(map[string]struct{}, len(result.Facts))
	for _, fact := range result.Facts {
		if fact.Workspace != string(workspaceID) || fact.Kind != fleet.FactHealth || fact.Ref.SourceKind != hubfleet.SourceKind ||
			fact.Source != fact.Ref.Scope || fact.Ref.Kind != request.ResourceKind || fact.Ref.Name != request.Name ||
			fact.Ref.Namespace != request.Namespace || fact.ObservedAt.IsZero() ||
			validateConsoleProjectionText(fact.Ref.Scope, 256, false) != nil ||
			validateConsoleProjectionText(fact.Ref.Kind, 128, false) != nil ||
			validateConsoleProjectionText(fact.Ref.Name, 256, false) != nil ||
			validateConsoleProjectionText(fact.Ref.Namespace, 256, true) != nil ||
			validateConsoleProjectionText(fact.StaleFor, 128, true) != nil || !validConsoleStaleFor(fact.Stale, fact.StaleFor) {
			return consoleCorrelationResponse{}, fmt.Errorf("project console correlation: invalid stored fact")
		}
		health, err := decodeConsoleHealth(fact.Observed)
		if err != nil || health == request.HealthNot {
			return consoleCorrelationResponse{}, fmt.Errorf("project console correlation: invalid stored health")
		}
		identity := strings.Join([]string{fact.Ref.Scope, fact.Ref.Kind, fact.Ref.Namespace, fact.Ref.Name}, "\x00")
		if _, exists := seen[identity]; exists {
			return consoleCorrelationResponse{}, fmt.Errorf("project console correlation: duplicate stored fact")
		}
		seen[identity] = struct{}{}
		matches = append(matches, consoleHealthMatch{
			Scope: fact.Ref.Scope, ResourceKind: fact.Ref.Kind, Namespace: fact.Ref.Namespace, Name: fact.Ref.Name,
			Health: health, ObservedAt: fact.ObservedAt.UTC(), Stale: fact.Stale, StaleFor: fact.StaleFor,
		})
	}
	return consoleCorrelationResponse{Matches: matches, Coverage: coverage, Assessment: coverage.Assessment()}, nil
}

func projectConsoleInventory(
	result fleet.QueryResult,
	workspaceID tenancy.WorkspaceID,
	request hubfleet.InventorySearchRequest,
) (consoleInventoryResponse, error) {
	if workspaceID == "" || len(result.Facts) > consoleInventoryMaxRecords {
		return consoleInventoryResponse{}, fmt.Errorf("project console inventory: invalid result bounds")
	}
	coverage, err := projectConsoleCoverage(result.Coverage)
	if err != nil {
		return consoleInventoryResponse{}, err
	}
	records := make([]consoleInventoryRecord, 0, len(result.Facts))
	seen := make(map[string]struct{}, len(result.Facts))
	for _, fact := range result.Facts {
		if fact.Workspace != string(workspaceID) || fact.Kind != fleet.FactInventory || fact.Ref.SourceKind != hubfleet.SourceKind ||
			fact.Source != fact.Ref.Scope || fact.Ref.Kind != request.ResourceKind ||
			(request.Namespace != "" && fact.Ref.Namespace != request.Namespace) ||
			(request.Name != "" && fact.Ref.Name != request.Name) || fact.ObservedAt.IsZero() ||
			len(fact.Ref.Attributes) != 0 || len(fact.Display) != 0 ||
			fact.Provenance.Adapter != hubfleet.SourceKind || fact.Provenance.ProtocolV != "1.0.0" ||
			fact.Provenance.NativeID != "" || fact.Provenance.DeepLink != "" || fact.Provenance.Collector != "" ||
			validateConsoleProjectionText(fact.Ref.Scope, 256, false) != nil ||
			validateConsoleProjectionText(fact.Ref.Kind, 128, false) != nil ||
			validateConsoleProjectionText(fact.Ref.Name, 256, false) != nil ||
			validateConsoleProjectionText(fact.Ref.Namespace, 256, true) != nil ||
			validateConsoleProjectionText(fact.StaleFor, 128, true) != nil || !validConsoleStaleFor(fact.Stale, fact.StaleFor) {
			return consoleInventoryResponse{}, fmt.Errorf("project console inventory: invalid stored fact")
		}
		identity := strings.Join([]string{fact.Ref.Scope, fact.Ref.Kind, fact.Ref.Namespace, fact.Ref.Name}, "\x00")
		if _, exists := seen[identity]; exists {
			return consoleInventoryResponse{}, fmt.Errorf("project console inventory: duplicate stored fact")
		}
		seen[identity] = struct{}{}
		record, err := decodeConsoleInventory(fact.Observed, fact.Ref.Kind)
		if err != nil {
			return consoleInventoryResponse{}, fmt.Errorf("project console inventory: invalid stored observation")
		}
		record.Scope = fact.Ref.Scope
		record.ResourceKind = fact.Ref.Kind
		record.Namespace = fact.Ref.Namespace
		record.Name = fact.Ref.Name
		record.ObservedAt = fact.ObservedAt.UTC()
		record.Stale = fact.Stale
		record.StaleFor = fact.StaleFor
		records = append(records, record)
	}
	return consoleInventoryResponse{Records: records, Coverage: coverage, Assessment: coverage.Assessment()}, nil
}

func projectConsoleCVEIdentifier(
	result fleet.QueryResult,
	workspaceID tenancy.WorkspaceID,
	request hubfleet.CVEIdentifierSearchRequest,
) (consoleCVEResponse, error) {
	if workspaceID == "" || len(result.Facts) > consoleCVEMaxRecords {
		return consoleCVEResponse{}, fmt.Errorf("project console CVE evidence: invalid result bounds")
	}
	coverage, err := projectConsoleCoverage(result.Coverage)
	if err != nil {
		return consoleCVEResponse{}, err
	}
	records := make([]consoleCVERecord, 0, len(result.Facts))
	seen := make(map[string]struct{}, len(result.Facts))
	for _, fact := range result.Facts {
		if fact.Workspace != string(workspaceID) || fact.Kind != fleet.FactCVE || fact.Ref.SourceKind != hubfleet.SourceKind ||
			fact.Source != fact.Ref.Scope || fact.Ref.Kind != "Image" || fact.Ref.Namespace != "" ||
			len(fact.Ref.Attributes) != 0 || len(fact.Display) != 0 || fact.ObservedAt.IsZero() ||
			fact.Provenance.Adapter != hubfleet.SourceKind || fact.Provenance.ProtocolV != "1.0.0" ||
			fact.Provenance.NativeID != "" || fact.Provenance.DeepLink != "" || fact.Provenance.Collector != "" ||
			validateConsoleProjectionText(fact.Ref.Scope, 256, false) != nil ||
			validateConsoleProjectionText(fact.StaleFor, 128, true) != nil || !validConsoleStaleFor(fact.Stale, fact.StaleFor) {
			return consoleCVEResponse{}, fmt.Errorf("project console CVE evidence: invalid stored fact")
		}
		if err := rejectConsoleDuplicateJSONMembers(fact.Observed); err != nil {
			return consoleCVEResponse{}, fmt.Errorf("project console CVE evidence: invalid stored observation")
		}
		decoder := json.NewDecoder(bytes.NewReader(fact.Observed))
		decoder.DisallowUnknownFields()
		var observation fleet.CVEObservation
		if err := decoder.Decode(&observation); err != nil || decoder.Decode(&struct{}{}) != io.EOF ||
			fleet.ValidateCVEObservation(observation) != nil || observation.Image != fact.Ref.Name {
			return consoleCVEResponse{}, fmt.Errorf("project console CVE evidence: invalid stored observation")
		}
		found := false
		for _, identifier := range observation.IDs {
			if identifier == request.Identifier {
				found = true
				break
			}
		}
		if !found {
			return consoleCVEResponse{}, fmt.Errorf("project console CVE evidence: stored observation does not match selector")
		}
		identity := fact.Ref.Scope + "\x00" + observation.Image + "\x00" + request.Identifier
		if _, exists := seen[identity]; exists {
			return consoleCVEResponse{}, fmt.Errorf("project console CVE evidence: duplicate stored fact")
		}
		seen[identity] = struct{}{}
		records = append(records, consoleCVERecord{
			Scope: fact.Ref.Scope, ImageDigest: observation.Image, Identifier: request.Identifier, Severity: observation.Severity,
			ObservedAt: fact.ObservedAt.UTC(), Stale: fact.Stale, StaleFor: fact.StaleFor,
		})
	}
	return consoleCVEResponse{Records: records, Coverage: coverage, Assessment: coverage.Assessment()}, nil
}

func decodeConsoleInventory(payload json.RawMessage, kind string) (consoleInventoryRecord, error) {
	if err := rejectConsoleDuplicateJSONMembers(payload); err != nil {
		return consoleInventoryRecord{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var observation struct {
		Resource          string          `json:"resource"`
		Replicas          *int64          `json:"replicas"`
		AvailableReplicas *int64          `json:"available_replicas"`
		Ready             *int64          `json:"ready"`
		Generation        *int64          `json:"generation"`
		ImageDigests      json.RawMessage `json:"image_digests"`
	}
	if err := decoder.Decode(&observation); err != nil {
		return consoleInventoryRecord{}, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF || observation.Resource != kind || observation.Generation == nil ||
		*observation.Generation < 0 || *observation.Generation > consoleMaximumSafeInteger {
		return consoleInventoryRecord{}, fmt.Errorf("inventory observation has an invalid envelope")
	}
	record := consoleInventoryRecord{Generation: *observation.Generation}
	switch kind {
	case "Deployment", "Rollout":
		if observation.Replicas == nil || observation.AvailableReplicas == nil || observation.Ready != nil || len(observation.ImageDigests) != 0 ||
			!validConsoleCount(*observation.Replicas) || !validConsoleCount(*observation.AvailableReplicas) {
			return consoleInventoryRecord{}, fmt.Errorf("workload inventory observation is invalid")
		}
		replicas, available := *observation.Replicas, *observation.AvailableReplicas
		record.Replicas = &replicas
		record.AvailableReplicas = &available
	case "Pod":
		if observation.Replicas != nil || observation.AvailableReplicas != nil || observation.Ready == nil ||
			(*observation.Ready != 0 && *observation.Ready != 1) {
			return consoleInventoryRecord{}, fmt.Errorf("pod inventory observation is invalid")
		}
		ready := *observation.Ready == 1
		record.Ready = &ready
		if len(observation.ImageDigests) != 0 {
			var digests []string
			if err := json.Unmarshal(observation.ImageDigests, &digests); err != nil || len(digests) == 0 || len(digests) > 64 {
				return consoleInventoryRecord{}, fmt.Errorf("pod inventory image digests are invalid")
			}
			for index, digest := range digests {
				if fleet.ValidateImageDigest(digest) != nil || (index > 0 && digests[index-1] >= digest) {
					return consoleInventoryRecord{}, fmt.Errorf("pod inventory image digests are invalid")
				}
			}
		}
	default:
		return consoleInventoryRecord{}, fmt.Errorf("inventory resource kind is unsupported")
	}
	return record, nil
}

func rejectConsoleDuplicateJSONMembers(payload json.RawMessage) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	first, err := decoder.Token()
	if err != nil {
		return err
	}
	if delimiter, ok := first.(json.Delim); !ok || delimiter != '{' {
		return fmt.Errorf("inventory observation must be an object")
	}
	seen := make(map[string]struct{})
	for decoder.More() {
		member, err := decoder.Token()
		if err != nil {
			return err
		}
		name, ok := member.(string)
		if !ok {
			return fmt.Errorf("inventory observation member is invalid")
		}
		if _, exists := seen[name]; exists {
			return fmt.Errorf("inventory observation contains a duplicate member")
		}
		seen[name] = struct{}{}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return err
		}
	}
	if _, err := decoder.Token(); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return fmt.Errorf("inventory observation contains trailing data")
	}
	return nil
}

func validConsoleCount(value int64) bool {
	return value >= 0 && value <= consoleMaximumSafeInteger
}

func validConsoleStaleFor(stale bool, value string) bool {
	if !stale {
		return value == ""
	}
	if value == "collection failed" {
		return true
	}
	duration, err := time.ParseDuration(value)
	return err == nil && duration >= time.Second && duration.String() == value
}

func projectConsoleCoverage(coverage fleet.Coverage) (fleet.Coverage, error) {
	if coverage.Requested < 0 || coverage.Reachable < 0 {
		return fleet.Coverage{}, fmt.Errorf("project console correlation: invalid coverage counts")
	}
	count := len(coverage.Unreachable) + len(coverage.Stale) + len(coverage.Truncated)
	if count > consoleCorrelationMaxCoverageScopes {
		return fleet.Coverage{}, fmt.Errorf("project console correlation: coverage exceeds response bound")
	}
	unreachable, err := projectConsoleCoverageScopes(coverage.Unreachable)
	if err != nil {
		return fleet.Coverage{}, err
	}
	stale, err := projectConsoleCoverageScopes(coverage.Stale)
	if err != nil {
		return fleet.Coverage{}, err
	}
	truncated, err := projectConsoleCoverageScopes(coverage.Truncated)
	if err != nil {
		return fleet.Coverage{}, err
	}
	return fleet.Coverage{
		Requested: coverage.Requested, Reachable: coverage.Reachable,
		Unreachable: unreachable, Stale: stale, Truncated: truncated,
	}, nil
}

func projectConsoleCoverageScopes(scopes []string) ([]string, error) {
	projected := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		if err := validateConsoleProjectionText(scope, 256, false); err != nil {
			return nil, fmt.Errorf("project console correlation: invalid coverage scope")
		}
		projected = append(projected, scope)
	}
	return projected, nil
}

func decodeConsoleHealth(payload json.RawMessage) (string, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var observation struct {
		Status string `json:"status"`
	}
	if err := decoder.Decode(&observation); err != nil {
		return "", err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return "", fmt.Errorf("health observation contains trailing data")
	}
	switch observation.Status {
	case "Healthy", "Degraded", "Progressing", "Unknown":
		return observation.Status, nil
	default:
		return "", fmt.Errorf("health observation contains an unsupported status")
	}
}

func validateConsoleProjectionText(value string, maximum int, allowEmpty bool) error {
	if value == "" && allowEmpty {
		return nil
	}
	if value == "" || len(value) > maximum || strings.TrimSpace(value) != value {
		return fmt.Errorf("invalid projected text")
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return fmt.Errorf("invalid projected text")
		}
	}
	return nil
}

func sameOriginFetch(values []string) bool {
	return len(values) == 0 || (len(values) == 1 && values[0] == "same-origin")
}

func (handler *ConsoleHandler) newCSRFToken(rawSession string, workspaceID tenancy.WorkspaceID, domain string) (string, error) {
	payload := make([]byte, consoleCSRFPayloadBytes)
	payload[0] = consoleCSRFVersion
	expiresAt := strconv.FormatInt(handler.now().UTC().Add(handler.csrfLifetime).Unix(), 10)
	if len(expiresAt) != consoleCSRFTimeBytes {
		return "", fmt.Errorf("generate console CSRF token")
	}
	copy(payload[1:1+consoleCSRFTimeBytes], expiresAt)
	handler.randomMu.Lock()
	_, err := io.ReadFull(handler.random, payload[1+consoleCSRFTimeBytes:])
	handler.randomMu.Unlock()
	if err != nil {
		return "", fmt.Errorf("generate console CSRF token")
	}
	mac := handler.csrfMAC(payload, rawSession, workspaceID, domain)
	return base64.RawURLEncoding.EncodeToString(append(payload, mac...)), nil
}

func (handler *ConsoleHandler) validCSRFToken(values []string, rawSession string, workspaceID tenancy.WorkspaceID, domain string) bool {
	if handler == nil || len(values) != 1 || values[0] == "" || strings.TrimSpace(values[0]) != values[0] {
		return false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(values[0])
	if err != nil || len(decoded) != consoleCSRFTokenBytes || decoded[0] != consoleCSRFVersion {
		return false
	}
	expiresUnix, err := strconv.ParseInt(string(decoded[1:1+consoleCSRFTimeBytes]), 10, 64)
	if err != nil {
		return false
	}
	now := handler.now().UTC()
	expiresAt := time.Unix(expiresUnix, 0).UTC()
	if !expiresAt.After(now) || expiresAt.After(now.Add(handler.csrfLifetime)) {
		return false
	}
	payload := decoded[:consoleCSRFPayloadBytes]
	expected := handler.csrfMAC(payload, rawSession, workspaceID, domain)
	return subtle.ConstantTimeCompare(decoded[consoleCSRFPayloadBytes:], expected) == 1
}

func (handler *ConsoleHandler) csrfMAC(payload []byte, rawSession string, workspaceID tenancy.WorkspaceID, domain string) []byte {
	sessionDigest := sha256.Sum256([]byte(rawSession))
	mac := hmac.New(sha256.New, handler.csrfKey[:])
	_, _ = mac.Write([]byte(domain))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write(payload)
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write(sessionDigest[:])
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(workspaceID))
	return mac.Sum(nil)
}

func setConsoleSecurityHeaders(header http.Header) {
	setNoStore(header)
	header.Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self'; font-src 'none'; object-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'; media-src 'none'; worker-src 'none'; manifest-src 'none'")
	header.Set("Permissions-Policy", "camera=(), geolocation=(), microphone=(), payment=(), usb=()")
	header.Set("Referrer-Policy", "no-referrer")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("X-Frame-Options", "DENY")
}

func writeConsoleJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}

func writeConsoleError(response http.ResponseWriter, status int, code string) {
	writeConsoleJSON(response, status, struct {
		Error string `json:"error"`
	}{Error: code})
}
