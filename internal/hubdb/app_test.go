// SPDX-License-Identifier: Apache-2.0

package hubdb

import (
	"context"
	"strings"
	"testing"

	"github.com/ArdurAI/sith/internal/tenancy"
)

func TestAppDBRejectsMissingBoundary(t *testing.T) {
	t.Parallel()

	var nilContext context.Context
	if _, err := OpenAppDB(nilContext, AppConfig{}); err == nil {
		t.Fatal("nil context and empty URL unexpectedly accepted")
	}
	if err := (*AppDB)(nil).InWorkspace(context.Background(), tenancy.Scope{}, nil); err == nil {
		t.Fatal("nil database unexpectedly accepted")
	}
	if err := (*AppDB)(nil).Ping(context.Background()); err == nil {
		t.Fatal("nil database readiness check unexpectedly succeeded")
	}
}

func TestSecureTransportRejectsMissingTLS(t *testing.T) {
	t.Parallel()

	if secureTransport(nil) {
		t.Fatal("nil connection configuration reported secure")
	}
}

func TestOpenAppDBRejectsRemotePlaintextEscapeHatch(t *testing.T) {
	t.Parallel()

	_, err := OpenAppDB(context.Background(), AppConfig{
		URL: "postgres://sith_app:secret@192.0.2.1/sith?sslmode=disable", AllowInsecure: true,
	})
	if err == nil || !strings.Contains(err.Error(), "TLS") {
		t.Fatalf("remote plaintext connection error = %v, want TLS refusal", err)
	}
}

func TestValidateRoleName(t *testing.T) {
	t.Parallel()

	for _, role := range []string{"", " sith_app", "sith\napp", string(make([]byte, 64))} {
		if err := validateRoleName(role); err == nil {
			t.Errorf("invalid role %q unexpectedly accepted", role)
		}
	}
	if err := validateRoleName("sith_app"); err != nil {
		t.Fatalf("valid role rejected: %v", err)
	}
}
