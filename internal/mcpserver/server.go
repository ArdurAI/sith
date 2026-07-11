// SPDX-License-Identifier: Apache-2.0

package mcpserver

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ArdurAI/sith/internal/fleetcache"
)

const readScope = "fleet.read"

// Config defines one loopback MCP endpoint and its optional local capability token.
type Config struct {
	Audience   string
	Version    string
	Token      string
	Expiration time.Time
	Logger     *slog.Logger
	Auditor    Auditor
}

type application struct {
	store      *fleetcache.Store
	audience   string
	token      string
	expiration time.Time
	auditor    Auditor
	now        func() time.Time
}

// New constructs a hardened Streamable HTTP MCP handler without opening a listener.
func New(store *fleetcache.Store, config Config) (http.Handler, error) {
	if store == nil {
		return nil, fmt.Errorf("construct MCP server: fleet store is nil")
	}
	parsed, err := validateAudience(config.Audience)
	if err != nil {
		return nil, fmt.Errorf("construct MCP server: %w", err)
	}
	if (config.Token == "") != config.Expiration.IsZero() {
		return nil, fmt.Errorf("construct MCP server: token and expiration must be configured together")
	}
	if config.Token != "" && (len(config.Token) < 32 || !config.Expiration.After(time.Now())) {
		return nil, fmt.Errorf("construct MCP server: token must be at least 32 bytes and unexpired")
	}
	if config.Auditor == nil {
		config.Auditor = NewSlogAuditor(config.Logger)
	}
	app := &application{
		store: store, audience: config.Audience, token: config.Token,
		expiration: config.Expiration, auditor: config.Auditor, now: time.Now,
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "sith", Version: cleanVersion(config.Version)}, nil)
	app.addTools(server)
	streamable := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{
		Stateless: true, JSONResponse: true, Logger: config.Logger,
	})
	handler := exactEndpoint(parsed, streamable)
	handler = http.NewCrossOriginProtection().Handler(handler)
	if config.Token != "" {
		handler = auth.RequireBearerToken(app.verifyToken, &auth.RequireBearerTokenOptions{Scopes: []string{readScope}})(handler)
	}
	return securityHeaders(handler), nil
}

func validateAudience(audience string) (*url.URL, error) {
	parsed, err := url.Parse(audience)
	if err != nil || parsed.Scheme != "http" || parsed.Host == "" || parsed.Path != "/mcp" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("audience must be an absolute http /mcp URL")
	}
	hostname := parsed.Hostname()
	if hostname != "localhost" {
		ip := net.ParseIP(hostname)
		if ip == nil || !ip.IsLoopback() {
			return nil, fmt.Errorf("audience host must be loopback")
		}
	}
	return parsed, nil
}

func (app *application) verifyToken(_ context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
	if subtle.ConstantTimeCompare([]byte(token), []byte(app.token)) != 1 {
		return nil, auth.ErrInvalidToken
	}
	return &auth.TokenInfo{
		Scopes: []string{readScope}, Expiration: app.expiration, UserID: "local-keychain-token",
		Extra: map[string]any{"audience": app.audience},
	}, nil
}

func (app *application) authorizeExecution(ctx context.Context) (string, error) {
	if app.token == "" {
		return "local-loopback", nil
	}
	info := auth.TokenInfoFromContext(ctx)
	if info == nil || !slices.Contains(info.Scopes, readScope) || !info.Expiration.After(app.now()) {
		return "", errors.New("MCP tool execution is not authorized")
	}
	audience, _ := info.Extra["audience"].(string)
	if subtle.ConstantTimeCompare([]byte(audience), []byte(app.audience)) != 1 {
		return "", errors.New("MCP token audience does not match this server")
	}
	return info.UserID, nil
}

func exactEndpoint(endpoint *url.URL, next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Host != endpoint.Host || request.URL.Path != endpoint.Path {
			http.Error(response, "request does not match the local MCP endpoint", http.StatusForbidden)
			return
		}
		next.ServeHTTP(response, request)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Cache-Control", "no-store")
		response.Header().Set("X-Content-Type-Options", "nosniff")
		response.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(response, request)
	})
}

func cleanVersion(version string) string {
	if trimmed := strings.TrimSpace(version); trimmed != "" {
		return trimmed
	}
	return "dev"
}
