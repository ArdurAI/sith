// SPDX-License-Identifier: Apache-2.0

package hubauth

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	googlePublicIssuer  = "https://accounts.google.com"
	googlePublicJWKSURL = "https://www.googleapis.com/oauth2/v3/certs"
)

// GoogleServiceAccountVerifierConfig pins the public Google service-account ID-token profile.
// OrganizationNumber is the Google-attested realm; it requires IAM Credentials minting with
// organizationNumberIncluded rather than inferring a project from an email address.
type GoogleServiceAccountVerifierConfig struct {
	Issuer             string
	JWKSURL            string
	Audience           string
	OrganizationNumber string
	RootCAs            *x509.CertPool
	Now                func() time.Time
}

// GoogleServiceAccountVerifier verifies Google-signed service-account ID tokens only.
type GoogleServiceAccountVerifier struct {
	service            *OIDCService
	provider           *oidcProvider
	jwksURL            string
	audience           string
	organizationNumber string
	now                func() time.Time
}

type googleServiceAccountClaims struct {
	AuthorizedParty string                           `json:"azp"`
	Email           string                           `json:"email"`
	EmailVerified   bool                             `json:"email_verified"`
	Google          *googleServiceAccountGoogleClaim `json:"google"`
	jwt.RegisteredClaims
}

type googleServiceAccountGoogleClaim struct {
	OrganizationNumber json.RawMessage `json:"organization_number"`
}

// NewGoogleServiceAccountVerifier constructs the production Google public-cloud verifier.
func NewGoogleServiceAccountVerifier(config GoogleServiceAccountVerifierConfig) (*GoogleServiceAccountVerifier, error) {
	return newGoogleServiceAccountVerifier(config, false)
}

func newGoogleServiceAccountVerifier(config GoogleServiceAccountVerifierConfig, allowPrivateForTest bool) (*GoogleServiceAccountVerifier, error) {
	if validateCloudIdentityValue("Google audience", config.Audience, maxCloudAudienceBytes) != nil ||
		!validGoogleNumericIdentifier(config.OrganizationNumber) {
		return nil, fmt.Errorf("construct Google service-account verifier: audience or organization number is invalid")
	}
	if _, err := parsePinnedOIDCJWKSURL(config.JWKSURL, allowPrivateForTest); err != nil {
		return nil, fmt.Errorf("construct Google service-account verifier: JWKS URL is invalid")
	}
	if !allowPrivateForTest && (config.Issuer != googlePublicIssuer || config.JWKSURL != googlePublicJWKSURL) {
		return nil, fmt.Errorf("construct Google service-account verifier: issuer and JWKS URL must be the explicit public Google pair")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	service, err := newOIDCVerifierTransport(OIDCServiceConfig{Providers: []OIDCProviderConfig{{
		Issuer: config.Issuer, Audience: config.Audience, Type: "JWT", Algorithms: []string{"RS256"}, MaxTokenLifetime: time.Hour,
	}}, RootCAs: config.RootCAs, Now: config.Now}, allowPrivateForTest)
	if err != nil {
		return nil, fmt.Errorf("construct Google service-account verifier: %w", err)
	}
	return &GoogleServiceAccountVerifier{
		service: service, provider: service.providers[config.Issuer], jwksURL: config.JWKSURL, audience: config.Audience,
		organizationNumber: config.OrganizationNumber, now: config.Now,
	}, nil
}

// Provider implements CloudProofVerifier.
func (verifier *GoogleServiceAccountVerifier) Provider() CloudProvider { return CloudProviderGCP }

// Verify accepts only a public Google-signed, organization-attested service-account ID token.
func (verifier *GoogleServiceAccountVerifier) Verify(ctx context.Context, rawToken string) (VerifiedCloudPrincipal, error) {
	if verifier == nil || verifier.service == nil || verifier.provider == nil || verifier.jwksURL == "" || ctx == nil || ctx.Err() != nil ||
		rawToken == "" || len(rawToken) > maxCloudProofBytes {
		return VerifiedCloudPrincipal{}, ErrInvalidCloudProof
	}
	header, unverified, err := inspectOIDCToken(rawToken)
	if err != nil || unverified.Issuer != verifier.provider.config.Issuer || header.Algorithm != "RS256" || header.Type != "JWT" || header.KeyID == "" {
		return VerifiedCloudPrincipal{}, ErrInvalidCloudProof
	}
	key, err := verifier.service.providerKeyFromJWKS(ctx, verifier.provider, verifier.jwksURL, header.KeyID, header.Algorithm)
	if err != nil {
		return VerifiedCloudPrincipal{}, ErrInvalidCloudProof
	}
	claims := &googleServiceAccountClaims{}
	token, err := jwt.NewParser(
		jwt.WithValidMethods([]string{"RS256"}),
		jwt.WithIssuer(verifier.provider.config.Issuer),
		jwt.WithAudience(verifier.audience),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithTimeFunc(verifier.now),
		jwt.WithStrictDecoding(),
	).ParseWithClaims(rawToken, claims, func(token *jwt.Token) (any, error) { return key, nil })
	organizationNumber, validOrganizationNumber := googleOrganizationNumber(claims.Google)
	if err != nil || token == nil || !token.Valid || claims.IssuedAt == nil || claims.ExpiresAt == nil || len(claims.Audience) != 1 ||
		claims.Audience[0] != verifier.audience || !validGoogleNumericIdentifier(claims.Subject) || claims.AuthorizedParty != claims.Subject ||
		!claims.EmailVerified || !validGoogleServiceAccountEmail(claims.Email) || !validOrganizationNumber || organizationNumber != verifier.organizationNumber ||
		claims.ExpiresAt.Sub(claims.IssuedAt.Time) > time.Hour || !claims.ExpiresAt.After(verifier.now()) {
		return VerifiedCloudPrincipal{}, ErrInvalidCloudProof
	}
	return VerifiedCloudPrincipal{
		Identity: CloudIdentity{Provider: CloudProviderGCP, Realm: organizationNumber, Subject: claims.Subject},
		Audience: verifier.audience, IssuedAt: claims.IssuedAt.Time, ExpiresAt: claims.ExpiresAt.Time,
	}, nil
}

func googleOrganizationNumber(claim *googleServiceAccountGoogleClaim) (string, bool) {
	if claim == nil {
		return "", false
	}
	value := string(claim.OrganizationNumber)
	return value, validGoogleNumericIdentifier(value)
}

func validGoogleNumericIdentifier(value string) bool {
	if value == "" || len(value) > maxCloudIdentityBytes {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func validGoogleServiceAccountEmail(value string) bool {
	if value == "" || len(value) > maxCloudIdentityBytes || strings.TrimSpace(value) != value || strings.Count(value, "@") != 1 {
		return false
	}
	local, domain, found := strings.Cut(value, "@")
	organizationDomain := strings.TrimSuffix(domain, ".gserviceaccount.com")
	return found && local != "" && organizationDomain != domain && organizationDomain != "" && !strings.ContainsAny(local, "\\/:")
}
