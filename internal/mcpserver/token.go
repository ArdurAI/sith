// SPDX-License-Identifier: Apache-2.0

package mcpserver

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"time"
)

const (
	tokenBytes = 32
	keyBytes   = 18
)

// SessionToken is one short-lived, audience-bound local MCP capability.
type SessionToken struct {
	Key        string
	Value      string
	Audience   string
	Expiration time.Time
}

// NewSessionToken generates a token and unique OS-keychain entry name for one listener.
func NewSessionToken(audience string, ttl time.Duration) (SessionToken, error) {
	if _, err := validateAudience(audience); err != nil {
		return SessionToken{}, fmt.Errorf("generate MCP token: %w", err)
	}
	if ttl < time.Minute || ttl > 24*time.Hour {
		return SessionToken{}, fmt.Errorf("generate MCP token: TTL must be between 1 minute and 24 hours")
	}
	value, err := randomText(tokenBytes)
	if err != nil {
		return SessionToken{}, fmt.Errorf("generate MCP token value: %w", err)
	}
	identifier, err := randomText(keyBytes)
	if err != nil {
		return SessionToken{}, fmt.Errorf("generate MCP token key: %w", err)
	}
	return SessionToken{
		Key: "mcp/session/" + identifier, Value: value, Audience: audience,
		Expiration: time.Now().UTC().Add(ttl),
	}, nil
}

func randomText(size int) (string, error) {
	payload := make([]byte, size)
	if _, err := rand.Read(payload); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(payload), nil
}
