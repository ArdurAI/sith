// SPDX-License-Identifier: Apache-2.0

package hubauth

import (
	"context"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/ArdurAI/sith/internal/tenancy"
)

const (
	maxUpstreamTokenBytes = 16 * 1024
	maxOIDCJSONBytes      = 256 * 1024
	defaultOIDCTimeout    = 5 * time.Second
	defaultOIDCCacheTTL   = 15 * time.Minute
	maximumOIDCCacheTTL   = time.Hour
	defaultOIDCMaxKeys    = 16
	maximumOIDCMaxKeys    = 64
	defaultOIDCLifetime   = time.Hour
	maximumOIDCLifetime   = 24 * time.Hour
	maxOIDCSubjectBytes   = 256
)

var (
	// ErrInvalidOIDCToken deliberately hides discovery, key, mapping, and claim details.
	ErrInvalidOIDCToken = errors.New("invalid OIDC token")
	// ErrOIDCBindingNotFound lets stores report a miss without exposing it through Exchange.
	ErrOIDCBindingNotFound = errors.New("OIDC binding not found")
)

// OIDCProviderConfig pins one upstream token profile.
type OIDCProviderConfig struct {
	Issuer           string
	Audience         string
	Type             string
	Algorithms       []string
	MaxTokenLifetime time.Duration
	CacheTTL         time.Duration
	MaxKeys          int
}

// OIDCStore persists explicit workspace bindings without trusting upstream authorization claims.
type OIDCStore interface {
	CreateOIDCBinding(context.Context, tenancy.Scope, string, string, string) error
	LookupOIDCMembership(context.Context, tenancy.WorkspaceID, string, string) (tenancy.Membership, error)
}

// OIDCServiceConfig supplies pinned providers and local trust roots.
type OIDCServiceConfig struct {
	Providers []OIDCProviderConfig
	Store     OIDCStore
	Issuer    *SessionIssuer
	RootCAs   *x509.CertPool
	Timeout   time.Duration
	Now       func() time.Time
}

// OIDCService validates upstream identities and exchanges them for Sith sessions.
type OIDCService struct {
	providers map[string]*oidcProvider
	store     OIDCStore
	issuer    *SessionIssuer
	client    *http.Client
	now       func() time.Time
	// allowPrivateTest is set only by the unexported test constructor.
	allowPrivateTest bool
}

type oidcProvider struct {
	config OIDCProviderConfig
	url    *url.URL
	mu     sync.Mutex
	cache  oidcKeyCache
}

type oidcKeyCache struct {
	keys      map[string]any
	fetchedAt time.Time
}

type oidcClaims struct {
	AuthorizedParty string `json:"azp,omitempty"`
	jwt.RegisteredClaims
}

type oidcHeader struct {
	Algorithm string `json:"alg"`
	KeyID     string `json:"kid"`
	Type      string `json:"typ"`
}

type oidcDiscovery struct {
	Issuer  string `json:"issuer"`
	JWKSURL string `json:"jwks_uri"`
}

type oidcJWKSet struct {
	Keys []oidcJWK `json:"keys"`
}

type oidcJWK struct {
	KeyType   string `json:"kty"`
	Use       string `json:"use,omitempty"`
	Algorithm string `json:"alg"`
	KeyID     string `json:"kid"`
	Curve     string `json:"crv,omitempty"`
	X         string `json:"x,omitempty"`
	N         string `json:"n,omitempty"`
	E         string `json:"e,omitempty"`
}

// NewOIDCService constructs a production service whose HTTP transport rejects private-network targets.
func NewOIDCService(config OIDCServiceConfig) (*OIDCService, error) {
	return newOIDCService(config, false)
}

