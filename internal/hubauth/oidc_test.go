// SPDX-License-Identifier: Apache-2.0

package hubauth

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/ArdurAI/sith/internal/tenancy"
)

type memoryOIDCStore struct {
	mu       sync.Mutex
	bindings map[string]string
	roles    map[tenancy.WorkspaceID]map[string]tenancy.Role
}

func (store *memoryOIDCStore) CreateOIDCBinding(_ context.Context, scope tenancy.Scope, issuer, upstreamSubject, memberSubject string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if scope.Role() != tenancy.RoleAdmin || !store.roles[scope.WorkspaceID()][memberSubject].Valid() {
		return errors.New("membership missing")
	}
	store.bindings[string(scope.WorkspaceID())+"\x00"+issuer+"\x00"+upstreamSubject] = memberSubject
	return nil
}

func (store *memoryOIDCStore) LookupOIDCMembership(_ context.Context, workspaceID tenancy.WorkspaceID, issuer, upstreamSubject string) (tenancy.Membership, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	memberSubject := store.bindings[string(workspaceID)+"\x00"+issuer+"\x00"+upstreamSubject]
	role := store.roles[workspaceID][memberSubject]
	if memberSubject == "" || !role.Valid() {
		return tenancy.Membership{}, ErrOIDCBindingNotFound
	}
	return tenancy.Membership{WorkspaceID: workspaceID, Subject: memberSubject, Role: role}, nil
}

type oidcTestProvider struct {
	server   *httptest.Server
	mu       sync.Mutex
	keys     []oidcJWK
	outage   bool
	metadata func(string) string
}

func newOIDCTestProvider(t *testing.T, keys []oidcJWK) *oidcTestProvider {
	t.Helper()
	provider := &oidcTestProvider{keys: keys}
	provider.server = httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		provider.mu.Lock()
		defer provider.mu.Unlock()
		if provider.outage {
			http.Error(response, "unavailable", http.StatusServiceUnavailable)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/.well-known/openid-configuration":
			if provider.metadata != nil {
				_, _ = response.Write([]byte(provider.metadata(provider.server.URL)))
				return
			}
			_ = json.NewEncoder(response).Encode(oidcDiscovery{Issuer: provider.server.URL, JWKSURL: provider.server.URL + "/jwks"})
		case "/jwks":
			_ = json.NewEncoder(response).Encode(oidcJWKSet{Keys: provider.keys})
		default:
			http.NotFound(response, request)
		}
	}))
	t.Cleanup(provider.server.Close)
	return provider
}

