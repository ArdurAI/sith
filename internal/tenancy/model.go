// SPDX-License-Identifier: Apache-2.0

package tenancy

import (
	"fmt"
	"strings"
	"unicode"
)

const maxIdentityLength = 256

// WorkspaceID is the immutable tenancy boundary carried by every hub-scoped entity.
type WorkspaceID string

// Role is one fail-safe workspace permission set.
type Role string

// Supported workspace roles.
const (
	RoleReader   Role = "reader"
	RoleOperator Role = "operator"
	RoleApprover Role = "approver"
	RoleAdmin    Role = "admin"
)

// Action is a closed authorization class rather than a free-form permission string.
type Action string

// Supported authorization classes.
const (
	ActionRead            Action = "read"
	ActionProposeIntent   Action = "propose-intent"
	ActionApproveIntent   Action = "approve-intent"
	ActionManageWorkspace Action = "manage-workspace"
)

// Workspace is the single hub tenancy anchor.
type Workspace struct {
	ID        WorkspaceID `json:"id"`
	Name      string      `json:"name"`
	TenantKey string      `json:"tenant_key"`
}

// Validate rejects ambiguous or unusable tenancy identifiers.
func (workspace Workspace) Validate() error {
	if err := validateIdentity("workspace ID", string(workspace.ID)); err != nil {
		return err
	}
	if err := validateDisplayName("workspace name", workspace.Name); err != nil {
		return err
	}
	if err := validateIdentity("tenant key", workspace.TenantKey); err != nil {
		return err
	}
	return nil
}

// Membership grants one subject exactly one role in one workspace.
type Membership struct {
	WorkspaceID WorkspaceID `json:"workspace_id"`
	Subject     string      `json:"subject"`
	Role        Role        `json:"role"`
}

// Validate rejects incomplete memberships and unknown roles.
func (membership Membership) Validate() error {
	if err := validateIdentity("workspace ID", string(membership.WorkspaceID)); err != nil {
		return err
	}
	if err := validateIdentity("subject", membership.Subject); err != nil {
		return err
	}
	if !membership.Role.Valid() {
		return fmt.Errorf("membership role %q is not supported", membership.Role)
	}
	return nil
}

// Valid reports whether the role belongs to the closed role vocabulary.
func (role Role) Valid() bool {
	switch role {
	case RoleReader, RoleOperator, RoleApprover, RoleAdmin:
		return true
	default:
		return false
	}
}

// Allows applies the least-privilege role matrix. Unknown actions always fail closed.
func (role Role) Allows(action Action) bool {
	switch role {
	case RoleReader:
		return action == ActionRead
	case RoleOperator:
		return action == ActionRead || action == ActionProposeIntent
	case RoleApprover:
		return action == ActionRead || action == ActionApproveIntent
	case RoleAdmin:
		return action == ActionRead || action == ActionManageWorkspace
	default:
		return false
	}
}

func validateIdentity(name, value string) error {
	if value == "" || strings.TrimSpace(value) != value {
		return fmt.Errorf("%s must be a non-empty, trimmed value", name)
	}
	if len(value) > maxIdentityLength {
		return fmt.Errorf("%s exceeds %d bytes", name, maxIdentityLength)
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return fmt.Errorf("%s contains a control character", name)
		}
	}
	return nil
}

func validateDisplayName(name, value string) error {
	if value == "" || strings.TrimSpace(value) != value {
		return fmt.Errorf("%s must be a non-empty, trimmed value", name)
	}
	if len(value) > maxIdentityLength {
		return fmt.Errorf("%s exceeds %d bytes", name, maxIdentityLength)
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return fmt.Errorf("%s contains a control character", name)
		}
	}
	return nil
}
