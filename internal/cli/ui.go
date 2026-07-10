// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newUICommand() *cobra.Command {
	return &cobra.Command{
		Use:   "ui",
		Short: "Start the local fleet IDE",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if _, err := fmt.Fprintln(command.OutOrStdout(), "sith ui: not yet implemented — see F11.3 (#34)."); err != nil {
				return fmt.Errorf("write ui status: %w", err)
			}
			return nil
		},
	}
}