func TestOIDCExchangeUsesPinnedIdentityAndCurrentMembership(t *testing.T) {
	now := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	upstreamPublic, upstreamPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	provider := newOIDCTestProvider(t, []oidcJWK{ed25519JWK("upstream-1", upstreamPublic)})
	service, store, verifier, admin := newOIDCTestFixture(t, provider, &now)
	if err := service.BindIdentity(context.Background(), admin, provider.server.URL, "upstream:alice", "user:alice"); err != nil {
		t.Fatal(err)
	}
	raw := signOIDCTestToken(t, provider.server.URL, "sith-hub", "upstream:alice", "upstream-1", upstreamPrivate, now, nil)
	session, err := service.Exchange(context.Background(), "workspace-a", raw)
	if err != nil {
		t.Fatal(err)
	}
	principal, err := verifier.Verify(context.Background(), session.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := principal.Scope("workspace-a")
	if err != nil || scope.Subject() != "user:alice" || scope.Role() != tenancy.RoleReader {
		t.Fatalf("mapped scope = subject %q role %q, error = %v", scope.Subject(), scope.Role(), err)
	}
	if _, err := principal.Scope("workspace-b"); err == nil {
		t.Fatal("upstream workspace claim granted an unbound workspace")
	}
	store.mu.Lock()
	store.roles["workspace-a"]["user:alice"] = tenancy.RoleOperator
	store.mu.Unlock()
	session, err = service.Exchange(context.Background(), "workspace-a", raw)
	if err != nil {
		t.Fatal(err)
	}
	principal, err = verifier.Verify(context.Background(), session.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	scope, err = principal.Scope("workspace-a")
	if err != nil || scope.Role() != tenancy.RoleOperator {
		t.Fatalf("current mapped role = %q, error = %v", scope.Role(), err)
	}
	if _, err := service.Exchange(context.Background(), "workspace-b", raw); !errors.Is(err, ErrInvalidOIDCToken) {
		t.Fatalf("cross-workspace exchange error = %v", err)
	}
}

func TestOIDCExchangeRejectsHostileTokens(t *testing.T) {
	now := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, attackerKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	provider := newOIDCTestProvider(t, []oidcJWK{ed25519JWK("key-1", publicKey)})
	service, _, _, admin := newOIDCTestFixture(t, provider, &now)
	if err := service.BindIdentity(context.Background(), admin, provider.server.URL, "upstream:alice", "user:alice"); err != nil {
		t.Fatal(err)
	}
	mutateClaims := func(mutate func(jwt.MapClaims)) string {
		return signOIDCTestToken(t, provider.server.URL, "sith-hub", "upstream:alice", "key-1", privateKey, now, mutate)
	}
	tests := map[string]string{
		"wrong issuer":       mutateClaims(func(claims jwt.MapClaims) { claims["iss"] = "https://other.example" }),
		"wrong audience":     mutateClaims(func(claims jwt.MapClaims) { claims["aud"] = "other" }),
		"wrong subject":      mutateClaims(func(claims jwt.MapClaims) { claims["sub"] = "upstream:mallory" }),
		"expired":            mutateClaims(func(claims jwt.MapClaims) { claims["exp"] = now.Add(-time.Minute).Unix() }),
		"missing not before": mutateClaims(func(claims jwt.MapClaims) { delete(claims, "nbf") }),
		"future issued at":   mutateClaims(func(claims jwt.MapClaims) { claims["iat"] = now.Add(time.Minute).Unix() }),
		"excess lifetime":    mutateClaims(func(claims jwt.MapClaims) { claims["exp"] = now.Add(2 * time.Hour).Unix() }),
		"multiple audience":  mutateClaims(func(claims jwt.MapClaims) { claims["aud"] = []string{"sith-hub", "other"} }),
		"unknown key":        signOIDCTestToken(t, provider.server.URL, "sith-hub", "upstream:alice", "unknown", privateKey, now, nil),
		"forged key":         signOIDCTestToken(t, provider.server.URL, "sith-hub", "upstream:alice", "key-1", attackerKey, now, nil),
		"wrong algorithm":    signOIDCHSTestToken(t, provider.server.URL, now),
		"wrong type":         signOIDCTestTokenWithHeader(t, provider.server.URL, "sith-hub", "upstream:alice", "key-1", privateKey, now, map[string]any{"typ": "at+jwt"}),
	}
	// Replace the helper marker with an actual forbidden JOSE header.
	tests["hostile jku"] = signOIDCTestTokenWithHeader(t, provider.server.URL, "sith-hub", "upstream:alice", "key-1", privateKey, now, map[string]any{"jku": "https://evil.example/jwks"})
	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := service.Exchange(context.Background(), "workspace-a", raw); !errors.Is(err, ErrInvalidOIDCToken) {
				t.Fatalf("hostile token error = %v", err)
			}
		})
	}
	multiAudience := mutateClaims(func(claims jwt.MapClaims) {
		claims["aud"] = []string{"sith-hub", "other"}
		claims["azp"] = "sith-hub"
	})
	if _, err := service.Exchange(context.Background(), "workspace-a", multiAudience); err != nil {
		t.Fatalf("multi-audience token with pinned azp rejected: %v", err)
	}
	duplicate := duplicateIssuerOIDCToken(t, provider.server.URL, privateKey, now)
	for _, raw := range []string{duplicate, strings.Repeat("x", maxUpstreamTokenBytes+1), "not.a.jwt.extra"} {
		if _, err := service.Exchange(context.Background(), "workspace-a", raw); !errors.Is(err, ErrInvalidOIDCToken) {
			t.Fatalf("malformed token error = %v", err)
		}
	}
}

