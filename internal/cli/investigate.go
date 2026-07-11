// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ArdurAI/sith/internal/brain"
	"github.com/ArdurAI/sith/internal/connector"
	"github.com/ArdurAI/sith/internal/fleet"
	"github.com/ArdurAI/sith/internal/fleetcache"
	"github.com/ArdurAI/sith/internal/fleetrender"
	"github.com/ArdurAI/sith/internal/hydrate"
)

func newInvestigateCommand(root *rootOptions, reader connector.Reader) *cobra.Command {
	var contextName string
	command := &cobra.Command{
		Use:   "investigate [name]",
		Short: "Explain degraded fleet signals with deterministic advisory rules",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			query := fleetcache.Query{}
			if contextName != "" {
				query.Scopes = []string{contextName}
			}
			if len(args) == 1 {
				query.Text = []string{strings.ToLower(args[0])}
			}
			store := fleetcache.New()
			hydrator, err := hydrate.New(reader, store, hydrate.WithKinds(hydrate.TierOneKinds()...))
			if err != nil {
				return err
			}
			syncErr := hydrator.SyncOnce(command.Context())
			snapshot := store.Query(fleet.LocalWorkspace, query)
			result, err := brain.Evaluate(brain.FromCache(fleet.LocalWorkspace, snapshot))
			if err != nil {
				return fmt.Errorf("evaluate investigation: %w", err)
			}
			if err := writeInvestigation(command, root.output, result); err != nil {
				return err
			}
			if snapshot.Coverage.Requested == 0 {
				return fmt.Errorf("no kubeconfig contexts discovered")
			}
			if snapshot.Coverage.Reachable == 0 {
				if syncErr != nil {
					return fmt.Errorf("fleet investigation sync failed: %w", syncErr)
				}
				return fmt.Errorf("fleet investigation reached 0/%d contexts", snapshot.Coverage.Requested)
			}
			if syncErr != nil || !snapshot.Coverage.Complete() {
				warning := fleetrender.CoverageLine(snapshot.Coverage)
				if syncErr != nil && !errors.Is(syncErr, hydrate.ErrPaused) {
					warning += ": " + syncErr.Error()
				}
				if _, err := fmt.Fprintln(command.ErrOrStderr(), "warning: "+warning); err != nil {
					return fmt.Errorf("write partial coverage warning: %w", err)
				}
			}
			return nil
		},
	}
	command.Flags().StringVar(&contextName, "context", "", "limit investigation to one kubeconfig context")
	return command
}

func writeInvestigation(command *cobra.Command, format string, result brain.Result) error {
	switch format {
	case "json":
		if err := json.NewEncoder(command.OutOrStdout()).Encode(result); err != nil {
			return fmt.Errorf("write investigation JSON: %w", err)
		}
		return nil
	case "yaml":
		return writeYAML(command.OutOrStdout(), result, "investigation")
	case "text", "wide":
		return writeInvestigationText(command, result)
	default:
		return fmt.Errorf("investigate supports text, wide, json, or yaml output")
	}
}

func writeInvestigationText(command *cobra.Command, result brain.Result) error {
	if len(result.Verdicts) == 0 {
		_, err := fmt.Fprintln(command.OutOrStdout(), "No supported degraded signals found in the reachable local fleet.")
		return err
	}
	for index, verdict := range result.Verdicts {
		if _, err := fmt.Fprintf(command.OutOrStdout(), "%d. %s %s [%s] — %s\n", index+1, verdict.Rule, verdict.FailureMode, verdict.Status, verdict.Hypothesis); err != nil {
			return fmt.Errorf("write investigation verdict: %w", err)
		}
		if verdict.FleetWide {
			if _, err := fmt.Fprintf(command.OutOrStdout(), "   fleet: %s\n", strings.Join(verdict.Clusters, ", ")); err != nil {
				return fmt.Errorf("write investigation scope: %w", err)
			}
		} else if _, err := fmt.Fprintf(command.OutOrStdout(), "   target: %s\n", verdict.Ref.String()); err != nil {
			return fmt.Errorf("write investigation target: %w", err)
		}
		if len(verdict.MissingLenses) > 0 {
			missing := make([]string, 0, len(verdict.MissingLenses))
			for _, lens := range verdict.MissingLenses {
				missing = append(missing, string(lens))
			}
			if _, err := fmt.Fprintf(command.OutOrStdout(), "   coverage gap: %s\n", strings.Join(missing, ", ")); err != nil {
				return fmt.Errorf("write investigation coverage: %w", err)
			}
		}
		for _, citation := range verdict.Citations {
			freshness := ""
			if citation.Stale {
				freshness = " [stale]"
			}
			if _, err := fmt.Fprintf(command.OutOrStdout(), "   evidence: %s %s=%s (weight %+d)%s\n", citation.Lens, citation.Predicate, citation.Observed, citation.Weight, freshness); err != nil {
				return fmt.Errorf("write investigation citation: %w", err)
			}
		}
		advice := verdict.Advisory.Command
		if advice == "" {
			advice = verdict.Advisory.PRDiff
		}
		if _, err := fmt.Fprintf(command.OutOrStdout(), "   suggested: %s\n", advice); err != nil {
			return fmt.Errorf("write investigation advisory: %w", err)
		}
		if verdict.Advisory.Sensitive {
			if _, err := fmt.Fprintln(command.OutOrStdout(), "   sensitive: human review required"); err != nil {
				return fmt.Errorf("write investigation sensitivity: %w", err)
			}
		}
	}
	return nil
}
