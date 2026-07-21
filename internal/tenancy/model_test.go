// SPDX-License-Identifier: Apache-2.0

package tenancy

import "testing"

func TestRoleAllowsExactActionClasses(t *testing.T) {
	t.Parallel()

	actions := []Action{ActionRead, ActionExportAudit, ActionProposeIntent, ActionApproveIntent, ActionManageWorkspace, "unknown"}
	tests := []struct {
		role    Role
		allowed map[Action]bool
	}{
		{role: RoleReader, allowed: map[Action]bool{ActionRead: true}},
		{role: RoleOperator, allowed: map[Action]bool{ActionRead: true, ActionProposeIntent: true}},
		{role: RoleApprover, allowed: map[Action]bool{ActionRead: true, ActionApproveIntent: true}},
		{role: RoleAdmin, allowed: map[Action]bool{ActionRead: true, ActionExportAudit: true, ActionManageWorkspace: true}},
		{role: "owner", allowed: map[Action]bool{}},
	}
	for _, test := range tests {
		for _, action := range actions {
			if got := test.role.Allows(action); got != test.allowed[action] {
				t.Errorf("role %q action %q allowed = %t, want %t", test.role, action, got, test.allowed[action])
			}
		}
	}
}

func TestPrincipalCopiesMembershipsAndScopesExactly(t *testing.T) {
	t.Parallel()

	memberships := map[WorkspaceID]Role{"workspace-a": RoleReader, "workspace-b": RoleOperator}
	principal, err := NewPrincipal("user:alice", memberships)
	if err != nil {
		t.Fatal(err)
	}
	memberships["workspace-a"] = RoleAdmin
	returned := principal.Memberships()
	returned["workspace-b"] = RoleAdmin

	scope, err := principal.Scope("workspace-a")
	if err != nil {
		t.Fatal(err)
	}
	if scope.Subject() != "user:alice" || scope.WorkspaceID() != "workspace-a" || scope.Role() != RoleReader {
		t.Fatalf("scope = subject %q workspace %q role %q", scope.Subject(), scope.WorkspaceID(), scope.Role())
	}
	if _, err := principal.Scope("workspace-c"); err == nil {
		t.Fatal("foreign workspace scope unexpectedly succeeded")
	}
}

func TestScopeHardFailsOnForeignRows(t *testing.T) {
	t.Parallel()

	principal, err := NewPrincipal("user:alice", map[WorkspaceID]Role{"workspace-a": RoleReader})
	if err != nil {
		t.Fatal(err)
	}
	scope, err := principal.Scope("workspace-a")
	if err != nil {
		t.Fatal(err)
	}
	type row struct{ workspace WorkspaceID }
	if err := RequireAll(scope, []row{{"workspace-a"}, {"workspace-b"}}, func(value row) WorkspaceID {
		return value.workspace
	}); err == nil {
		t.Fatal("foreign workspace row was silently accepted")
	}
	if err := RequireAll(scope, []row{{"workspace-a"}}, func(value row) WorkspaceID {
		return value.workspace
	}); err != nil {
		t.Fatalf("same-workspace row rejected: %v", err)
	}
}

func TestTenancyModelsRejectAmbiguousIdentity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		run  func() error
	}{
		{name: "blank workspace ID", run: func() error { return (Workspace{Name: "A", TenantKey: "tenant-a"}).Validate() }},
		{name: "padded tenant key", run: func() error { return (Workspace{ID: "a", Name: "A", TenantKey: " tenant-a"}).Validate() }},
		{name: "unknown role", run: func() error { return (Membership{WorkspaceID: "a", Subject: "alice", Role: "owner"}).Validate() }},
		{name: "control character", run: func() error { return (Membership{WorkspaceID: "a\n", Subject: "alice", Role: RoleReader}).Validate() }},
		{name: "malformed UTF-8 workspace ID", run: func() error {
			return ValidateWorkspaceID(WorkspaceID(string([]byte{'a', 0x80})))
		}},
		{name: "malformed UTF-8 display name", run: func() error {
			return (Workspace{ID: "a", Name: string([]byte{'A', 0x80}), TenantKey: "tenant-a"}).Validate()
		}},
		{name: "malformed UTF-8 subject", run: func() error {
			return (Membership{WorkspaceID: "a", Subject: string([]byte{'a', 0x80}), Role: RoleReader}).Validate()
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if err := test.run(); err == nil {
				t.Fatal("invalid identity unexpectedly accepted")
			}
		})
	}
}
