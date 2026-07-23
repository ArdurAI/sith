// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/ArdurAI/sith/internal/connector"
	"github.com/ArdurAI/sith/internal/localops"
)

type launchMode string

const (
	launchModeAuto    launchMode = "auto"
	launchModeDesktop launchMode = "desktop"
	launchModeUI      launchMode = "ui"
)

type launchOptions struct {
	mode string
	ui   uiOptions
}

func newLaunchCommand(reader connector.Reader, local localops.Client) *cobra.Command {
	options := &launchOptions{
		mode: string(launchModeAuto),
		ui:   uiOptions{address: "127.0.0.1"},
	}
	command := &cobra.Command{
		Use:   "launch",
		Short: "Open the local fleet IDE",
		Long: "Open the local fleet IDE using the native desktop on macOS and the loopback-only UI " +
			"on other supported platforms. Listener and browser flags require --mode ui.",
		Example: "  sith launch\n" +
			"  sith launch --kubeconfig-dir /path/to/kubeconfigs\n" +
			"  sith launch --mode ui --no-open",
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			mode, err := resolveLaunchMode(options.mode, runtime.GOOS)
			if err != nil {
				return err
			}
			switch mode {
			case launchModeDesktop:
				if err := rejectDesktopUIFlags(command); err != nil {
					return err
				}
				if err := validateDesktopDependencies(reader, local, options.ui.kubeconfigDir); err != nil {
					return err
				}
				return runDesktop(command.Context(), reader, local, options.ui.kubeconfigDir)
			case launchModeUI:
				selectedReader, selectedLocal, err := selectUIBackend(reader, local, options.ui.kubeconfigDir)
				if err != nil {
					return err
				}
				return runWebUI(command.Context(), command, selectedReader, selectedLocal, &options.ui)
			default:
				return fmt.Errorf("launch mode %q is unsupported", mode)
			}
		},
	}
	command.Flags().StringVar(&options.mode, "mode", options.mode, "launch mode: auto, desktop, or ui")
	bindUIFlags(command, &options.ui)
	return command
}

func resolveLaunchMode(requested, goos string) (launchMode, error) {
	switch launchMode(requested) {
	case launchModeAuto:
		if goos == "darwin" {
			return launchModeDesktop, nil
		}
		return launchModeUI, nil
	case launchModeDesktop:
		if goos != "darwin" {
			return "", fmt.Errorf("launch mode %q is available only on macOS", requested)
		}
		return launchModeDesktop, nil
	case launchModeUI:
		return launchModeUI, nil
	default:
		return "", fmt.Errorf("invalid launch mode %q: expected auto, desktop, or ui", requested)
	}
}

func rejectDesktopUIFlags(command *cobra.Command) error {
	for _, name := range []string{"address", "port", "no-open"} {
		if command.Flags().Changed(name) {
			return fmt.Errorf("--%s requires --mode ui", name)
		}
	}
	return nil
}
