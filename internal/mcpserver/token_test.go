// SPDX-License-Identifier: Apache-2.0

package mcpserver

import (
	"strings"
	"testing"
	"time"
)

func TestSessionTokenIsUniqueBoundedAndAudienceScoped(t *testing.T) {
	t.Parallel()
	first, err := NewSessionToken("http://127.0.0.1:8080/mcp", 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewSessionToken("http://127.0.0.1:8080/mcp", 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if first.Key == second.Key || first.Value == second.Value || !strings.HasPrefix(first.Key, "mcp/session/") {
		t.Fatalf("tokens are not unique and namespaced: %#v %#v", first, second)
	}
	if first.Audience != "http://127.0.0.1:8080/mcp" || len(first.Value) < 32 || !first.Expiration.After(time.Now()) {
		t.Fatalf("token = %#v", first)
	}
}

func TestSessionTokenRejectsUnsafeAudienceAndTTL(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		audience string
		ttl      time.Duration
	}{
		{audience: "https://example.com/mcp", ttl: time.Minute},
		{audience: "http://example.com/mcp", ttl: time.Minute},
		{audience: "http://127.0.0.1:8080/other", ttl: time.Minute},
		{audience: "http://127.0.0.1:8080/mcp", ttl: time.Second},
		{audience: "http://127.0.0.1:8080/mcp", ttl: 25 * time.Hour},
	} {
		if _, err := NewSessionToken(test.audience, test.ttl); err == nil {
			t.Errorf("NewSessionToken(%q, %s) error = nil", test.audience, test.ttl)
		}
	}
}
