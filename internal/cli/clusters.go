// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/ArdurAI/sith/internal/fleet"
)

func newClustersCommand(options *rootOptions, source fleet.Source) *cobra.Command {
	return &cobra.Command{
		Use:   "clusters",
		Short: "List clusters from the configured fleet source",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if source == nil {
				return fmt.Errorf("fleet source is not configured")
			}

			result, err := source.Fleet(command.Context())
			if err != nil {
				return fmt.Errorf("read fleet from %s source: %w", source.Kind(), err)
			}
			if result.Clusters == nil {
				result.Clusters = []fleet.Cluster{}
			}
			if options.output == "yaml" {
				return writeYAML(command.OutOrStdout(), result, "clusters")
			}

			if options.output == "json" {
				if err := json.NewEncoder(command.OutOrStdout()).Encode(result); err != nil {
					return fmt.Errorf("write clusters JSON: %w", err)
				}
				return nil
			}

			if len(result.Clusters) == 0 {
				message := fmt.Sprintf("No clusters found (source: %s).", source.Kind())
				if source.Kind() == "stub" {
					message = "No clusters found (source: stub — F2.1/#38 not yet implemented)."
				}
				if _, err := fmt.Fprintln(command.OutOrStdout(), message); err != nil {
					return fmt.Errorf("write empty clusters result: %w", err)
				}
				return nil
			}

			return writeClusterTable(command.OutOrStdout(), result.Clusters)
		},
	}
}

func writeClusterTable(output io.Writer, clusters []fleet.Cluster) error {
	var rendered bytes.Buffer
	table := tabwriter.NewWriter(&rendered, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(table, "NAME\tCONTEXT\tSOURCE\tREACHABLE\tOBSERVED"); err != nil {
		return fmt.Errorf("write cluster table header: %w", err)
	}

	for _, cluster := range clusters {
		contextName := cluster.Context
		if contextName == "" {
			contextName = "-"
		}
		observed := "-"
		if !cluster.ObservedAt.IsZero() {
			observed = cluster.ObservedAt.UTC().Format(time.RFC3339)
		}
		if _, err := fmt.Fprintf(
			table,
			"%s\t%s\t%s\t%t\t%s\n",
			cluster.Name,
			contextName,
			cluster.SourceKind,
			cluster.Reachable,
			observed,
		); err != nil {
			return fmt.Errorf("write cluster table row: %w", err)
		}
	}

	if err := table.Flush(); err != nil {
		return fmt.Errorf("flush cluster table: %w", err)
	}
	if _, err := io.Copy(output, &rendered); err != nil {
		return fmt.Errorf("write cluster table: %w", err)
	}

	return nil
}
