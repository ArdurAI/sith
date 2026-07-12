// SPDX-License-Identifier: Apache-2.0

package hubauth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/ArdurAI/sith/internal/tenancy"
)

const (
	maxCloudProofBytes        = 32 * 1024
	maxCloudIdentityBytes     = 256
	maxCloudAudienceBytes     = 2048
	maximumCloudProofLifetime = time.Hour
	maximumCloudReplayEntries = 100_000
	defaultCloudReplayEntries = 8_192
)

var (
	// ErrInvalidCloudProof deliberately hides verifier, binding, and replay details.
	ErrInvalidCloudProof = errors.New("invalid cloud identity proof")
	// ErrCloudBindingNotFound lets stores report a miss without exposing it through Exchange.
	ErrCloudBindingNotFound = errors.New("cloud identity binding not found")
	// ErrCloudReplayDetected is returned only inside the replay boundary.
	ErrCloudReplayDetected = errors.New("cloud identity proof has already been used")
)

// CloudProvider identifies a closed native cloud identity verifier.
type CloudProvider string

// Supported cloud identity providers.
const (
	CloudProviderAWS   CloudProvider = "aws"
	CloudProviderAzure CloudProvider = "azure"
	CloudProviderGCP   CloudProvider = "gcp"
)

// Valid reports whether a provider belongs to the closed cloud identity vocabulary.
func (provider CloudProvider) Valid() bool {
	switch provider {
	case CloudProviderAWS, CloudProviderAzure, CloudProviderGCP:
		return true
	default:
		return false
	}
}

// CloudIdentity is the provider-neutral, immutable identity selected by a verifier.
type CloudIdentity struct {
	Provider CloudProvider
	Realm    string
	Subject  string
}

// Validate rejects an ambiguous provider, realm, or subject before a binding reaches persistence.
func (identity CloudIdentity) Validate() error {
	if !identity.Provider.Valid() {
		return fmt.Errorf("cloud provider is unsupported")
	}
	if err := validateCloudIdentityValue("cloud realm", identity.Realm, maxCloudIdentityBytes); err != nil {
		return err
	}
	if err := validateCloudIdentityValue("cloud subject", identity.Subject, maxCloudIdentityBytes); err != nil {
		return err
	}
	return nil
}

// VerifiedCloudPrincipal is the short-lived identity result returned by a native cloud verifier.
type VerifiedCloudPrincipal struct {
	Identity  CloudIdentity
	Audience  string
	IssuedAt  time.Time
	ExpiresAt time.Time
}

// Validate verifies the generic invariants every native verifier must provide.
func (principal VerifiedCloudPrincipal) Validate(now time.Time) error {
	if err := principal.Identity.Validate(); err != nil {
		return err
	}
	if err := validateCloudIdentityValue("cloud audience", principal.Audience, maxCloudAudienceBytes); err != nil {
		return err
	}
	if principal.IssuedAt.IsZero() || principal.ExpiresAt.IsZero() || !principal.ExpiresAt.After(principal.IssuedAt) {
		return fmt.Errorf("cloud proof has an invalid lifetime")
	}
	if principal.ExpiresAt.Sub(principal.IssuedAt) > maximumCloudProofLifetime ||
		principal.IssuedAt.After(now.Add(time.Minute)) || !principal.ExpiresAt.After(now) {
		return fmt.Errorf("cloud proof lifetime is outside the accepted window")
	}
	return nil
}

// CloudProofVerifier validates one provider's native proof and emits only normalized identity data.
type CloudProofVerifier interface {
	Provider() CloudProvider
	Verify(context.Context, string) (VerifiedCloudPrincipal, error)
}

// CloudIdentityStore persists explicit workspace bindings without trusting cloud authorization claims.
type CloudIdentityStore interface {
	CreateCloudIdentityBinding(context.Context, tenancy.Scope, CloudIdentity, string) error
	LookupCloudIdentityMembership(context.Context, tenancy.WorkspaceID, CloudIdentity) (tenancy.Membership, error)
}

