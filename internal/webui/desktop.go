// SPDX-License-Identifier: Apache-2.0

package webui

import (
	"net/http"
	"sync"
)

// LocalHandler is a same-process HTTP request handler. It exists so a native
// WebView can reuse the hardened web UI without opening a TCP listener.
type LocalHandler interface {
	ServeHTTP(http.ResponseWriter, *http.Request)
}

// InProcessHandler routes one request at a time to the active local UI session.
// Replace waits for an in-flight request before the caller closes the old session.
type InProcessHandler struct {
	mu      sync.RWMutex
	current LocalHandler
}

// NewInProcessHandler constructs a handler whose active UI session can be
// replaced without starting a network listener.
func NewInProcessHandler(initial LocalHandler) *InProcessHandler {
	return &InProcessHandler{current: initial}
}

func (handler *InProcessHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	handler.mu.RLock()
	defer handler.mu.RUnlock()
	if handler.current == nil {
		http.Error(response, "local desktop is shutting down", http.StatusServiceUnavailable)
		return
	}
	handler.current.ServeHTTP(response, request)
}

// Replace waits for the active request, then atomically selects the next local
// UI session. A nil handler makes future requests fail closed.
func (handler *InProcessHandler) Replace(next LocalHandler) {
	handler.mu.Lock()
	handler.current = next
	handler.mu.Unlock()
}

// InProcessMiddleware adapts a local handler to Wails without using the
// framework default asset route or opening a listener.
func InProcessMiddleware(handler LocalHandler) func(http.Handler) http.Handler {
	return func(http.Handler) http.Handler { return handler }
}