func newOIDCService(config OIDCServiceConfig, allowPrivateForTest bool) (*OIDCService, error) {
	if config.Store == nil || config.Issuer == nil || len(config.Providers) == 0 {
		return nil, fmt.Errorf("construct OIDC service: store, session issuer, and providers are required")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.Timeout == 0 {
		config.Timeout = defaultOIDCTimeout
	}
	if config.Timeout < time.Second || config.Timeout > 30*time.Second {
		return nil, fmt.Errorf("construct OIDC service: timeout must be between one and 30 seconds")
	}
	providers := make(map[string]*oidcProvider, len(config.Providers))
	for index := range config.Providers {
		providerConfig := config.Providers[index]
		parsed, err := validateOIDCProvider(&providerConfig, allowPrivateForTest)
		if err != nil {
			return nil, err
		}
		if _, exists := providers[providerConfig.Issuer]; exists {
			return nil, fmt.Errorf("construct OIDC service: duplicate issuer %q", providerConfig.Issuer)
		}
		providers[providerConfig.Issuer] = &oidcProvider{config: providerConfig, url: parsed}
	}
	transport := &http.Transport{
		Proxy:               nil,
		DisableCompression:  true,
		MaxIdleConnsPerHost: 2,
		IdleConnTimeout:     time.Minute,
		TLSClientConfig: &tls.Config{ // #nosec G402 -- TLS 1.2 is the explicit compatibility floor.
			MinVersion: tls.VersionTLS12,
			RootCAs:    cloneCertPool(config.RootCAs),
		},
	}
	transport.DialContext = safeOIDCDialer(allowPrivateForTest)
	client := &http.Client{
		Transport: transport,
		Timeout:   config.Timeout,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) == 0 || len(via) > 3 || !sameOrigin(via[0].URL, request.URL) {
				return fmt.Errorf("OIDC redirect escaped pinned origin")
			}
			return validateOIDCURL(request.URL, allowPrivateForTest)
		},
	}
	return &OIDCService{
		providers: providers, store: config.Store, issuer: config.Issuer, client: client, now: config.Now,
		allowPrivateTest: allowPrivateForTest,
	}, nil
}

func cloneCertPool(pool *x509.CertPool) *x509.CertPool {
	if pool == nil {
		return nil
	}
	return pool.Clone()
}

// BindIdentity maps one verified upstream identity to an existing workspace member.
func (service *OIDCService) BindIdentity(ctx context.Context, admin tenancy.Scope, issuer, upstreamSubject, memberSubject string) error {
	if service == nil || ctx == nil || ctx.Err() != nil {
		return fmt.Errorf("bind OIDC identity: service and active context are required")
	}
	if err := admin.Authorize(tenancy.ActionManageWorkspace); err != nil {
		return fmt.Errorf("bind OIDC identity: %w", err)
	}
	if service.providers[issuer] == nil || validateOIDCSubject(upstreamSubject) != nil {
		return fmt.Errorf("bind OIDC identity: invalid issuer or upstream subject")
	}
	if err := service.store.CreateOIDCBinding(ctx, admin, issuer, upstreamSubject, memberSubject); err != nil {
		return fmt.Errorf("bind OIDC identity: persist mapping: %w", err)
	}
	return nil
}

