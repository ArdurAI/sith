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

// InProcessHandler routes requests to the active local UI session. A replaced
// session remains leased only by requests that selected it before the swap.
type InProcessHandler struct {
	mu      sync.Mutex
	current *inProcessSession
}

type inProcessSession struct {
	handler LocalHandler
	active  uint64
	retired bool
	drained chan struct{}
}

// NewInProcessHandler constructs a handler whose active UI session can be
// replaced without starting a network listener.
func NewInProcessHandler(initial LocalHandler) *InProcessHandler {
	handler := &InProcessHandler{}
	if initial != nil {
		handler.current = newInProcessSession(initial)
	}
	return handler
}

func (handler *InProcessHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	session := handler.acquire()
	if session == nil {
		http.Error(response, "local desktop is shutting down", http.StatusServiceUnavailable)
		return
	}
	defer handler.release(session)
	session.handler.ServeHTTP(response, request)
}

// Replace atomically selects the next local UI session. It returns a channel
// that closes once requests leased to the previous session have drained. A nil
// handler makes future requests fail closed without blocking new requests on an
// old, slow operation.
func (handler *InProcessHandler) Replace(next LocalHandler) <-chan struct{} {
	handler.mu.Lock()
	previous := handler.current
	if next == nil {
		handler.current = nil
	} else {
		handler.current = newInProcessSession(next)
	}
	if previous == nil {
		handler.mu.Unlock()
		return nil
	}
	previous.retired = true
	if previous.active == 0 {
		close(previous.drained)
	}
	drained := previous.drained
	handler.mu.Unlock()
	return drained
}

func newInProcessSession(next LocalHandler) *inProcessSession {
	return &inProcessSession{handler: next, drained: make(chan struct{})}
}

func (handler *InProcessHandler) acquire() *inProcessSession {
	handler.mu.Lock()
	defer handler.mu.Unlock()
	if handler.current == nil {
		return nil
	}
	handler.current.active++
	return handler.current
}

func (handler *InProcessHandler) release(session *inProcessSession) {
	handler.mu.Lock()
	defer handler.mu.Unlock()
	session.active--
	if session.retired && session.active == 0 {
		close(session.drained)
	}
}

// InProcessMiddleware adapts a local handler to Wails without using the
// framework default asset route or opening a listener.
func InProcessMiddleware(handler LocalHandler) func(http.Handler) http.Handler {
	return func(http.Handler) http.Handler { return handler }
}
