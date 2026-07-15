// SPDX-License-Identifier: Apache-2.0

package hubserver

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/ArdurAI/sith/internal/hubauth"
	"github.com/ArdurAI/sith/internal/tenancy"
)

const (
	browserOIDCTransactionCookie  = "__Host-sith-oidc-tx"
	browserOIDCSessionCookie      = "__Host-sith-session"
	browserOIDCRandomBytes        = 32
	defaultBrowserOIDCTTL         = 5 * time.Minute
	defaultBrowserOIDCMaxSessions = 256
	maximumBrowserOIDCSessions    = 4096
)

// BrowserOIDCService is the server-side OIDC code-flow boundary. It never returns a token to a
// browser response; BrowserOIDCHandler stores the resulting Sith session only in an HttpOnly cookie.
type BrowserOIDCService interface {
	BrowserAuthorizationURL(context.Context, hubauth.OIDCBrowserAuthorizationRequest) (string, error)
	ExchangeAuthorizationCode(context.Context, hubauth.OIDCBrowserCodeExchange) (string, error)
	ExchangeWithNonce(context.Context, tenancy.WorkspaceID, string, string) (hubauth.IssuedSession, error)
}

// BrowserOIDCHandlerConfig fixes one OIDC issuer, public client, and callback URL for a Hub.
type BrowserOIDCHandlerConfig struct {
	Service         BrowserOIDCService
	ProviderIssuer  string
	ClientID        string
	RedirectURI     string
	Limiter         *AttemptLimiter
	TransactionTTL  time.Duration
	MaxTransactions int
	Now             func() time.Time
	Random          io.Reader
}

// BrowserOIDCHandler owns bounded, restart-ephemeral authorization-code transactions.
type BrowserOIDCHandler struct {
	service        BrowserOIDCService
	providerIssuer string
	clientID       string
	redirectURI    string
	callbackURL    *url.URL
	limiter        *AttemptLimiter
	transactionTTL time.Duration
	maxSessions    int
	now            func() time.Time
	random         io.Reader

	mu           sync.Mutex
	transactions map[string]browserOIDCTransaction
}

type browserOIDCTransaction struct {
	workspaceID  tenancy.WorkspaceID
	state        string
	nonce        string
	codeVerifier string
	expiresAt    time.Time
}

