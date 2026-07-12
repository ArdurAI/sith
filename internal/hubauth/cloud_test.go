// SPDX-License-Identifier: Apache-2.0

package hubauth

import (
	"context"
	"crypto/ed25519"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ArdurAI/sith/internal/tenancy"
)

type cloudMemoryStore struct {
	mu       sync.Mutex
	bindings map[string]string
	roles    map[tenancy.WorkspaceID]map[string]tenancy.Role
}

func (store *cloudMemoryStore) CreateCloudIdentityBinding(
	_ context.Context,
	scope tenancy.Scope,
	identity CloudIdentity,
	memberSubject string,
) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if scope.Role() != tenancy.RoleAdmin || !store.roles[scope.WorkspaceID()][memberSubject].Valid() {
		return errors.New("membership missing")
	}
	store.bindings[cloudBindingKey(scope.WorkspaceID(), identity)] = memberSubject
	return nil
}

func (store *cloudMemoryStore) LookupCloudIdentityMembership(
	_ context.Context,
	workspaceID tenancy.WorkspaceID,
	identity CloudIdentity,
) (tenancy.Membership, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	memberSubject := store.bindings[cloudBindingKey(workspaceID, identity)]
	role := store.roles[workspaceID][memberSubject]
	if memberSubject == "" || !role.Valid() {
		return tenancy.Membership{}, ErrCloudBindingNotFound
	}
	return tenancy.Membership{WorkspaceID: workspaceID, Subject: memberSubject, Role: role}, nil
}

type cloudVerifierStub struct {
	provider CloudProvider
	proofs   map[string]VerifiedCloudPrincipal
}

func (stub cloudVerifierStub) Provider() CloudProvider {
	return stub.provider
}

func (stub cloudVerifierStub) Verify(_ context.Context, rawProof string) (VerifiedCloudPrincipal, error) {
	principal, found := stub.proofs[rawProof]
	if !found {
		return VerifiedCloudPrincipal{}, ErrInvalidCloudProof
	}
	return principal, nil
}

