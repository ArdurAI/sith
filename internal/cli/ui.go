// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/ArdurAI/sith/internal/connector"
	"github.com/ArdurAI/sith/internal/connector/kubeconfig"
	"github.com/ArdurAI/sith/internal/fleetcache"
	"github.com/ArdurAI/sith/internal/hydrate"
	"github.com/ArdurAI/sith/internal/localops"
	"github.com/ArdurAI/sith/internal/webui"
)

type uiOptions struct {
	address       string
	port          int
	noOpen        bool
	kubeconfigDir string
}

func newUICommand(reader connector.Reader, local localops.Client) *cobra.Command {
	options := &uiOptions{address: "127.0.0.1"}
	command := &cobra.Command{
		Use:   "ui",
		Short: "Start the loopback-only local fleet IDE",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if reader == nil || local == nil {
				return fmt.Errorf("local fleet UI requires a Kubernetes reader and local operations client")
			}
			if options.kubeconfigDir != "" {
				adapter, err := kubeconfig.New(kubeconfig.WithDirectory(options.kubeconfigDir))
				if err != nil {
					return fmt.Errorf("import kubeconfig directory: %w", err)
				}
				reader, local = adapter, adapter
			}
			return runWebUI(command.Context(), command, reader, local, options)
		},
	}
	command.Flags().StringVar(&options.address, "address", options.address, "loopback listen address")
	command.Flags().IntVar(&options.port, "port", 0, "loopback listen port; 0 selects an available port")
	command.Flags().BoolVar(&options.noOpen, "no-open", false, "do not open the system browser")
	command.Flags().StringVar(&options.kubeconfigDir, "kubeconfig-dir", "", "import kubeconfig files from this directory for this local UI session")
	return command
}

func runWebUI(
	ctx context.Context,
	command *cobra.Command,
	reader connector.Reader,
	local localops.Client,
	options *uiOptions,
) error {
	if err := webui.ValidateLoopbackAddress(options.address); err != nil {
		return err
	}
	if options.port < 0 || options.port > 65535 {
		return fmt.Errorf("local web UI port must be between 0 and 65535")
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(options.address, strconv.Itoa(options.port)))
	if err != nil {
		return fmt.Errorf("listen for local fleet UI: %w", err)
	}
	defer func() { _ = listener.Close() }()
	tcpAddress, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		return fmt.Errorf("local fleet UI listener returned an unexpected address type")
	}
	origin := "http://" + net.JoinHostPort(options.address, strconv.Itoa(tcpAddress.Port))
	store := fleetcache.New()
	hydrator, err := hydrate.New(reader, store)
	if err != nil {
		return err
	}
	application, err := webui.New(ctx, store, hydrator, local)
	if err != nil {
		return err
	}
	defer func() { _ = application.Close() }()
	handler, err := application.Handler(origin)
	if err != nil {
		return err
	}
	server := &http.Server{
		Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 0,
		IdleTimeout: 2 * time.Minute, MaxHeaderBytes: 32 << 10,
	}
	serverErrors := make(chan error, 1)
	go func() { serverErrors <- server.Serve(listener) }()
	go func() { _ = hydrator.Run(ctx) }()
	if _, err := fmt.Fprintf(command.OutOrStdout(), "sith ui listening on %s\n", origin); err != nil {
		return fmt.Errorf("write local fleet UI address: %w", err)
	}
	if !options.noOpen {
		go func() {
			if err := openBrowser(origin); err != nil {
				_, _ = fmt.Fprintf(command.ErrOrStderr(), "warning: open browser: %v\n", err)
			}
		}()
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

func openBrowser(url string) error {
	var name string
	var arguments []string
	switch runtime.GOOS {
	case "darwin":
		name, arguments = "open", []string{url}
	case "windows":
		name, arguments = "rundll32", []string{"url.dll,FileProtocolHandler", url}
	default:
		name, arguments = "xdg-open", []string{url}
	}
	// #nosec G204 -- executable names are fixed above and the URL is a generated loopback origin.
	if err := exec.Command(name, arguments...).Start(); err != nil {
		return err
	}
	return nil
}
