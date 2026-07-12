// SPDX-License-Identifier: Apache-2.0

package hubauth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	entraTestTenant = "11111111-2222-3333-4444-555555555555"
	entraTestObject = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
)

func TestEntraVerifierAcceptsPinnedAppOnlyIdentity(t *testing.T) {
	now := time.Date(2026, 7, 12, 18, 0, 0, 0, time.UTC)
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	server := newEntraTestServer(t, rsaJWK("entra-key", &privateKey.PublicKey))
	verifier := newEntraTestVerifier(t, server, &now)
	token := signEntraTestToken(t, server.URL, privateKey, now, nil)
	principal, err := verifier.Verify(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	if principal.Identity != (CloudIdentity{Provider: CloudProviderAzure, Realm: entraTestTenant, Subject: entraTestObject}) || principal.Audience != "api://sith-hub" {
		t.Fatalf("principal = %#v", principal)
	}
	service, _, _, admin := newCloudTestFixture(t, &now, nil)
	service.verifiers[CloudProviderAzure] = verifier
	if err := service.BindIdentity(context.Background(), admin, principal.Identity, "user:alice"); err != nil {
		t.Fatal(err)
	}
	if session, err := service.Exchange(context.Background(), "workspace-a", CloudProviderAzure, token); err != nil || session.AccessToken == "" {
		t.Fatalf("bound Entra exchange = %#v, error = %v", session, err)
	}
	if _, err := service.Exchange(context.Background(), "workspace-a", CloudProviderAzure, token); err == nil {
		t.Fatal("replayed Entra token minted a second session")
	}
}

func TestEntraVerifierRejectsTenantAndWorkloadConfusion(t *testing.T) {
	now := time.Date(2026, 7, 12, 18, 0, 0, 0, time.UTC)
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	server := newEntraTestServer(t, rsaJWK("entra-key", &privateKey.PublicKey))
	verifier := newEntraTestVerifier(t, server, &now)
	for name, mutate := range map[string]func(jwt.MapClaims){
		"wrong tenant":        func(c jwt.MapClaims) { c["tid"] = "99999999-2222-3333-4444-555555555555" },
		"wrong audience":      func(c jwt.MapClaims) { c["aud"] = "api://other" },
		"delegated identity":  func(c jwt.MapClaims) { c["idtyp"] = "user" },
		"wrong token version": func(c jwt.MapClaims) { c["ver"] = "1.0" },
		"expired":             func(c jwt.MapClaims) { c["exp"] = now.Add(-time.Minute).Unix() },
		"wrong actor":         func(c jwt.MapClaims) { c["azp"] = "other-client" },
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := verifier.Verify(context.Background(), signEntraTestToken(t, server.URL, privateKey, now, mutate)); err == nil {
				t.Fatal("unsafe Entra token accepted")
			}
		})
	}
}

func TestEntraVerifierRejectsAuthorityFallbackAndUnsafeConfiguration(t *testing.T) {
	for _, authority := range []string{
		"https://login.microsoftonline.com/common", "https://login.microsoftonline.com/organizations",
		"https://evil.example", "https://login.microsoftonline.com/",
	} {
		if _, err := NewEntraVerifier(EntraVerifierConfig{Authority: authority, TenantID: entraTestTenant, Audience: "api://sith-hub"}); err == nil {
			t.Errorf("unsafe authority %q was accepted", authority)
		}
	}
	for _, value := range []string{"not-a-guid", "11111111-2222-3333-4444-55555555555z"} {
		if _, err := NewEntraVerifier(EntraVerifierConfig{Authority: "https://login.microsoftonline.com", TenantID: value, Audience: "api://sith-hub"}); err == nil {
			t.Errorf("unsafe tenant %q was accepted", value)
		}
	}
}

func newEntraTestServer(t *testing.T, key oidcJWK) *httptest.Server {
	t.Helper()
	return httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/" + entraTestTenant + "/v2.0/.well-known/openid-configuration":
			_ = json.NewEncoder(w).Encode(oidcDiscovery{Issuer: "https://" + r.Host + "/" + entraTestTenant + "/v2.0", JWKSURL: "https://" + r.Host + "/jwks"})
		case "/jwks":
			_ = json.NewEncoder(w).Encode(oidcJWKSet{Keys: []oidcJWK{key}})
		default:
			http.NotFound(w, r)
		}
	}))
}

func newEntraTestVerifier(t *testing.T, server *httptest.Server, now *time.Time) *EntraVerifier {
	t.Helper()
	transport := server.Client().Transport.(*http.Transport)
	verifier, err := newEntraVerifier(EntraVerifierConfig{Authority: server.URL, TenantID: entraTestTenant, Audience: "api://sith-hub", ActorID: "sith-client", RootCAs: transport.TLSClientConfig.RootCAs, Now: func() time.Time { return *now }}, true)
	if err != nil {
		t.Fatal(err)
	}
	return verifier
}

func signEntraTestToken(t *testing.T, authority string, privateKey *rsa.PrivateKey, now time.Time, mutate func(jwt.MapClaims)) string {
	t.Helper()
	claims := jwt.MapClaims{"iss": authority + "/" + entraTestTenant + "/v2.0", "aud": "api://sith-hub", "tid": entraTestTenant, "oid": entraTestObject, "idtyp": "app", "azp": "sith-client", "ver": "2.0", "iat": now.Unix(), "nbf": now.Unix(), "exp": now.Add(30 * time.Minute).Unix(), "sub": "ignored"}
	if mutate != nil {
		mutate(claims)
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = "entra-key"
	token.Header["typ"] = "JWT"
	raw, err := token.SignedString(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
