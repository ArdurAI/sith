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
	googleTestSubject      = "107517467455664443765"
	googleTestOrganization = "123456789012"
)

func TestGoogleServiceAccountVerifierExchangesPinnedIdentity(t *testing.T) {
	now := time.Date(2026, 7, 12, 19, 0, 0, 0, time.UTC)
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	server := newGoogleTestServer(t, rsaJWK("google-key", &privateKey.PublicKey))
	defer server.Close()
	verifier := newGoogleTestVerifier(t, server, &now)
	token := signGoogleTestToken(t, server.URL, privateKey, now, nil, nil)
	principal, err := verifier.Verify(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	if principal.Identity != (CloudIdentity{Provider: CloudProviderGCP, Realm: googleTestOrganization, Subject: googleTestSubject}) || principal.Audience != "https://hub.sith.test" {
		t.Fatalf("principal = %#v", principal)
	}
	service, _, _, admin := newCloudTestFixture(t, &now, nil)
	service.verifiers[CloudProviderGCP] = verifier
	if err := service.BindIdentity(context.Background(), admin, principal.Identity, "user:alice"); err != nil {
		t.Fatal(err)
	}
	if session, err := service.Exchange(context.Background(), "workspace-a", CloudProviderGCP, token); err != nil || session.AccessToken == "" {
		t.Fatalf("bound Google exchange = %#v, error = %v", session, err)
	}
	if _, err := service.Exchange(context.Background(), "workspace-a", CloudProviderGCP, token); err == nil {
		t.Fatal("replayed Google token minted a second session")
	}
}

func TestGoogleServiceAccountVerifierRejectsConfusedTokens(t *testing.T) {
	now := time.Date(2026, 7, 12, 19, 0, 0, 0, time.UTC)
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	server := newGoogleTestServer(t, rsaJWK("google-key", &privateKey.PublicKey))
	defer server.Close()
	verifier := newGoogleTestVerifier(t, server, &now)
	for name, mutate := range map[string]func(jwt.MapClaims){
		"wrong issuer":       func(c jwt.MapClaims) { c["iss"] = "https://accounts.google.com" },
		"wrong audience":     func(c jwt.MapClaims) { c["aud"] = "https://other.sith.test" },
		"multiple audiences": func(c jwt.MapClaims) { c["aud"] = []string{"https://hub.sith.test", "https://other.sith.test"} },
		"wrong realm": func(c jwt.MapClaims) {
			c["google"] = map[string]any{"organization_number": json.Number("999999999999")}
		},
		"quoted realm": func(c jwt.MapClaims) {
			c["google"] = map[string]any{"organization_number": googleTestOrganization}
		},
		"missing realm":             func(c jwt.MapClaims) { delete(c, "google") },
		"unverified email":          func(c jwt.MapClaims) { c["email_verified"] = false },
		"non-service-account email": func(c jwt.MapClaims) { c["email"] = "alice@example.com" },
		"empty service domain":      func(c jwt.MapClaims) { c["email"] = "sith@.gserviceaccount.com" },
		"authorized party mismatch": func(c jwt.MapClaims) { c["azp"] = "999999999999999999999" },
		"non-numeric subject":       func(c jwt.MapClaims) { c["sub"] = "service@example.iam.gserviceaccount.com" },
		"expired":                   func(c jwt.MapClaims) { c["exp"] = now.Add(-time.Minute).Unix() },
		"overlong token lifetime":   func(c jwt.MapClaims) { c["exp"] = now.Add(2 * time.Hour).Unix() },
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := verifier.Verify(context.Background(), signGoogleTestToken(t, server.URL, privateKey, now, mutate, nil)); err == nil {
				t.Fatal("unsafe Google token accepted")
			}
		})
	}
	for name, mutateHeader := range map[string]func(map[string]any){
		"wrong type": func(header map[string]any) { header["typ"] = "at+jwt" },
		"no key ID":  func(header map[string]any) { delete(header, "kid") },
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := verifier.Verify(context.Background(), signGoogleTestToken(t, server.URL, privateKey, now, nil, mutateHeader)); err == nil {
				t.Fatal("unsafe Google token header accepted")
			}
		})
	}
	unsigned := jwt.NewWithClaims(jwt.SigningMethodNone, googleTestClaims(server.URL, now))
	unsigned.Header["typ"] = "JWT"
	unsigned.Header["kid"] = "google-key"
	rawUnsigned, err := unsigned.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifier.Verify(context.Background(), rawUnsigned); err == nil {
		t.Fatal("unsigned Google token accepted")
	}
}

