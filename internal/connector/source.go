// SPDX-License-Identifier: Apache-2.0

package connector

import (
	"context"
	"fmt"
	"sort"

	"github.com/ArdurAI/sith/internal/fleet"
)

var _ fleet.Source = readerSource{}

// AsSource adapts a Reader to the stable fleet snapshot seam used by the CLI.
func AsSource(reader Reader) fleet.Source {
	return readerSource{reader: reader}
}

type readerSource struct {
	reader Reader
}

func (source readerSource) Kind() string {
	if source.reader == nil {
		return "invalid"
	}
	return source.reader.Kind()
}

func (source readerSource) Fleet(ctx context.Context) (fleet.FleetResult, error) {
	if source.reader == nil {
		return fleet.FleetResult{}, fmt.Errorf("adapt reader: reader is nil")
	}

	discovery, err := source.reader.Discover(ctx)
	if err != nil {
		return fleet.FleetResult{}, fmt.Errorf("discover %s scopes: %w", source.reader.Kind(), err)
	}
	queryResult, err := source.reader.Query(ctx, fleet.Query{Kinds: []fleet.FactKind{fleet.FactInventory, fleet.FactHealth}})
	if err != nil {
		return fleet.FleetResult{}, fmt.Errorf("query %s snapshot: %w", source.reader.Kind(), err)
	}

	clusters := make([]fleet.Cluster, 0, len(discovery.Scopes)+len(discovery.Unreachable))
	seen := make(map[string]struct{}, len(discovery.Scopes))
	for _, scope := range discovery.Scopes {
		clusters = append(clusters, fleet.Cluster{
			Name:       scope.Name,
			Context:    scope.Name,
			SourceKind: source.reader.Kind(),
			Reachable:  scope.Reachable,
			ObservedAt: scope.ObservedAt,
		})
		seen[scope.Name] = struct{}{}
	}
	for _, name := range discovery.Unreachable {
		if _, exists := seen[name]; exists {
			continue
		}
		clusters = append(clusters, fleet.Cluster{
			Name:       name,
			Context:    name,
			SourceKind: source.reader.Kind(),
		})
	}
	sort.Slice(clusters, func(left, right int) bool {
		return clusters[left].Name < clusters[right].Name
	})

	coverage := queryResult.Coverage
	if coverage.Requested == 0 && len(clusters) != 0 {
		coverage.Requested = len(clusters)
		for _, cluster := range clusters {
			if cluster.Reachable {
				coverage.Reachable++
			} else {
				coverage.Unreachable = append(coverage.Unreachable, cluster.Name)
			}
		}
	}
	coverage.Unreachable = sortedUnique(coverage.Unreachable)
	coverage.Stale = sortedUnique(coverage.Stale)

	return fleet.FleetResult{Clusters: clusters, Coverage: coverage}, nil
}

func sortedUnique(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value != "" {
			set[value] = struct{}{}
		}
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
