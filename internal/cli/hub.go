// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newHubCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "hub",
		Short: "Start the governed fleet hub",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if _, err := fmt.Fprintln(command.OutOrStdout(), "sith hub: not yet implemented — hub mode is phase-1+ (E1–E10)."); err != nil {
				return fmt.Errorf("write hub status: %w", err)
			}
			return nil
		},
	}
}