// Exchange validates a pinned upstream token and resolves one workspace binding server-side.
func (service *OIDCService) Exchange(ctx context.Context, workspaceID tenancy.WorkspaceID, rawToken string) (IssuedSession, error) {
	if service == nil || ctx == nil || ctx.Err() != nil || tenancy.ValidateWorkspaceID(workspaceID) != nil ||
		rawToken == "" || len(rawToken) > maxUpstreamTokenBytes {
		return IssuedSession{}, ErrInvalidOIDCToken
	}
	header, unverifiedClaims, err := inspectOIDCToken(rawToken)
	if err != nil {
		return IssuedSession{}, ErrInvalidOIDCToken
	}
	provider := service.providers[unverifiedClaims.Issuer]
	if provider == nil || !providerAllowsAlgorithm(provider.config, header.Algorithm) ||
		header.Type != provider.config.Type || header.KeyID == "" || len(header.KeyID) > maxKeyIDBytes {
		return IssuedSession{}, ErrInvalidOIDCToken
	}
	key, err := service.providerKey(ctx, provider, header.KeyID, header.Algorithm)
	if err != nil {
		return IssuedSession{}, ErrInvalidOIDCToken
	}
	claims := &oidcClaims{}
	parser := jwt.NewParser(
		jwt.WithValidMethods(provider.config.Algorithms),
		jwt.WithIssuer(provider.config.Issuer),
		jwt.WithAudience(provider.config.Audience),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithTimeFunc(service.now),
		jwt.WithStrictDecoding(),
	)
	token, err := parser.ParseWithClaims(rawToken, claims, func(token *jwt.Token) (any, error) {
		if token.Header["alg"] != header.Algorithm || token.Header["kid"] != header.KeyID || token.Header["typ"] != header.Type {
			return nil, ErrInvalidOIDCToken
		}
		return key, nil
	})
	if err != nil || token == nil || !token.Valid || claims.IssuedAt == nil || claims.NotBefore == nil ||
		claims.ExpiresAt == nil || validateOIDCSubject(claims.Subject) != nil {
		return IssuedSession{}, ErrInvalidOIDCToken
	}
	lifetime := claims.ExpiresAt.Sub(claims.IssuedAt.Time)
	if lifetime <= 0 || lifetime > provider.config.MaxTokenLifetime || claims.NotBefore.Before(claims.IssuedAt.Add(-time.Minute)) {
		return IssuedSession{}, ErrInvalidOIDCToken
	}
	if len(claims.Audience) != 1 && claims.AuthorizedParty != provider.config.Audience {
		return IssuedSession{}, ErrInvalidOIDCToken
	}
	membership, err := service.store.LookupOIDCMembership(ctx, workspaceID, provider.config.Issuer, claims.Subject)
	if err != nil || membership.WorkspaceID != workspaceID || membership.Subject == "" || !membership.Role.Valid() {
		return IssuedSession{}, ErrInvalidOIDCToken
	}
	principal, err := tenancy.NewPrincipal(membership.Subject, map[tenancy.WorkspaceID]tenancy.Role{workspaceID: membership.Role})
	if err != nil {
		return IssuedSession{}, ErrInvalidOIDCToken
	}
	session, err := service.issuer.Issue(ctx, principal)
	if err != nil {
		return IssuedSession{}, fmt.Errorf("exchange OIDC identity: issue session: %w", err)
	}
	return session, nil
}

func validateOIDCProvider(config *OIDCProviderConfig, allowPrivateForTest bool) (*url.URL, error) {
	parsed, err := url.Parse(config.Issuer)
	if err != nil || len(config.Issuer) > 2048 || validateOIDCURL(parsed, allowPrivateForTest) != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil ||
		strings.HasSuffix(config.Issuer, "/") {
		return nil, fmt.Errorf("construct OIDC service: issuer must be an exact HTTPS URL without userinfo, query, fragment, or trailing slash")
	}
	if config.Audience == "" || strings.TrimSpace(config.Audience) != config.Audience {
		return nil, fmt.Errorf("construct OIDC service: audience must be a non-empty trimmed value")
	}
	if config.Type == "" {
		config.Type = "JWT"
	}
	if config.Type != "JWT" && config.Type != "at+jwt" {
		return nil, fmt.Errorf("construct OIDC service: token type must be JWT or at+jwt")
	}
	if len(config.Algorithms) == 0 {
		config.Algorithms = []string{"RS256"}
	}
	seen := make(map[string]bool, len(config.Algorithms))
	for _, algorithm := range config.Algorithms {
		if (algorithm != "RS256" && algorithm != "EdDSA") || seen[algorithm] {
			return nil, fmt.Errorf("construct OIDC service: algorithms must be unique RS256 or EdDSA values")
		}
		seen[algorithm] = true
	}
	config.Algorithms = append([]string(nil), config.Algorithms...)
	if config.MaxTokenLifetime == 0 {
		config.MaxTokenLifetime = defaultOIDCLifetime
	}
	if config.MaxTokenLifetime < time.Minute || config.MaxTokenLifetime > maximumOIDCLifetime {
		return nil, fmt.Errorf("construct OIDC service: token lifetime must be between one minute and 24 hours")
	}
	if config.CacheTTL == 0 {
		config.CacheTTL = defaultOIDCCacheTTL
	}
	if config.CacheTTL < time.Minute || config.CacheTTL > maximumOIDCCacheTTL {
		return nil, fmt.Errorf("construct OIDC service: cache TTL must be between one minute and one hour")
	}
	if config.MaxKeys == 0 {
		config.MaxKeys = defaultOIDCMaxKeys
	}
	if config.MaxKeys < 1 || config.MaxKeys > maximumOIDCMaxKeys {
		return nil, fmt.Errorf("construct OIDC service: key limit must be between one and 64")
	}
	return parsed, nil
}

