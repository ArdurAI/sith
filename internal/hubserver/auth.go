// SPDX-License-Identifier: Apache-2.0

package hubserver

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/ArdurAI/sith/internal/tenancy"
)

type principalContextKey struct{}

const maxBearerTokenBytes = 16 * 1024

// Verifier authenticates a bearer token without granting access from request metadata.
type Verifier interface {
	Verify(context.Context, string) (tenancy.Principal, error)
}

// Authenticate constructs middleware that removes spoofable identity headers and requires a token.
// It uses no authentication observer; composition roots that need local security logging should
// use AuthenticateWithObserver.
func Authenticate(verifier Verifier, next http.Handler) (http.Handler, error) {
	return AuthenticateWithObserver(verifier, nil, next)
}

// AuthenticateWithObserver constructs authentication middleware with one passive outcome observer.
// The observer is never given request metadata, credentials, verifier errors, or caller correlation
// values, and cannot alter the uniform unauthorized response or successful handler path.
func AuthenticateWithObserver(verifier Verifier, observer AuthObserver, next http.Handler) (http.Handler, error) {
	if verifier == nil {
		return nil, fmt.Errorf("construct authentication middleware: verifier is required")
	}
	if next == nil {
		return nil, fmt.Errorf("construct authentication middleware: next handler is required")
	}
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		cloned := request.Clone(request.Context())
		cloned.Header = request.Header.Clone()
		stripUntrustedIdentityHeaders(cloned.Header)
		stripUntrustedCorrelationHeaders(cloned.Header)
		rawToken, ok := bearerToken(cloned.Header.Values("Authorization"))
		if !ok {
			refuseAuthentication(observer, response)
			return
		}
		cloned.Header.Del("Authorization")
		principal, err := verifier.Verify(cloned.Context(), rawToken)
		if err != nil {
			refuseAuthentication(observer, response)
			return
		}
		ObserveAuth(observer, AuthEvent{Outcome: AuthOutcomeAccepted})
		ctx := context.WithValue(cloned.Context(), principalContextKey{}, principal)
		next.ServeHTTP(response, cloned.WithContext(ctx))
	}), nil
}

// PrincipalFromContext returns a defensive copy of the authenticated principal.
func PrincipalFromContext(ctx context.Context) (tenancy.Principal, bool) {
	if ctx == nil {
		return tenancy.Principal{}, false
	}
	principal, ok := ctx.Value(principalContextKey{}).(tenancy.Principal)
	if !ok || principal.Subject() == "" {
		return tenancy.Principal{}, false
	}
	cloned, err := tenancy.NewPrincipal(principal.Subject(), principal.Memberships())
	return cloned, err == nil
}

// ScopeFromContext resolves one workspace from signed membership claims only.
func ScopeFromContext(ctx context.Context, workspaceID tenancy.WorkspaceID) (tenancy.Scope, error) {
	principal, ok := PrincipalFromContext(ctx)
	if !ok {
		return tenancy.Scope{}, fmt.Errorf("authenticated principal is required")
	}
	return principal.Scope(workspaceID)
}

func bearerToken(values []string) (string, bool) {
	return authorizationCredential(values, "Bearer", maxBearerTokenBytes)
}

func stripUntrustedIdentityHeaders(headers http.Header) {
	for name := range headers {
		normalized := strings.ToLower(name)
		if !strings.HasPrefix(normalized, "x-") {
			continue
		}
		for _, suffix := range []string{"-role", "-roles", "-tenant", "-workspace", "-subject", "-user", "-identity", "-membership", "-memberships"} {
			if normalized == "x"+suffix || strings.HasSuffix(normalized, suffix) {
				headers.Del(name)
				break
			}
		}
	}
}

func stripUntrustedCorrelationHeaders(headers http.Header) {
	for name := range headers {
		normalized := strings.ToLower(name)
		if normalized == "traceparent" || normalized == "tracestate" || normalized == "b3" ||
			normalized == "x-request-id" || normalized == "x-correlation-id" || normalized == "x-trace-id" ||
			strings.HasPrefix(normalized, "x-b3-") {
			headers.Del(name)
		}
	}
}

func writeUnauthorized(response http.ResponseWriter) {
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(http.StatusUnauthorized)
	_, _ = response.Write([]byte("{\"error\":\"unauthorized\"}\n"))
}

func refuseAuthentication(observer AuthObserver, response http.ResponseWriter) {
	ObserveAuth(observer, AuthEvent{Outcome: AuthOutcomeRefused})
	writeUnauthorized(response)
}