// CloudReplayGuard atomically consumes a one-way proof identifier until its verified expiry.
type CloudReplayGuard interface {
	Consume(context.Context, []byte, time.Time) error
}

// CloudIdentityServiceConfig supplies verification, binding, replay, and session dependencies.
type CloudIdentityServiceConfig struct {
	Verifiers    []CloudProofVerifier
	Store        CloudIdentityStore
	Issuer       *SessionIssuer
	ReplayGuard  CloudReplayGuard
	ReplayPepper []byte
	Now          func() time.Time
}

// CloudIdentityService exchanges one verified native cloud proof for a Sith session.
type CloudIdentityService struct {
	verifiers    map[CloudProvider]CloudProofVerifier
	store        CloudIdentityStore
	issuer       *SessionIssuer
	replayGuard  CloudReplayGuard
	replayPepper []byte
	now          func() time.Time
}

// NewCloudIdentityService constructs a verifier-neutral identity boundary.
func NewCloudIdentityService(config CloudIdentityServiceConfig) (*CloudIdentityService, error) {
	if config.Store == nil || config.Issuer == nil || config.ReplayGuard == nil || len(config.Verifiers) == 0 {
		return nil, fmt.Errorf("construct cloud identity service: store, session issuer, replay guard, and verifiers are required")
	}
	if len(config.ReplayPepper) < minimumPepperBytes {
		return nil, fmt.Errorf("construct cloud identity service: replay pepper must contain at least %d bytes", minimumPepperBytes)
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	verifiers := make(map[CloudProvider]CloudProofVerifier, len(config.Verifiers))
	for _, verifier := range config.Verifiers {
		if verifier == nil || !verifier.Provider().Valid() || verifiers[verifier.Provider()] != nil {
			return nil, fmt.Errorf("construct cloud identity service: verifiers must have unique supported providers")
		}
		verifiers[verifier.Provider()] = verifier
	}
	return &CloudIdentityService{
		verifiers: verifiers, store: config.Store, issuer: config.Issuer, replayGuard: config.ReplayGuard,
		replayPepper: append([]byte(nil), config.ReplayPepper...), now: config.Now,
	}, nil
}

// BindIdentity maps one verified cloud identity to an existing workspace member.
func (service *CloudIdentityService) BindIdentity(
	ctx context.Context,
	admin tenancy.Scope,
	identity CloudIdentity,
	memberSubject string,
) error {
	if service == nil || ctx == nil || ctx.Err() != nil {
		return fmt.Errorf("bind cloud identity: service and active context are required")
	}
	if err := admin.Authorize(tenancy.ActionManageWorkspace); err != nil {
		return fmt.Errorf("bind cloud identity: %w", err)
	}
	if identity.Validate() != nil || service.verifiers[identity.Provider] == nil ||
		validateCloudIdentityValue("member subject", memberSubject, maxCloudIdentityBytes) != nil {
		return fmt.Errorf("bind cloud identity: invalid provider, identity, or member subject")
	}
	if err := service.store.CreateCloudIdentityBinding(ctx, admin, identity, memberSubject); err != nil {
		return fmt.Errorf("bind cloud identity: persist mapping: %w", err)
	}
	return nil
}

// Exchange validates one fixed provider proof and resolves its current workspace membership.
func (service *CloudIdentityService) Exchange(
	ctx context.Context,
	workspaceID tenancy.WorkspaceID,
	provider CloudProvider,
	rawProof string,
) (IssuedSession, error) {
	if service == nil || ctx == nil || ctx.Err() != nil || tenancy.ValidateWorkspaceID(workspaceID) != nil ||
		!provider.Valid() || rawProof == "" || len(rawProof) > maxCloudProofBytes || strings.TrimSpace(rawProof) != rawProof {
		return IssuedSession{}, ErrInvalidCloudProof
	}
	verifier := service.verifiers[provider]
	if verifier == nil {
		return IssuedSession{}, ErrInvalidCloudProof
	}
	principal, err := verifier.Verify(ctx, rawProof)
	now := service.now().UTC()
	if err != nil || principal.Identity.Provider != provider || principal.Validate(now) != nil {
		return IssuedSession{}, ErrInvalidCloudProof
	}
	membership, err := service.store.LookupCloudIdentityMembership(ctx, workspaceID, principal.Identity)
	if err != nil || membership.WorkspaceID != workspaceID || membership.Subject == "" || !membership.Role.Valid() {
		return IssuedSession{}, ErrInvalidCloudProof
	}
	if err := service.replayGuard.Consume(ctx, service.replayDigest(rawProof), principal.ExpiresAt); err != nil {
		return IssuedSession{}, ErrInvalidCloudProof
	}
	sithPrincipal, err := tenancy.NewPrincipal(
		membership.Subject,
		map[tenancy.WorkspaceID]tenancy.Role{workspaceID: membership.Role},
	)
	if err != nil {
		return IssuedSession{}, ErrInvalidCloudProof
	}
	session, err := service.issuer.Issue(ctx, sithPrincipal)
	if err != nil {
		return IssuedSession{}, fmt.Errorf("exchange cloud identity: issue session: %w", err)
	}
	return session, nil
}

func (service *CloudIdentityService) replayDigest(rawProof string) []byte {
	hash := hmac.New(sha256.New, service.replayPepper)
	_, _ = hash.Write([]byte(rawProof))
	return hash.Sum(nil)
}

// MemoryCloudReplayGuardConfig configures the bounded single-process replay adapter.
type MemoryCloudReplayGuardConfig struct {
	Capacity int
	Now      func() time.Time
}

// MemoryCloudReplayGuard stores only HMAC proof digests until their verified expiry.
type MemoryCloudReplayGuard struct {
	mu       sync.Mutex
	capacity int
	now      func() time.Time
	entries  map[string]time.Time
}

// NewMemoryCloudReplayGuard constructs the bounded local replay adapter.
func NewMemoryCloudReplayGuard(config MemoryCloudReplayGuardConfig) (*MemoryCloudReplayGuard, error) {
	if config.Capacity == 0 {
		config.Capacity = defaultCloudReplayEntries
	}
	if config.Capacity < 1 || config.Capacity > maximumCloudReplayEntries {
		return nil, fmt.Errorf("construct cloud replay guard: capacity must be between one and %d", maximumCloudReplayEntries)
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &MemoryCloudReplayGuard{capacity: config.Capacity, now: config.Now, entries: make(map[string]time.Time)}, nil
}

// Consume records a digest once and rejects a second use before its verified expiry.
func (guard *MemoryCloudReplayGuard) Consume(ctx context.Context, digest []byte, expiresAt time.Time) error {
	if guard == nil || guard.now == nil || ctx == nil || ctx.Err() != nil || len(digest) != sha256.Size ||
		!expiresAt.After(guard.now()) {
		return ErrCloudReplayDetected
	}
	guard.mu.Lock()
	defer guard.mu.Unlock()
	if guard.capacity < 1 || guard.entries == nil {
		return ErrCloudReplayDetected
	}
	now := guard.now().UTC()
	for key, expiry := range guard.entries {
		if !expiry.After(now) {
			delete(guard.entries, key)
		}
	}
	key := string(digest)
	if existing, found := guard.entries[key]; found && existing.After(now) {
		return ErrCloudReplayDetected
	}
	if len(guard.entries) >= guard.capacity {
		return ErrCloudReplayDetected
	}
	guard.entries[key] = expiresAt.UTC()
	return nil
}

func validateCloudIdentityValue(name, value string, maximum int) error {
	if value == "" || strings.TrimSpace(value) != value || len(value) > maximum {
		return fmt.Errorf("%s is invalid", name)
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return fmt.Errorf("%s is invalid", name)
		}
	}
	return nil
}