func inspectOIDCToken(rawToken string) (oidcHeader, oidcClaims, error) {
	parts := strings.Split(rawToken, ".")
	if len(parts) != 3 {
		return oidcHeader{}, oidcClaims{}, ErrInvalidOIDCToken
	}
	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || len(headerJSON) > 4096 || rejectDuplicateJSON(headerJSON) != nil {
		return oidcHeader{}, oidcClaims{}, ErrInvalidOIDCToken
	}
	payloadJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || len(payloadJSON) > maxUpstreamTokenBytes || rejectDuplicateJSON(payloadJSON) != nil {
		return oidcHeader{}, oidcClaims{}, ErrInvalidOIDCToken
	}
	var rawHeader map[string]json.RawMessage
	if err := json.Unmarshal(headerJSON, &rawHeader); err != nil {
		return oidcHeader{}, oidcClaims{}, ErrInvalidOIDCToken
	}
	for _, forbidden := range []string{"jku", "x5u", "jwk", "x5c", "crit", "cty", "zip"} {
		if rawHeader[forbidden] != nil {
			return oidcHeader{}, oidcClaims{}, ErrInvalidOIDCToken
		}
	}
	var header oidcHeader
	var claims oidcClaims
	if json.Unmarshal(headerJSON, &header) != nil || json.Unmarshal(payloadJSON, &claims) != nil {
		return oidcHeader{}, oidcClaims{}, ErrInvalidOIDCToken
	}
	return header, claims, nil
}

func rejectDuplicateJSON(document []byte) error {
	decoder := json.NewDecoder(strings.NewReader(string(document)))
	decoder.UseNumber()
	if err := consumeUniqueJSON(decoder); err != nil {
		return err
	}
	if token, err := decoder.Token(); err != io.EOF || token != nil {
		return fmt.Errorf("JSON contains trailing data")
	}
	return nil
}

func consumeUniqueJSON(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, isDelimiter := token.(json.Delim)
	if !isDelimiter {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]bool)
		for decoder.More() {
			nameToken, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := nameToken.(string)
			if !ok || seen[name] {
				return fmt.Errorf("JSON contains a duplicate or invalid object member")
			}
			seen[name] = true
			if err := consumeUniqueJSON(decoder); err != nil {
				return err
			}
		}
	case '[':
		for decoder.More() {
			if err := consumeUniqueJSON(decoder); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("JSON contains an invalid delimiter")
	}
	closing, err := decoder.Token()
	if err != nil || closing != matchingDelimiter(delimiter) {
		return fmt.Errorf("JSON contains an invalid closing delimiter")
	}
	return nil
}

func matchingDelimiter(open json.Delim) json.Delim {
	if open == '{' {
		return '}'
	}
	return ']'
}

