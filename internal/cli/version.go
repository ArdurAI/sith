// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ArdurAI/sith/internal/buildinfo"
)

func newVersionCommand(options *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print build information",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			info := buildinfo.Get()
			if options.output == "yaml" {
				return writeYAML(command.OutOrStdout(), info, "version")
			}
			if options.output == "json" {
				encoded, err := info.JSON()
				if err != nil {
					return err
				}
				if _, err := fmt.Fprintln(command.OutOrStdout(), encoded); err != nil {
					return fmt.Errorf("write version output: %w", err)
				}
				return nil
			}

			if _, err := fmt.Fprintln(command.OutOrStdout(), info.String()); err != nil {
				return fmt.Errorf("write version output: %w", err)
			}
			return nil
		},
	}
}