// NewBrowserOIDCHandler constructs explicit login and callback handlers without a cookie-auth API.
func NewBrowserOIDCHandler(config BrowserOIDCHandlerConfig) (*BrowserOIDCHandler, error) {
	if config.Service == nil || config.Limiter == nil || config.ProviderIssuer == "" || strings.TrimSpace(config.ProviderIssuer) != config.ProviderIssuer ||
		config.ClientID == "" || strings.TrimSpace(config.ClientID) != config.ClientID || len(config.ClientID) > 256 {
		return nil, fmt.Errorf("construct browser OIDC handler: service, limiter, issuer, and client ID are required")
	}
	callbackURL, err := parseBrowserOIDCRedirectURI(config.RedirectURI)
	if err != nil {
		return nil, fmt.Errorf("construct browser OIDC handler: callback URL is invalid")
	}
	if config.TransactionTTL == 0 {
		config.TransactionTTL = defaultBrowserOIDCTTL
	}
	if config.TransactionTTL < time.Second || config.TransactionTTL > 10*time.Minute {
		return nil, fmt.Errorf("construct browser OIDC handler: transaction lifetime must be between one second and ten minutes")
	}
	if config.MaxTransactions == 0 {
		config.MaxTransactions = defaultBrowserOIDCMaxSessions
	}
	if config.MaxTransactions < 1 || config.MaxTransactions > maximumBrowserOIDCSessions {
		return nil, fmt.Errorf("construct browser OIDC handler: transaction capacity is invalid")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.Random == nil {
		config.Random = rand.Reader
	}
	return &BrowserOIDCHandler{
		service: config.Service, providerIssuer: config.ProviderIssuer, clientID: config.ClientID, redirectURI: config.RedirectURI,
		callbackURL: callbackURL, limiter: config.Limiter, transactionTTL: config.TransactionTTL, maxSessions: config.MaxTransactions,
		now: config.Now, random: config.Random, transactions: make(map[string]browserOIDCTransaction),
	}, nil
}

// CallbackPath is the exact path that must be registered with the Hub's HTTPS mux.
func (handler *BrowserOIDCHandler) CallbackPath() string {
	if handler == nil || handler.callbackURL == nil {
		return ""
	}
	return handler.callbackURL.Path
}

// Login starts one code+PKCE transaction for the workspace encoded in the fixed login route.
func (handler *BrowserOIDCHandler) Login(response http.ResponseWriter, request *http.Request) {
	if !handler.acceptRequest(response, request, true) {
		return
	}
	workspaceID, ok := browserOIDCWorkspace(request.URL.Path)
	if !ok {
		browserOIDCFailure(response, http.StatusNotFound)
		return
	}
	transactionID, err := handler.randomValue()
	if err != nil {
		browserOIDCFailure(response, http.StatusServiceUnavailable)
		return
	}
	state, err := handler.randomValue()
	if err != nil {
		browserOIDCFailure(response, http.StatusServiceUnavailable)
		return
	}
	nonce, err := handler.randomValue()
	if err != nil {
		browserOIDCFailure(response, http.StatusServiceUnavailable)
		return
	}
	codeVerifier, err := handler.randomValue()
	if err != nil {
		browserOIDCFailure(response, http.StatusServiceUnavailable)
		return
	}
	expiresAt := handler.now().UTC().Add(handler.transactionTTL)
	if !handler.storeTransaction(transactionID, browserOIDCTransaction{
		workspaceID: workspaceID, state: state, nonce: nonce, codeVerifier: codeVerifier, expiresAt: expiresAt,
	}) {
		browserOIDCFailure(response, http.StatusTooManyRequests)
		return
	}
	challenge := sha256.Sum256([]byte(codeVerifier))
	authorizationURL, err := handler.service.BrowserAuthorizationURL(request.Context(), hubauth.OIDCBrowserAuthorizationRequest{
		Issuer: handler.providerIssuer, ClientID: handler.clientID, RedirectURI: handler.redirectURI,
		State: state, Nonce: nonce, CodeChallenge: base64.RawURLEncoding.EncodeToString(challenge[:]),
	})
	if err != nil {
		handler.deleteTransaction(transactionID)
		browserOIDCFailure(response, http.StatusServiceUnavailable)
		return
	}
	browserOIDCHeaders(response.Header())
	http.SetCookie(response, &http.Cookie{
		Name: browserOIDCTransactionCookie, Value: transactionID, Path: "/", Secure: true, HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Expires: expiresAt, MaxAge: max(1, int(handler.transactionTTL.Seconds())),
	})
	response.Header().Set("Location", authorizationURL)
	response.WriteHeader(http.StatusFound)
}

// Callback consumes one transaction and sets the final strict host-only session cookie on success.
func (handler *BrowserOIDCHandler) Callback(response http.ResponseWriter, request *http.Request) {
	if !handler.acceptRequest(response, request, false) {
		return
	}
	query, err := url.ParseQuery(request.URL.RawQuery)
	if err != nil || len(query["state"]) != 1 || query.Get("state") == "" {
		handler.clearTransactionCookie(response)
		browserOIDCFailure(response, http.StatusUnauthorized)
		return
	}
	cookie, err := request.Cookie(browserOIDCTransactionCookie)
	if err != nil || cookie.Value == "" {
		handler.clearTransactionCookie(response)
		browserOIDCFailure(response, http.StatusUnauthorized)
		return
	}
	transaction, ok := handler.takeTransaction(cookie.Value, query.Get("state"))
	if !ok {
		handler.clearTransactionCookie(response)
		browserOIDCFailure(response, http.StatusUnauthorized)
		return
	}
	if len(query) != 2 || len(query["code"]) != 1 || query.Get("code") == "" {
		handler.clearTransactionCookie(response)
		browserOIDCFailure(response, http.StatusUnauthorized)
		return
	}
	if !handler.limiter.Allow(browserOIDCClientAddress(request.RemoteAddr)) {
		handler.clearTransactionCookie(response)
		browserOIDCFailure(response, http.StatusTooManyRequests)
		return
	}
	rawToken, err := handler.service.ExchangeAuthorizationCode(request.Context(), hubauth.OIDCBrowserCodeExchange{
		Issuer: handler.providerIssuer, ClientID: handler.clientID, RedirectURI: handler.redirectURI,
		Code: query.Get("code"), CodeVerifier: transaction.codeVerifier,
	})
	if err != nil {
		handler.clearTransactionCookie(response)
		browserOIDCFailure(response, http.StatusUnauthorized)
		return
	}
	session, err := handler.service.ExchangeWithNonce(request.Context(), transaction.workspaceID, rawToken, transaction.nonce)
	if err != nil || session.TokenType != "Bearer" || session.AccessToken == "" || !session.ExpiresAt.After(handler.now()) {
		handler.clearTransactionCookie(response)
		browserOIDCFailure(response, http.StatusUnauthorized)
		return
	}
	handler.clearTransactionCookie(response)
	browserOIDCHeaders(response.Header())
	remaining := int(session.ExpiresAt.Sub(handler.now()).Seconds())
	http.SetCookie(response, &http.Cookie{
		Name: browserOIDCSessionCookie, Value: session.AccessToken, Path: "/", Secure: true, HttpOnly: true,
		SameSite: http.SameSiteStrictMode, Expires: session.ExpiresAt.UTC(), MaxAge: max(1, remaining),
	})
	response.WriteHeader(http.StatusNoContent)
}

func (handler *BrowserOIDCHandler) acceptRequest(response http.ResponseWriter, request *http.Request, login bool) bool {
	if handler == nil || request == nil || request.Method != http.MethodGet || request.URL == nil || request.URL.Fragment != "" ||
		!strings.EqualFold(request.Host, handler.callbackURL.Host) || (login && request.URL.RawQuery != "") || (!login && request.URL.Path != handler.callbackURL.Path) {
		browserOIDCFailure(response, http.StatusNotFound)
		return false
	}
	if !handler.limiter.Allow(browserOIDCClientAddress(request.RemoteAddr)) {
		browserOIDCFailure(response, http.StatusTooManyRequests)
		return false
	}
	return true
}

func (handler *BrowserOIDCHandler) randomValue() (string, error) {
	value := make([]byte, browserOIDCRandomBytes)
	if _, err := io.ReadFull(handler.random, value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func (handler *BrowserOIDCHandler) storeTransaction(identifier string, transaction browserOIDCTransaction) bool {
	handler.mu.Lock()
	defer handler.mu.Unlock()
	now := handler.now().UTC()
	for candidate, stored := range handler.transactions {
		if !now.Before(stored.expiresAt) {
			delete(handler.transactions, candidate)
		}
	}
	if _, exists := handler.transactions[identifier]; exists || len(handler.transactions) >= handler.maxSessions {
		return false
	}
	handler.transactions[identifier] = transaction
	return true
}

func (handler *BrowserOIDCHandler) takeTransaction(identifier, state string) (browserOIDCTransaction, bool) {
	handler.mu.Lock()
	defer handler.mu.Unlock()
	transaction, exists := handler.transactions[identifier]
	if !exists {
		return browserOIDCTransaction{}, false
	}
	delete(handler.transactions, identifier)
	if !handler.now().UTC().Before(transaction.expiresAt) || subtle.ConstantTimeCompare([]byte(transaction.state), []byte(state)) != 1 {
		return browserOIDCTransaction{}, false
	}
	return transaction, true
}

func (handler *BrowserOIDCHandler) deleteTransaction(identifier string) {
	handler.mu.Lock()
	defer handler.mu.Unlock()
	delete(handler.transactions, identifier)
}

func (handler *BrowserOIDCHandler) clearTransactionCookie(response http.ResponseWriter) {
	http.SetCookie(response, &http.Cookie{
		Name: browserOIDCTransactionCookie, Value: "", Path: "/", Secure: true, HttpOnly: true,
		SameSite: http.SameSiteLaxMode, MaxAge: -1, Expires: time.Unix(1, 0).UTC(),
	})
}

func parseBrowserOIDCRedirectURI(rawURI string) (*url.URL, error) {
	parsed, err := url.Parse(rawURI)
	if err != nil || len(rawURI) > 2048 || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path == "" || parsed.Path == "/" || parsed.EscapedPath() != parsed.Path {
		return nil, fmt.Errorf("invalid callback URL")
	}
	return parsed, nil
}

func browserOIDCWorkspace(path string) (tenancy.WorkspaceID, bool) {
	const prefix = "/v1/workspaces/"
	const suffix = "/console/login"
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return "", false
	}
	segment := strings.TrimSuffix(strings.TrimPrefix(path, prefix), suffix)
	if segment == "" || strings.Contains(segment, "/") {
		return "", false
	}
	workspace, err := url.PathUnescape(segment)
	if err != nil || url.PathEscape(workspace) != segment || tenancy.ValidateWorkspaceID(tenancy.WorkspaceID(workspace)) != nil {
		return "", false
	}
	return tenancy.WorkspaceID(workspace), true
}

func browserOIDCClientAddress(remoteAddress string) string {
	host, _, err := net.SplitHostPort(remoteAddress)
	if err != nil || host == "" {
		return "unknown"
	}
	return host
}

func browserOIDCHeaders(header http.Header) {
	header.Set("Cache-Control", "no-store")
	header.Set("Pragma", "no-cache")
	header.Set("Referrer-Policy", "no-referrer")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("X-Frame-Options", "DENY")
	header.Set("Content-Security-Policy", "default-src 'none'; base-uri 'none'; frame-ancestors 'none'")
}

func browserOIDCFailure(response http.ResponseWriter, status int) {
	browserOIDCHeaders(response.Header())
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.WriteHeader(status)
	_, _ = response.Write([]byte("{\"error\":\"oidc_login_failed\"}\n"))
}
