// SPDX-License-Identifier: Apache-2.0

// Package cli wires the Sith command tree to source-abstract domain packages.
package cli

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/spf13/cobra"

	"github.com/ArdurAI/sith/internal/config"
	"github.com/ArdurAI/sith/internal/connector"
	"github.com/ArdurAI/sith/internal/connector/kubeconfig"
	"github.com/ArdurAI/sith/internal/fleet"
	"github.com/ArdurAI/sith/internal/logging"
)

type runtimeKey struct{}

type runtimeState struct {
	config config.Config
	logger *slog.Logger
}

type rootOptions struct {
	configPath string
	logLevel   string
	logFormat  string
	output     string
}

// Execute builds and runs the command tree, returning a process exit code.
func Execute() int {
	return execute(os.Args[1:], connector.AsSource(kubeconfig.Default()), os.Stdout, os.Stderr)
}

func execute(args []string, source fleet.Source, stdout, stderr io.Writer) int {
	command := newRootCommand(source, stdout, stderr)
	command.SetArgs(args)
	if err := command.Execute(); err != nil {
		if _, writeErr := fmt.Fprintln(stderr, err); writeErr != nil {
			return 2
		}
		return 1
	}

	return 0
}

func newRootCommand(source fleet.Source, stdout, stderr io.Writer) *cobra.Command {
	options := &rootOptions{output: "text"}
	command := &cobra.Command{
		Use:           "sith",
		Short:         "ArdurAI's local-first Kubernetes fleet client",
		Long:          "Sith is ArdurAI's local-first client for source-abstract, cross-cluster Kubernetes fleet operations.",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(command *cobra.Command, _ []string) error {
			return command.Help()
		},
		PersistentPreRunE: func(command *cobra.Command, _ []string) error {
			if options.output != "text" && options.output != "json" {
				return fmt.Errorf("invalid output format %q: expected text or json", options.output)
			}

			resolved, err := config.Load(options.configPath, config.Overrides{
				LogLevel:  options.logLevel,
				LogFormat: options.logFormat,
			})
			if err != nil {
				return fmt.Errorf("load configuration: %w", err)
			}

			logger, err := logging.New(stderr, resolved.LogLevel, resolved.LogFormat)
			if err != nil {
				return fmt.Errorf("configure logging: %w", err)
			}

			state := runtimeState{config: resolved, logger: logger}
			command.SetContext(context.WithValue(command.Context(), runtimeKey{}, state))
			return nil
		},
	}
	command.SetOut(stdout)
	command.SetErr(stderr)
	command.CompletionOptions.DisableDefaultCmd = true

	flags := command.PersistentFlags()
	flags.StringVar(&options.configPath, "config", "", "path to the YAML configuration file")
	flags.StringVar(&options.logLevel, "log-level", "", "logging level: debug, info, warn, or error (default info)")
	flags.StringVar(&options.logFormat, "log-format", "", "logging format: text or json (default text)")
	flags.StringVarP(&options.output, "output", "o", "text", "output format: text or json")

	command.AddCommand(
		newVersionCommand(options),
		newClustersCommand(options, source),
		newUICommand(),
		newHubCommand(),
	)

	return command
}
