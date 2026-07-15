// SPDX-License-Identifier: Apache-2.0

package hubserver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/ArdurAI/sith/internal/hubauth"
	"github.com/ArdurAI/sith/internal/tenancy"
)

type browserOIDCServiceStub struct {
	authorization    hubauth.OIDCBrowserAuthorizationRequest
	exchange         hubauth.OIDCBrowserCodeExchange
	workspaceID      tenancy.WorkspaceID
	nonce            string
	authorizeCalls   int
	exchangeCalls    int
	sessionCalls     int
	rawToken         string
	session          hubauth.IssuedSession
	authorizationErr error
}

func (stub *browserOIDCServiceStub) BrowserAuthorizationURL(_ context.Context, request hubauth.OIDCBrowserAuthorizationRequest) (string, error) {
	stub.authorizeCalls++
	stub.authorization = request
	if stub.authorizationErr != nil {
		return "", stub.authorizationErr
	}
	return "https://idp.sith.test/authorize?provider=fixed", nil
}

func (stub *browserOIDCServiceStub) ExchangeAuthorizationCode(_ context.Context, request hubauth.OIDCBrowserCodeExchange) (string, error) {
	stub.exchangeCalls++
	stub.exchange = request
	return stub.rawToken, nil
}

func (stub *browserOIDCServiceStub) ExchangeWithNonce(
	_ context.Context,
	workspaceID tenancy.WorkspaceID,
	rawToken, nonce string,
) (hubauth.IssuedSession, error) {
	stub.sessionCalls++
	stub.workspaceID = workspaceID
	stub.nonce = nonce
	if rawToken != stub.rawToken {
		return hubauth.IssuedSession{}, errors.New("unexpected raw token")
	}
	return stub.session, nil
}

func TestBrowserOIDCHandlerKeepsTokensOutOfBrowserPayloads(t *testing.T) {
	now := time.Date(2026, 7, 15, 14, 0, 0, 0, time.UTC)
	stub := &browserOIDCServiceStub{
		rawToken: "upstream.id.token", session: hubauth.IssuedSession{
			AccessToken: "sith.signed.session", TokenType: "Bearer", ExpiresAt: now.Add(15 * time.Minute),
		},
	}
	handler := newBrowserOIDCTestHandler(t, stub, &now)
	login := httptest.NewRequest(http.MethodGet, "https://hub.sith.test/v1/workspaces/workspace-a/console/login", nil)
	login.RemoteAddr = "192.0.2.10:8443"
	loginResponse := httptest.NewRecorder()
	handler.Login(loginResponse, login)
	if loginResponse.Code != http.StatusFound || loginResponse.Header().Get("Location") != "https://idp.sith.test/authorize?provider=fixed" {
		t.Fatalf("login status/location = %d/%q", loginResponse.Code, loginResponse.Header().Get("Location"))
	}
	transactionCookie := requiredBrowserCookie(t, loginResponse.Result().Cookies(), browserOIDCTransactionCookie)
	if !transactionCookie.Secure || !transactionCookie.HttpOnly || transactionCookie.Path != "/" || transactionCookie.Domain != "" || transactionCookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("transaction cookie = %#v", transactionCookie)
	}
	state := browserOIDCTestValue(0x02)
	nonce := browserOIDCTestValue(0x03)
	verifier := browserOIDCTestValue(0x04)
	challenge := sha256.Sum256([]byte(verifier))
	if stub.authorizeCalls != 1 || stub.authorization.Issuer != "https://issuer.sith.test" || stub.authorization.ClientID != "sith-browser" ||
		stub.authorization.RedirectURI != "https://hub.sith.test/v1/console/oidc/callback" || stub.authorization.State != state ||
		stub.authorization.Nonce != nonce || stub.authorization.CodeChallenge != base64.RawURLEncoding.EncodeToString(challenge[:]) {
		t.Fatalf("authorization request = %#v", stub.authorization)
	}
	if strings.Contains(loginResponse.Body.String(), stub.rawToken) || strings.Contains(loginResponse.Body.String(), stub.session.AccessToken) {
		t.Fatalf("login body leaked a token: %q", loginResponse.Body.String())
	}

	callbackURL := "https://hub.sith.test/v1/console/oidc/callback?code=provider-code&state=" + url.QueryEscape(state)
	callback := httptest.NewRequest(http.MethodGet, callbackURL, nil)
	callback.RemoteAddr = login.RemoteAddr
	callback.AddCookie(transactionCookie)
	callbackResponse := httptest.NewRecorder()
	handler.Callback(callbackResponse, callback)
	if callbackResponse.Code != http.StatusNoContent || callbackResponse.Body.Len() != 0 {
		t.Fatalf("callback status/body = %d/%q", callbackResponse.Code, callbackResponse.Body.String())
	}
	sessionCookie := requiredBrowserCookie(t, callbackResponse.Result().Cookies(), browserOIDCSessionCookie)
	if !sessionCookie.Secure || !sessionCookie.HttpOnly || sessionCookie.Path != "/" || sessionCookie.Domain != "" || sessionCookie.SameSite != http.SameSiteStrictMode || sessionCookie.Value != stub.session.AccessToken {
		t.Fatalf("session cookie = %#v", sessionCookie)
	}
	if stub.exchangeCalls != 1 || stub.exchange.Code != "provider-code" || stub.exchange.CodeVerifier != verifier ||
		stub.workspaceID != "workspace-a" || stub.nonce != nonce || stub.sessionCalls != 1 {
		t.Fatalf("exchange/session = %#v/%q/%q/%d", stub.exchange, stub.workspaceID, stub.nonce, stub.sessionCalls)
	}
	if strings.Contains(callbackResponse.Body.String(), stub.rawToken) || strings.Contains(callbackResponse.Body.String(), stub.session.AccessToken) ||
		strings.Contains(callbackResponse.Header().Get("Location"), stub.rawToken) || strings.Contains(callbackResponse.Header().Get("Location"), stub.session.AccessToken) {
		t.Fatal("callback response leaked a raw token outside its HttpOnly cookie")
	}

	replay := httptest.NewRequest(http.MethodGet, callbackURL, nil)
	replay.RemoteAddr = login.RemoteAddr
	replay.AddCookie(transactionCookie)
	replayResponse := httptest.NewRecorder()
	handler.Callback(replayResponse, replay)
	if replayResponse.Code != http.StatusUnauthorized || stub.exchangeCalls != 1 || stub.sessionCalls != 1 {
		t.Fatalf("replayed callback status/calls = %d/%d/%d", replayResponse.Code, stub.exchangeCalls, stub.sessionCalls)
	}
}

