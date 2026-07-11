// SPDX-License-Identifier: Apache-2.0

package localops

import (
	"errors"
	"testing"
)

func TestTargetRequiresExplicitContextAndIdentity(t *testing.T) {
	t.Parallel()
	for _, target := range []Target{
		{Kind: "Pod", Name: "api"},
		{Context: "alpha", Name: "api"},
		{Context: "alpha", Kind: "Pod"},
	} {
		if err := target.Validate(); !errors.Is(err, ErrInvalidTarget) {
			t.Fatalf("Validate(%#v) error = %v, want ErrInvalidTarget", target, err)
		}
	}
	valid := Target{Context: "alpha", Namespace: "apps", Kind: "Pod", Name: "api"}
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate(valid) error = %v", err)
	}
	ref := valid.ResourceRef("local-kubeconfig")
	if ref.Scope != "alpha" || ref.Namespace != "apps" || ref.Name != "api" {
		t.Fatalf("ResourceRef() = %#v", ref)
	}
}
