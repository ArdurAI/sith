// SPDX-License-Identifier: Apache-2.0

package hubruntime

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"
)

const loopbackMetricsPath = "/metrics"

// loopbackMetricsServer owns the Hub's optional local-only metrics listener. The listener is
// deliberately separate from the tenant-authenticated TLS Hub API and serves only fixed metrics.
type loopbackMetricsServer struct {
	server          *http.Server
	done            chan error
	shutdownTimeout time.Duration
	closeOnce       sync.Once
	closeErr        error
}

type loopbackMetricsServerConfig struct {
	Listener        net.Listener
	Handler         http.Handler
	ShutdownTimeout time.Duration
}

func newLoopbackMetricsServer(config loopbackMetricsServerConfig) (*loopbackMetricsServer, error) {
	if config.Listener == nil || config.Handler == nil {
		return nil, fmt.Errorf("construct loopback metrics server: listener and handler are required")
	}
	if config.ShutdownTimeout == 0 {
		config.ShutdownTimeout = defaultShutdownTimeout
	}
	if config.ShutdownTimeout < time.Second || config.ShutdownTimeout > time.Minute {
		return nil, fmt.Errorf("construct loopback metrics server: shutdown timeout must be between 1s and 1m")
	}
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != loopbackMetricsPath || request.URL.RawQuery != "" {
			http.NotFound(writer, request)
			return
		}
		config.Handler.ServeHTTP(writer, request)
	})
	metricsServer := &loopbackMetricsServer{
		server: &http.Server{
			Handler:           handler,
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       15 * time.Second,
			WriteTimeout:      30 * time.Second,
			IdleTimeout:       time.Minute,
			MaxHeaderBytes:    16 * 1024,
		},
		done:            make(chan error, 1),
		shutdownTimeout: config.ShutdownTimeout,
	}
	go func() {
		metricsServer.done <- metricsServer.server.Serve(config.Listener)
	}()
	return metricsServer, nil
}

// Close stops the listener within a fixed deadline. If graceful shutdown times out, it closes
// active connections and always waits for the Serve goroutine to return.
func (server *loopbackMetricsServer) Close() error {
	if server == nil || server.server == nil || server.done == nil {
		return nil
	}
	server.closeOnce.Do(func() {
		shutdownContext, cancel := context.WithTimeout(context.Background(), server.shutdownTimeout)
		shutdownErr := server.server.Shutdown(shutdownContext)
		cancel()
		if shutdownErr != nil {
			_ = server.server.Close()
		}
		serveErr := <-server.done
		if shutdownErr != nil {
			server.closeErr = fmt.Errorf("stop loopback metrics server: %w", shutdownErr)
			return
		}
		if !errors.Is(serveErr, http.ErrServerClosed) {
			server.closeErr = fmt.Errorf("stop loopback metrics server: %w", serveErr)
		}
	})
	return server.closeErr
}
