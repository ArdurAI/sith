// SPDX-License-Identifier: Apache-2.0

package hubauth

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/ArdurAI/sith/internal/tenancy"
)

const (
	defaultJWTType = "sith-session+jwt"
	maxTokenBytes  = 16 * 1024
	maxKeyIDBytes  = 128
)

// ErrInvalidToken deliberately hides signature and claim details from callers.
var ErrInvalidToken = errors.New("invalid signed token")

// JWTConfig defines one strict, locally configured hub session-token profile.
type JWTConfig struct {
	Issuer   string
	Audience string
	Type     string
	Keys     map[string]ed25519.PublicKey
	Leeway   time.Duration
	Now      func() time.Time
}

// JWTVerifier verifies Ed25519 session tokens without remote key discovery or algorithm agility.
type JWTVerifier struct {
	issuer   string
	audience string
	typeName string
	keys     map[string]ed25519.PublicKey
	parser   *jwt.Parser
}

type sessionClaims struct {
	Memberships map[string]tenancy.Role `json:"memberships"`
	jwt.RegisteredClaims
}

// NewJWTVerifier copies a static keyset and builds an RFC 8725-aligned parser profile.
func NewJWTVerifier(config JWTConfig) (*JWTVerifier, error) {
	if strings.TrimSpace(config.Issuer) == "" || strings.TrimSpace(config.Audience) == "" {
		return nil, fmt.Errorf("construct JWT verifier: issuer and audience are required")
	}
	if config.Type == "" {
		config.Type = defaultJWTType
	}
	if strings.TrimSpace(config.Type) != config.Type {
		return nil, fmt.Errorf("construct JWT verifier: token type must be trimmed")
	}
	if len(config.Keys) == 0 {
		return nil, fmt.Errorf("construct JWT verifier: at least one verification key is required")
	}
	keys := make(map[string]ed25519.PublicKey, len(config.Keys))
	for keyID, key := range config.Keys {
		if keyID == "" || strings.TrimSpace(keyID) != keyID || len(keyID) > maxKeyIDBytes {
			return nil, fmt.Errorf("construct JWT verifier: invalid key ID")
		}
		if len(key) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("construct JWT verifier: key %q is not Ed25519", keyID)
		}
		keys[keyID] = append(ed25519.PublicKey(nil), key...)
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.Leeway < 0 || config.Leeway > time.Minute {
		return nil, fmt.Errorf("construct JWT verifier: leeway must be between zero and one minute")
	}
	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{jwt.SigningMethodEdDSA.Alg()}),
		jwt.WithIssuer(config.Issuer),
		jwt.WithAudience(config.Audience),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithLeeway(config.Leeway),
		jwt.WithTimeFunc(config.Now),
		jwt.WithStrictDecoding(),
	)
	return &JWTVerifier{
		issuer: config.Issuer, audience: config.Audience, typeName: config.Type,
		keys: keys, parser: parser,
	}, nil
}

// Verify authenticates the token and returns only validated, defensively copied claims.
func (verifier *JWTVerifier) Verify(ctx context.Context, rawToken string) (tenancy.Principal, error) {
	if verifier == nil || verifier.parser == nil || ctx == nil || ctx.Err() != nil || rawToken == "" || len(rawToken) > maxTokenBytes {
		return tenancy.Principal{}, ErrInvalidToken
	}
	claims := &sessionClaims{}
	token, err := verifier.parser.ParseWithClaims(rawToken, claims, func(token *jwt.Token) (any, error) {
		if token.Method != jwt.SigningMethodEdDSA || token.Header["alg"] != jwt.SigningMethodEdDSA.Alg() {
			return nil, ErrInvalidToken
		}
		if token.Header["typ"] != verifier.typeName || token.Header["jku"] != nil || token.Header["x5u"] != nil || token.Header["crit"] != nil {
			return nil, ErrInvalidToken
		}
		keyID, ok := token.Header["kid"].(string)
		if !ok || keyID == "" || len(keyID) > maxKeyIDBytes {
			return nil, ErrInvalidToken
		}
		key, exists := verifier.keys[keyID]
		if !exists {
			return nil, ErrInvalidToken
		}
		return key, nil
	})
	if err != nil || token == nil || !token.Valid || claims.IssuedAt == nil || claims.ID == "" || strings.TrimSpace(claims.Subject) == "" {
		return tenancy.Principal{}, ErrInvalidToken
	}
	memberships := make(map[tenancy.WorkspaceID]tenancy.Role, len(claims.Memberships))
	for workspaceID, role := range claims.Memberships {
		memberships[tenancy.WorkspaceID(workspaceID)] = role
	}
	principal, err := tenancy.NewPrincipal(claims.Subject, memberships)
	if err != nil {
		return tenancy.Principal{}, ErrInvalidToken
	}
	return principal, nil
}
