// SPDX-License-Identifier: Apache-2.0

package pep

import (
	"testing"

	"github.com/ArdurAI/sith/internal/intent"
)

func TestProposalApprovalBindingIsExactAndPrivacyMinimized(t *testing.T) {
	t.Parallel()

	input, err := NewProposalInput(
		"intent-250", "workspace-a", "user:operator", intent.VerbDeploymentRestart,
		testProposalTarget(), digestFor("validated-arguments"),
	)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := input.ApprovalBinding()
	if err != nil {
		t.Fatalf("ApprovalBinding() error = %v", err)
	}
	if err := binding.Validate(); err != nil {
		t.Fatalf("ApprovalBinding.Validate() error = %v", err)
	}
	if binding.IntentID() != "intent-250" || binding.WorkspaceID() != "workspace-a" ||
		binding.Proposer() != "user:operator" || binding.ResolvedDigest() != input.resolvedDigest {
		t.Fatalf("approval binding = %#v", binding)
	}
	if binding.ResolvedDigest() == input.argumentsDigest {
		t.Fatal("approval binding exposed the argument-only digest instead of the resolved envelope")
	}
}

func TestProposalApprovalBindingRejectsInvalidOrMutatedProposal(t *testing.T) {
	t.Parallel()

	if _, err := (ProposalInput{}).ApprovalBinding(); err == nil {
		t.Fatal("zero proposal produced an approval binding")
	}
	if err := (ApprovalBinding{}).Validate(); err == nil {
		t.Fatal("zero approval binding validated")
	}

	input, err := NewProposalInput(
		"intent-250", "workspace-a", "user:operator", intent.VerbDeploymentRestart,
		testProposalTarget(), digestFor("validated-arguments"),
	)
	if err != nil {
		t.Fatal(err)
	}
	input.resolvedDigest = digestFor("attacker-replacement")
	if _, err := input.ApprovalBinding(); err == nil {
		t.Fatal("mutated proposal produced an approval binding")
	}
}
