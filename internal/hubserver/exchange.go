// SPDX-License-Identifier: Apache-2.0

package hubserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/ArdurAI/sith/internal/hubauth"
)

const maxSithKeyBytes = 512

// SessionExchanger trades one API key for a short-lived signed session.
type SessionExchanger interface {
	Exchange(context.Context, string) (hubauth.IssuedSession, error)
}

// AttemptLimiterConfig bounds failed and successful exchange attempts per client address.
type AttemptLimiterConfig struct {
	Attempts int
	Window   time.Duration
	MaxKeys  int
	Now      func() time.Time
}

// AttemptLimiter is a bounded, in-memory fixed-window defense for the exchange endpoint.
type AttemptLimiter struct {
	mu       sync.Mutex
	attempts int
	window   time.Duration
	maxKeys  int
	now      func() time.Time
	entries  map[string]attemptWindow
}

type attemptWindow struct {
	started time.Time
	count   int
}

type exchangeResponse struct {
	// #nosec G117 -- the exchange protocol intentionally returns a short-lived access token under no-store headers.
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int64  `json:"expires_in"`
}

// NewAttemptLimiter creates a bounded limiter. Distributed deployments should add an upstream shared limit.
func NewAttemptLimiter(config AttemptLimiterConfig) (*AttemptLimiter, error) {
	if config.Attempts < 1 || config.Attempts > 1000 || config.Window < time.Second || config.Window > time.Hour || config.MaxKeys < 1 {
		return nil, fmt.Errorf("construct exchange limiter: invalid attempts, window, or key capacity")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &AttemptLimiter{
		attempts: config.Attempts, window: config.Window, maxKeys: config.MaxKeys,
		now: config.Now, entries: make(map[string]attemptWindow),
	}, nil
}

// Allow records one attempt while keeping attacker-controlled cardinality bounded.
func (limiter *AttemptLimiter) Allow(key string) bool {
	if limiter == nil || key == "" {
		return false
	}
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	now := limiter.now().UTC()
	entry, exists := limiter.entries[key]
	if !exists && len(limiter.entries) >= limiter.maxKeys {
		for candidate, candidateWindow := range limiter.entries {
			if !now.Before(candidateWindow.started.Add(limiter.window)) {
				delete(limiter.entries, candidate)
				break
			}
		}
		if len(limiter.entries) >= limiter.maxKeys {
			return false
		}
	}
	if !exists || !now.Before(entry.started.Add(limiter.window)) {
		limiter.entries[key] = attemptWindow{started: now, count: 1}
		return true
	}
	if entry.count >= limiter.attempts {
		return false
	}
	entry.count++
	limiter.entries[key] = entry
	return true
}

// NewExchangeHandler constructs the only HTTP endpoint that accepts the SithKey scheme.
func NewExchangeHandler(exchanger SessionExchanger, limiter *AttemptLimiter) (http.Handler, error) {
	if exchanger == nil || limiter == nil {
		return nil, fmt.Errorf("construct API key exchange handler: exchanger and limiter are required")
	}
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		setNoStore(response.Header())
		if request.Method != http.MethodPost {
			response.Header().Set("Allow", http.MethodPost)
			writeExchangeError(response, http.StatusMethodNotAllowed)
			return
		}
		if !limiter.Allow(clientAddress(request.RemoteAddr)) {
			response.Header().Set("Retry-After", "60")
			writeExchangeError(response, http.StatusTooManyRequests)
			return
		}
		raw, ok := authorizationCredential(request.Header.Values("Authorization"), "SithKey", maxSithKeyBytes)
		if !ok {
			writeExchangeError(response, http.StatusUnauthorized)
			return
		}
		session, err := exchanger.Exchange(request.Context(), raw)
		if err != nil || session.AccessToken == "" || session.TokenType != "Bearer" || !session.ExpiresAt.After(time.Now().Add(-time.Minute)) {
			writeExchangeError(response, http.StatusUnauthorized)
			return
		}
		expiresIn := int64(time.Until(session.ExpiresAt).Seconds())
		if expiresIn < 0 {
			writeExchangeError(response, http.StatusUnauthorized)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		response.WriteHeader(http.StatusOK)
		// #nosec G117 -- the protocol response is deliberate and protected by no-store headers.
		_ = json.NewEncoder(response).Encode(exchangeResponse{
			AccessToken: session.AccessToken, TokenType: session.TokenType, ExpiresIn: expiresIn,
		})
	}), nil
}

func authorizationCredential(values []string, expectedScheme string, maxBytes int) (string, bool) {
	if len(values) != 1 {
		return "", false
	}
	scheme, credentials, found := strings.Cut(values[0], " ")
	if !found || !strings.EqualFold(scheme, expectedScheme) || credentials == "" || len(credentials) > maxBytes ||
		strings.TrimSpace(credentials) != credentials || strings.ContainsAny(credentials, " \t\r\n,") {
		return "", false
	}
	return credentials, true
}

func clientAddress(remoteAddress string) string {
	host, _, err := net.SplitHostPort(remoteAddress)
	if err == nil && host != "" {
		return host
	}
	if len(remoteAddress) > 256 {
		return remoteAddress[:256]
	}
	if remoteAddress == "" {
		return "unknown"
	}
	return remoteAddress
}

func setNoStore(header http.Header) {
	header.Set("Cache-Control", "no-store")
	header.Set("Pragma", "no-cache")
}

func writeExchangeError(response http.ResponseWriter, status int) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_, _ = response.Write([]byte("{\"error\":\"credential_exchange_failed\"}\n"))
}
