// SPDX-License-Identifier: Apache-2.0

package hubruntime

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"
)

const defaultShutdownTimeout = 10 * time.Second

// ServerConfig fixes the TLS listener and authenticated handler for one hub process.
type ServerConfig struct {
	Listener        net.Listener
	Handler         http.Handler
	TLSConfig       *tls.Config
	ShutdownTimeout time.Duration
}

// Server runs the hub's fixed authenticated HTTP surface over deployment-provided TLS.
type Server struct {
	listener        net.Listener
	handler         http.Handler
	tlsConfig       *tls.Config
	shutdownTimeout time.Duration
}

// NewServer constructs a server that refuses plaintext and dynamically supplied certificates.
func NewServer(config ServerConfig) (*Server, error) {
	if config.Listener == nil || config.Handler == nil || config.TLSConfig == nil {
		return nil, fmt.Errorf("construct hub server: listener, handler, and TLS configuration are required")
	}
	if config.TLSConfig.MinVersion < tls.VersionTLS12 || len(config.TLSConfig.Certificates) != 1 ||
		len(config.TLSConfig.Certificates[0].Certificate) == 0 || config.TLSConfig.Certificates[0].PrivateKey == nil ||
		config.TLSConfig.GetCertificate != nil || config.TLSConfig.GetConfigForClient != nil {
		return nil, fmt.Errorf("construct hub server: TLS 1.2+ and one static server certificate are required")
	}
	if config.ShutdownTimeout == 0 {
		config.ShutdownTimeout = defaultShutdownTimeout
	}
	if config.ShutdownTimeout < time.Second || config.ShutdownTimeout > time.Minute {
		return nil, fmt.Errorf("construct hub server: shutdown timeout must be between 1s and 1m")
	}
	return &Server{
		listener:        config.Listener,
		handler:         config.Handler,
		tlsConfig:       config.TLSConfig.Clone(),
		shutdownTimeout: config.ShutdownTimeout,
	}, nil
}

// Run serves until the process context is canceled or a non-shutdown server error occurs.
func (server *Server) Run(ctx context.Context) error {
	if server == nil || server.listener == nil || server.handler == nil || server.tlsConfig == nil || ctx == nil {
		return fmt.Errorf("run hub server: server and context are required")
	}
	httpServer := &http.Server{
		Handler:           server.handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       time.Minute,
		MaxHeaderBytes:    16 * 1024,
	}
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- httpServer.Serve(tls.NewListener(server.listener, server.tlsConfig.Clone()))
	}()

	select {
	case err := <-serveDone:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("run hub server: %w", err)
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), server.shutdownTimeout)
		defer cancel()
		shutdownErr := httpServer.Shutdown(shutdownCtx)
		serveErr := <-serveDone
		if shutdownErr != nil {
			return fmt.Errorf("stop hub server: %w", shutdownErr)
		}
		if !errors.Is(serveErr, http.ErrServerClosed) {
			return fmt.Errorf("stop hub server: %w", serveErr)
		}
		return nil
	}
}
