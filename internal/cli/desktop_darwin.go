//go:build darwin

// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"context"
	"fmt"
	"sync"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/ArdurAI/sith/internal/connector"
	"github.com/ArdurAI/sith/internal/localops"
	"github.com/ArdurAI/sith/internal/webui"
)

// DesktopBridge is the only native capability exposed to the embedded UI.
// It returns a boolean, never the selected directory path.
type DesktopBridge struct {
	ctx  context.Context
	host *desktopHost
}

// ChooseKubeconfigDirectory opens the native directory picker and replaces the
// in-memory source only after the existing bounded importer accepts it.
func (bridge *DesktopBridge) ChooseKubeconfigDirectory() (bool, error) {
	directory, err := runtime.OpenDirectoryDialog(bridge.ctx, runtime.OpenDialogOptions{
		Title:                "Import kubeconfig folder",
		CanCreateDirectories: false,
		ShowHiddenFiles:      false,
	})
	if err != nil {
		// Native dialog errors may include local filesystem details, so never
		// return the underlying error across the WebView bridge.
		return false, fmt.Errorf("select kubeconfig directory")
	}
	if directory == "" {
		return false, nil
	}
	if err := bridge.host.importDirectory(directory); err != nil {
		return false, err
	}
	return true, nil
}

func runDesktop(ctx context.Context, reader connector.Reader, local localops.Client, directory string) error {
	if directory != "" {
		var err error
		reader, local, err = desktopDirectorySource(directory)
		if err != nil {
			// Import errors can contain a selected local path; the CLI receives
			// a stable category rather than that private detail.
			return fmt.Errorf("import selected kubeconfig directory")
		}
	}
	host, err := newDesktopHost(ctx, reader, local)
	if err != nil {
		return err
	}
	bridge := &DesktopBridge{host: host}
	started := make(chan context.Context, 1)
	stopped := make(chan struct{})
	var stopOnce sync.Once
	stop := func() { stopOnce.Do(func() { close(stopped) }) }
	go quitDesktopOnCancellation(ctx, started, stopped, runtime.Quit)
	err = wails.Run(&options.App{
		Title:            "Sith — Fleet IDE",
		Width:            1440,
		Height:           900,
		MinWidth:         960,
		MinHeight:        640,
		BackgroundColour: &options.RGBA{R: 16, G: 24, B: 32, A: 255},
		OnStartup: func(appContext context.Context) {
			bridge.ctx = appContext
			select {
			case started <- appContext:
			case <-stopped:
			}
		},
		OnShutdown: func(context.Context) {
			stop()
			host.Close()
		},
		Bind:                     []interface{}{bridge},
		EnableDefaultContextMenu: false,
		AssetServer: &assetserver.Options{
			Middleware: webui.InProcessMiddleware(host.Handler()),
		},
	})
	stop()
	host.Close()
	if err != nil {
		return fmt.Errorf("start local fleet desktop: %w", err)
	}
	return nil
}

func quitDesktopOnCancellation(parent context.Context, started <-chan context.Context, stopped <-chan struct{}, quit func(context.Context)) {
	select {
	case <-parent.Done():
		select {
		case appContext := <-started:
			quit(appContext)
		case <-stopped:
		}
	case <-stopped:
	}
}
