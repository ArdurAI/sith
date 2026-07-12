// SPDX-License-Identifier: Apache-2.0

package hubauth

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ArdurAI/sith/internal/tenancy"
)

type memoryAPIKeyStore struct {
	records     map[string]APIKeyRecord
	memberships map[tenancy.WorkspaceID]map[string]tenancy.Role
}

func (store *memoryAPIKeyStore) CreateAPIKey(_ context.Context, scope tenancy.Scope, record APIKeyRecord) error {
	if scope.WorkspaceID() != record.WorkspaceID || store.memberships[record.WorkspaceID][record.Subject] == "" {
		return errors.New("membership missing")
	}
	store.records[string(record.WorkspaceID)+"/"+record.ID] = cloneAPIKeyRecord(record)
	return nil
}

func (store *memoryAPIKeyStore) LookupAPIKey(_ context.Context, workspaceID tenancy.WorkspaceID, keyID string) (APIKeyRecord, tenancy.Membership, error) {
	record, ok := store.records[string(workspaceID)+"/"+keyID]
	role := store.memberships[workspaceID][record.Subject]
	if !ok || !role.Valid() {
		return APIKeyRecord{}, tenancy.Membership{}, ErrAPIKeyNotFound
	}
	return cloneAPIKeyRecord(record), tenancy.Membership{WorkspaceID: workspaceID, Subject: record.Subject, Role: role}, nil
}

func (store *memoryAPIKeyStore) RotateAPIKey(ctx context.Context, scope tenancy.Scope, keyID string, replacement APIKeyRecord, retireAt time.Time) error {
	key := string(scope.WorkspaceID()) + "/" + keyID
	original, ok := store.records[key]
	if !ok || original.Subject != replacement.Subject {
		return ErrAPIKeyNotFound
	}
	if err := store.CreateAPIKey(ctx, scope, replacement); err != nil {
		return err
	}
	original.RetireAt = &retireAt
	original.ReplacedBy = replacement.ID
	store.records[key] = original
	return nil
}

func (store *memoryAPIKeyStore) RevokeAPIKey(_ context.Context, scope tenancy.Scope, keyID string, revokedAt time.Time) error {
	key := string(scope.WorkspaceID()) + "/" + keyID
	record, ok := store.records[key]
	if !ok {
		return ErrAPIKeyNotFound
	}
	record.RevokedAt = &revokedAt
	store.records[key] = record
	return nil
}

