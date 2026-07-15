// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/spf13/cobra"

	"github.com/ArdurAI/sith/internal/connector"
	"github.com/ArdurAI/sith/internal/connector/kubeconfig"
	"github.com/ArdurAI/sith/internal/fleetcache"
	"github.com/ArdurAI/sith/internal/hydrate"
	"github.com/ArdurAI/sith/internal/localops"
	"github.com/ArdurAI/sith/internal/webui"
)

type desktopOptions struct {
	kubeconfigDir string
}

func newDesktopCommand(reader connector.Reader, local localops.Client) *cobra.Command {
	options := &desktopOptions{}
	command := &cobra.Command{
		Use:   "desktop",
		Short: "Open the native local fleet IDE on macOS",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if reader == nil || local == nil {
				return fmt.Errorf("local fleet desktop requires a Kubernetes reader and local operations client")
			}
			return runDesktop(command.Context(), reader, local, options.kubeconfigDir)
		},
	}
	command.Flags().StringVar(&options.kubeconfigDir, "kubeconfig-dir", "", "import kubeconfig files from this directory for this local desktop session")
	return command
}

type desktopSourceFactory func(string) (connector.Reader, localops.Client, error)

type desktopSession struct {
	cancel      context.CancelFunc
	application *webui.Application
	handler     webui.LocalHandler
}

func newDesktopSession(parent context.Context, reader connector.Reader, local localops.Client) (*desktopSession, error) {
	ctx, cancel := context.WithCancel(parent)
	store := fleetcache.New()
	hydrator, err := hydrate.New(reader, store)
	if err != nil {
		cancel()
		return nil, err
	}
	application, err := webui.New(ctx, store, hydrator, local)
	if err != nil {
		cancel()
		return nil, err
	}
	handler, err := application.Handler(webui.DesktopOrigin)
	if err != nil {
		_ = application.Close()
		cancel()
		return nil, err
	}
	go func() { _ = hydrator.Run(ctx) }()
	return &desktopSession{cancel: cancel, application: application, handler: handler}, nil
}

func (session *desktopSession) close() {
	if session == nil {
		return
	}
	session.cancel()
	_ = session.application.Close()
}

// desktopHost swaps complete in-memory sessions after a native folder choice.
// It never persists or returns the selected filesystem path.
type desktopHost struct {
	ctx       context.Context
	newSource desktopSourceFactory

	mu      sync.RWMutex
	closed  bool
	session *desktopSession
	handler *webui.InProcessHandler
}

func newDesktopHost(ctx context.Context, reader connector.Reader, local localops.Client) (*desktopHost, error) {
	if reader == nil || local == nil {
		return nil, fmt.Errorf("construct local fleet desktop: Kubernetes access is unavailable")
	}
	host := &desktopHost{
		ctx:       ctx,
		newSource: desktopDirectorySource,
	}
	session, err := newDesktopSession(ctx, reader, local)
	if err != nil {
		return nil, err
	}
	host.session = session
	host.handler = webui.NewInProcessHandler(session.handler)
	return host, nil
}

func desktopDirectorySource(directory string) (connector.Reader, localops.Client, error) {
	adapter, err := kubeconfig.New(kubeconfig.WithDirectory(directory))
	if err != nil {
		return nil, nil, err
	}
	return adapter, adapter, nil
}

func (host *desktopHost) Handler() webui.LocalHandler {
	return host.handler
}

func (host *desktopHost) importDirectory(directory string) error {
	if strings.TrimSpace(directory) == "" {
		return fmt.Errorf("import selected kubeconfig directory")
	}
	reader, local, err := host.newSource(directory)
	if err != nil {
		return fmt.Errorf("import selected kubeconfig directory")
	}
	next, err := newDesktopSession(host.ctx, reader, local)
	if err != nil {
		return fmt.Errorf("open selected kubeconfig directory")
	}
	host.mu.Lock()
	if host.closed {
		host.mu.Unlock()
		next.close()
		return fmt.Errorf("open selected kubeconfig directory")
	}
	previous := host.session
	host.handler.Replace(next.handler)
	host.session = next
	host.mu.Unlock()
	previous.close()
	return nil
}

func (host *desktopHost) Close() {
	host.mu.Lock()
	if host.closed {
		host.mu.Unlock()
		return
	}
	host.closed = true
	session := host.session
	host.session = nil
	host.handler.Replace(nil)
	host.mu.Unlock()
	session.close()
}
