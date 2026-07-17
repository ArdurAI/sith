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

	"github.com/ArdurAI/sith/internal/fleet"
	"github.com/ArdurAI/sith/internal/hubfleet"
	"github.com/ArdurAI/sith/internal/pep"
	"github.com/ArdurAI/sith/internal/tenancy"
)

const (
	consoleCSRFHeader          = "X-Sith-CSRF"
	consoleCSRFVersion         = byte(1)
	consoleCSRFTimeBytes       = 10
	consoleCSRFRandomBytes     = 32
	consoleCSRFKeyBytes        = 32
	consoleCSRFDigestBytes     = sha256.Size
	consoleCSRFPayloadBytes    = 1 + consoleCSRFTimeBytes + consoleCSRFRandomBytes
	consoleCSRFTokenBytes      = consoleCSRFPayloadBytes + consoleCSRFDigestBytes
	consoleCSRFDomain          = "sith-hub-console-fleet/v1"
	defaultConsoleCSRFLifetime = 5 * time.Minute
	maximumConsoleCSRFLifetime = 10 * time.Minute
)

//go:embed console_assets/*
var embeddedConsoleAssets embed.FS

// ConsoleHandlerConfig supplies only the authenticated read dependencies for the Hub console.
type ConsoleHandlerConfig struct {
	Verifier     Verifier
	AuthObserver AuthObserver
	Reader       hubfleet.FleetReader
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
	Workspace string
	CSRFToken string
}

type consoleFleetResponse struct {
	Fleet      fleet.FleetResult        `json:"fleet"`
	Assessment fleet.CoverageAssessment `json:"assessment"`
}

// NewConsoleHandler constructs a separate cookie-authenticated read surface. It does not alter
// bearer API authentication and exposes no refresh, connector, local-operation, or write seam.
func NewConsoleHandler(config ConsoleHandlerConfig) (*ConsoleHandler, error) {
	if config.Verifier == nil || config.Reader == nil || config.PEP == nil {
		return nil, fmt.Errorf("construct Hub console: verifier, reader, and policy enforcer are required")
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
		verifier: config.Verifier, authObserver: config.AuthObserver, reader: config.Reader, pep: config.PEP,
		csrfLifetime: config.CSRFLifetime, now: config.Now, random: config.Random, page: page, assets: assets,
	}
	if _, err := io.ReadFull(handler.random, handler.csrfKey[:]); err != nil {
		return nil, fmt.Errorf("construct Hub console: CSRF key generation failed")
	}
	return handler, nil
}

// ServePage returns the authenticated shell and one session/workspace-bound CSRF token.
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
	csrfToken, err := handler.newCSRFToken(rawSession, scope.WorkspaceID())
	if err != nil {
		writeConsoleError(response, http.StatusServiceUnavailable, "console_unavailable")
		return
	}
	var rendered bytes.Buffer
	if err := handler.page.Execute(&rendered, consolePageData{Workspace: string(scope.WorkspaceID()), CSRFToken: csrfToken}); err != nil {
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
	if !sameOriginFetch(request.Header.Values("Sec-Fetch-Site")) || !handler.validCSRFToken(request.Header.Values(consoleCSRFHeader), rawSession, scope.WorkspaceID()) {
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

func sameOriginFetch(values []string) bool {
	return len(values) == 0 || (len(values) == 1 && values[0] == "same-origin")
}

func (handler *ConsoleHandler) newCSRFToken(rawSession string, workspaceID tenancy.WorkspaceID) (string, error) {
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
	mac := handler.csrfMAC(payload, rawSession, workspaceID)
	return base64.RawURLEncoding.EncodeToString(append(payload, mac...)), nil
}

func (handler *ConsoleHandler) validCSRFToken(values []string, rawSession string, workspaceID tenancy.WorkspaceID) bool {
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
	expected := handler.csrfMAC(payload, rawSession, workspaceID)
	return subtle.ConstantTimeCompare(decoded[consoleCSRFPayloadBytes:], expected) == 1
}

func (handler *ConsoleHandler) csrfMAC(payload []byte, rawSession string, workspaceID tenancy.WorkspaceID) []byte {
	sessionDigest := sha256.Sum256([]byte(rawSession))
	mac := hmac.New(sha256.New, handler.csrfKey[:])
	_, _ = mac.Write([]byte(consoleCSRFDomain))
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
