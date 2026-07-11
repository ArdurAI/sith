// SPDX-License-Identifier: Apache-2.0

// Package cli wires the Sith command tree to source-abstract domain packages.
package cli

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/ArdurAI/sith/internal/config"
	"github.com/ArdurAI/sith/internal/connector"
	"github.com/ArdurAI/sith/internal/connector/kubeconfig"
	"github.com/ArdurAI/sith/internal/fleet"
	"github.com/ArdurAI/sith/internal/fleetcache"
	"github.com/ArdurAI/sith/internal/hydrate"
	"github.com/ArdurAI/sith/internal/keychain"
	"github.com/ArdurAI/sith/internal/localops"
	"github.com/ArdurAI/sith/internal/logging"
	"github.com/ArdurAI/sith/internal/tui"
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

type backend struct {
	source   fleet.Source
	reader   connector.Reader
	local    localops.Client
	secrets  keychain.Store
	tuiInput io.Reader
}

// Execute builds and runs the command tree, returning a process exit code.
func Execute() int {
	adapter := kubeconfig.Default()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return executeBackendContext(ctx, os.Args[1:], backend{
		source: connector.AsSource(adapter), reader: adapter, local: adapter,
		secrets: keychain.NewSystemStore(), tuiInput: os.Stdin,
	}, os.Stdout, os.Stderr)
}

func execute(args []string, source fleet.Source, stdout, stderr io.Writer) int {
	return executeBackend(args, backend{source: source}, stdout, stderr)
}

func executeWithReader(args []string, reader connector.Reader, stdout, stderr io.Writer) int {
	return executeBackend(args, backend{source: connector.AsSource(reader), reader: reader}, stdout, stderr)
}

func executeBackend(args []string, runtime backend, stdout, stderr io.Writer) int {
	return executeBackendContext(context.Background(), args, runtime, stdout, stderr)
}

func executeBackendContext(
	ctx context.Context,
	args []string,
	runtime backend,
	stdout, stderr io.Writer,
) int {
	command := newRootCommand(runtime, stdout, stderr)
	command.SetArgs(args)
	if err := command.ExecuteContext(ctx); err != nil {
		if _, writeErr := fmt.Fprintln(stderr, err); writeErr != nil {
			return 2
		}
		return 1
	}

	return 0
}

func newRootCommand(runtime backend, stdout, stderr io.Writer) *cobra.Command {
	options := &rootOptions{output: "text"}
	command := &cobra.Command{
		Use:           "sith",
		Short:         "ArdurAI's local-first Kubernetes fleet client",
		Long:          "Sith is ArdurAI's local-first client for source-abstract, cross-cluster Kubernetes fleet operations.",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(command *cobra.Command, _ []string) error {
			if runtime.reader != nil && terminalIO(runtime.tuiInput, stdout) {
				return runFleetTUI(command.Context(), runtime.reader, runtime.local, runtime.tuiInput, stdout)
			}
			return command.Help()
		},
		PersistentPreRunE: func(command *cobra.Command, _ []string) error {
			if options.output != "text" && options.output != "json" && options.output != "yaml" &&
				options.output != "wide" && options.output != "name" {
				return fmt.Errorf("invalid output format %q: expected text, json, yaml, wide, or name", options.output)
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
	if runtime.tuiInput != nil {
		command.SetIn(runtime.tuiInput)
	}
	command.CompletionOptions.DisableDefaultCmd = true

	flags := command.PersistentFlags()
	flags.StringVar(&options.configPath, "config", "", "path to the YAML configuration file")
	flags.StringVar(&options.logLevel, "log-level", "", "logging level: debug, info, warn, or error (default info)")
	flags.StringVar(&options.logFormat, "log-format", "", "logging format: text or json (default text)")
	flags.StringVarP(&options.output, "output", "o", "text", "output format: text, json, yaml, wide, or name")

	commands := []*cobra.Command{
		newVersionCommand(options),
		newClustersCommand(options, runtime.source),
		newUICommand(runtime.reader, runtime.local),
		newHubCommand(),
	}
	if runtime.reader != nil {
		commands = append(commands,
			newServeCommand(runtime.reader, runtime.secrets),
			newTUICommand(runtime.reader, runtime.local, runtime.tuiInput, stdout),
			newGetCommand(options, runtime.reader),
			newSearchCommand(options, runtime.reader),
			newCorrelateCommand(options, runtime.reader),
		)
	}
	if runtime.secrets != nil {
		commands = append(commands, newMCPTokenCommand(runtime.secrets))
	}
	if runtime.local != nil {
		commands = append(commands, newLocalCommands(options, runtime.local)...)
	}
	command.AddCommand(commands...)

	return command
}

func newTUICommand(reader connector.Reader, local localops.Client, input io.Reader, output io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "tui",
		Short: "Open the cache-first interactive fleet view",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if input == nil {
				return fmt.Errorf("TUI input is unavailable")
			}
			return runFleetTUI(command.Context(), reader, local, input, output)
		},
	}
}

func runFleetTUI(
	ctx context.Context,
	reader connector.Reader,
	local localops.Client,
	input io.Reader,
	output io.Writer,
) error {
	store := fleetcache.New()
	hydrator, err := hydrate.New(reader, store)
	if err != nil {
		return err
	}
	return tui.RunWithLocal(ctx, store, hydrator, local, input, output)
}

func terminalIO(input io.Reader, output io.Writer) bool {
	inputFile, inputOK := input.(*os.File)
	outputFile, outputOK := output.(*os.File)
	return inputOK && outputOK && term.IsTerminal(int(inputFile.Fd())) && term.IsTerminal(int(outputFile.Fd()))
}