func TestCloudIdentityExchangeUsesFixedWorkspaceAndSingleUseProof(t *testing.T) {
	now := time.Date(2026, 7, 12, 14, 0, 0, 0, time.UTC)
	identity := cloudTestIdentity()
	service, store, verifier, admin := newCloudTestFixture(t, &now, map[string]VerifiedCloudPrincipal{
		"proof-one": cloudTestPrincipal(identity, now),
		"proof-two": cloudTestPrincipal(identity, now),
	})
	if err := service.BindIdentity(context.Background(), admin, identity, "user:alice"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Exchange(context.Background(), "workspace-b", CloudProviderAWS, "proof-one"); !errors.Is(err, ErrInvalidCloudProof) {
		t.Fatalf("cross-workspace exchange error = %v", err)
	}
	session, err := service.Exchange(context.Background(), "workspace-a", CloudProviderAWS, "proof-one")
	if err != nil {
		t.Fatal(err)
	}
	principal, err := verifier.Verify(context.Background(), session.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := principal.Scope("workspace-a")
	if err != nil || scope.Subject() != "user:alice" || scope.Role() != tenancy.RoleReader {
		t.Fatalf("mapped scope = subject %q role %q error %v", scope.Subject(), scope.Role(), err)
	}
	if _, err := principal.Scope("workspace-b"); err == nil {
		t.Fatal("cloud proof granted an unbound workspace")
	}
	if _, err := service.Exchange(context.Background(), "workspace-a", CloudProviderAWS, "proof-one"); !errors.Is(err, ErrInvalidCloudProof) {
		t.Fatalf("replayed proof error = %v", err)
	}
	store.mu.Lock()
	store.roles["workspace-a"]["user:alice"] = tenancy.RoleOperator
	store.mu.Unlock()
	session, err = service.Exchange(context.Background(), "workspace-a", CloudProviderAWS, "proof-two")
	if err != nil {
		t.Fatal(err)
	}
	principal, err = verifier.Verify(context.Background(), session.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	scope, err = principal.Scope("workspace-a")
	if err != nil || scope.Role() != tenancy.RoleOperator {
		t.Fatalf("current mapped role = %q error %v", scope.Role(), err)
	}
}

func TestCloudIdentityExchangeRejectsInvalidProofs(t *testing.T) {
	now := time.Date(2026, 7, 12, 14, 0, 0, 0, time.UTC)
	identity := cloudTestIdentity()
	expired := cloudTestPrincipal(identity, now)
	expired.ExpiresAt = now.Add(-time.Minute)
	future := cloudTestPrincipal(identity, now)
	future.IssuedAt = now.Add(2 * time.Minute)
	crossProvider := cloudTestPrincipal(identity, now)
	crossProvider.Identity.Provider = CloudProviderAzure
	service, _, _, admin := newCloudTestFixture(t, &now, map[string]VerifiedCloudPrincipal{
		"expired": expired, "future": future, "cross-provider": crossProvider,
		"unbound": cloudTestPrincipal(CloudIdentity{Provider: CloudProviderAWS, Realm: "222222222222", Subject: "AROAX:unbound"}, now),
	})
	if err := service.BindIdentity(context.Background(), admin, identity, "user:alice"); err != nil {
		t.Fatal(err)
	}
	tests := map[string]struct {
		provider CloudProvider
		proof    string
	}{
		"unknown proof":    {provider: CloudProviderAWS, proof: "unknown"},
		"expired":          {provider: CloudProviderAWS, proof: "expired"},
		"future issued":    {provider: CloudProviderAWS, proof: "future"},
		"cross provider":   {provider: CloudProviderAWS, proof: "cross-provider"},
		"unbound identity": {provider: CloudProviderAWS, proof: "unbound"},
		"wrong provider":   {provider: CloudProviderGCP, proof: "unbound"},
		"empty proof":      {provider: CloudProviderAWS, proof: ""},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := service.Exchange(context.Background(), "workspace-a", test.provider, test.proof); !errors.Is(err, ErrInvalidCloudProof) {
				t.Fatalf("exchange error = %v", err)
			}
		})
	}
	if _, err := service.Exchange(context.Background(), "workspace-a", CloudProviderAWS, strings.Repeat("x", maxCloudProofBytes+1)); !errors.Is(err, ErrInvalidCloudProof) {
		t.Fatalf("oversized proof error = %v", err)
	}
}

func TestMemoryCloudReplayGuardBoundsAndExpiresProofDigests(t *testing.T) {
	now := time.Date(2026, 7, 12, 14, 0, 0, 0, time.UTC)
	guard, err := NewMemoryCloudReplayGuard(MemoryCloudReplayGuardConfig{
		Capacity: 1,
		Now:      func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	first := make([]byte, 32)
	second := make([]byte, 32)
	first[0] = 1
	second[0] = 2
	if err := guard.Consume(context.Background(), first, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := guard.Consume(context.Background(), first, now.Add(time.Minute)); !errors.Is(err, ErrCloudReplayDetected) {
		t.Fatalf("replay error = %v", err)
	}
	if err := guard.Consume(context.Background(), second, now.Add(time.Minute)); !errors.Is(err, ErrCloudReplayDetected) {
		t.Fatalf("capacity error = %v", err)
	}
	now = now.Add(time.Minute)
	if err := guard.Consume(context.Background(), second, now.Add(time.Minute)); err != nil {
		t.Fatalf("expired replay was not pruned: %v", err)
	}
	if err := (&MemoryCloudReplayGuard{}).Consume(context.Background(), second, now.Add(time.Minute)); !errors.Is(err, ErrCloudReplayDetected) {
		t.Fatalf("invalid direct guard error = %v", err)
	}
}

func TestCloudIdentityConfigurationAndValidationFailClosed(t *testing.T) {
	now := time.Date(2026, 7, 12, 14, 0, 0, 0, time.UTC)
	identity := cloudTestIdentity()
	_, store, _, _ := newCloudTestFixture(t, &now, map[string]VerifiedCloudPrincipal{"proof": cloudTestPrincipal(identity, now)})
	issuer, err := cloudTestIssuer(&now)
	if err != nil {
		t.Fatal(err)
	}
	guard, err := NewMemoryCloudReplayGuard(MemoryCloudReplayGuardConfig{})
	if err != nil {
		t.Fatal(err)
	}
	for _, config := range []CloudIdentityServiceConfig{
		{Store: store, Issuer: issuer, ReplayGuard: guard, ReplayPepper: []byte(strings.Repeat("p", minimumPepperBytes))},
		{Verifiers: []CloudProofVerifier{cloudVerifierStub{provider: CloudProviderAWS}, cloudVerifierStub{provider: CloudProviderAWS}}, Store: store, Issuer: issuer, ReplayGuard: guard, ReplayPepper: []byte(strings.Repeat("p", minimumPepperBytes))},
		{Verifiers: []CloudProofVerifier{cloudVerifierStub{provider: CloudProviderAWS}}, Store: store, Issuer: issuer, ReplayGuard: guard, ReplayPepper: []byte("short")},
	} {
		if _, err := NewCloudIdentityService(config); err == nil {
			t.Errorf("unsafe service configuration accepted: %#v", config)
		}
	}
	for _, identity := range []CloudIdentity{
		{Provider: "unknown", Realm: "realm", Subject: "subject"},
		{Provider: CloudProviderAWS, Realm: " ", Subject: "subject"},
		{Provider: CloudProviderAWS, Realm: "realm", Subject: "\n"},
	} {
		if identity.Validate() == nil {
			t.Errorf("invalid identity accepted: %#v", identity)
		}
	}
}

func newCloudTestFixture(
	t *testing.T,
	now *time.Time,
	proofs map[string]VerifiedCloudPrincipal,
) (*CloudIdentityService, *cloudMemoryStore, *JWTVerifier, tenancy.Scope) {
	t.Helper()
	store := &cloudMemoryStore{
		bindings: map[string]string{},
		roles: map[tenancy.WorkspaceID]map[string]tenancy.Role{
			"workspace-a": {"user:admin": tenancy.RoleAdmin, "user:alice": tenancy.RoleReader},
			"workspace-b": {"user:bob": tenancy.RoleReader},
		},
	}
	issuer, err := cloudTestIssuer(now)
	if err != nil {
		t.Fatal(err)
	}
	guard, err := NewMemoryCloudReplayGuard(MemoryCloudReplayGuardConfig{Now: func() time.Time { return *now }})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewCloudIdentityService(CloudIdentityServiceConfig{
		Verifiers: []CloudProofVerifier{cloudVerifierStub{provider: CloudProviderAWS, proofs: proofs}},
		Store:     store, Issuer: issuer, ReplayGuard: guard, ReplayPepper: []byte(strings.Repeat("p", minimumPepperBytes)),
		Now: func() time.Time { return *now },
	})
	if err != nil {
		t.Fatal(err)
	}
	sessionKey := issuer.privateKey.Public().(ed25519.PublicKey)
	verifier, err := NewJWTVerifier(JWTConfig{
		Issuer: "https://issuer.sith.test", Audience: "https://hub.sith.test",
		Keys: map[string]ed25519.PublicKey{"sith-session": sessionKey},
		Now:  func() time.Time { return *now }, MaxLifetime: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	admin := cloudAdminScope(t)
	return service, store, verifier, admin
}

func cloudTestIssuer(now *time.Time) (*SessionIssuer, error) {
	privateKey := ed25519.NewKeyFromSeed([]byte(strings.Repeat("c", ed25519.SeedSize)))
	return NewSessionIssuer(SessionIssuerConfig{
		Issuer: "https://issuer.sith.test", Audience: "https://hub.sith.test", KeyID: "sith-session",
		PrivateKey: privateKey, Now: func() time.Time { return *now },
	})
}

func cloudTestIdentity() CloudIdentity {
	return CloudIdentity{Provider: CloudProviderAWS, Realm: "111111111111", Subject: "AROAX:build-agent"}
}

func cloudTestPrincipal(identity CloudIdentity, now time.Time) VerifiedCloudPrincipal {
	return VerifiedCloudPrincipal{
		Identity: identity, Audience: "https://hub.sith.test",
		IssuedAt: now, ExpiresAt: now.Add(30 * time.Minute),
	}
}

func cloudAdminScope(t *testing.T) tenancy.Scope {
	t.Helper()
	principal, err := tenancy.NewPrincipal("user:admin", map[tenancy.WorkspaceID]tenancy.Role{"workspace-a": tenancy.RoleAdmin})
	if err != nil {
		t.Fatal(err)
	}
	scope, err := principal.Scope("workspace-a")
	if err != nil {
		t.Fatal(err)
	}
	return scope
}

func cloudBindingKey(workspaceID tenancy.WorkspaceID, identity CloudIdentity) string {
	return string(workspaceID) + "\x00" + string(identity.Provider) + "\x00" + identity.Realm + "\x00" + identity.Subject
}