func TestAPIKeyExchangeUsesCurrentMembershipAndStoresOnlyVerifier(t *testing.T) {
	now := time.Date(2026, 7, 11, 18, 0, 0, 0, time.UTC)
	service, store, verifier, admin := newAPIKeyTestFixture(t, &now)

	raw, record, err := service.Issue(context.Background(), admin, "user:alice")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(raw, apiKeyPrefix+".") {
		t.Fatalf("issued key has unsafe shape")
	}
	stored := store.records["workspace-a/"+record.ID]
	if bytes.Contains(stored.Verifier, []byte(raw)) || len(stored.Verifier) != 32 {
		t.Fatal("plaintext credential reached persistence")
	}
	store.memberships["workspace-a"]["user:alice"] = tenancy.RoleOperator
	session, err := service.Exchange(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	principal, err := verifier.Verify(context.Background(), session.AccessToken)
	if err != nil {
		t.Fatalf("issued session did not verify: %v", err)
	}
	scope, err := principal.Scope("workspace-a")
	if err != nil || scope.Role() != tenancy.RoleOperator {
		t.Fatalf("session role = %q, error = %v, want current operator role", scope.Role(), err)
	}
	if _, err := verifier.Verify(context.Background(), raw); err == nil {
		t.Fatal("raw API key was accepted as a normal bearer session")
	}
	delete(store.memberships["workspace-a"], "user:alice")
	if _, err := service.Exchange(context.Background(), raw); !errors.Is(err, ErrInvalidAPIKey) {
		t.Fatalf("removed membership exchange error = %v", err)
	}
}

func TestAPIKeyRotationOverlapAndRevocation(t *testing.T) {
	now := time.Date(2026, 7, 11, 18, 0, 0, 0, time.UTC)
	service, _, _, admin := newAPIKeyTestFixture(t, &now)
	original, originalRecord, err := service.Issue(context.Background(), admin, "user:alice")
	if err != nil {
		t.Fatal(err)
	}
	replacement, replacementRecord, err := service.Rotate(context.Background(), admin, originalRecord.ID, "user:alice", 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Exchange(context.Background(), original); err != nil {
		t.Fatalf("original rejected during overlap: %v", err)
	}
	if _, err := service.Exchange(context.Background(), replacement); err != nil {
		t.Fatalf("replacement rejected: %v", err)
	}
	now = now.Add(10 * time.Minute)
	if _, err := service.Exchange(context.Background(), original); !errors.Is(err, ErrInvalidAPIKey) {
		t.Fatalf("retired original error = %v", err)
	}
	if err := service.Revoke(context.Background(), admin, replacementRecord.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Exchange(context.Background(), replacement); !errors.Is(err, ErrInvalidAPIKey) {
		t.Fatalf("revoked replacement error = %v", err)
	}
}

func TestAPIKeyServiceFailsClosed(t *testing.T) {
	now := time.Date(2026, 7, 11, 18, 0, 0, 0, time.UTC)
	service, _, _, admin := newAPIKeyTestFixture(t, &now)
	readerPrincipal, err := tenancy.NewPrincipal("user:alice", map[tenancy.WorkspaceID]tenancy.Role{"workspace-a": tenancy.RoleReader})
	if err != nil {
		t.Fatal(err)
	}
	reader, err := readerPrincipal.Scope("workspace-a")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Issue(context.Background(), reader, "user:alice"); err == nil {
		t.Fatal("reader issued an API key")
	}
	raw, record, err := service.Issue(context.Background(), admin, "user:alice")
	if err != nil {
		t.Fatal(err)
	}
	last := "A"
	if strings.HasSuffix(raw, "A") {
		last = "B"
	}
	mutated := raw[:len(raw)-1] + last
	for _, invalid := range []string{"", "Bearer " + raw, strings.Repeat("x", maxAPIKeyBytes+1), mutated, raw + ".extra"} {
		if _, err := service.Exchange(context.Background(), invalid); !errors.Is(err, ErrInvalidAPIKey) {
			t.Errorf("invalid key exchange error = %v", err)
		}
	}
	now = record.ExpiresAt
	if _, err := service.Exchange(context.Background(), raw); !errors.Is(err, ErrInvalidAPIKey) {
		t.Fatalf("expired key exchange error = %v", err)
	}
	if _, _, err := service.Rotate(context.Background(), admin, record.ID, "user:alice", maximumRotateOverlap+time.Second); err == nil {
		t.Fatal("excessive rotation overlap accepted")
	}
}

func TestAPIKeyServiceRejectsWeakConfigurationAndRandomFailure(t *testing.T) {
	now := time.Date(2026, 7, 11, 18, 0, 0, 0, time.UTC)
	service, store, _, admin := newAPIKeyTestFixture(t, &now)
	if _, err := NewAPIKeyService(APIKeyServiceConfig{Store: store, Issuer: service.issuer, Pepper: []byte("short")}); err == nil {
		t.Fatal("short pepper accepted")
	}
	service.random = errorReader{}
	if _, _, err := service.Issue(context.Background(), admin, "user:alice"); err == nil {
		t.Fatal("random source failure ignored")
	}
}

func FuzzParseAPIKeyFailsClosed(f *testing.F) {
	f.Add("sith_api_v1.d29ya3NwYWNlLWE.QUFBQUFBQUFBQUFBQUFBQQ.QUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUE")
	f.Add("Bearer not-an-api-key")
	f.Add(strings.Repeat("x", maxAPIKeyBytes+1))
	f.Fuzz(func(t *testing.T, raw string) {
		workspaceID, keyID, ok := parseAPIKey(raw)
		if !ok {
			return
		}
		if tenancy.ValidateWorkspaceID(workspaceID) != nil || !apiKeyIDPatternForTest(keyID) {
			t.Fatalf("parser accepted invalid workspace %q or key ID %q", workspaceID, keyID)
		}
	})
}

func apiKeyIDPatternForTest(value string) bool {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	return err == nil && len(decoded) == apiKeyIDBytes && base64.RawURLEncoding.EncodeToString(decoded) == value
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) { return 0, errors.New("entropy unavailable") }

func newAPIKeyTestFixture(t *testing.T, now *time.Time) (*APIKeyService, *memoryAPIKeyStore, *JWTVerifier, tenancy.Scope) {
	t.Helper()
	privateKey := ed25519.NewKeyFromSeed([]byte(strings.Repeat("k", ed25519.SeedSize)))
	issuer, err := NewSessionIssuer(SessionIssuerConfig{
		Issuer: "https://issuer.sith.test", Audience: "https://hub.sith.test", KeyID: "session-key",
		PrivateKey: privateKey, Now: func() time.Time { return *now },
	})
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := NewJWTVerifier(JWTConfig{
		Issuer: "https://issuer.sith.test", Audience: "https://hub.sith.test",
		Keys: map[string]ed25519.PublicKey{"session-key": privateKey.Public().(ed25519.PublicKey)},
		Now:  func() time.Time { return *now }, MaxLifetime: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	store := &memoryAPIKeyStore{
		records: map[string]APIKeyRecord{},
		memberships: map[tenancy.WorkspaceID]map[string]tenancy.Role{
			"workspace-a": {"user:admin": tenancy.RoleAdmin, "user:alice": tenancy.RoleReader},
		},
	}
	service, err := NewAPIKeyService(APIKeyServiceConfig{
		Store: store, Issuer: issuer, Pepper: []byte(strings.Repeat("p", minimumPepperBytes)),
		Now: func() time.Time { return *now },
	})
	if err != nil {
		t.Fatal(err)
	}
	principal, err := tenancy.NewPrincipal("user:admin", map[tenancy.WorkspaceID]tenancy.Role{"workspace-a": tenancy.RoleAdmin})
	if err != nil {
		t.Fatal(err)
	}
	admin, err := principal.Scope("workspace-a")
	if err != nil {
		t.Fatal(err)
	}
	return service, store, verifier, admin
}