func TestBrowserOIDCHandlerFailsClosedAndDoesNotAuthenticateBearerRoutes(t *testing.T) {
	now := time.Date(2026, 7, 15, 14, 0, 0, 0, time.UTC)
	stub := &browserOIDCServiceStub{rawToken: "upstream.id.token", session: hubauth.IssuedSession{AccessToken: "sith.signed.session", TokenType: "Bearer", ExpiresAt: now.Add(time.Minute)}}
	handler := newBrowserOIDCTestHandler(t, stub, &now)
	login := httptest.NewRequest(http.MethodGet, "https://hub.sith.test/v1/workspaces/workspace-a/console/login", nil)
	login.RemoteAddr = "192.0.2.11:8443"
	loginResponse := httptest.NewRecorder()
	handler.Login(loginResponse, login)
	transactionCookie := requiredBrowserCookie(t, loginResponse.Result().Cookies(), browserOIDCTransactionCookie)
	state := browserOIDCTestValue(0x02)
	wrongState := browserOIDCTestValue(0x09)
	wrongCallback := httptest.NewRequest(http.MethodGet, "https://hub.sith.test/v1/console/oidc/other?code=provider-code&state="+state, nil)
	wrongCallback.RemoteAddr = login.RemoteAddr
	wrongCallback.AddCookie(transactionCookie)
	wrongCallbackResponse := httptest.NewRecorder()
	handler.Callback(wrongCallbackResponse, wrongCallback)
	if wrongCallbackResponse.Code != http.StatusNotFound || stub.exchangeCalls != 0 {
		t.Fatalf("wrong callback status/calls = %d/%d", wrongCallbackResponse.Code, stub.exchangeCalls)
	}
	wrong := httptest.NewRequest(http.MethodGet, "https://hub.sith.test/v1/console/oidc/callback?code=provider-code&state="+wrongState, nil)
	wrong.RemoteAddr = login.RemoteAddr
	wrong.AddCookie(transactionCookie)
	wrongResponse := httptest.NewRecorder()
	handler.Callback(wrongResponse, wrong)
	if wrongResponse.Code != http.StatusUnauthorized || stub.exchangeCalls != 0 {
		t.Fatalf("wrong-state callback status/calls = %d/%d", wrongResponse.Code, stub.exchangeCalls)
	}
	if browserOIDCTestValue(0x01) == state {
		t.Fatal("test transaction values unexpectedly collided")
	}

	protected, err := Authenticate(authVerifierFunc(func(context.Context, string) (tenancy.Principal, error) {
		t.Fatal("cookie unexpectedly reached bearer verifier")
		return tenancy.Principal{}, nil
	}), http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("cookie unexpectedly reached bearer route")
	}))
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "https://hub.sith.test/v1/workspaces/workspace-a/fleet", nil)
	request.AddCookie(&http.Cookie{Name: browserOIDCSessionCookie, Value: stub.session.AccessToken})
	response := httptest.NewRecorder()
	protected.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("cookie-only bearer route status = %d", response.Code)
	}

	failing := &browserOIDCServiceStub{authorizationErr: errors.New("provider unavailable")}
	failingHandler := newBrowserOIDCTestHandler(t, failing, &now)
	failingLogin := httptest.NewRequest(http.MethodGet, "https://hub.sith.test/v1/workspaces/workspace-a/console/login", nil)
	failingLogin.RemoteAddr = "192.0.2.12:8443"
	failingResponse := httptest.NewRecorder()
	failingHandler.Login(failingResponse, failingLogin)
	if failingResponse.Code != http.StatusServiceUnavailable || len(failingHandler.transactions) != 0 {
		t.Fatalf("provider outage status/transactions = %d/%d", failingResponse.Code, len(failingHandler.transactions))
	}

	expiring := &browserOIDCServiceStub{rawToken: "upstream.id.token", session: stub.session}
	expiringHandler := newBrowserOIDCTestHandler(t, expiring, &now)
	expiringLogin := httptest.NewRequest(http.MethodGet, "https://hub.sith.test/v1/workspaces/workspace-a/console/login", nil)
	expiringLogin.RemoteAddr = "192.0.2.13:8443"
	expiringLoginResponse := httptest.NewRecorder()
	expiringHandler.Login(expiringLoginResponse, expiringLogin)
	expiringCookie := requiredBrowserCookie(t, expiringLoginResponse.Result().Cookies(), browserOIDCTransactionCookie)
	now = now.Add(6 * time.Minute)
	expired := httptest.NewRequest(http.MethodGet, "https://hub.sith.test/v1/console/oidc/callback?code=provider-code&state="+state, nil)
	expired.RemoteAddr = expiringLogin.RemoteAddr
	expired.AddCookie(expiringCookie)
	expiredResponse := httptest.NewRecorder()
	expiringHandler.Callback(expiredResponse, expired)
	if expiredResponse.Code != http.StatusUnauthorized || expiring.exchangeCalls != 0 {
		t.Fatalf("expired callback status/calls = %d/%d", expiredResponse.Code, expiring.exchangeCalls)
	}
}

