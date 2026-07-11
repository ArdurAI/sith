// SPDX-License-Identifier: Apache-2.0

package tenancy

import "fmt"

// Principal is an authenticated subject and an immutable copy of its workspace roles.
type Principal struct {
	subject     string
	memberships map[WorkspaceID]Role
}

// NewPrincipal validates and copies signed membership claims.
func NewPrincipal(subject string, memberships map[WorkspaceID]Role) (Principal, error) {
	if err := validateIdentity("subject", subject); err != nil {
		return Principal{}, err
	}
	if len(memberships) == 0 {
		return Principal{}, fmt.Errorf("principal must have at least one workspace membership")
	}
	cloned := make(map[WorkspaceID]Role, len(memberships))
	for workspaceID, role := range memberships {
		membership := Membership{WorkspaceID: workspaceID, Subject: subject, Role: role}
		if err := membership.Validate(); err != nil {
			return Principal{}, err
		}
		cloned[workspaceID] = role
	}
	return Principal{subject: subject, memberships: cloned}, nil
}

// Subject returns the cryptographically verified subject.
func (principal Principal) Subject() string {
	return principal.subject
}

// Memberships returns a defensive copy of the verified role map.
func (principal Principal) Memberships() map[WorkspaceID]Role {
	cloned := make(map[WorkspaceID]Role, len(principal.memberships))
	for workspaceID, role := range principal.memberships {
		cloned[workspaceID] = role
	}
	return cloned
}

// Scope resolves one requested workspace only from the principal's signed memberships.
func (principal Principal) Scope(workspaceID WorkspaceID) (Scope, error) {
	if err := validateIdentity("workspace ID", string(workspaceID)); err != nil {
		return Scope{}, err
	}
	role, exists := principal.memberships[workspaceID]
	if !exists || !role.Valid() {
		return Scope{}, fmt.Errorf("subject is not a member of workspace %q", workspaceID)
	}
	return Scope{workspaceID: workspaceID, subject: principal.subject, role: role}, nil
}

// Scope is the verified app-layer boundary threaded through hub data access.
type Scope struct {
	workspaceID WorkspaceID
	subject     string
	role        Role
}

// WorkspaceID returns the only workspace this scope may address.
func (scope Scope) WorkspaceID() WorkspaceID {
	return scope.workspaceID
}

// Subject returns the verified subject bound to the scope.
func (scope Scope) Subject() string {
	return scope.subject
}

// Role returns the verified role bound to the scope.
func (scope Scope) Role() Role {
	return scope.role
}

// Authorize fails closed when the role does not explicitly allow an action class.
func (scope Scope) Authorize(action Action) error {
	if !scope.role.Allows(action) {
		return fmt.Errorf("role %q does not permit action %q", scope.role, action)
	}
	return nil
}

// RequireWorkspace hard-fails instead of filtering a foreign workspace silently.
func (scope Scope) RequireWorkspace(workspaceID WorkspaceID) error {
	if scope.workspaceID == "" || workspaceID != scope.workspaceID {
		return fmt.Errorf("workspace scope mismatch")
	}
	return nil
}

// RequireAll hard-fails if any returned or targeted entity escapes the active scope.
func RequireAll[T any](scope Scope, values []T, workspaceOf func(T) WorkspaceID) error {
	if workspaceOf == nil {
		return fmt.Errorf("workspace accessor is required")
	}
	for index, value := range values {
		if err := scope.RequireWorkspace(workspaceOf(value)); err != nil {
			return fmt.Errorf("workspace scope mismatch at index %d: %w", index, err)
		}
	}
	return nil
}