func TestOIDCJWKSRotationAndOutageFailClosed(t *testing.T) {
	now := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	publicOne, privateOne, _ := ed25519.GenerateKey(rand.Reader)
	publicTwo, privateTwo, _ := ed25519.GenerateKey(rand.Reader)
	provider := newOIDCTestProvider(t, []oidcJWK{ed25519JWK("key-1", publicOne)})
	service, _, _, admin := newOIDCTestFixture(t, provider, &now)
	if err := service.BindIdentity(context.Background(), admin, provider.server.URL, "upstream:alice", "user:alice"); err != nil {
		t.Fatal(err)
	}
	tokenOne := signOIDCTestToken(t, provider.server.URL, "sith-hub", "upstream:alice", "key-1", privateOne, now, nil)
	if _, err := service.Exchange(context.Background(), "workspace-a", tokenOne); err != nil {
		t.Fatal(err)
	}
	provider.mu.Lock()
	provider.keys = []oidcJWK{ed25519JWK("key-2", publicTwo)}
	provider.mu.Unlock()
	tokenTwo := signOIDCTestToken(t, provider.server.URL, "sith-hub", "upstream:alice", "key-2", privateTwo, now, nil)
	if _, err := service.Exchange(context.Background(), "workspace-a", tokenTwo); err != nil {
		t.Fatalf("rotated key exchange: %v", err)
	}
	if _, err := service.Exchange(context.Background(), "workspace-a", tokenOne); !errors.Is(err, ErrInvalidOIDCToken) {
		t.Fatalf("retired key exchange error = %v", err)
	}
	provider.mu.Lock()
	provider.outage = true
	provider.mu.Unlock()
	now = now.Add(defaultOIDCCacheTTL)
	if _, err := service.Exchange(context.Background(), "workspace-a", tokenTwo); !errors.Is(err, ErrInvalidOIDCToken) {
		t.Fatalf("expired-cache outage error = %v", err)
	}
}

func TestOIDCDiscoveryRejectsMismatchesPrivateTargetsAndDuplicateMetadata(t *testing.T) {
	publicKey, privateKey, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	provider := newOIDCTestProvider(t, []oidcJWK{ed25519JWK("key-1", publicKey)})
	config := oidcTestConfig(t, provider, &now)
	if _, err := NewOIDCService(config); err == nil {
		t.Fatal("production service accepted a loopback issuer")
	}
	service, err := newOIDCService(config, true)
	if err != nil {
		t.Fatal(err)
	}
	admin := oidcAdminScope(t)
	if err := service.BindIdentity(context.Background(), admin, provider.server.URL, "upstream:alice", "user:alice"); err != nil {
		t.Fatal(err)
	}
	raw := signOIDCTestToken(t, provider.server.URL, "sith-hub", "upstream:alice", "key-1", privateKey, now, nil)
	provider.mu.Lock()
	provider.metadata = func(issuer string) string {
		return fmt.Sprintf(`{"issuer":%q,"issuer":%q,"jwks_uri":%q}`, issuer, issuer, issuer+"/jwks")
	}
	provider.mu.Unlock()
	if _, err := service.Exchange(context.Background(), "workspace-a", raw); !errors.Is(err, ErrInvalidOIDCToken) {
		t.Fatalf("duplicate discovery error = %v", err)
	}
	provider.mu.Lock()
	provider.metadata = func(issuer string) string {
		return fmt.Sprintf(`{"issuer":%q,"jwks_uri":"https://example.com/jwks"}`, issuer)
	}
	provider.mu.Unlock()
	if _, err := service.Exchange(context.Background(), "workspace-a", raw); !errors.Is(err, ErrInvalidOIDCToken) {
		t.Fatalf("foreign JWKS origin error = %v", err)
	}
}