func (service *OIDCService) providerKey(ctx context.Context, provider *oidcProvider, keyID, algorithm string) (any, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	now := service.now().UTC()
	if now.Before(provider.cache.fetchedAt.Add(provider.config.CacheTTL)) {
		if key := provider.cache.keys[keyID]; keyMatchesAlgorithm(key, algorithm) {
			return key, nil
		}
	}
	keys, err := service.fetchProviderKeys(ctx, provider)
	if err != nil {
		return nil, err
	}
	provider.cache = oidcKeyCache{keys: keys, fetchedAt: now}
	key := keys[keyID]
	if !keyMatchesAlgorithm(key, algorithm) {
		return nil, fmt.Errorf("OIDC key is missing or incompatible")
	}
	return key, nil
}

func (service *OIDCService) fetchProviderKeys(ctx context.Context, provider *oidcProvider) (map[string]any, error) {
	discoveryURL, _ := url.Parse(strings.TrimSuffix(provider.config.Issuer, "/") + "/.well-known/openid-configuration")
	var discovery oidcDiscovery
	if err := service.getOIDCJSON(ctx, discoveryURL, &discovery); err != nil {
		return nil, err
	}
	if discovery.Issuer != provider.config.Issuer {
		return nil, fmt.Errorf("OIDC discovery issuer mismatch")
	}
	jwksURL, err := url.Parse(discovery.JWKSURL)
	if err != nil || validateOIDCURL(jwksURL, service.allowPrivateTest) != nil || !sameOrigin(provider.url, jwksURL) {
		return nil, fmt.Errorf("OIDC JWKS URL escaped pinned issuer origin")
	}
	var set oidcJWKSet
	if err := service.getOIDCJSON(ctx, jwksURL, &set); err != nil {
		return nil, err
	}
	if len(set.Keys) == 0 || len(set.Keys) > provider.config.MaxKeys {
		return nil, fmt.Errorf("OIDC JWKS key count is outside configured bounds")
	}
	keys := make(map[string]any, len(set.Keys))
	for _, jwk := range set.Keys {
		if jwk.KeyID == "" || len(jwk.KeyID) > maxKeyIDBytes || keys[jwk.KeyID] != nil ||
			jwk.Use != "sig" || !providerAllowsAlgorithm(provider.config, jwk.Algorithm) {
			return nil, fmt.Errorf("OIDC JWKS contains an ambiguous or unsupported key")
		}
		key, err := parseOIDCJWK(jwk)
		if err != nil {
			return nil, err
		}
		keys[jwk.KeyID] = key
	}
	return keys, nil
}

func (service *OIDCService) getOIDCJSON(ctx context.Context, endpoint *url.URL, target any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	response, err := service.client.Do(request)
	if err != nil {
		return fmt.Errorf("fetch OIDC metadata: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch OIDC metadata: unexpected HTTP status")
	}
	contentType := strings.ToLower(strings.TrimSpace(strings.Split(response.Header.Get("Content-Type"), ";")[0]))
	if contentType != "application/json" && !strings.HasSuffix(contentType, "+json") {
		return fmt.Errorf("fetch OIDC metadata: response is not JSON")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxOIDCJSONBytes+1))
	if err != nil || len(body) > maxOIDCJSONBytes || rejectDuplicateJSON(body) != nil {
		return fmt.Errorf("fetch OIDC metadata: invalid bounded JSON response")
	}
	if err := json.Unmarshal(body, target); err != nil {
		return fmt.Errorf("fetch OIDC metadata: decode response: %w", err)
	}
	return nil
}

