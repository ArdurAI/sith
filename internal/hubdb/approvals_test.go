// SPDX-License-Identifier: Apache-2.0

package hubdb

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"strings"
	"testing"

	"github.com/ArdurAI/sith/internal/pep"
	"github.com/ArdurAI/sith/internal/tenancy"
)

func TestApprovalGrantIdentifierIsOpaqueAndCanonical(t *testing.T) {
	t.Parallel()

	identifier, err := newApprovalGrantID(bytes.NewReader(bytes.Repeat([]byte{0x42}, approvalGrantIDBytes)))
	if err != nil {
		t.Fatal(err)
	}
	if !approvalGrantIDPattern.MatchString(identifier.String()) || len(identifier.String()) != 22 {
		t.Fatalf("approval grant identifier = %q", identifier)
	}
	if _, err := newApprovalGrantID(failingApprovalReader{}); err == nil {
		t.Fatal("entropy failure minted an approval identifier")
	}
	if _, err := newApprovalGrantID(nil); err == nil {
		t.Fatal("nil entropy source minted an approval identifier")
	}
}

func TestApprovalGrantBoundaryClassifiesMissingDatabaseAsOperationalError(t *testing.T) {
	t.Parallel()

	if _, err := (*AppDB)(nil).CreateApprovalGrant(context.Background(), tenancy.Scope{}, pep.ApprovalBinding{}); err == nil {
		t.Fatal("nil database created an approval grant")
	} else if errors.Is(err, ErrApprovalGrantUnavailable) {
		t.Fatalf("nil database error was misclassified as safe approval refusal: %v", err)
	}
	if err := (*AppDB)(nil).ConsumeApprovalGrant(context.Background(), tenancy.Scope{}, pep.ApprovalBinding{}, "AAAAAAAAAAAAAAAAAAAAAA"); err == nil {
		t.Fatal("nil database consumed an approval grant")
	} else if errors.Is(err, ErrApprovalGrantUnavailable) {
		t.Fatalf("nil database error was misclassified as safe approval refusal: %v", err)
	}
}

func TestApprovalGrantExpiryMigrationIsFixedVersionedAndFailClosed(t *testing.T) {
	t.Parallel()

	migration, err := fs.ReadFile(migrationFiles, "migrations/0013_approval_grant_expiry.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(migration)
	for _, required := range []string{
		"ADD COLUMN expires_at timestamptz", "ADD COLUMN evidence_version smallint",
		"expires_at = approved_at + interval '10 minutes'", "evidence_version IN (1, 2)",
		"evidence_version = 1 OR consumed_at IS NULL OR consumed_at < expires_at",
		"NO FORCE ROW LEVEL SECURITY", "FORCE ROW LEVEL SECURITY", "format_version IN (1, 2, 3)",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("approval expiry migration is missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"DELETE FROM sith.approval_grants", "SET consumed_at", "DROP TABLE", "DISABLE ROW LEVEL SECURITY",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("approval expiry migration contains destructive legacy handling %q", forbidden)
		}
	}
	noForce := strings.Index(text, "NO FORCE ROW LEVEL SECURITY")
	backfill := strings.Index(text, "UPDATE sith.approval_grants")
	restoreForce := strings.LastIndex(text, "FORCE ROW LEVEL SECURITY")
	if noForce < 0 || backfill <= noForce || restoreForce <= backfill {
		t.Fatal("approval expiry backfill does not restore FORCE RLS around its transactional owner window")
	}
}

func TestApprovalGrantMigrationIsPrivacyMinimizedAndForcedRLS(t *testing.T) {
	t.Parallel()

	migration, err := fs.ReadFile(migrationFiles, "migrations/0010_approval_grants.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(migration)
	for _, required := range []string{
		"CREATE TABLE sith.approval_grants", "UNIQUE (workspace_id, intent_id, approver, resolved_digest)",
		"proposer <> approver", "consumed_at IS NULL OR consumed_at >= approved_at",
		"ENABLE ROW LEVEL SECURITY", "FORCE ROW LEVEL SECURITY", "CREATE POLICY workspace_isolation",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("approval migration is missing %q", required)
		}
	}
	for _, forbidden := range []string{"jsonb", "argument_payload", "target_payload", "justification", "credential", "token", "elicitation"} {
		if strings.Contains(strings.ToLower(text), forbidden) {
			t.Fatalf("approval migration contains forbidden payload surface %q", forbidden)
		}
	}
}

func FuzzApprovalGrantIDCanonicalVocabulary(f *testing.F) {
	f.Add("AAAAAAAAAAAAAAAAAAAAAA")
	f.Add("not/canonical")
	f.Fuzz(func(t *testing.T, value string) {
		matches := approvalGrantIDPattern.MatchString(value)
		if matches && len(value) != 22 {
			t.Fatalf("pattern accepted non-canonical length %d", len(value))
		}
	})
}

type failingApprovalReader struct{}

func (failingApprovalReader) Read([]byte) (int, error) { return 0, errors.New("entropy unavailable") }
