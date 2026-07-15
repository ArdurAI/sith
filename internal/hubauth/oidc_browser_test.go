// SPDX-License-Identifier: Apache-2.0

package hubauth

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestOIDCBrowserCodeFlowPinsPKCEAndNonce(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	upstreamPublic, upstreamPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	provider := newOIDCTestProvider(t, []oidcJWK{ed25519JWK("browser-key", upstreamPublic)})
	service, _, verifier, admin := newOIDCTestFixture(t, provider, &now)
	if err := service.BindIdentity(context.Background(), admin, provider.server.URL, "upstream:alice", "user:alice"); err != nil {
		t.Fatal(err)
	}
	state := browserTestValue(0x11)
	nonce := browserTestValue(0x22)
	verifierValue := strings.Repeat("v", 43)
	challengeDigest := sha256.Sum256([]byte(verifierValue))
	challenge := base64.RawURLEncoding.EncodeToString(challengeDigest[:])
	provider.mu.Lock()
	provider.metadata = func(issuer string) string {
		return `{"issuer":` + quoteJSON(issuer) + `,"jwks_uri":` + quoteJSON(issuer+"/jwks") +
			`,"authorization_endpoint":` + quoteJSON(issuer+"/authorize") + `,"token_endpoint":` + quoteJSON(issuer+"/token") + `}`
	}
	provider.token = func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
			t.Errorf("token request method/content type = %s/%q", request.Method, request.Header.Get("Content-Type"))
			http.Error(response, "invalid", http.StatusBadRequest)
			return
		}
		if err := request.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if request.Form.Get("grant_type") != "authorization_code" || request.Form.Get("client_id") != "sith-browser" ||
			request.Form.Get("redirect_uri") != "https://hub.sith.test/v1/console/oidc/callback" ||
			request.Form.Get("code") != "provider-code" || request.Form.Get("code_verifier") != verifierValue {
			t.Errorf("token request form = %#v", request.Form)
			http.Error(response, "invalid", http.StatusBadRequest)
			return
		}
		raw := signOIDCTestToken(t, provider.server.URL, "sith-hub", "upstream:alice", "browser-key", upstreamPrivate, now, func(claims jwt.MapClaims) {
			claims["nonce"] = nonce
		})
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"id_token":` + quoteJSON(raw) + `}`))
	}
	provider.mu.Unlock()

	authorizationURL, err := service.BrowserAuthorizationURL(context.Background(), OIDCBrowserAuthorizationRequest{
		Issuer: provider.server.URL, ClientID: "sith-browser", RedirectURI: "https://hub.sith.test/v1/console/oidc/callback",
		State: state, Nonce: nonce, CodeChallenge: challenge,
	})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(authorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Path != "/authorize" || parsed.Query().Get("response_type") != "code" || parsed.Query().Get("scope") != "openid" ||
		parsed.Query().Get("state") != state || parsed.Query().Get("nonce") != nonce || parsed.Query().Get("code_challenge") != challenge ||
		parsed.Query().Get("code_challenge_method") != "S256" || parsed.Query().Get("code_verifier") != "" {
		t.Fatalf("authorization query = %#v", parsed.Query())
	}
	rawToken, err := service.ExchangeAuthorizationCode(context.Background(), OIDCBrowserCodeExchange{
		Issuer: provider.server.URL, ClientID: "sith-browser", RedirectURI: "https://hub.sith.test/v1/console/oidc/callback",
		Code: "provider-code", CodeVerifier: verifierValue,
	})
	if err != nil {
		t.Fatal(err)
	}
	session, err := service.ExchangeWithNonce(context.Background(), "workspace-a", rawToken, nonce)
	if err != nil {
		t.Fatal(err)
	}
	principal, err := verifier.Verify(context.Background(), session.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := principal.Scope("workspace-a")
	if err != nil || scope.Subject() != "user:alice" {
		t.Fatalf("browser scope = %#v, error = %v", scope, err)
	}
	if _, err := service.ExchangeWithNonce(context.Background(), "workspace-a", rawToken, browserTestValue(0x33)); !errors.Is(err, ErrInvalidOIDCToken) {
		t.Fatalf("wrong nonce error = %v", err)
	}
	wrongAudience := signOIDCTestToken(t, provider.server.URL, "other-client", "upstream:alice", "browser-key", upstreamPrivate, now, func(claims jwt.MapClaims) {
		claims["nonce"] = nonce
	})
	if _, err := service.ExchangeWithNonce(context.Background(), "workspace-a", wrongAudience, nonce); !errors.Is(err, ErrInvalidOIDCToken) {
		t.Fatalf("wrong audience error = %v", err)
	}
	multipleAudiences := signOIDCTestToken(t, provider.server.URL, "sith-hub", "upstream:alice", "browser-key", upstreamPrivate, now, func(claims jwt.MapClaims) {
		claims["nonce"] = nonce
		claims["aud"] = []string{"sith-hub", "other-client"}
		claims["azp"] = "sith-hub"
	})
	if _, err := service.ExchangeWithNonce(context.Background(), "workspace-a", multipleAudiences, nonce); !errors.Is(err, ErrInvalidOIDCToken) {
		t.Fatalf("multiple browser audiences error = %v", err)
	}
	if _, err := service.ExchangeWithNonce(context.Background(), "workspace-b", rawToken, nonce); !errors.Is(err, ErrInvalidOIDCToken) {
		t.Fatalf("cross-workspace browser exchange error = %v", err)
	}
	withoutNotBefore := signOIDCTestToken(t, provider.server.URL, "sith-hub", "upstream:alice", "browser-key", upstreamPrivate, now, func(claims jwt.MapClaims) {
		claims["nonce"] = nonce
		delete(claims, "nbf")
	})
	if _, err := service.ExchangeWithNonce(context.Background(), "workspace-a", withoutNotBefore, nonce); err != nil {
		t.Fatalf("standards-compliant browser ID token without nbf rejected: %v", err)
	}
}

func TestOIDCBrowserCodeExchangeFailsClosed(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	upstreamPublic, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	provider := newOIDCTestProvider(t, []oidcJWK{ed25519JWK("browser-key", upstreamPublic)})
	service, _, _, _ := newOIDCTestFixture(t, provider, &now)
	provider.mu.Lock()
	provider.metadata = func(issuer string) string {
		return `{"issuer":` + quoteJSON(issuer) + `,"jwks_uri":` + quoteJSON(issuer+"/jwks") +
			`,"authorization_endpoint":` + quoteJSON(issuer+"/authorize") + `,"token_endpoint":` + quoteJSON(issuer+"/token") + `}`
	}
	provider.token = func(response http.ResponseWriter, request *http.Request) {
		if err := request.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if request.Form.Get("code") == "wrong-verifier" {
			http.Error(response, "invalid verifier", http.StatusBadRequest)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		if request.Form.Get("code") == "malformed" {
			_, _ = response.Write([]byte("not JSON"))
			return
		}
		_, _ = response.Write([]byte(`{"id_token":"one","id_token":"two"}`))
	}
	provider.mu.Unlock()

	request := OIDCBrowserCodeExchange{
		Issuer: provider.server.URL, ClientID: "sith-browser", RedirectURI: "https://hub.sith.test/v1/console/oidc/callback",
		Code: "provider-code", CodeVerifier: strings.Repeat("v", 43),
	}
	if _, err := service.ExchangeAuthorizationCode(context.Background(), request); !errors.Is(err, ErrInvalidOIDCBrowserLogin) {
		t.Fatalf("duplicate token response error = %v", err)
	}
	request.Code = "wrong-verifier"
	request.CodeVerifier = strings.Repeat("w", 43)
	if _, err := service.ExchangeAuthorizationCode(context.Background(), request); !errors.Is(err, ErrInvalidOIDCBrowserLogin) {
		t.Fatalf("PKCE verifier mismatch error = %v", err)
	}
	request.Code = "malformed"
	request.CodeVerifier = strings.Repeat("v", 43)
	if _, err := service.ExchangeAuthorizationCode(context.Background(), request); !errors.Is(err, ErrInvalidOIDCBrowserLogin) {
		t.Fatalf("malformed token response error = %v", err)
	}
	request.CodeVerifier = "plain"
	if _, err := service.ExchangeAuthorizationCode(context.Background(), request); !errors.Is(err, ErrInvalidOIDCBrowserLogin) {
		t.Fatalf("plain PKCE verifier error = %v", err)
	}
	if _, err := service.BrowserAuthorizationURL(context.Background(), OIDCBrowserAuthorizationRequest{
		Issuer: provider.server.URL, ClientID: "sith-browser", RedirectURI: "https://hub.sith.test/v1/console/oidc/callback",
		State: browserTestValue(0x11), Nonce: browserTestValue(0x22), CodeChallenge: "plain",
	}); !errors.Is(err, ErrInvalidOIDCBrowserLogin) {
		t.Fatalf("plain PKCE challenge error = %v", err)
	}
	if _, err := service.BrowserAuthorizationURL(context.Background(), OIDCBrowserAuthorizationRequest{
		Issuer: "https://other.sith.test", ClientID: "sith-browser", RedirectURI: "https://hub.sith.test/v1/console/oidc/callback",
		State: browserTestValue(0x11), Nonce: browserTestValue(0x22), CodeChallenge: browserTestValue(0x33),
	}); !errors.Is(err, ErrInvalidOIDCBrowserLogin) {
		t.Fatalf("wrong issuer error = %v", err)
	}
	provider.mu.Lock()
	provider.outage = true
	provider.mu.Unlock()
	request.Code = "provider-code"
	request.CodeVerifier = strings.Repeat("v", 43)
	if _, err := service.ExchangeAuthorizationCode(context.Background(), request); !errors.Is(err, ErrInvalidOIDCBrowserLogin) {
		t.Fatalf("provider outage error = %v", err)
	}
}

func browserTestValue(fill byte) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat(string([]byte{fill}), oidcBrowserValueBytes)))
}

func quoteJSON(value string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(value, `\`, `\\`), `"`, `\"`) + `"`
}
