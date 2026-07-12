// SPDX-License-Identifier: Apache-2.0

package hubauth

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/ArdurAI/sith/internal/tenancy"
)

const (
	defaultSessionLifetime = 15 * time.Minute
	maximumIssuedLifetime  = time.Hour
)

// SessionIssuerConfig defines one locally controlled Ed25519 session issuer.
type SessionIssuerConfig struct {
	Issuer     string
	Audience   string
	Type       string
	KeyID      string
	PrivateKey ed25519.PrivateKey
	Lifetime   time.Duration
	Now        func() time.Time
	Random     io.Reader
}

// SessionIssuer creates short-lived tokens accepted by JWTVerifier's strict profile.
type SessionIssuer struct {
	issuer     string
	audience   string
	typeName   string
	keyID      string
	privateKey ed25519.PrivateKey
	lifetime   time.Duration
	now        func() time.Time
	random     io.Reader
}

// IssuedSession is the only successful result returned by credential exchange.
type IssuedSession struct {
	AccessToken string    `json:"access_token"`
	TokenType   string    `json:"token_type"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// NewSessionIssuer validates and copies signing configuration.
func NewSessionIssuer(config SessionIssuerConfig) (*SessionIssuer, error) {
	if strings.TrimSpace(config.Issuer) == "" || strings.TrimSpace(config.Issuer) != config.Issuer ||
		strings.TrimSpace(config.Audience) == "" || strings.TrimSpace(config.Audience) != config.Audience {
		return nil, fmt.Errorf("construct session issuer: issuer and audience must be non-empty trimmed values")
	}
	if config.Type == "" {
		config.Type = defaultJWTType
	}
	if strings.TrimSpace(config.Type) != config.Type || config.Type == "" {
		return nil, fmt.Errorf("construct session issuer: token type must be a trimmed value")
	}
	if config.KeyID == "" || strings.TrimSpace(config.KeyID) != config.KeyID || len(config.KeyID) > maxKeyIDBytes {
		return nil, fmt.Errorf("construct session issuer: invalid key ID")
	}
	if len(config.PrivateKey) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("construct session issuer: signing key is not Ed25519")
	}
	if config.Lifetime == 0 {
		config.Lifetime = defaultSessionLifetime
	}
	if config.Lifetime < time.Minute || config.Lifetime > maximumIssuedLifetime {
		return nil, fmt.Errorf("construct session issuer: lifetime must be between one minute and one hour")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.Random == nil {
		config.Random = rand.Reader
	}
	return &SessionIssuer{
		issuer: config.Issuer, audience: config.Audience, typeName: config.Type, keyID: config.KeyID,
		privateKey: append(ed25519.PrivateKey(nil), config.PrivateKey...), lifetime: config.Lifetime,
		now: config.Now, random: config.Random,
	}, nil
}

// Issue signs a fresh session containing only validated, current memberships.
func (issuer *SessionIssuer) Issue(ctx context.Context, principal tenancy.Principal) (IssuedSession, error) {
	if issuer == nil || len(issuer.privateKey) != ed25519.PrivateKeySize || ctx == nil || ctx.Err() != nil {
		return IssuedSession{}, fmt.Errorf("issue session: issuer and active context are required")
	}
	validated, err := tenancy.NewPrincipal(principal.Subject(), principal.Memberships())
	if err != nil {
		return IssuedSession{}, fmt.Errorf("issue session: invalid principal: %w", err)
	}
	tokenIDBytes := make([]byte, 16)
	if _, err := io.ReadFull(issuer.random, tokenIDBytes); err != nil {
		return IssuedSession{}, fmt.Errorf("issue session: generate token ID: %w", err)
	}
	now := issuer.now().UTC()
	expiresAt := now.Add(issuer.lifetime)
	memberships := make(map[string]tenancy.Role, len(validated.Memberships()))
	for workspaceID, role := range validated.Memberships() {
		memberships[string(workspaceID)] = role
	}
	claims := sessionClaims{
		Memberships: memberships,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer: issuer.issuer, Subject: validated.Subject(), Audience: jwt.ClaimStrings{issuer.audience},
			ExpiresAt: jwt.NewNumericDate(expiresAt), NotBefore: jwt.NewNumericDate(now),
			IssuedAt: jwt.NewNumericDate(now), ID: base64.RawURLEncoding.EncodeToString(tokenIDBytes),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	token.Header["typ"] = issuer.typeName
	token.Header["kid"] = issuer.keyID
	rawToken, err := token.SignedString(issuer.privateKey)
	if err != nil {
		return IssuedSession{}, fmt.Errorf("issue session: sign token: %w", err)
	}
	return IssuedSession{AccessToken: rawToken, TokenType: "Bearer", ExpiresAt: expiresAt}, nil
}
