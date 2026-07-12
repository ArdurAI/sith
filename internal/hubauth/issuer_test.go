// SPDX-License-Identifier: Apache-2.0

package hubauth

import (
	"context"
	"crypto/ed25519"
	"strings"
	"testing"
	"time"

	"github.com/ArdurAI/sith/internal/tenancy"
)

func TestSessionIssuerCopiesKeyAndRejectsUnsafeConfiguration(t *testing.T) {
	privateKey := ed25519.NewKeyFromSeed([]byte(strings.Repeat("i", ed25519.SeedSize)))
	configs := []SessionIssuerConfig{
		{Audience: "aud", KeyID: "key", PrivateKey: privateKey},
		{Issuer: "iss", KeyID: "key", PrivateKey: privateKey},
		{Issuer: "iss", Audience: "aud", PrivateKey: privateKey},
		{Issuer: "iss", Audience: "aud", KeyID: "key", PrivateKey: []byte("short")},
		{Issuer: "iss", Audience: "aud", KeyID: "key", PrivateKey: privateKey, Lifetime: time.Second},
	}
	for index, config := range configs {
		if _, err := NewSessionIssuer(config); err == nil {
			t.Errorf("unsafe issuer config %d accepted", index)
		}
	}
	issuer, err := NewSessionIssuer(SessionIssuerConfig{Issuer: "iss", Audience: "aud", KeyID: "key", PrivateKey: privateKey})
	if err != nil {
		t.Fatal(err)
	}
	clear(privateKey)
	principal, err := tenancy.NewPrincipal("user:a", map[tenancy.WorkspaceID]tenancy.Role{"workspace-a": tenancy.RoleReader})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := issuer.Issue(context.Background(), principal); err != nil {
		t.Fatalf("caller key mutation changed issuer: %v", err)
	}
}
