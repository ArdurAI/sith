// SPDX-License-Identifier: Apache-2.0

package hubauth

import (
	"context"
	"crypto/x509"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// EntraVerifierConfig pins one tenant-specific Microsoft Entra app-only token profile.
type EntraVerifierConfig struct {
	Authority string
	TenantID  string
	Audience  string
	ActorID   string
	RootCAs   *x509.CertPool
	Now       func() time.Time
}

// EntraVerifier verifies a tenant-specific app-only Entra token and emits no authorization claims.
type EntraVerifier struct {
	service  *OIDCService
	provider *oidcProvider
	tenantID string
	audience string
	actorID  string
	now      func() time.Time
}

type entraClaims struct {
	TenantID        string `json:"tid"`
	ObjectID        string `json:"oid"`
	IdentityType    string `json:"idtyp"`
	AuthorizedParty string `json:"azp"`
	Version         string `json:"ver"`
	jwt.RegisteredClaims
}

// NewEntraVerifier constructs a tenant-specific v2.0 Entra verifier.
func NewEntraVerifier(config EntraVerifierConfig) (*EntraVerifier, error) {
	return newEntraVerifier(config, false)
}

func newEntraVerifier(config EntraVerifierConfig, allowPrivateForTest bool) (*EntraVerifier, error) {
	if !validEntraTenantID(config.TenantID) || validateCloudIdentityValue("Entra audience", config.Audience, maxCloudAudienceBytes) != nil ||
		(config.ActorID != "" && validateCloudIdentityValue("Entra actor", config.ActorID, maxCloudIdentityBytes) != nil) {
		return nil, fmt.Errorf("construct Entra verifier: tenant, audience, or actor is invalid")
	}
	authority := config.Authority
	if strings.HasSuffix(authority, "/") || (!allowPrivateForTest && !validEntraAuthority(authority)) {
		return nil, fmt.Errorf("construct Entra verifier: authority is not an explicit supported cloud endpoint")
	}
	issuer := authority + "/" + config.TenantID + "/v2.0"
	if config.Now == nil {
		config.Now = time.Now
	}
	service, err := newOIDCVerifierTransport(OIDCServiceConfig{Providers: []OIDCProviderConfig{{
		Issuer: issuer, Audience: config.Audience, Type: "JWT", Algorithms: []string{"RS256"}, MaxTokenLifetime: time.Hour,
	}}, RootCAs: config.RootCAs, Now: config.Now}, allowPrivateForTest)
	if err != nil {
		return nil, fmt.Errorf("construct Entra verifier: %w", err)
	}
	return &EntraVerifier{service: service, provider: service.providers[issuer], tenantID: config.TenantID, audience: config.Audience, actorID: config.ActorID, now: config.Now}, nil
}

// Provider implements CloudProofVerifier.
func (verifier *EntraVerifier) Provider() CloudProvider { return CloudProviderAzure }

// Verify accepts only an exactly pinned, app-only Entra v2 JWT.
func (verifier *EntraVerifier) Verify(ctx context.Context, rawToken string) (VerifiedCloudPrincipal, error) {
	if verifier == nil || verifier.service == nil || verifier.provider == nil || ctx == nil || ctx.Err() != nil || rawToken == "" || len(rawToken) > maxCloudProofBytes {
		return VerifiedCloudPrincipal{}, ErrInvalidCloudProof
	}
	header, unverified, err := inspectOIDCToken(rawToken)
	if err != nil || unverified.Issuer != verifier.provider.config.Issuer || header.Algorithm != "RS256" || header.Type != "JWT" || header.KeyID == "" {
		return VerifiedCloudPrincipal{}, ErrInvalidCloudProof
	}
	key, err := verifier.service.providerKey(ctx, verifier.provider, header.KeyID, header.Algorithm)
	if err != nil {
		return VerifiedCloudPrincipal{}, ErrInvalidCloudProof
	}
	claims := &entraClaims{}
	token, err := jwt.NewParser(jwt.WithValidMethods([]string{"RS256"}), jwt.WithIssuer(verifier.provider.config.Issuer), jwt.WithAudience(verifier.audience), jwt.WithExpirationRequired(), jwt.WithIssuedAt(), jwt.WithTimeFunc(verifier.now), jwt.WithStrictDecoding()).ParseWithClaims(rawToken, claims, func(token *jwt.Token) (any, error) { return key, nil })
	if err != nil || token == nil || !token.Valid || claims.IssuedAt == nil || claims.NotBefore == nil || claims.ExpiresAt == nil ||
		claims.TenantID != verifier.tenantID || !validEntraTenantID(claims.TenantID) || !validEntraObjectID(claims.ObjectID) || claims.IdentityType != "app" || claims.Version != "2.0" ||
		(verifier.actorID != "" && claims.AuthorizedParty != verifier.actorID) || claims.ExpiresAt.Sub(claims.IssuedAt.Time) > time.Hour || !claims.ExpiresAt.After(verifier.now()) {
		return VerifiedCloudPrincipal{}, ErrInvalidCloudProof
	}
	return VerifiedCloudPrincipal{Identity: CloudIdentity{Provider: CloudProviderAzure, Realm: claims.TenantID, Subject: claims.ObjectID}, Audience: verifier.audience, IssuedAt: claims.IssuedAt.Time, ExpiresAt: claims.ExpiresAt.Time}, nil
}

func validEntraAuthority(authority string) bool {
	return authority == "https://login.microsoftonline.com" || authority == "https://login.microsoftonline.us" || authority == "https://login.chinacloudapi.cn"
}

func validEntraTenantID(value string) bool { return validEntraGUID(value) }
func validEntraObjectID(value string) bool { return validEntraGUID(value) }
func validEntraGUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for index, character := range value {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			if character != '-' {
				return false
			}
			continue
		}
		if (character < 'a' || character > 'f') && (character < 'A' || character > 'F') && (character < '0' || character > '9') {
			return false
		}
	}
	return true
}