func TestGoogleServiceAccountVerifierRejectsEndpointFallbackAndUnsafeConfiguration(t *testing.T) {
	valid := GoogleServiceAccountVerifierConfig{
		Issuer: googlePublicIssuer, JWKSURL: googlePublicJWKSURL, Audience: "https://hub.sith.test", OrganizationNumber: googleTestOrganization,
	}
	if _, err := NewGoogleServiceAccountVerifier(valid); err != nil {
		t.Fatalf("valid public Google configuration rejected: %v", err)
	}
	for name, mutate := range map[string]func(*GoogleServiceAccountVerifierConfig){
		"issuer fallback":   func(c *GoogleServiceAccountVerifierConfig) { c.Issuer = "https://accounts.google.com/" },
		"issuer escape":     func(c *GoogleServiceAccountVerifierConfig) { c.Issuer = "https://evil.example" },
		"alternate JWKS":    func(c *GoogleServiceAccountVerifierConfig) { c.JWKSURL = "https://www.googleapis.com/oauth2/v1/certs" },
		"private JWKS":      func(c *GoogleServiceAccountVerifierConfig) { c.JWKSURL = "https://127.0.0.1/jwks" },
		"JWKS query":        func(c *GoogleServiceAccountVerifierConfig) { c.JWKSURL = googlePublicJWKSURL + "?fallback=1" },
		"non-numeric realm": func(c *GoogleServiceAccountVerifierConfig) { c.OrganizationNumber = "organization-a" },
		"empty audience":    func(c *GoogleServiceAccountVerifierConfig) { c.Audience = "" },
	} {
		t.Run(name, func(t *testing.T) {
			config := valid
			mutate(&config)
			if _, err := NewGoogleServiceAccountVerifier(config); err == nil {
				t.Fatal("unsafe Google configuration accepted")
			}
		})
	}
}

func newGoogleTestServer(t *testing.T, key oidcJWK) *httptest.Server {
	t.Helper()
	return httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/jwks" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(oidcJWKSet{Keys: []oidcJWK{key}})
	}))
}

func newGoogleTestVerifier(t *testing.T, server *httptest.Server, now *time.Time) *GoogleServiceAccountVerifier {
	t.Helper()
	transport := server.Client().Transport.(*http.Transport)
	verifier, err := newGoogleServiceAccountVerifier(GoogleServiceAccountVerifierConfig{
		Issuer: server.URL, JWKSURL: server.URL + "/jwks", Audience: "https://hub.sith.test", OrganizationNumber: googleTestOrganization,
		RootCAs: transport.TLSClientConfig.RootCAs, Now: func() time.Time { return *now },
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	return verifier
}

func googleTestClaims(issuer string, now time.Time) jwt.MapClaims {
	return jwt.MapClaims{
		"iss": issuer, "aud": "https://hub.sith.test", "sub": googleTestSubject, "azp": googleTestSubject,
		"email": "sith-hub@project-a.iam.gserviceaccount.com", "email_verified": true,
		"google": map[string]any{"organization_number": json.Number(googleTestOrganization)},
		"iat":    now.Unix(), "exp": now.Add(30 * time.Minute).Unix(),
	}
}

func signGoogleTestToken(
	t *testing.T,
	issuer string,
	privateKey *rsa.PrivateKey,
	now time.Time,
	mutateClaims func(jwt.MapClaims),
	mutateHeader func(map[string]any),
) string {
	t.Helper()
	claims := googleTestClaims(issuer, now)
	if mutateClaims != nil {
		mutateClaims(claims)
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = "google-key"
	token.Header["typ"] = "JWT"
	if mutateHeader != nil {
		mutateHeader(token.Header)
	}
	raw, err := token.SignedString(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
