// SPDX-License-Identifier: Apache-2.0

package hubauth

import (
	"crypto/ed25519"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ArdurAI/sith/internal/tenancy"
)

func TestAuthenticateIgnoresInjectedIdentityHeaders(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	publicKey, privateKey := testKeyPair()
	verifier, err := NewJWTVerifier(JWTConfig{
		Issuer: testIssuer, Audience: testAudience, Keys: map[string]ed25519.PublicKey{testKeyID: publicKey}, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	next := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
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
	request.Header.Set("Authorization", "Bearer "+signToken(t, validClaims(now), privateKey, nil))
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
}

func TestAuthenticateRejectsMissingForgedAndAmbiguousBearerTokens(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	publicKey, privateKey := testKeyPair()
	verifier, err := NewJWTVerifier(JWTConfig{
		Issuer: testIssuer, Audience: testAudience, Keys: map[string]ed25519.PublicKey{testKeyID: publicKey}, Now: func() time.Time { return now },
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
	valid := signToken(t, validClaims(now), privateKey, nil)
	tests := []struct {
		name   string
		values []string
	}{
		{name: "missing"},
		{name: "wrong scheme", values: []string{"Basic " + valid}},
		{name: "multiple headers", values: []string{"Bearer " + valid, "Bearer " + valid}},
		{name: "comma joined", values: []string{"Bearer " + valid + ",Bearer " + valid}},
		{name: "extra whitespace", values: []string{"Bearer  " + valid}},
		{name: "forged", values: []string{"Bearer " + valid[:len(valid)-1] + "A"}},
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
		if ok && (token == "" || len(token) > maxTokenBytes) {
			t.Fatalf("accepted invalid token length %d", len(token))
		}
	})
}