func TestNewBrowserOIDCHandlerRejectsUnsafeConfiguration(t *testing.T) {
	limiter, err := NewAttemptLimiter(AttemptLimiterConfig{Attempts: 2, Window: time.Minute, MaxKeys: 2})
	if err != nil {
		t.Fatal(err)
	}
	for _, config := range []BrowserOIDCHandlerConfig{
		{},
		{Service: &browserOIDCServiceStub{}, Limiter: limiter, ProviderIssuer: "https://issuer.sith.test", ClientID: "client", RedirectURI: "http://hub.sith.test/callback"},
		{Service: &browserOIDCServiceStub{}, Limiter: limiter, ProviderIssuer: "https://issuer.sith.test", ClientID: "client", RedirectURI: "https://hub.sith.test/callback?query=forbidden"},
		{Service: &browserOIDCServiceStub{}, Limiter: limiter, ProviderIssuer: "https://issuer.sith.test", ClientID: "client", RedirectURI: "https://hub.sith.test/callback", MaxTransactions: maximumBrowserOIDCSessions + 1},
	} {
		if _, err := NewBrowserOIDCHandler(config); err == nil {
			t.Errorf("unsafe browser OIDC configuration accepted: %#v", config)
		}
	}
}

func newBrowserOIDCTestHandler(t *testing.T, service BrowserOIDCService, now *time.Time) *BrowserOIDCHandler {
	t.Helper()
	limiter, err := NewAttemptLimiter(AttemptLimiterConfig{Attempts: 8, Window: time.Minute, MaxKeys: 8, Now: func() time.Time { return *now }})
	if err != nil {
		t.Fatal(err)
	}
	random := bytes.NewReader(append(append(append(
		bytes.Repeat([]byte{0x01}, browserOIDCRandomBytes),
		bytes.Repeat([]byte{0x02}, browserOIDCRandomBytes)...),
		bytes.Repeat([]byte{0x03}, browserOIDCRandomBytes)...),
		bytes.Repeat([]byte{0x04}, browserOIDCRandomBytes)...))
	handler, err := NewBrowserOIDCHandler(BrowserOIDCHandlerConfig{
		Service: service, ProviderIssuer: "https://issuer.sith.test", ClientID: "sith-browser", RedirectURI: "https://hub.sith.test/v1/console/oidc/callback",
		Limiter: limiter, Now: func() time.Time { return *now }, Random: random,
	})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func browserOIDCTestValue(fill byte) string {
	return base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{fill}, browserOIDCRandomBytes))
}

func requiredBrowserCookie(t *testing.T, cookies []*http.Cookie, name string) *http.Cookie {
	t.Helper()
	for _, cookie := range cookies {
		if cookie.Name == name {
			return cookie
		}
	}
	t.Fatalf("missing %s cookie in %#v", name, cookies)
	return nil
}
