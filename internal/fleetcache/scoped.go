// SPDX-License-Identifier: Apache-2.0

package fleetcache

import (
	"fmt"

	"github.com/ArdurAI/sith/internal/tenancy"
)

// QueryScoped is the hub-facing cache path: scope selects rows and then revalidates every result.
func (store *Store) QueryScoped(scope tenancy.Scope, query Query) (Snapshot, error) {
	if store == nil {
		return Snapshot{}, fmt.Errorf("query scoped cache: store is nil")
	}
	if scope.WorkspaceID() == "" {
		return Snapshot{}, fmt.Errorf("query scoped cache: workspace scope is required")
	}
	if err := scope.Authorize(tenancy.ActionRead); err != nil {
		return Snapshot{}, fmt.Errorf("query scoped cache: %w", err)
	}
	snapshot := store.Query(string(scope.WorkspaceID()), query)
	if err := tenancy.RequireAll(scope, snapshot.Records, func(record Record) tenancy.WorkspaceID {
		return tenancy.WorkspaceID(record.Workspace)
	}); err != nil {
		return Snapshot{}, fmt.Errorf("query scoped cache: %w", err)
	}
	return snapshot, nil
}
