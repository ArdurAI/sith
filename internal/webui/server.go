// SPDX-License-Identifier: Apache-2.0

// Package webui serves Sith's embedded fleet frontend against a mode-neutral fleet API.
package webui

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/ArdurAI/sith/internal/fleetcache"
	"github.com/ArdurAI/sith/internal/localops"
)

const (
	csrfHeader = "X-Sith-CSRF"
	localMode  = "local"

	// DesktopOrigin is Wails' in-process macOS WebView origin. It never binds a
	// TCP listener and is accepted only by the native desktop host.
	DesktopOrigin = "wails://wails"
)

//go:embed assets/*
var embeddedAssets embed.FS

// Syncer is the explicit background-refresh seam used by the web API.
type Syncer interface {
	SyncOnce(ctx context.Context) error
	SyncKinds(ctx context.Context, kinds ...string) error
}

// Application owns the embedded frontend, cache API, and live local sessions.
type Application struct {
	store      *fleetcache.Store
	syncer     Syncer
	local      localops.Client
	mode       string
	token      string
	assets     fs.FS
	forwards   *forwardManager
	previews   *previewManager
	ctx        context.Context
	cancel     context.CancelFunc
	refreshing atomic.Bool

	closeOnce sync.Once
}

// New constructs a local-mode fleet web application without opening a listener.
func New(ctx context.Context, store *fleetcache.Store, syncer Syncer, local localops.Client) (*Application, error) {
	if ctx == nil {
		return nil, fmt.Errorf("construct web UI: context is nil")
	}
	if store == nil {
		return nil, fmt.Errorf("construct web UI: store is nil")
	}
	if syncer == nil {
		return nil, fmt.Errorf("construct web UI: syncer is nil")
	}
	if local == nil {
		return nil, fmt.Errorf("construct web UI: local operations client is nil")
	}
	assets, err := fs.Sub(embeddedAssets, "assets")
	if err != nil {
		return nil, fmt.Errorf("load embedded frontend: %w", err)
	}
	token, err := randomToken(32)
	if err != nil {
		return nil, err
	}
	applicationCtx, cancel := context.WithCancel(ctx)
	return &Application{
		store: store, syncer: syncer, local: local, mode: localMode, token: token,
		assets: assets, forwards: newForwardManager(), previews: newPreviewManager(),
		ctx: applicationCtx, cancel: cancel,
	}, nil
}

// Handler returns the hardened frontend/API handler for one exact local origin.
func (application *Application) Handler(baseURL string) (http.Handler, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("configure web UI handler: base URL must be an exact local origin")
	}
	if baseURL != DesktopOrigin {
		if parsed.Scheme != "http" || ValidateLoopbackAddress(parsed.Hostname()) != nil {
			return nil, fmt.Errorf("configure web UI handler: base URL must be a loopback http origin")
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", application.serveIndex)
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServerFS(application.assets)))
	application.registerAPI(mux)
	return application.securityMiddleware(parsed)(mux), nil
}

// Close stops every long-lived local session owned by this application.
func (application *Application) Close() error {
	var closeErr error
	application.closeOnce.Do(func() {
		application.cancel()
		application.previews.clear()
		closeErr = application.forwards.closeAll()
	})
	return closeErr
}

// ValidateLoopbackAddress refuses any local-mode listener that is not loopback.
func ValidateLoopbackAddress(address string) error {
	trimmed := strings.TrimSpace(address)
	if trimmed == "localhost" {
		return nil
	}
	ip := net.ParseIP(trimmed)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("local web UI address %q is not loopback", address)
	}
	return nil
}

func (application *Application) serveIndex(response http.ResponseWriter, request *http.Request) {
	if request.URL.Path != "/" {
		http.NotFound(response, request)
		return
	}
	payload, err := fs.ReadFile(application.assets, "index.html")
	if err != nil {
		writeAPIError(response, http.StatusInternalServerError, "embedded frontend is unavailable")
		return
	}
	page := strings.ReplaceAll(string(payload), "__SITH_CSRF_TOKEN__", application.token)
	page = strings.ReplaceAll(page, "__SITH_MODE__", application.mode)
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.Header().Set("Cache-Control", "no-store")
	_, _ = response.Write([]byte(page))
}

func (application *Application) securityMiddleware(origin *url.URL) func(http.Handler) http.Handler {
	expectedHost := origin.Host
	expectedOrigin := origin.Scheme + "://" + origin.Host
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			setSecurityHeaders(response.Header())
			if request.Host != expectedHost {
				writeAPIError(response, http.StatusForbidden, "request host does not match the local listener")
				return
			}
			if strings.HasPrefix(request.URL.Path, "/api/") {
				if supplied := request.Header.Get(csrfHeader); subtle.ConstantTimeCompare([]byte(supplied), []byte(application.token)) != 1 {
					writeAPIError(response, http.StatusForbidden, "missing or invalid local session capability")
					return
				}
				if suppliedOrigin := request.Header.Get("Origin"); suppliedOrigin != "" && suppliedOrigin != expectedOrigin {
					writeAPIError(response, http.StatusForbidden, "request origin does not match the local listener")
					return
				}
				response.Header().Set("Cache-Control", "no-store")
			}
			next.ServeHTTP(response, request)
		})
	}
}

func setSecurityHeaders(header http.Header) {
	header.Set("Content-Security-Policy", "default-src 'self'; base-uri 'none'; connect-src 'self'; font-src 'self'; form-action 'self'; frame-ancestors 'none'; img-src 'self' data:; object-src 'none'; script-src 'self'; style-src 'self'")
	header.Set("Permissions-Policy", "camera=(), display-capture=(), geolocation=(), microphone=(), payment=(), usb=()")
	header.Set("Referrer-Policy", "no-referrer")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("X-Frame-Options", "DENY")
	header.Set("Cross-Origin-Resource-Policy", "same-origin")
}

func randomToken(bytes int) (string, error) {
	payload := make([]byte, bytes)
	if _, err := rand.Read(payload); err != nil {
		return "", fmt.Errorf("generate local web session capability: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(payload), nil
}

func decodeJSON(response http.ResponseWriter, request *http.Request, limit int64, destination any) error {
	request.Body = http.MaxBytesReader(response, request.Body, limit)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("decode request JSON: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return fmt.Errorf("decode request JSON: multiple values are not allowed")
	}
	return nil
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}

func writeAPIError(response http.ResponseWriter, status int, message string) {
	writeJSON(response, status, map[string]string{"error": message})
}
