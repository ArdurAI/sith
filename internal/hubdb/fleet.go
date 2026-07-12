// SPDX-License-Identifier: Apache-2.0

package hubdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ArdurAI/sith/internal/fleet"
	"github.com/ArdurAI/sith/internal/hubfleet"
	"github.com/ArdurAI/sith/internal/tenancy"
)

const (
	defaultFleetFreshness = 5 * time.Minute
	maxFleetFreshness     = time.Hour
	defaultFleetFactLimit = 500
	maxFleetFactLimit     = 1_000
)

// ErrSpokeNotRegistered means the selected tenancy does not own the requested registered spoke.
var ErrSpokeNotRegistered = errors.New("registered spoke not found")

// RegisteredSpokes returns the active workspace's registered OCM spokes only.
func (database *AppDB) RegisteredSpokes(ctx context.Context, scope tenancy.Scope) ([]hubfleet.Spoke, error) {
	if err := requireReadScope(scope); err != nil {
		return nil, fmt.Errorf("list registered spokes: %w", err)
	}
	spokes := make([]hubfleet.Spoke, 0)
	err := database.InWorkspace(ctx, scope, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, managed_cluster_ref, labels
			FROM sith.clusters
			WHERE workspace_id = $1
			ORDER BY id
		`, scope.WorkspaceID())
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var (
				spoke  hubfleet.Spoke
				labels []byte
			)
			if err := rows.Scan(&spoke.ID, &spoke.ManagedClusterRef, &labels); err != nil {
				return err
			}
			if err := json.Unmarshal(labels, &spoke.Labels); err != nil {
				return fmt.Errorf("decode registered spoke labels: %w", err)
			}
			if err := spoke.Validate(); err != nil {
				return fmt.Errorf("validate registered spoke: %w", err)
			}
			spokes = append(spokes, spoke)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("list registered spokes: %w", err)
	}
	return spokes, nil
}

// ReplaceSnapshot atomically replaces one registered spoke's normalized inventory and health facts.
func (database *AppDB) ReplaceSnapshot(
	ctx context.Context,
	scope tenancy.Scope,
	spoke hubfleet.Spoke,
	snapshot hubfleet.Snapshot,
	attemptedAt time.Time,
) error {
	if err := requireReadScope(scope); err != nil {
		return fmt.Errorf("replace spoke snapshot: %w", err)
	}
	if err := validateCollectionInput(spoke, snapshot, attemptedAt); err != nil {
		return fmt.Errorf("replace spoke snapshot: %w", err)
	}
	return database.InWorkspace(ctx, scope, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE sith.clusters
			SET last_seen = $3, last_attempted_at = $4, last_error_kind = NULL
			WHERE workspace_id = $1 AND id = $2 AND managed_cluster_ref = $5
		`, scope.WorkspaceID(), spoke.ID, snapshot.ObservedAt, attemptedAt, spoke.ManagedClusterRef)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return ErrSpokeNotRegistered
		}
		if _, err := tx.Exec(ctx,
			`DELETE FROM sith.fleet_facts WHERE workspace_id = $1 AND cluster_id = $2`,
			scope.WorkspaceID(), spoke.ID,
		); err != nil {
			return err
		}
		for index := range snapshot.Facts {
			fact := snapshot.Facts[index]
			resourceRef, err := json.Marshal(fact.Ref)
			if err != nil {
				return fmt.Errorf("encode resource reference: %w", err)
			}
			provenance, err := json.Marshal(fact.Provenance)
			if err != nil {
				return fmt.Errorf("encode provenance: %w", err)
			}
			displayFields := fact.Display
			if displayFields == nil {
				displayFields = []fleet.DisplayField{}
			}
			display, err := json.Marshal(displayFields)
			if err != nil {
				return fmt.Errorf("encode display fields: %w", err)
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO sith.fleet_facts(
					workspace_id, cluster_id, kind, payload, observed_at, resource_ref, source, provenance, display
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
			`, scope.WorkspaceID(), spoke.ID, fact.Kind, fact.Observed, fact.ObservedAt, resourceRef, fact.Source, provenance, display); err != nil {
				return err
			}
		}
		return nil
	})
}

// MarkSnapshotFailure preserves the last normalized snapshot and records only a closed failure kind.
func (database *AppDB) MarkSnapshotFailure(
	ctx context.Context,
	scope tenancy.Scope,
	spoke hubfleet.Spoke,
	failure hubfleet.FailureKind,
	attemptedAt time.Time,
) (bool, error) {
	if err := requireReadScope(scope); err != nil {
		return false, fmt.Errorf("mark spoke snapshot failure: %w", err)
	}
	if err := spoke.Validate(); err != nil || !validFailureKind(failure) || attemptedAt.IsZero() {
		return false, fmt.Errorf("mark spoke snapshot failure: invalid spoke, failure kind, or attempt time")
	}
	retained := false
	err := database.InWorkspace(ctx, scope, func(tx pgx.Tx) error {
		var lastSeen *time.Time
		err := tx.QueryRow(ctx, `
			UPDATE sith.clusters
			SET last_attempted_at = $3, last_error_kind = $4
			WHERE workspace_id = $1 AND id = $2 AND managed_cluster_ref = $5
			RETURNING last_seen
		`, scope.WorkspaceID(), spoke.ID, attemptedAt, failure, spoke.ManagedClusterRef).Scan(&lastSeen)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrSpokeNotRegistered
			}
			return err
		}
		retained = lastSeen != nil
		return nil
	})
	if err != nil {
		return false, fmt.Errorf("mark spoke snapshot failure: %w", err)
	}
	return retained, nil
}

// ReadFleet adapts persisted tenant-scoped spoke state to the common fleet.FleetResult model.
func (database *AppDB) ReadFleet(
	ctx context.Context,
	scope tenancy.Scope,
	freshness time.Duration,
	now time.Time,
) (fleet.FleetResult, error) {
	if err := requireReadScope(scope); err != nil {
		return fleet.FleetResult{}, fmt.Errorf("read federated fleet: %w", err)
	}
	if err := validateFreshness(freshness, now); err != nil {
		return fleet.FleetResult{}, fmt.Errorf("read federated fleet: %w", err)
	}
	states, requested, err := database.clusterStates(ctx, scope, nil)
	if err != nil {
		return fleet.FleetResult{}, fmt.Errorf("read federated fleet: %w", err)
	}
	coverage := coverageFor(states, requested, freshness, now)
	clusters := make([]fleet.Cluster, 0, len(requested))
	for _, id := range requested {
		state, exists := states[id]
		if !exists {
			continue
		}
		clusters = append(clusters, fleet.Cluster{
			Name:       state.id,
			Context:    state.managedClusterRef,
			SourceKind: hubfleet.SourceKind,
			Reachable:  state.lastSeen != nil && state.lastErrorKind == "",
			ObservedAt: dereferenceTime(state.lastSeen),
		})
	}
	return fleet.FleetResult{Clusters: clusters, Coverage: coverage}, nil
}

// QueryFleet returns only the active workspace's normalized inventory and health facts.
func (database *AppDB) QueryFleet(
	ctx context.Context,
	scope tenancy.Scope,
	query fleet.Query,
	freshness time.Duration,
	now time.Time,
) (fleet.QueryResult, error) {
	if err := requireReadScope(scope); err != nil {
		return fleet.QueryResult{}, fmt.Errorf("query federated fleet: %w", err)
	}
	query, requestedScopes, err := normalizeFleetQuery(query)
	if err != nil {
		return fleet.QueryResult{}, fmt.Errorf("query federated fleet: %w", err)
	}
	if err := validateFreshness(freshness, now); err != nil {
		return fleet.QueryResult{}, fmt.Errorf("query federated fleet: %w", err)
	}
	states, requested, err := database.clusterStates(ctx, scope, requestedScopes)
	if err != nil {
		return fleet.QueryResult{}, fmt.Errorf("query federated fleet: %w", err)
	}
	result := fleet.QueryResult{Coverage: coverageFor(states, requested, freshness, now), Facts: []fleet.Fact{}}
	err = database.InWorkspace(ctx, scope, func(tx pgx.Tx) error {
		facts, err := queryFacts(ctx, tx, scope.WorkspaceID(), query)
		if err != nil {
			return err
		}
		for index := range facts {
			state, exists := states[facts[index].Ref.Scope]
			if !exists {
				return fmt.Errorf("stored fact references an unregistered spoke")
			}
			facts[index].Workspace = string(scope.WorkspaceID())
			facts[index].Stale, facts[index].StaleFor = staleState(state, freshness, now)
		}
		result.Facts = facts
		return nil
	})
	if err != nil {
		return fleet.QueryResult{}, fmt.Errorf("query federated fleet: %w", err)
	}
	return result, nil
}

type clusterState struct {
	id                string
	managedClusterRef string
	lastSeen          *time.Time
	lastErrorKind     string
}

func (database *AppDB) clusterStates(
	ctx context.Context,
	scope tenancy.Scope,
	requestedScopes []string,
) (map[string]clusterState, []string, error) {
	states := make(map[string]clusterState)
	requested := append([]string(nil), requestedScopes...)
	err := database.InWorkspace(ctx, scope, func(tx pgx.Tx) error {
		query := `SELECT id, managed_cluster_ref, last_seen, COALESCE(last_error_kind, '')
			FROM sith.clusters WHERE workspace_id = $1`
		arguments := []any{scope.WorkspaceID()}
		if len(requestedScopes) > 0 {
			query += " AND id = ANY($2)"
			arguments = append(arguments, requestedScopes)
		}
		query += " ORDER BY id"
		rows, err := tx.Query(ctx, query, arguments...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var state clusterState
			if err := rows.Scan(&state.id, &state.managedClusterRef, &state.lastSeen, &state.lastErrorKind); err != nil {
				return err
			}
			states[state.id] = state
			if len(requestedScopes) == 0 {
				requested = append(requested, state.id)
			}
		}
		return rows.Err()
	})
	if err != nil {
		return nil, nil, err
	}
	return states, requested, nil
}

func queryFacts(ctx context.Context, tx pgx.Tx, workspaceID tenancy.WorkspaceID, query fleet.Query) ([]fleet.Fact, error) {
	conditions := []string{"fact.workspace_id = $1"}
	arguments := []any{workspaceID}
	placeholder := func(value any) string {
		arguments = append(arguments, value)
		return fmt.Sprintf("$%d", len(arguments))
	}
	if len(query.Scopes) > 0 {
		conditions = append(conditions, "fact.cluster_id = ANY("+placeholder(query.Scopes)+")")
	}
	if len(query.Kinds) > 0 {
		kinds := make([]string, len(query.Kinds))
		for index := range query.Kinds {
			kinds[index] = string(query.Kinds[index])
		}
		conditions = append(conditions, "fact.kind = ANY("+placeholder(kinds)+")")
	}
	if query.Selector.ResourceKind != "" {
		conditions = append(conditions, "fact.resource_ref->>'kind' = "+placeholder(query.Selector.ResourceKind))
	}
	if query.Selector.Namespace != "" {
		conditions = append(conditions, "fact.resource_ref->>'namespace' = "+placeholder(query.Selector.Namespace))
	}
	if query.Selector.NamePrefix != "" {
		prefix := placeholder(query.Selector.NamePrefix)
		conditions = append(conditions, "LEFT(fact.resource_ref->>'name', char_length("+prefix+")) = "+prefix)
	}
	if query.Selector.Name != "" {
		conditions = append(conditions, "fact.resource_ref->>'name' = "+placeholder(query.Selector.Name))
	}
	if query.Selector.Health != "" {
		conditions = append(conditions, "fact.payload->>'status' = "+placeholder(query.Selector.Health))
	}
	if query.Selector.HealthNot != "" {
		conditions = append(conditions, "fact.payload ? 'status'")
		conditions = append(conditions, "fact.payload->>'status' <> "+placeholder(query.Selector.HealthNot))
	}
	limit := query.Limit
	if limit == 0 {
		limit = defaultFleetFactLimit
	}
	statement := `
		SELECT fact.cluster_id, fact.kind, fact.payload, fact.observed_at, fact.resource_ref, fact.source, fact.provenance, fact.display
		FROM sith.fleet_facts fact
		WHERE ` + strings.Join(conditions, " AND ") + `
		ORDER BY fact.cluster_id, fact.observed_at DESC, fact.id DESC
		LIMIT ` + placeholder(limit)
	rows, err := tx.Query(ctx, statement, arguments...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	facts := make([]fleet.Fact, 0)
	for rows.Next() {
		var (
			clusterID   string
			fact        fleet.Fact
			kind        string
			resourceRef []byte
			provenance  []byte
			display     []byte
		)
		if err := rows.Scan(&clusterID, &kind, &fact.Observed, &fact.ObservedAt, &resourceRef, &fact.Source, &provenance, &display); err != nil {
			return nil, err
		}
		fact.Kind = fleet.FactKind(kind)
		if err := json.Unmarshal(resourceRef, &fact.Ref); err != nil {
			return nil, fmt.Errorf("decode stored resource reference: %w", err)
		}
		if err := json.Unmarshal(provenance, &fact.Provenance); err != nil {
			return nil, fmt.Errorf("decode stored provenance: %w", err)
		}
		if err := json.Unmarshal(display, &fact.Display); err != nil {
			return nil, fmt.Errorf("decode stored display fields: %w", err)
		}
		if fact.Ref.SourceKind != hubfleet.SourceKind || fact.Ref.Scope != clusterID || fact.Source != clusterID {
			return nil, fmt.Errorf("stored fact source does not match its registered spoke")
		}
		facts = append(facts, fact)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return facts, nil
}

func coverageFor(states map[string]clusterState, requested []string, freshness time.Duration, now time.Time) fleet.Coverage {
	coverage := fleet.Coverage{Requested: len(requested)}
	for _, id := range requested {
		state, exists := states[id]
		if !exists || state.lastSeen == nil || state.lastErrorKind != "" {
			coverage.Unreachable = append(coverage.Unreachable, id)
		}
		if exists && state.lastSeen != nil {
			if state.lastErrorKind == "" {
				coverage.Reachable++
			}
			stale, _ := staleState(state, freshness, now)
			if stale {
				coverage.Stale = append(coverage.Stale, id)
			}
		}
	}
	sort.Strings(coverage.Unreachable)
	sort.Strings(coverage.Stale)
	return coverage
}

func staleState(state clusterState, freshness time.Duration, now time.Time) (bool, string) {
	if state.lastSeen == nil {
		return false, ""
	}
	if state.lastErrorKind != "" {
		return true, "collection failed"
	}
	age := now.Sub(*state.lastSeen)
	if age > freshness {
		return true, age.Round(time.Second).String()
	}
	return false, ""
}

func normalizeFleetQuery(query fleet.Query) (fleet.Query, []string, error) {
	if err := query.Validate(); err != nil {
		return fleet.Query{}, nil, err
	}
	for _, kind := range query.Kinds {
		if kind != fleet.FactInventory && kind != fleet.FactHealth {
			return fleet.Query{}, nil, fmt.Errorf("fact kind %q is not available from persisted spoke snapshots", kind)
		}
	}
	if (query.Selector.Health != "" || query.Selector.HealthNot != "") &&
		(len(query.Kinds) != 1 || query.Kinds[0] != fleet.FactHealth) {
		return fleet.Query{}, nil, fmt.Errorf("health selectors require exactly the health fact kind")
	}
	if len(query.Selector.Labels) != 0 || query.Selector.Image != "" || query.Selector.CVE != "" {
		return fleet.Query{}, nil, fmt.Errorf("requested selector is not available from persisted spoke snapshots")
	}
	if query.Limit > maxFleetFactLimit {
		return fleet.Query{}, nil, fmt.Errorf("fact limit exceeds %d", maxFleetFactLimit)
	}
	requested, err := normalizeRequestedScopes(query.Scopes)
	if err != nil {
		return fleet.Query{}, nil, err
	}
	query.Scopes = requested
	return query, requested, nil
}

func normalizeRequestedScopes(scopes []string) ([]string, error) {
	if len(scopes) > 256 {
		return nil, fmt.Errorf("requested scopes exceed 256 entries")
	}
	seen := make(map[string]struct{}, len(scopes))
	requested := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		if err := validateScopeSelector(scope); err != nil {
			return nil, err
		}
		if _, exists := seen[scope]; !exists {
			seen[scope] = struct{}{}
			requested = append(requested, scope)
		}
	}
	sort.Strings(requested)
	return requested, nil
}

func validateScopeSelector(scope string) error {
	if scope == "" || strings.TrimSpace(scope) != scope || len(scope) > 256 {
		return fmt.Errorf("requested scope must be a trimmed value of at most 256 bytes")
	}
	for _, character := range scope {
		if character < 0x20 || character == 0x7f {
			return fmt.Errorf("requested scope contains a control character")
		}
	}
	return nil
}

func validateCollectionInput(spoke hubfleet.Spoke, snapshot hubfleet.Snapshot, attemptedAt time.Time) error {
	if attemptedAt.IsZero() {
		return fmt.Errorf("attempt time is required")
	}
	if err := hubfleet.ValidateSnapshot(spoke, snapshot, attemptedAt); err != nil {
		return err
	}
	return nil
}

func validFailureKind(failure hubfleet.FailureKind) bool {
	switch failure {
	case hubfleet.FailureTransport, hubfleet.FailureDeadline, hubfleet.FailureInvalidSnapshot:
		return true
	default:
		return false
	}
}

func requireReadScope(scope tenancy.Scope) error {
	if scope.WorkspaceID() == "" || tenancy.ValidateWorkspaceID(scope.WorkspaceID()) != nil {
		return fmt.Errorf("validated workspace scope is required")
	}
	if err := scope.Authorize(tenancy.ActionRead); err != nil {
		return err
	}
	return nil
}

func validateFreshness(freshness time.Duration, now time.Time) error {
	if now.IsZero() || freshness < time.Second || freshness > maxFleetFreshness {
		return fmt.Errorf("freshness must be between 1s and %s and now is required", maxFleetFreshness)
	}
	return nil
}

func dereferenceTime(value *time.Time) time.Time {
	if value == nil {
		return time.Time{}
	}
	return *value
}
