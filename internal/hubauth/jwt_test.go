// SPDX-License-Identifier: Apache-2.0

package hubauth

import (
	"context"
	"crypto/ed25519"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/ArdurAI/sith/internal/tenancy"
)

const (
	testIssuer   = "https://issuer.sith.test"
	testAudience = "https://hub.sith.test"
	testKeyID    = "session-2026-07"
)

func TestJWTVerifierAcceptsOnlyStrictSessionProfile(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	publicKey, privateKey := testKeyPair()
	verifier, err := NewJWTVerifier(JWTConfig{
		Issuer: testIssuer, Audience: testAudience, Keys: map[string]ed25519.PublicKey{testKeyID: publicKey}, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	valid := validClaims(now)
	tests := []struct {
		name   string
		claims sessionClaims
		header func(map[string]any)
		key    ed25519.PrivateKey
		wantOK bool
	}{
		{name: "valid", claims: valid, key: privateKey, wantOK: true},
		{name: "expired", claims: mutateClaims(valid, func(claims *sessionClaims) { claims.ExpiresAt = jwt.NewNumericDate(now.Add(-time.Second)) }), key: privateKey},
		{name: "wrong issuer", claims: mutateClaims(valid, func(claims *sessionClaims) { claims.Issuer = "https://evil.test" }), key: privateKey},
		{name: "wrong audience", claims: mutateClaims(valid, func(claims *sessionClaims) { claims.Audience = jwt.ClaimStrings{"https://other.test"} }), key: privateKey},
		{name: "missing expiry", claims: mutateClaims(valid, func(claims *sessionClaims) { claims.ExpiresAt = nil }), key: privateKey},
		{name: "not active", claims: mutateClaims(valid, func(claims *sessionClaims) { claims.NotBefore = jwt.NewNumericDate(now.Add(time.Minute)) }), key: privateKey},
		{name: "missing issued at", claims: mutateClaims(valid, func(claims *sessionClaims) { claims.IssuedAt = nil }), key: privateKey},
		{name: "missing token ID", claims: mutateClaims(valid, func(claims *sessionClaims) { claims.ID = "" }), key: privateKey},
		{name: "missing subject", claims: mutateClaims(valid, func(claims *sessionClaims) { claims.Subject = "" }), key: privateKey},
		{name: "no memberships", claims: mutateClaims(valid, func(claims *sessionClaims) { claims.Memberships = nil }), key: privateKey},
		{name: "unknown role", claims: mutateClaims(valid, func(claims *sessionClaims) { claims.Memberships["workspace-a"] = "owner" }), key: privateKey},
		{name: "wrong type", claims: valid, header: func(header map[string]any) { header["typ"] = "JWT" }, key: privateKey},
		{name: "missing type", claims: valid, header: func(header map[string]any) { delete(header, "typ") }, key: privateKey},
		{name: "unknown key", claims: valid, header: func(header map[string]any) { header["kid"] = "unknown" }, key: privateKey},
		{name: "missing key ID", claims: valid, header: func(header map[string]any) { delete(header, "kid") }, key: privateKey},
		{name: "remote key URL", claims: valid, header: func(header map[string]any) { header["jku"] = "https://evil.test/keys" }, key: privateKey},
		{name: "remote certificate URL", claims: valid, header: func(header map[string]any) { header["x5u"] = "https://evil.test/cert" }, key: privateKey},
		{name: "unsupported critical header", claims: valid, header: func(header map[string]any) { header["crit"] = []string{"custom"} }, key: privateKey},
		{name: "forged signature", claims: valid, key: ed25519.NewKeyFromSeed([]byte(strings.Repeat("b", ed25519.SeedSize)))},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			rawToken := signToken(t, test.claims, test.key, test.header)
			principal, err := verifier.Verify(context.Background(), rawToken)
			if test.wantOK {
				if err != nil {
					t.Fatalf("Verify() error = %v", err)
				}
				scope, scopeErr := principal.Scope("workspace-a")
				if scopeErr != nil || scope.Role() != tenancy.RoleReader {
					t.Fatalf("scope role = %q, error = %v", scope.Role(), scopeErr)
				}
				return
			}
			if err == nil {
				t.Fatalf("Verify() accepted principal %#v", principal)
			}
		})
	}
}

