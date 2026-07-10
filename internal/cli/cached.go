// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ArdurAI/sith/internal/connector"
	"github.com/ArdurAI/sith/internal/fleetcache"
	"github.com/ArdurAI/sith/internal/fleetrender"
	"github.com/ArdurAI/sith/internal/hydrate"
)

type cacheCommandOptions struct {
	allClusters   bool
	allNamespaces bool
	context       string
	namespace     string
}

func newGetCommand(root *rootOptions, reader connector.Reader) *cobra.Command {
	options := &cacheCommandOptions{}
	command := &cobra.Command{
		Use:   "get <kind> [name]",
		Short: "Read a resource lens from the local fleet cache",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(command *cobra.Command, args []string) error {
			query := fleetcache.Query{Kind: args[0]}
			if len(args) == 2 {
				query.Name = args[1]
			}
			if err := applyExplicitScope(query.Kind, options, &query); err != nil {
				return err
			}
			return runCacheCommand(command, root, reader, []string{query.Kind}, query)
		},
	}
	addCacheScopeFlags(command, options, true)
	return command
}

func newSearchCommand(root *rootOptions, reader connector.Reader) *cobra.Command {
	options := &cacheCommandOptions{}
	command := &cobra.Command{
		Use:   "search <query>",
		Short: "Search normalized records across the local fleet cache",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			query, err := fleetcache.ParseSearch(strings.Join(args, " "))
			if err != nil {
				return err
			}
			if options.context != "" {
				query.Scopes = []string{options.context}
			}
			return runCacheCommand(command, root, reader, hydrate.TierOneKinds(), query)
		},
	}
	command.Flags().StringVar(&options.context, "context", "", "limit search to one kubeconfig context")
	return command
}

func newCorrelateCommand(root *rootOptions, reader connector.Reader) *cobra.Command {
	options := &cacheCommandOptions{}
	command := &cobra.Command{
		Use:   "correlate <expression>",
		Short: "Answer a coverage-honest cross-cluster correlation",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			query, err := fleetcache.ParseCorrelation(strings.Join(args, " "))
			if err != nil {
				return err
			}
			if options.context != "" {
				query.Scopes = []string{options.context}
			}
			return runCacheCommand(command, root, reader, []string{query.Kind}, query)
		},
	}
	command.Flags().StringVar(&options.context, "context", "", "limit correlation to one kubeconfig context")
	return command
}

func addCacheScopeFlags(command *cobra.Command, options *cacheCommandOptions, namespaceFlags bool) {
	command.Flags().BoolVar(&options.allClusters, "all-clusters", false, "query every discovered context")
	command.Flags().StringVar(&options.context, "context", "", "query one kubeconfig context")
	if namespaceFlags {
		command.Flags().BoolVarP(&options.allNamespaces, "all-namespaces", "A", false, "query every namespace")
		command.Flags().StringVarP(&options.namespace, "namespace", "n", "", "query one namespace (default default)")
	}
}

func applyExplicitScope(kind string, options *cacheCommandOptions, query *fleetcache.Query) error {
	if options.allClusters == (options.context != "") {
		return fmt.Errorf("choose exactly one of --all-clusters or --context")
	}
	if options.context != "" {
		query.Scopes = []string{options.context}
	}
	if options.allNamespaces && options.namespace != "" {
		return fmt.Errorf("--all-namespaces and --namespace are mutually exclusive")
	}
	switch strings.ToLower(kind) {
	case "node", "nodes", "no", "namespace", "namespaces":
		if options.allNamespaces || options.namespace != "" {
			return fmt.Errorf("namespace flags cannot select cluster-scoped %s", kind)
		}
	default:
		if !options.allNamespaces {
			query.Namespace = options.namespace
			if query.Namespace == "" {
				query.Namespace = "default"
			}
		}
	}
	return nil
}

func runCacheCommand(
	command *cobra.Command,
	root *rootOptions,
	reader connector.Reader,
	kinds []string,
	query fleetcache.Query,
) error {
	store := fleetcache.New()
	hydrator, err := hydrate.New(reader, store, hydrate.WithKinds(kinds...))
	if err != nil {
		return err
	}
	syncErr := hydrator.SyncOnce(command.Context())
	snapshot := store.Query(query)
	if err := writeCacheSnapshot(command, root.output, query.Kind, snapshot); err != nil {
		return err
	}
	if snapshot.Coverage.Requested == 0 {
		return fmt.Errorf("no kubeconfig contexts discovered")
	}
	if snapshot.Coverage.Reachable == 0 {
		if syncErr != nil {
			return fmt.Errorf("fleet cache sync failed: %w", syncErr)
		}
		return fmt.Errorf("fleet cache query reached 0/%d contexts", snapshot.Coverage.Requested)
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
}

func writeCacheSnapshot(command *cobra.Command, format, lens string, snapshot fleetcache.Snapshot) error {
	switch format {
	case "json":
		if err := json.NewEncoder(command.OutOrStdout()).Encode(snapshot); err != nil {
			return fmt.Errorf("write cache JSON: %w", err)
		}
	case "yaml":
		return writeYAML(command.OutOrStdout(), snapshot, "cache")
	case "name":
		return fleetrender.WriteNames(command.OutOrStdout(), snapshot)
	default:
		table := fleetrender.Build(snapshot, fleetrender.Options{Lens: lens, Wide: format == "wide"})
		return fleetrender.WriteText(command.OutOrStdout(), table)
	}
	return nil
}
