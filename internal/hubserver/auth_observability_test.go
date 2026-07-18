// SPDX-License-Identifier: Apache-2.0

package hubserver

import (
	"context"
	"crypto/ed25519"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ArdurAI/sith/internal/hubauth"
	"github.com/ArdurAI/sith/internal/tenancy"
)

type authVerifierFunc func(context.Context, string) (tenancy.Principal, error)

func (function authVerifierFunc) Verify(ctx context.Context, token string) (tenancy.Principal, error) {
	return function(ctx, token)
}

func TestAuthenticateWithObserverRecordsOnlyUniformRefusals(t *testing.T) {
	var events []AuthEvent
	observer := AuthObserverFunc(func(event AuthEvent) { events = append(events, event) })
	verifier := authVerifierFunc(func(context.Context, string) (tenancy.Principal, error) {
		return tenancy.Principal{}, errors.New("verifier token=secret")
	})
	handler, err := AuthenticateWithObserver(verifier, observer, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("unauthorized request reached handler")
	}))
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name   string
		values []string
	}{
		{name: "missing"},
		{name: "wrong scheme", values: []string{"Basic token=secret"}},
		{name: "ambiguous", values: []string{"Bearer one", "Bearer two"}},
		{name: "verifier rejection", values: []string{"Bearer token=secret"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "https://hub.sith.test/api?token=secret", nil)
			request.Header.Set("X-Correlation-ID", "workspace-a/token=secret")
			for _, value := range test.values {
				request.Header.Add("Authorization", value)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusUnauthorized || response.Body.String() != "{\"error\":\"unauthorized\"}\n" {
				t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
			}
			if len(events) != 1 || events[0] != (AuthEvent{Outcome: AuthOutcomeRefused}) {
				t.Fatalf("events = %#v, want one fixed refusal", events)
			}
			events = nil
		})
	}
}

func TestAuthenticateWithObserverIsSilentAfterValidAuthentication(t *testing.T) {
	now := time.Date(2026, 7, 14, 13, 0, 0, 0, time.UTC)
	publicKey, privateKey := hubTestKeyPair()
	verifier, err := hubauth.NewJWTVerifier(hubauth.JWTConfig{
		Issuer: hubTestIssuer, Audience: hubTestAudience, Keys: map[string]ed25519.PublicKey{hubTestKeyID: publicKey}, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	var events []AuthEvent
	handler, err := AuthenticateWithObserver(verifier, AuthObserverFunc(func(event AuthEvent) { events = append(events, event) }), http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusNoContent)
	}))
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "https://hub.sith.test/api", nil)
	request.Header.Set("Authorization", "Bearer "+signHubTestToken(t, hubValidClaims(now), privateKey))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || len(events) != 0 {
		t.Fatalf("status = %d events = %#v", response.Code, events)
	}
}

func TestObserveAuthRejectsUnsafeEventsAndContainsObserverPanics(t *testing.T) {
	called := false
	ObserveAuth(AuthObserverFunc(func(AuthEvent) { called = true }), AuthEvent{Outcome: "token=secret"})
	if called {
		t.Fatal("unsafe authentication event reached observer")
	}

	ObserveAuth(AuthObserverFunc(func(AuthEvent) { panic("observer fault") }), AuthEvent{Outcome: AuthOutcomeRefused})

	handler, err := AuthenticateWithObserver(authVerifierFunc(func(context.Context, string) (tenancy.Principal, error) {
		return tenancy.Principal{}, errors.New("invalid")
	}), AuthObserverFunc(func(AuthEvent) { panic("observer fault") }), http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("unauthorized request reached handler")
	}))
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "https://hub.sith.test/api", nil))
	if response.Code != http.StatusUnauthorized || response.Body.String() != "{\"error\":\"unauthorized\"}\n" {
		t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
	}
}

func TestAuthObserverFanoutIsolatesEachRequiredDestination(t *testing.T) {
	var deliveries []string
	first := AuthObserverFunc(func(AuthEvent) {
		deliveries = append(deliveries, "first")
		panic("observer fault")
	})
	second := AuthObserverFunc(func(AuthEvent) { deliveries = append(deliveries, "second") })
	observers := []AuthObserver{first, second}
	fanout, err := NewAuthObserverFanout(observers...)
	if err != nil {
		t.Fatal(err)
	}
	observers[1] = AuthObserverFunc(func(AuthEvent) { t.Fatal("fanout retained caller-owned slice") })

	ObserveAuth(fanout, AuthEvent{Outcome: AuthOutcomeRefused})
	if len(deliveries) != 2 || deliveries[0] != "first" || deliveries[1] != "second" {
		t.Fatalf("fanout deliveries = %#v, want independently isolated order", deliveries)
	}

	deliveries = nil
	ObserveAuth(fanout, AuthEvent{Outcome: "token=secret"})
	if len(deliveries) != 0 {
		t.Fatalf("unsafe event reached fanout destinations: %#v", deliveries)
	}
}

func TestNewAuthObserverFanoutRejectsIncompleteConfiguration(t *testing.T) {
	observer := AuthObserverFunc(func(AuthEvent) {})
	for _, observers := range [][]AuthObserver{
		nil,
		{observer},
		{observer, nil},
	} {
		if fanout, err := NewAuthObserverFanout(observers...); err == nil || fanout != nil {
			t.Fatalf("NewAuthObserverFanout(%#v) = %#v, %v", observers, fanout, err)
		}
	}
}