func TestOIDCRejectsHostileTLSAndAmbiguousJWKS(t *testing.T) {
	publicKey, privateKey, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	provider := newOIDCTestProvider(t, []oidcJWK{
		ed25519JWK("duplicate", publicKey),
		ed25519JWK("duplicate", publicKey),
	})
	service, _, _, admin := newOIDCTestFixture(t, provider, &now)
	if err := service.BindIdentity(context.Background(), admin, provider.server.URL, "upstream:alice", "user:alice"); err != nil {
		t.Fatal(err)
	}
	raw := signOIDCTestToken(t, provider.server.URL, "sith-hub", "upstream:alice", "duplicate", privateKey, now, nil)
	if _, err := service.Exchange(context.Background(), "workspace-a", raw); !errors.Is(err, ErrInvalidOIDCToken) {
		t.Fatalf("duplicate JWKS key error = %v", err)
	}

	untrustedConfig := oidcTestConfig(t, provider, &now)
	untrustedConfig.RootCAs = x509.NewCertPool()
	untrusted, err := newOIDCService(untrustedConfig, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := untrusted.BindIdentity(context.Background(), admin, provider.server.URL, "upstream:alice", "user:alice"); err != nil {
		t.Fatal(err)
	}
	if _, err := untrusted.Exchange(context.Background(), "workspace-a", raw); !errors.Is(err, ErrInvalidOIDCToken) {
		t.Fatalf("untrusted TLS error = %v", err)
	}
}

func TestOIDCRejectsUnsafeConfigurationAndWeakJWK(t *testing.T) {
	store := &memoryOIDCStore{}
	for _, provider := range []OIDCProviderConfig{
		{Issuer: "http://issuer.example", Audience: "aud"},
		{Issuer: "https://127.0.0.1", Audience: "aud"},
		{Issuer: "https://issuer.example/", Audience: "aud"},
		{Issuer: "https://issuer.example:8443", Audience: "aud"},
		{Issuer: "https://issuer.example", Audience: ""},
		{Issuer: "https://issuer.example", Audience: "aud", Algorithms: []string{"HS256"}},
		{Issuer: "https://issuer.example", Audience: "aud", MaxKeys: maximumOIDCMaxKeys + 1},
	} {
		if _, err := NewOIDCService(OIDCServiceConfig{Providers: []OIDCProviderConfig{provider}, Store: store, Issuer: &SessionIssuer{}}); err == nil {
			t.Errorf("unsafe provider %#v accepted", provider)
		}
	}
	weakRSA, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseOIDCJWK(rsaJWK("weak", &weakRSA.PublicKey)); err == nil {
		t.Fatal("1024-bit RSA JWK accepted")
	}
	strongRSA, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseOIDCJWK(rsaJWK("strong", &strongRSA.PublicKey)); err != nil {
		t.Fatalf("2048-bit RSA JWK rejected: %v", err)
	}
}

func TestPublicOIDCAddressRejectsSpecialUseNetworks(t *testing.T) {
	for _, address := range []string{
		"127.0.0.1", "10.0.0.1", "169.254.169.254", "100.64.0.1", "192.0.2.1", "198.18.0.1", "203.0.113.1", "::1", "fc00::1", "2001:db8::1",
	} {
		ip := netip.MustParseAddr(address)
		if publicOIDCAddress(ip) {
			t.Errorf("special-use address %s accepted", address)
		}
	}
	for _, address := range []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111"} {
		if !publicOIDCAddress(netip.MustParseAddr(address)) {
			t.Errorf("public address %s rejected", address)
		}
	}
}