func parseOIDCJWK(jwk oidcJWK) (any, error) {
	switch jwk.Algorithm {
	case "RS256":
		if jwk.KeyType != "RSA" || jwk.N == "" || jwk.E == "" {
			return nil, fmt.Errorf("OIDC RSA key is incomplete")
		}
		modulusBytes, err := base64.RawURLEncoding.DecodeString(jwk.N)
		if err != nil || base64.RawURLEncoding.EncodeToString(modulusBytes) != jwk.N {
			return nil, fmt.Errorf("OIDC RSA modulus is invalid")
		}
		exponentBytes, err := base64.RawURLEncoding.DecodeString(jwk.E)
		if err != nil || len(exponentBytes) == 0 || len(exponentBytes) > 4 ||
			base64.RawURLEncoding.EncodeToString(exponentBytes) != jwk.E {
			return nil, fmt.Errorf("OIDC RSA exponent is invalid")
		}
		exponent := 0
		for _, value := range exponentBytes {
			exponent = exponent<<8 | int(value)
		}
		modulus := new(big.Int).SetBytes(modulusBytes)
		if modulus.BitLen() < 2048 || exponent < 3 || exponent%2 == 0 {
			return nil, fmt.Errorf("OIDC RSA key is below the security floor")
		}
		return &rsa.PublicKey{N: modulus, E: exponent}, nil
	case "EdDSA":
		if jwk.KeyType != "OKP" || jwk.Curve != "Ed25519" {
			return nil, fmt.Errorf("OIDC EdDSA key is not Ed25519")
		}
		key, err := base64.RawURLEncoding.DecodeString(jwk.X)
		if err != nil || len(key) != ed25519.PublicKeySize || base64.RawURLEncoding.EncodeToString(key) != jwk.X {
			return nil, fmt.Errorf("OIDC Ed25519 key is invalid")
		}
		return ed25519.PublicKey(key), nil
	default:
		return nil, fmt.Errorf("OIDC key algorithm is unsupported")
	}
}

func safeOIDCDialer(allowPrivateForTest bool) func(context.Context, string, string) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: defaultOIDCTimeout, KeepAlive: 30 * time.Second}
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, fmt.Errorf("resolve OIDC address: %w", err)
		}
		addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil || len(addresses) == 0 {
			return nil, fmt.Errorf("resolve OIDC host")
		}
		for _, addressIP := range addresses {
			ip := addressIP.Unmap()
			if !allowPrivateForTest && !publicOIDCAddress(ip) {
				continue
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		}
		return nil, fmt.Errorf("OIDC host resolved only to prohibited addresses")
	}
}

func validateOIDCURL(endpoint *url.URL, allowPrivateForTest bool) error {
	if endpoint == nil || endpoint.Scheme != "https" || endpoint.Host == "" || endpoint.User != nil || endpoint.Fragment != "" {
		return fmt.Errorf("OIDC endpoint must be an absolute HTTPS URL")
	}
	if !allowPrivateForTest && endpoint.Port() != "" && endpoint.Port() != "443" {
		return fmt.Errorf("OIDC endpoint uses a prohibited port")
	}
	host := endpoint.Hostname()
	if ip, err := netip.ParseAddr(host); err == nil && !allowPrivateForTest && !publicOIDCAddress(ip.Unmap()) {
		return fmt.Errorf("OIDC endpoint uses a prohibited address")
	}
	return nil
}

func publicOIDCAddress(ip netip.Addr) bool {
	if !ip.IsValid() || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	for _, prefix := range prohibitedOIDCPrefixes {
		if prefix.Contains(ip) {
			return false
		}
	}
	return true
}

var prohibitedOIDCPrefixes = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("2001:db8::/32"),
}

func sameOrigin(left, right *url.URL) bool {
	return left != nil && right != nil && left.Scheme == right.Scheme && strings.EqualFold(left.Host, right.Host)
}

func providerAllowsAlgorithm(config OIDCProviderConfig, algorithm string) bool {
	for _, allowed := range config.Algorithms {
		if algorithm == allowed {
			return true
		}
	}
	return false
}

func keyMatchesAlgorithm(key any, algorithm string) bool {
	switch algorithm {
	case "RS256":
		_, ok := key.(*rsa.PublicKey)
		return ok
	case "EdDSA":
		value, ok := key.(ed25519.PublicKey)
		return ok && len(value) == ed25519.PublicKeySize
	default:
		return false
	}
}

func validateOIDCSubject(subject string) error {
	membership := tenancy.Membership{WorkspaceID: "oidc", Subject: subject, Role: tenancy.RoleReader}
	if len(subject) > maxOIDCSubjectBytes || membership.Validate() != nil {
		return fmt.Errorf("invalid OIDC subject")
	}
	return nil
}
