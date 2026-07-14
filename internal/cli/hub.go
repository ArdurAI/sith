// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ArdurAI/sith/internal/hubruntime"
)

func newHubCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "hub",
		Short: "Start the governed fleet hub",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			state, ok := command.Context().Value(runtimeKey{}).(runtimeState)
			if !ok || state.logger == nil {
				return fmt.Errorf("start hub: runtime logging is unavailable")
			}
			runtime, err := hubruntime.NewFromEnvironment(command.Context(), state.logger)
			if err != nil {
				return fmt.Errorf("start hub: %w", err)
			}
			return runtime.Run(command.Context())
		},
	}
	command.AddCommand(newHubMigrateCommand())
	return command
}

func newHubMigrateCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "migrate",
		Short: "Apply hub schema migrations with the owner credential",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if err := hubruntime.MigrateFromEnvironment(command.Context()); err != nil {
				return fmt.Errorf("migrate hub schema: %w", err)
			}
			_, err := fmt.Fprintln(command.OutOrStdout(), "Hub schema migrations completed.")
			return err
		},
	}
}