func newOIDCTestFixture(
	t *testing.T,
	provider *oidcTestProvider,
	now *time.Time,
) (*OIDCService, *memoryOIDCStore, *JWTVerifier, tenancy.Scope) {
	t.Helper()
	config := oidcTestConfig(t, provider, now)
	service, err := newOIDCService(config, true)
	if err != nil {
		t.Fatal(err)
	}
	privateKey := config.Issuer.privateKey
	verifier, err := NewJWTVerifier(JWTConfig{
		Issuer: "https://issuer.sith.test", Audience: "https://hub.sith.test",
		Keys: map[string]ed25519.PublicKey{"sith-session": privateKey.Public().(ed25519.PublicKey)},
		Now:  func() time.Time { return *now }, MaxLifetime: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	return service, config.Store.(*memoryOIDCStore), verifier, oidcAdminScope(t)
}

func oidcTestConfig(t *testing.T, provider *oidcTestProvider, now *time.Time) OIDCServiceConfig {
	t.Helper()
	sessionPrivate := ed25519.NewKeyFromSeed([]byte(strings.Repeat("o", ed25519.SeedSize)))
	issuer, err := NewSessionIssuer(SessionIssuerConfig{
		Issuer: "https://issuer.sith.test", Audience: "https://hub.sith.test", KeyID: "sith-session",
		PrivateKey: sessionPrivate, Now: func() time.Time { return *now },
	})
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(provider.server.Certificate())
	store := &memoryOIDCStore{
		bindings: map[string]string{},
		roles: map[tenancy.WorkspaceID]map[string]tenancy.Role{
			"workspace-a": {"user:admin": tenancy.RoleAdmin, "user:alice": tenancy.RoleReader},
			"workspace-b": {"user:bob": tenancy.RoleReader},
		},
	}
	return OIDCServiceConfig{
		Providers: []OIDCProviderConfig{{
			Issuer: provider.server.URL, Audience: "sith-hub", Type: "JWT", Algorithms: []string{"EdDSA"},
			MaxTokenLifetime: time.Hour, CacheTTL: defaultOIDCCacheTTL, MaxKeys: 4,
		}},
		Store: store, Issuer: issuer, RootCAs: roots, Now: func() time.Time { return *now },
	}
}

func oidcAdminScope(t *testing.T) tenancy.Scope {
	t.Helper()
	principal, err := tenancy.NewPrincipal("user:admin", map[tenancy.WorkspaceID]tenancy.Role{"workspace-a": tenancy.RoleAdmin})
	if err != nil {
		t.Fatal(err)
	}
	scope, err := principal.Scope("workspace-a")
	if err != nil {
		t.Fatal(err)
	}
	return scope
}

func ed25519JWK(keyID string, key ed25519.PublicKey) oidcJWK {
	return oidcJWK{KeyType: "OKP", Use: "sig", Algorithm: "EdDSA", KeyID: keyID, Curve: "Ed25519", X: base64.RawURLEncoding.EncodeToString(key)}
}

func rsaJWK(keyID string, key *rsa.PublicKey) oidcJWK {
	exponent := make([]byte, 0, 4)
	for value := key.E; value > 0; value >>= 8 {
		exponent = append([]byte{byte(value)}, exponent...)
	}
	return oidcJWK{
		KeyType: "RSA", Use: "sig", Algorithm: "RS256", KeyID: keyID,
		N: base64.RawURLEncoding.EncodeToString(key.N.Bytes()), E: base64.RawURLEncoding.EncodeToString(exponent),
	}
}

func signOIDCTestToken(
	t *testing.T,
	issuer, audience, subject, keyID string,
	privateKey ed25519.PrivateKey,
	now time.Time,
	mutate func(jwt.MapClaims),
) string {
	t.Helper()
	return signOIDCTestTokenWithHeaderAndClaims(t, issuer, audience, subject, keyID, privateKey, now, nil, mutate)
}

func signOIDCTestTokenWithHeader(
	t *testing.T,
	issuer, audience, subject, keyID string,
	privateKey ed25519.PrivateKey,
	now time.Time,
	header map[string]any,
) string {
	t.Helper()
	return signOIDCTestTokenWithHeaderAndClaims(t, issuer, audience, subject, keyID, privateKey, now, header, nil)
}

func signOIDCTestTokenWithHeaderAndClaims(
	t *testing.T,
	issuer, audience, subject, keyID string,
	privateKey ed25519.PrivateKey,
	now time.Time,
	header map[string]any,
	mutate func(jwt.MapClaims),
) string {
	t.Helper()
	claims := jwt.MapClaims{
		"iss": issuer, "aud": audience, "sub": subject,
		"iat": now.Unix(), "nbf": now.Unix(), "exp": now.Add(30 * time.Minute).Unix(),
		"roles": []string{"admin"}, "workspace": "workspace-b",
	}
	if mutate != nil {
		mutate(claims)
	}
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	token.Header["typ"] = "JWT"
	token.Header["kid"] = keyID
	for name, value := range header {
		token.Header[name] = value
	}
	raw, err := token.SignedString(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func duplicateIssuerOIDCToken(t *testing.T, issuer string, privateKey ed25519.PrivateKey, now time.Time) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"EdDSA","kid":"key-1","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(
		`{"iss":%q,"iss":%q,"aud":"sith-hub","sub":"upstream:alice","iat":%d,"nbf":%d,"exp":%d}`,
		issuer, issuer, now.Unix(), now.Unix(), now.Add(30*time.Minute).Unix(),
	)))
	signingInput := header + "." + payload
	signature := ed25519.Sign(privateKey, []byte(signingInput))
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func signOIDCHSTestToken(t *testing.T, issuer string, now time.Time) string {
	t.Helper()
	claims := jwt.MapClaims{
		"iss": issuer, "aud": "sith-hub", "sub": "upstream:alice",
		"iat": now.Unix(), "nbf": now.Unix(), "exp": now.Add(30 * time.Minute).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	token.Header["typ"] = "JWT"
	token.Header["kid"] = "key-1"
	raw, err := token.SignedString([]byte(strings.Repeat("h", 32)))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
