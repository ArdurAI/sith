// SPDX-License-Identifier: Apache-2.0

package hubserver

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ArdurAI/sith/internal/hubauth"
	"github.com/ArdurAI/sith/internal/tenancy"
)

type exchangeStub struct {
	want string
}

type oidcExchangeStub struct {
	wantWorkspace tenancy.WorkspaceID
	wantToken     string
}

func (stub oidcExchangeStub) Exchange(_ context.Context, workspaceID tenancy.WorkspaceID, raw string) (hubauth.IssuedSession, error) {
	if workspaceID != stub.wantWorkspace || raw != stub.wantToken {
		return hubauth.IssuedSession{}, errors.New("invalid")
	}
	return hubauth.IssuedSession{AccessToken: "oidc-session", TokenType: "Bearer", ExpiresAt: time.Now().Add(15 * time.Minute)}, nil
}

func (stub exchangeStub) Exchange(_ context.Context, raw string) (hubauth.IssuedSession, error) {
	if raw != stub.want {
		return hubauth.IssuedSession{}, errors.New("invalid")
	}
	return hubauth.IssuedSession{AccessToken: "signed-session", TokenType: "Bearer", ExpiresAt: time.Now().Add(15 * time.Minute)}, nil
}

func TestExchangeHandlerUsesDistinctSchemeAndNoStore(t *testing.T) {
	limiter, err := NewAttemptLimiter(AttemptLimiterConfig{Attempts: 5, Window: time.Minute, MaxKeys: 10})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewExchangeHandler(exchangeStub{want: "opaque-key"}, limiter)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "https://hub.sith.test/v1/session/exchange", nil)
	request.RemoteAddr = "192.0.2.10:1234"
	request.Header.Set("Authorization", "SithKey opaque-key")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"access_token":"signed-session"`) {
		t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
	}
	if response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("Pragma") != "no-cache" {
		t.Fatal("exchange response may be cached")
	}

	for _, authorization := range []string{"Bearer opaque-key", "SithKey wrong", "SithKey  opaque-key", "SithKey opaque-key,other"} {
		request := httptest.NewRequest(http.MethodPost, "https://hub.sith.test/v1/session/exchange", nil)
		request.RemoteAddr = "192.0.2.11:1234"
		request.Header.Set("Authorization", authorization)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized || response.Body.String() != "{\"error\":\"credential_exchange_failed\"}\n" {
			t.Errorf("authorization %q: status = %d, body = %q", authorization, response.Code, response.Body.String())
		}
	}
}

func TestExchangeHandlerBoundsAttemptsAndClientCardinality(t *testing.T) {
	now := time.Date(2026, 7, 11, 18, 0, 0, 0, time.UTC)
	limiter, err := NewAttemptLimiter(AttemptLimiterConfig{
		Attempts: 2, Window: time.Minute, MaxKeys: 1, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewExchangeHandler(exchangeStub{want: "opaque-key"}, limiter)
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 1; attempt <= 3; attempt++ {
		request := httptest.NewRequest(http.MethodPost, "https://hub.sith.test/v1/session/exchange", nil)
		request.RemoteAddr = "192.0.2.20:1234"
		request.Header.Set("Authorization", "SithKey wrong")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		want := http.StatusUnauthorized
		if attempt == 3 {
			want = http.StatusTooManyRequests
		}
		if response.Code != want {
			t.Fatalf("attempt %d status = %d, want %d", attempt, response.Code, want)
		}
	}
	request := httptest.NewRequest(http.MethodPost, "https://hub.sith.test/v1/session/exchange", nil)
	request.RemoteAddr = "192.0.2.21:1234"
	request.Header.Set("Authorization", "SithKey opaque-key")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("cardinality overflow status = %d", response.Code)
	}
	now = now.Add(time.Minute)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("expired limiter window status = %d", response.Code)
	}
}

func TestExchangeHandlerRejectsWrongMethodAndUnsafeConstruction(t *testing.T) {
	if _, err := NewAttemptLimiter(AttemptLimiterConfig{}); err == nil {
		t.Fatal("unsafe limiter configuration accepted")
	}
	if _, err := NewExchangeHandler(nil, nil); err == nil {
		t.Fatal("nil exchange dependencies accepted")
	}
	limiter, err := NewAttemptLimiter(AttemptLimiterConfig{Attempts: 1, Window: time.Minute, MaxKeys: 1})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewExchangeHandler(exchangeStub{want: "opaque-key"}, limiter)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "https://hub.sith.test/v1/session/exchange", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusMethodNotAllowed || response.Header().Get("Allow") != http.MethodPost {
		t.Fatalf("status = %d, Allow = %q", response.Code, response.Header().Get("Allow"))
	}
}

func TestOIDCExchangeHandlerFixesWorkspaceAndScheme(t *testing.T) {
	limiter, err := NewAttemptLimiter(AttemptLimiterConfig{Attempts: 3, Window: time.Minute, MaxKeys: 3})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewOIDCExchangeHandler("workspace-a", oidcExchangeStub{wantWorkspace: "workspace-a", wantToken: "upstream.jwt.token"}, limiter)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "https://hub.sith.test/v1/workspaces/workspace-a/session/oidc", nil)
	request.Header.Set("Authorization", "OIDC upstream.jwt.token")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"access_token":"oidc-session"`) {
		t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodPost, "https://hub.sith.test/v1/workspaces/workspace-a/session/oidc", nil)
	request.Header.Set("Authorization", "Bearer upstream.jwt.token")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("Bearer upstream token status = %d", response.Code)
	}
	if _, err := NewOIDCExchangeHandler("", oidcExchangeStub{}, limiter); err == nil {
		t.Fatal("empty workspace accepted")
	}
}
