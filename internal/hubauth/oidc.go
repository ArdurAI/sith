// SPDX-License-Identifier: Apache-2.0

package hubauth

import (
	"context"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/subtle"
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
	"unicode"

	"github.com/golang-jwt/jwt/v5"

	"github.com/ArdurAI/sith/internal/tenancy"
)

const (
	maxUpstreamTokenBytes = 16 * 1024
	maxOIDCJSONBytes      = 256 * 1024
	maxOIDCClientIDBytes  = 256
	maxOIDCCodeBytes      = 2048
	maxOIDCCodeVerifier   = 128
	oidcBrowserValueBytes = 32
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
	// ErrInvalidOIDCBrowserLogin deliberately hides provider and token-exchange details.
	ErrInvalidOIDCBrowserLogin = errors.New("invalid OIDC browser login")
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

// OIDCBrowserAuthorizationRequest carries one server-generated authorization-code request.
// State, nonce, and code challenge are opaque values; the service accepts only the S256 shape.
type OIDCBrowserAuthorizationRequest struct {
	Issuer        string
	ClientID      string
	RedirectURI   string
	State         string
	Nonce         string
	CodeChallenge string
}

// OIDCBrowserCodeExchange carries one server-side authorization-code redemption.
type OIDCBrowserCodeExchange struct {
	Issuer       string
	ClientID     string
	RedirectURI  string
	Code         string
	CodeVerifier string
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
	Nonce           string `json:"nonce,omitempty"`
	jwt.RegisteredClaims
}

type oidcHeader struct {
	Algorithm string `json:"alg"`
	KeyID     string `json:"kid"`
	Type      string `json:"typ"`
}

type oidcDiscovery struct {
	Issuer                string `json:"issuer"`
	JWKSURL               string `json:"jwks_uri"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
}

type oidcTokenResponse struct {
	IDToken string `json:"id_token"`
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
	return newOIDCVerifierTransport(config, allowPrivateForTest)
}

// newOIDCVerifierTransport creates the pinned discovery and JWKS boundary without an authorization store.
// Provider-specific adapters use it only for verification; OIDCService owns OIDC workspace authorization.
func newOIDCVerifierTransport(config OIDCServiceConfig, allowPrivateForTest bool) (*OIDCService, error) {
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
	return service.exchange(ctx, workspaceID, rawToken, "", true, false)
}

// ExchangeWithNonce validates a pinned ID token and requires the exact transaction nonce.
// Browser callers use it only after a successful, server-side authorization-code exchange. Unlike
// the raw exchange profile, this accepts a standards-compliant ID token without an nbf claim.
func (service *OIDCService) ExchangeWithNonce(
	ctx context.Context,
	workspaceID tenancy.WorkspaceID,
	rawToken, expectedNonce string,
) (IssuedSession, error) {
	if !validOIDCBrowserValue(expectedNonce) {
		return IssuedSession{}, ErrInvalidOIDCToken
	}
	return service.exchange(ctx, workspaceID, rawToken, expectedNonce, false, true)
}

func (service *OIDCService) exchange(
	ctx context.Context,
	workspaceID tenancy.WorkspaceID,
	rawToken, expectedNonce string,
	requireNotBefore bool,
	requireExactAudience bool,
) (IssuedSession, error) {
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
	if err != nil || token == nil || !token.Valid || claims.IssuedAt == nil || (requireNotBefore && claims.NotBefore == nil) ||
		claims.ExpiresAt == nil || validateOIDCSubject(claims.Subject) != nil {
		return IssuedSession{}, ErrInvalidOIDCToken
	}
	lifetime := claims.ExpiresAt.Sub(claims.IssuedAt.Time)
	if lifetime <= 0 || lifetime > provider.config.MaxTokenLifetime ||
		(claims.NotBefore != nil && claims.NotBefore.Before(claims.IssuedAt.Add(-time.Minute))) {
		return IssuedSession{}, ErrInvalidOIDCToken
	}
	if (requireExactAudience && (len(claims.Audience) != 1 || claims.Audience[0] != provider.config.Audience)) ||
		(!requireExactAudience && len(claims.Audience) != 1 && claims.AuthorizedParty != provider.config.Audience) ||
		(claims.AuthorizedParty != "" && claims.AuthorizedParty != provider.config.Audience) {
		return IssuedSession{}, ErrInvalidOIDCToken
	}
	if expectedNonce != "" && subtle.ConstantTimeCompare([]byte(claims.Nonce), []byte(expectedNonce)) != 1 {
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

// BrowserAuthorizationURL creates one pinned OIDC authorization-code request with PKCE S256.
func (service *OIDCService) BrowserAuthorizationURL(
	ctx context.Context,
	request OIDCBrowserAuthorizationRequest,
) (string, error) {
	provider, err := service.browserProvider(request.Issuer)
	if err != nil || validateOIDCBrowserAuthorizationRequest(request) != nil || ctx == nil || ctx.Err() != nil {
		return "", ErrInvalidOIDCBrowserLogin
	}
	discovery, err := service.fetchProviderDiscovery(ctx, provider)
	if err != nil {
		return "", ErrInvalidOIDCBrowserLogin
	}
	endpoint, err := parsePinnedOIDCBrowserEndpoint(discovery.AuthorizationEndpoint, provider, service.allowPrivateTest)
	if err != nil {
		return "", ErrInvalidOIDCBrowserLogin
	}
	query := endpoint.Query()
	query.Set("response_type", "code")
	query.Set("client_id", request.ClientID)
	query.Set("redirect_uri", request.RedirectURI)
	query.Set("scope", "openid")
	query.Set("state", request.State)
	query.Set("nonce", request.Nonce)
	query.Set("code_challenge", request.CodeChallenge)
	query.Set("code_challenge_method", "S256")
	endpoint.RawQuery = query.Encode()
	return endpoint.String(), nil
}

// ExchangeAuthorizationCode redeems one PKCE-bound authorization code without exposing its ID token.
func (service *OIDCService) ExchangeAuthorizationCode(
	ctx context.Context,
	request OIDCBrowserCodeExchange,
) (string, error) {
	provider, err := service.browserProvider(request.Issuer)
	if err != nil || validateOIDCBrowserCodeExchange(request) != nil || ctx == nil || ctx.Err() != nil {
		return "", ErrInvalidOIDCBrowserLogin
	}
	discovery, err := service.fetchProviderDiscovery(ctx, provider)
	if err != nil {
		return "", ErrInvalidOIDCBrowserLogin
	}
	endpoint, err := parsePinnedOIDCBrowserEndpoint(discovery.TokenEndpoint, provider, service.allowPrivateTest)
	if err != nil {
		return "", ErrInvalidOIDCBrowserLogin
	}
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", request.ClientID)
	form.Set("redirect_uri", request.RedirectURI)
	form.Set("code", request.Code)
	form.Set("code_verifier", request.CodeVerifier)
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), strings.NewReader(form.Encode()))
	if err != nil {
		return "", ErrInvalidOIDCBrowserLogin
	}
	httpRequest.Header.Set("Accept", "application/json")
	httpRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := *service.client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(httpRequest)
	if err != nil {
		return "", ErrInvalidOIDCBrowserLogin
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK || !isOIDCJSON(response.Header.Get("Content-Type")) {
		return "", ErrInvalidOIDCBrowserLogin
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxOIDCJSONBytes+1))
	if err != nil || len(body) > maxOIDCJSONBytes || rejectDuplicateJSON(body) != nil {
		return "", ErrInvalidOIDCBrowserLogin
	}
	var tokenResponse oidcTokenResponse
	if json.Unmarshal(body, &tokenResponse) != nil || tokenResponse.IDToken == "" || len(tokenResponse.IDToken) > maxUpstreamTokenBytes {
		return "", ErrInvalidOIDCBrowserLogin
	}
	return tokenResponse.IDToken, nil
}

func (service *OIDCService) browserProvider(issuer string) (*oidcProvider, error) {
	if service == nil || issuer == "" || strings.TrimSpace(issuer) != issuer {
		return nil, ErrInvalidOIDCBrowserLogin
	}
	provider := service.providers[issuer]
	if provider == nil {
		return nil, ErrInvalidOIDCBrowserLogin
	}
	return provider, nil
}

func validateOIDCBrowserAuthorizationRequest(request OIDCBrowserAuthorizationRequest) error {
	if validateOIDCBrowserClient(request.ClientID) != nil || validateOIDCBrowserRedirectURI(request.RedirectURI) != nil ||
		!validOIDCBrowserValue(request.State) || !validOIDCBrowserValue(request.Nonce) || !validOIDCBrowserValue(request.CodeChallenge) {
		return ErrInvalidOIDCBrowserLogin
	}
	return nil
}

func validateOIDCBrowserCodeExchange(request OIDCBrowserCodeExchange) error {
	if validateOIDCBrowserClient(request.ClientID) != nil || validateOIDCBrowserRedirectURI(request.RedirectURI) != nil ||
		!validOIDCCode(request.Code) || !validOIDCCodeVerifier(request.CodeVerifier) {
		return ErrInvalidOIDCBrowserLogin
	}
	return nil
}

func validateOIDCBrowserClient(clientID string) error {
	if clientID == "" || len(clientID) > maxOIDCClientIDBytes || strings.TrimSpace(clientID) != clientID || strings.IndexFunc(clientID, unicode.IsControl) >= 0 {
		return ErrInvalidOIDCBrowserLogin
	}
	return nil
}

func validateOIDCBrowserRedirectURI(rawURI string) error {
	parsed, err := url.Parse(rawURI)
	if err != nil || len(rawURI) > 2048 || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path == "" || parsed.EscapedPath() != parsed.Path {
		return ErrInvalidOIDCBrowserLogin
	}
	return nil
}

func validOIDCBrowserValue(value string) bool {
	if len(value) != base64.RawURLEncoding.EncodedLen(oidcBrowserValueBytes) {
		return false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	return err == nil && len(decoded) == oidcBrowserValueBytes && base64.RawURLEncoding.EncodeToString(decoded) == value
}

func validOIDCCode(code string) bool {
	return code != "" && len(code) <= maxOIDCCodeBytes && strings.TrimSpace(code) == code && strings.IndexFunc(code, unicode.IsControl) < 0
}

func validOIDCCodeVerifier(verifier string) bool {
	if len(verifier) < 43 || len(verifier) > maxOIDCCodeVerifier {
		return false
	}
	for _, value := range verifier {
		if !validPKCEVerifierRune(value) {
			return false
		}
	}
	return true
}

func validPKCEVerifierRune(value rune) bool {
	return (value >= 'A' && value <= 'Z') || (value >= 'a' && value <= 'z') || (value >= '0' && value <= '9') ||
		value == '-' || value == '.' || value == '_' || value == '~'
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
	return service.providerKeyWithFetcher(ctx, provider, keyID, algorithm, service.fetchProviderKeys)
}

// providerKeyFromJWKS loads a verifier's explicitly pinned JWKS without provider-controlled discovery.
func (service *OIDCService) providerKeyFromJWKS(ctx context.Context, provider *oidcProvider, rawJWKSURL, keyID, algorithm string) (any, error) {
	return service.providerKeyWithFetcher(ctx, provider, keyID, algorithm, func(ctx context.Context, provider *oidcProvider) (map[string]any, error) {
		jwksURL, err := parsePinnedOIDCJWKSURL(rawJWKSURL, service.allowPrivateTest)
		if err != nil {
			return nil, err
		}
		return service.fetchJWKSKeys(ctx, provider, jwksURL)
	})
}

func (service *OIDCService) providerKeyWithFetcher(
	ctx context.Context,
	provider *oidcProvider,
	keyID, algorithm string,
	fetch func(context.Context, *oidcProvider) (map[string]any, error),
) (any, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	now := service.now().UTC()
	if now.Before(provider.cache.fetchedAt.Add(provider.config.CacheTTL)) {
		if key := provider.cache.keys[keyID]; keyMatchesAlgorithm(key, algorithm) {
			return key, nil
		}
	}
	keys, err := fetch(ctx, provider)
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
	discovery, err := service.fetchProviderDiscovery(ctx, provider)
	if err != nil {
		return nil, err
	}
	jwksURL, err := url.Parse(discovery.JWKSURL)
	if err != nil || validateOIDCURL(jwksURL, service.allowPrivateTest) != nil || !sameOrigin(provider.url, jwksURL) {
		return nil, fmt.Errorf("OIDC JWKS URL escaped pinned issuer origin")
	}
	return service.fetchJWKSKeys(ctx, provider, jwksURL)
}

func (service *OIDCService) fetchProviderDiscovery(ctx context.Context, provider *oidcProvider) (oidcDiscovery, error) {
	if service == nil || provider == nil || ctx == nil || ctx.Err() != nil {
		return oidcDiscovery{}, fmt.Errorf("OIDC discovery is unavailable")
	}
	discoveryURL, _ := url.Parse(strings.TrimSuffix(provider.config.Issuer, "/") + "/.well-known/openid-configuration")
	var discovery oidcDiscovery
	if err := service.getOIDCJSON(ctx, discoveryURL, &discovery); err != nil {
		return oidcDiscovery{}, err
	}
	if discovery.Issuer != provider.config.Issuer {
		return oidcDiscovery{}, fmt.Errorf("OIDC discovery issuer mismatch")
	}
	return discovery, nil
}

func (service *OIDCService) fetchJWKSKeys(ctx context.Context, provider *oidcProvider, jwksURL *url.URL) (map[string]any, error) {
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
	if !isOIDCJSON(response.Header.Get("Content-Type")) {
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

func isOIDCJSON(contentType string) bool {
	contentType = strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	return contentType == "application/json" || strings.HasSuffix(contentType, "+json")
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

func parsePinnedOIDCJWKSURL(rawURL string, allowPrivateForTest bool) (*url.URL, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || validateOIDCURL(parsed, allowPrivateForTest) != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil ||
		strings.HasSuffix(rawURL, "/") {
		return nil, fmt.Errorf("OIDC JWKS URL is invalid")
	}
	return parsed, nil
}

func parsePinnedOIDCBrowserEndpoint(rawURL string, provider *oidcProvider, allowPrivateForTest bool) (*url.URL, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || provider == nil || validateOIDCURL(parsed, allowPrivateForTest) != nil || parsed.RawQuery != "" ||
		parsed.Fragment != "" || parsed.User != nil || parsed.Path == "" || parsed.EscapedPath() != parsed.Path || !sameOrigin(provider.url, parsed) {
		return nil, fmt.Errorf("OIDC browser endpoint is invalid")
	}
	return parsed, nil
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
