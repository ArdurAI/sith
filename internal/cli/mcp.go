// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/ArdurAI/sith/internal/buildinfo"
	"github.com/ArdurAI/sith/internal/connector"
	"github.com/ArdurAI/sith/internal/fleetcache"
	"github.com/ArdurAI/sith/internal/hydrate"
	"github.com/ArdurAI/sith/internal/keychain"
	"github.com/ArdurAI/sith/internal/mcpserver"
	"github.com/ArdurAI/sith/internal/webui"
)

type serveOptions struct {
	mcp          bool
	address      string
	port         int
	requireToken bool
	tokenTTL     time.Duration
}

func newServeCommand(reader connector.Reader, secrets keychain.Store) *cobra.Command {
	options := &serveOptions{address: "127.0.0.1", tokenTTL: 15 * time.Minute}
	command := &cobra.Command{
		Use:   "serve",
		Short: "Serve an optional local integration surface",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if !options.mcp {
				return fmt.Errorf("serve requires --mcp")
			}
			if reader == nil {
				return fmt.Errorf("local MCP server requires a Kubernetes reader")
			}
			return runMCPServer(command.Context(), command, reader, secrets, options)
		},
	}
	command.Flags().BoolVar(&options.mcp, "mcp", false, "serve the read-only MCP endpoint")
	command.Flags().StringVar(&options.address, "address", options.address, "loopback listen address")
	command.Flags().IntVar(&options.port, "port", 0, "loopback listen port; 0 selects an available port")
	command.Flags().BoolVar(&options.requireToken, "require-token", false, "require a short-lived keychain-backed bearer token")
	command.Flags().DurationVar(&options.tokenTTL, "token-ttl", options.tokenTTL, "short-lived token lifetime (1m to 24h)")
	return command
}

func runMCPServer(
	ctx context.Context,
	command *cobra.Command,
	reader connector.Reader,
	secrets keychain.Store,
	options *serveOptions,
) (returnErr error) {
	serverCtx, stop := context.WithCancel(ctx)
	defer stop()
	if err := webui.ValidateLoopbackAddress(options.address); err != nil {
		return err
	}
	if options.port < 0 || options.port > 65535 {
		return fmt.Errorf("local MCP port must be between 0 and 65535")
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(options.address, strconv.Itoa(options.port)))
	if err != nil {
		return fmt.Errorf("listen for local MCP: %w", err)
	}
	defer func() {
		if err := listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			returnErr = errors.Join(returnErr, err)
		}
	}()
	tcpAddress, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		return fmt.Errorf("local MCP listener returned an unexpected address type")
	}
	endpoint := "http://" + net.JoinHostPort(options.address, strconv.Itoa(tcpAddress.Port)) + "/mcp"
	store := fleetcache.New()
	hydrator, err := hydrate.New(reader, store)
	if err != nil {
		return err
	}
	state, _ := command.Context().Value(runtimeKey{}).(runtimeState)
	config := mcpserver.Config{
		Audience: endpoint, Version: buildinfo.Get().Version, Logger: state.logger,
		Auditor: mcpserver.NewSlogAuditor(state.logger),
	}
	var token mcpserver.SessionToken
	if options.requireToken {
		if secrets == nil {
			return fmt.Errorf("keychain-backed MCP token is unavailable")
		}
		token, err = mcpserver.NewSessionToken(endpoint, options.tokenTTL)
		if err != nil {
			return err
		}
		if err := secrets.Put(ctx, token.Key, []byte(token.Value)); err != nil {
			return fmt.Errorf("persist short-lived MCP token: %w", err)
		}
		defer func() {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := secrets.Delete(cleanupCtx, token.Key); err != nil && !errors.Is(err, keychain.ErrNotFound) {
				returnErr = errors.Join(returnErr, fmt.Errorf("delete short-lived MCP token: %w", err))
			}
		}()
		config.Token, config.Expiration = token.Value, token.Expiration
	}
	handler, err := mcpserver.New(store, config)
	if err != nil {
		return err
	}
	server := &http.Server{
		Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second,
		WriteTimeout: 30 * time.Second, IdleTimeout: 2 * time.Minute, MaxHeaderBytes: 32 << 10,
	}
	serverErrors := make(chan error, 1)
	go func() { serverErrors <- server.Serve(listener) }()
	go func() { _ = hydrator.Run(serverCtx) }()
	if _, err := fmt.Fprintf(command.OutOrStdout(), "sith MCP listening on %s\n", endpoint); err != nil {
		return fmt.Errorf("write local MCP address: %w", err)
	}
	if options.requireToken {
		if _, err := fmt.Fprintf(
			command.OutOrStdout(),
			"token stored in OS keychain as %s; retrieve explicitly with: sith mcp-token --key %s\n",
			token.Key,
			token.Key,
		); err != nil {
			return fmt.Errorf("write local MCP token reference: %w", err)
		}
	}
	var serveErr error
	select {
	case <-ctx.Done():
	case serveErr = <-serverErrors:
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	shutdownErr := server.Shutdown(shutdownCtx)
	if errors.Is(serveErr, http.ErrServerClosed) {
		serveErr = nil
	}
	return errors.Join(serveErr, shutdownErr)
}

func newMCPTokenCommand(secrets keychain.Store) *cobra.Command {
	var key string
	command := &cobra.Command{
		Use:   "mcp-token",
		Short: "Explicitly reveal one running MCP session token from the OS keychain",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			secret, err := secrets.Get(command.Context(), key)
			if err != nil {
				return err
			}
			if _, err := fmt.Fprintln(command.OutOrStdout(), string(secret)); err != nil {
				return fmt.Errorf("write MCP token: %w", err)
			}
			return nil
		},
	}
	command.Flags().StringVar(&key, "key", "", "keychain entry printed by sith serve --mcp --require-token")
	_ = command.MarkFlagRequired("key")
	return command
}
