// SPDX-License-Identifier: Apache-2.0

package hubserver

import (
	"crypto/ed25519"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/ArdurAI/sith/internal/hubauth"
	"github.com/ArdurAI/sith/internal/tenancy"
)

const (
	hubTestIssuer   = "https://issuer.sith.test"
	hubTestAudience = "https://hub.sith.test"
	hubTestKeyID    = "session-2026-07"
)

type hubTestClaims struct {
	Memberships map[string]tenancy.Role `json:"memberships"`
	jwt.RegisteredClaims
}

func TestAuthenticateIgnoresInjectedIdentityHeaders(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	publicKey, privateKey := hubTestKeyPair()
	verifier, err := hubauth.NewJWTVerifier(hubauth.JWTConfig{
		Issuer: hubTestIssuer, Audience: hubTestAudience, Keys: map[string]ed25519.PublicKey{hubTestKeyID: publicKey}, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	next := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "" {
			t.Fatal("bearer credential reached downstream handler")
		}
		for _, name := range []string{"X-Sith-Role", "X-User-Tenant", "X-Forwarded-User", "X-Workspace"} {
			if value := request.Header.Get(name); value != "" {
				t.Errorf("untrusted header %s reached handler: %q", name, value)
			}
		}
		scope, scopeErr := ScopeFromContext(request.Context(), "workspace-a")
		if scopeErr != nil || scope.Role() != tenancy.RoleReader {
			t.Fatalf("signed scope role = %q, error = %v", scope.Role(), scopeErr)
		}
		if _, scopeErr := ScopeFromContext(request.Context(), "workspace-b"); scopeErr == nil {
			t.Fatal("injected tenant header granted a foreign workspace scope")
		}
		response.WriteHeader(http.StatusNoContent)
	})
	handler, err := Authenticate(verifier, next)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "https://hub.sith.test/api", nil)
	request.Header.Set("Authorization", "Bearer "+signHubTestToken(t, hubValidClaims(now), privateKey))
	request.Header.Set("X-Sith-Role", "admin")
	request.Header.Set("X-User-Tenant", "workspace-b")
	request.Header.Set("X-Forwarded-User", "mallory")
	request.Header.Set("X-Workspace", "workspace-b")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
	}
	if request.Header.Get("X-Sith-Role") != "admin" {
		t.Fatal("authentication middleware mutated the caller's request")
	}
	if request.Header.Get("Authorization") == "" {
		t.Fatal("authentication middleware removed the credential from the caller's request")
	}
}

func TestAuthenticateRejectsMissingForgedAndAmbiguousBearerTokens(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	publicKey, privateKey := hubTestKeyPair()
	verifier, err := hubauth.NewJWTVerifier(hubauth.JWTConfig{
		Issuer: hubTestIssuer, Audience: hubTestAudience, Keys: map[string]ed25519.PublicKey{hubTestKeyID: publicKey}, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := Authenticate(verifier, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("unauthorized request reached handler")
	}))
	if err != nil {
		t.Fatal(err)
	}
	valid := signHubTestToken(t, hubValidClaims(now), privateKey)
	forgedKey := ed25519.NewKeyFromSeed([]byte(strings.Repeat("d", ed25519.SeedSize)))
	forged := signHubTestToken(t, hubValidClaims(now), forgedKey)
	tests := []struct {
		name   string
		values []string
	}{
		{name: "missing"},
		{name: "wrong scheme", values: []string{"Basic " + valid}},
		{name: "multiple headers", values: []string{"Bearer " + valid, "Bearer " + valid}},
		{name: "comma joined", values: []string{"Bearer " + valid + ",Bearer " + valid}},
		{name: "extra whitespace", values: []string{"Bearer  " + valid}},
		{name: "forged", values: []string{"Bearer " + forged}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			request := httptest.NewRequest(http.MethodGet, "https://hub.sith.test/api", nil)
			for _, value := range test.values {
				request.Header.Add("Authorization", value)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusUnauthorized || response.Body.String() != "{\"error\":\"unauthorized\"}\n" {
				t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
			}
			if response.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("unauthorized response may be cached")
			}
		})
	}
}

func FuzzBearerTokenNeverAcceptsMetadata(f *testing.F) {
	f.Add("Bearer token")
	f.Add("bearer abc.def.ghi")
	f.Add("Bearer token,other")
	f.Fuzz(func(t *testing.T, value string) {
		token, ok := bearerToken([]string{value})
		if ok && (token == "" || len(token) > maxBearerTokenBytes) {
			t.Fatalf("accepted invalid token length %d", len(token))
		}
	})
}

func hubValidClaims(now time.Time) hubTestClaims {
	return hubTestClaims{
		Memberships: map[string]tenancy.Role{"workspace-a": tenancy.RoleReader},
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer: hubTestIssuer, Subject: "user:alice", Audience: jwt.ClaimStrings{hubTestAudience},
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)), NotBefore: jwt.NewNumericDate(now.Add(-time.Minute)),
			IssuedAt: jwt.NewNumericDate(now.Add(-time.Minute)), ID: "session-1",
		},
	}
}

func hubTestKeyPair() (ed25519.PublicKey, ed25519.PrivateKey) {
	privateKey := ed25519.NewKeyFromSeed([]byte(strings.Repeat("c", ed25519.SeedSize)))
	publicKey := append(ed25519.PublicKey(nil), privateKey.Public().(ed25519.PublicKey)...)
	return publicKey, privateKey
}

func signHubTestToken(t *testing.T, claims hubTestClaims, privateKey ed25519.PrivateKey) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	token.Header["typ"] = "sith-session+jwt"
	token.Header["kid"] = hubTestKeyID
	rawToken, err := token.SignedString(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return rawToken
}