func TestJWTVerifierRejectsAlgorithmSubstitution(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	publicKey, _ := testKeyPair()
	verifier, err := NewJWTVerifier(JWTConfig{
		Issuer: testIssuer, Audience: testAudience, Keys: map[string]ed25519.PublicKey{testKeyID: publicKey}, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, validClaims(now))
	token.Header["typ"] = defaultJWTType
	token.Header["kid"] = testKeyID
	rawToken, err := token.SignedString([]byte(publicKey))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifier.Verify(context.Background(), rawToken); err == nil {
		t.Fatal("algorithm-substituted token unexpectedly verified")
	}
}

func TestJWTVerifierRejectsCanceledAndOversizedInputs(t *testing.T) {
	t.Parallel()

	publicKey, _ := testKeyPair()
	verifier, err := NewJWTVerifier(JWTConfig{Issuer: testIssuer, Audience: testAudience, Keys: map[string]ed25519.PublicKey{testKeyID: publicKey}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := verifier.Verify(ctx, "not-a-token"); err == nil {
		t.Fatal("canceled verification unexpectedly succeeded")
	}
	if _, err := verifier.Verify(context.Background(), strings.Repeat("x", maxTokenBytes+1)); err == nil {
		t.Fatal("oversized token unexpectedly verified")
	}
}

func TestNewJWTVerifierRejectsUnsafeConfiguration(t *testing.T) {
	t.Parallel()

	publicKey, _ := testKeyPair()
	tests := []JWTConfig{
		{Audience: testAudience, Keys: map[string]ed25519.PublicKey{testKeyID: publicKey}},
		{Issuer: testIssuer, Keys: map[string]ed25519.PublicKey{testKeyID: publicKey}},
		{Issuer: testIssuer, Audience: testAudience},
		{Issuer: testIssuer, Audience: testAudience, Keys: map[string]ed25519.PublicKey{"": publicKey}},
		{Issuer: testIssuer, Audience: testAudience, Keys: map[string]ed25519.PublicKey{testKeyID: {1, 2, 3}}},
		{Issuer: testIssuer, Audience: testAudience, Keys: map[string]ed25519.PublicKey{testKeyID: publicKey}, Leeway: time.Minute + time.Second},
	}
	for index, config := range tests {
		if _, err := NewJWTVerifier(config); err == nil {
			t.Errorf("unsafe config %d unexpectedly accepted", index)
		}
	}
}

func TestJWTVerifierCopiesKeys(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	publicKey, privateKey := testKeyPair()
	verifier, err := NewJWTVerifier(JWTConfig{
		Issuer: testIssuer, Audience: testAudience, Keys: map[string]ed25519.PublicKey{testKeyID: publicKey}, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	clear(publicKey)
	if _, err := verifier.Verify(context.Background(), signToken(t, validClaims(now), privateKey, nil)); err != nil {
		t.Fatalf("mutating caller key changed verifier: %v", err)
	}
}

func validClaims(now time.Time) sessionClaims {
	return sessionClaims{
		Memberships: map[string]tenancy.Role{"workspace-a": tenancy.RoleReader},
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer: testIssuer, Subject: "user:alice", Audience: jwt.ClaimStrings{testAudience},
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)), NotBefore: jwt.NewNumericDate(now.Add(-time.Minute)),
			IssuedAt: jwt.NewNumericDate(now.Add(-time.Minute)), ID: "session-1",
		},
	}
}

func mutateClaims(original sessionClaims, mutate func(*sessionClaims)) sessionClaims {
	cloned := original
	cloned.Memberships = make(map[string]tenancy.Role, len(original.Memberships))
	for workspaceID, role := range original.Memberships {
		cloned.Memberships[workspaceID] = role
	}
	mutate(&cloned)
	return cloned
}

func testKeyPair() (ed25519.PublicKey, ed25519.PrivateKey) {
	privateKey := ed25519.NewKeyFromSeed([]byte(strings.Repeat("a", ed25519.SeedSize)))
	publicKey := append(ed25519.PublicKey(nil), privateKey.Public().(ed25519.PublicKey)...)
	return publicKey, privateKey
}

func signToken(t *testing.T, claims sessionClaims, privateKey ed25519.PrivateKey, mutateHeader func(map[string]any)) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	token.Header["typ"] = defaultJWTType
	token.Header["kid"] = testKeyID
	if mutateHeader != nil {
		mutateHeader(token.Header)
	}
	rawToken, err := token.SignedString(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return rawToken
}
