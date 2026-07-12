// SPDX-License-Identifier: Apache-2.0

package hubauth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/ArdurAI/sith/internal/tenancy"
)

const (
	// #nosec G101 -- this is a public credential type prefix, not credential material.
	apiKeyPrefix          = "sith_api_v1"
	apiKeyIDBytes         = 16
	apiKeySecretBytes     = 32
	maxAPIKeyBytes        = 512
	minimumPepperBytes    = 32
	defaultAPIKeyLifetime = 90 * 24 * time.Hour
	maximumAPIKeyLifetime = 365 * 24 * time.Hour
	maximumRotateOverlap  = 24 * time.Hour
)

var (
	// ErrInvalidAPIKey deliberately hides lookup and credential-state details.
	ErrInvalidAPIKey = errors.New("invalid API key")
	// ErrAPIKeyNotFound lets stores report a miss without leaking it through Exchange.
	ErrAPIKeyNotFound = errors.New("API key not found")
)

// APIKeyRecord is the non-secret credential state persisted by an APIKeyStore.
type APIKeyRecord struct {
	WorkspaceID tenancy.WorkspaceID
	ID          string
	Subject     string
	Verifier    []byte
	CreatedAt   time.Time
	ExpiresAt   time.Time
	RetireAt    *time.Time
	RevokedAt   *time.Time
	ReplacedBy  string
}

// APIKeyMetadata is the non-sensitive lifecycle data returned alongside one-time plaintext issuance.
type APIKeyMetadata struct {
	WorkspaceID tenancy.WorkspaceID
	ID          string
	Subject     string
	CreatedAt   time.Time
	ExpiresAt   time.Time
}

// APIKeyStore is the narrow persistence boundary required by credential lifecycle operations.
type APIKeyStore interface {
	CreateAPIKey(context.Context, tenancy.Scope, APIKeyRecord) error
	LookupAPIKey(context.Context, tenancy.WorkspaceID, string) (APIKeyRecord, tenancy.Membership, error)
	RotateAPIKey(context.Context, tenancy.Scope, string, APIKeyRecord, time.Time) error
	RevokeAPIKey(context.Context, tenancy.Scope, string, time.Time) error
}

// APIKeyServiceConfig supplies cryptographic and persistence dependencies.
type APIKeyServiceConfig struct {
	Store   APIKeyStore
	Issuer  *SessionIssuer
	Pepper  []byte
	Now     func() time.Time
	Random  io.Reader
	KeyLife time.Duration
}

// APIKeyService issues opaque credentials and exchanges them for signed sessions.
type APIKeyService struct {
	store   APIKeyStore
	issuer  *SessionIssuer
	pepper  []byte
	now     func() time.Time
	random  io.Reader
	keyLife time.Duration
}

// NewAPIKeyService constructs a credential service without retaining caller-owned key material.
func NewAPIKeyService(config APIKeyServiceConfig) (*APIKeyService, error) {
	if config.Store == nil || config.Issuer == nil {
		return nil, fmt.Errorf("construct API key service: store and session issuer are required")
	}
	if len(config.Pepper) < minimumPepperBytes {
		return nil, fmt.Errorf("construct API key service: pepper must contain at least %d bytes", minimumPepperBytes)
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.Random == nil {
		config.Random = rand.Reader
	}
	if config.KeyLife == 0 {
		config.KeyLife = defaultAPIKeyLifetime
	}
	if config.KeyLife < time.Hour || config.KeyLife > maximumAPIKeyLifetime {
		return nil, fmt.Errorf("construct API key service: key lifetime must be between one hour and one year")
	}
	return &APIKeyService{
		store: config.Store, issuer: config.Issuer, pepper: append([]byte(nil), config.Pepper...),
		now: config.Now, random: config.Random, keyLife: config.KeyLife,
	}, nil
}

// Issue creates a high-entropy key for one existing member. The plaintext is returned exactly once.
func (service *APIKeyService) Issue(ctx context.Context, admin tenancy.Scope, subject string) (string, APIKeyMetadata, error) {
	if service == nil || ctx == nil || ctx.Err() != nil {
		return "", APIKeyMetadata{}, fmt.Errorf("issue API key: service and active context are required")
	}
	if err := admin.Authorize(tenancy.ActionManageWorkspace); err != nil {
		return "", APIKeyMetadata{}, fmt.Errorf("issue API key: %w", err)
	}
	raw, record, err := service.newKey(admin.WorkspaceID(), subject)
	if err != nil {
		return "", APIKeyMetadata{}, err
	}
	if err := service.store.CreateAPIKey(ctx, admin, record); err != nil {
		return "", APIKeyMetadata{}, fmt.Errorf("issue API key: persist verifier: %w", err)
	}
	return raw, apiKeyMetadata(record), nil
}

// Rotate creates a replacement and retires the old key after a bounded overlap.
func (service *APIKeyService) Rotate(ctx context.Context, admin tenancy.Scope, keyID, subject string, overlap time.Duration) (string, APIKeyMetadata, error) {
	if service == nil || ctx == nil || ctx.Err() != nil {
		return "", APIKeyMetadata{}, fmt.Errorf("rotate API key: service and active context are required")
	}
	if err := admin.Authorize(tenancy.ActionManageWorkspace); err != nil {
		return "", APIKeyMetadata{}, fmt.Errorf("rotate API key: %w", err)
	}
	if overlap < 0 || overlap > maximumRotateOverlap {
		return "", APIKeyMetadata{}, fmt.Errorf("rotate API key: overlap must be between zero and 24 hours")
	}
	raw, replacement, err := service.newKey(admin.WorkspaceID(), subject)
	if err != nil {
		return "", APIKeyMetadata{}, err
	}
	retireAt := service.now().UTC().Add(overlap)
	if err := service.store.RotateAPIKey(ctx, admin, keyID, replacement, retireAt); err != nil {
		return "", APIKeyMetadata{}, fmt.Errorf("rotate API key: persist replacement: %w", err)
	}
	return raw, apiKeyMetadata(replacement), nil
}

// Revoke permanently disables one key immediately.
func (service *APIKeyService) Revoke(ctx context.Context, admin tenancy.Scope, keyID string) error {
	if service == nil || ctx == nil || ctx.Err() != nil {
		return fmt.Errorf("revoke API key: service and active context are required")
	}
	if err := admin.Authorize(tenancy.ActionManageWorkspace); err != nil {
		return fmt.Errorf("revoke API key: %w", err)
	}
	if err := service.store.RevokeAPIKey(ctx, admin, keyID, service.now().UTC()); err != nil {
		return fmt.Errorf("revoke API key: persist revocation: %w", err)
	}
	return nil
}

// Exchange verifies an opaque key and resolves its current membership before issuing a session.
func (service *APIKeyService) Exchange(ctx context.Context, raw string) (IssuedSession, error) {
	if service == nil || ctx == nil || ctx.Err() != nil {
		return IssuedSession{}, ErrInvalidAPIKey
	}
	workspaceID, keyID, ok := parseAPIKey(raw)
	presentedVerifier := service.verifier(raw)
	if !ok {
		workspaceID, keyID = tenancy.WorkspaceID("invalid"), "invalid"
	}
	record, membership, lookupErr := service.store.LookupAPIKey(ctx, workspaceID, keyID)
	expected := make([]byte, sha256.Size)
	if lookupErr == nil && len(record.Verifier) == sha256.Size {
		copy(expected, record.Verifier)
	}
	verifierMatches := subtle.ConstantTimeCompare(expected, presentedVerifier) == 1
	now := service.now().UTC()
	active := lookupErr == nil && ok && verifierMatches && record.WorkspaceID == workspaceID && record.ID == keyID &&
		record.Subject == membership.Subject && membership.WorkspaceID == workspaceID && membership.Role.Valid() &&
		record.RevokedAt == nil && now.Before(record.ExpiresAt) && (record.RetireAt == nil || now.Before(*record.RetireAt))
	if !active {
		return IssuedSession{}, ErrInvalidAPIKey
	}
	principal, err := tenancy.NewPrincipal(record.Subject, map[tenancy.WorkspaceID]tenancy.Role{workspaceID: membership.Role})
	if err != nil {
		return IssuedSession{}, ErrInvalidAPIKey
	}
	session, err := service.issuer.Issue(ctx, principal)
	if err != nil {
		return IssuedSession{}, fmt.Errorf("exchange API key: issue session: %w", err)
	}
	return session, nil
}

func (service *APIKeyService) newKey(workspaceID tenancy.WorkspaceID, subject string) (string, APIKeyRecord, error) {
	membership := tenancy.Membership{WorkspaceID: workspaceID, Subject: subject, Role: tenancy.RoleReader}
	if err := membership.Validate(); err != nil {
		return "", APIKeyRecord{}, fmt.Errorf("issue API key: invalid identity: %w", err)
	}
	id := make([]byte, apiKeyIDBytes)
	secret := make([]byte, apiKeySecretBytes)
	if _, err := io.ReadFull(service.random, id); err != nil {
		return "", APIKeyRecord{}, fmt.Errorf("issue API key: generate identifier: %w", err)
	}
	if _, err := io.ReadFull(service.random, secret); err != nil {
		return "", APIKeyRecord{}, fmt.Errorf("issue API key: generate secret: %w", err)
	}
	encodedWorkspace := base64.RawURLEncoding.EncodeToString([]byte(workspaceID))
	encodedID := base64.RawURLEncoding.EncodeToString(id)
	raw := strings.Join([]string{apiKeyPrefix, encodedWorkspace, encodedID, base64.RawURLEncoding.EncodeToString(secret)}, ".")
	now := service.now().UTC()
	record := APIKeyRecord{
		WorkspaceID: workspaceID, ID: encodedID, Subject: subject, Verifier: service.verifier(raw),
		CreatedAt: now, ExpiresAt: now.Add(service.keyLife),
	}
	return raw, record, nil
}

func (service *APIKeyService) verifier(raw string) []byte {
	hash := hmac.New(sha256.New, service.pepper)
	_, _ = hash.Write([]byte(raw))
	return hash.Sum(nil)
}

func parseAPIKey(raw string) (tenancy.WorkspaceID, string, bool) {
	if raw == "" || len(raw) > maxAPIKeyBytes || strings.TrimSpace(raw) != raw {
		return "", "", false
	}
	parts := strings.Split(raw, ".")
	if len(parts) != 4 || parts[0] != apiKeyPrefix {
		return "", "", false
	}
	workspace, workspaceErr := base64.RawURLEncoding.DecodeString(parts[1])
	id, idErr := base64.RawURLEncoding.DecodeString(parts[2])
	secret, secretErr := base64.RawURLEncoding.DecodeString(parts[3])
	workspaceID := tenancy.WorkspaceID(workspace)
	if workspaceErr != nil || idErr != nil || secretErr != nil || len(id) != apiKeyIDBytes || len(secret) != apiKeySecretBytes ||
		base64.RawURLEncoding.EncodeToString(id) != parts[2] || base64.RawURLEncoding.EncodeToString(secret) != parts[3] ||
		tenancy.ValidateWorkspaceID(workspaceID) != nil {
		return "", "", false
	}
	return workspaceID, parts[2], true
}

func cloneAPIKeyRecord(record APIKeyRecord) APIKeyRecord {
	cloned := record
	cloned.Verifier = append([]byte(nil), record.Verifier...)
	if record.RetireAt != nil {
		value := *record.RetireAt
		cloned.RetireAt = &value
	}
	if record.RevokedAt != nil {
		value := *record.RevokedAt
		cloned.RevokedAt = &value
	}
	return cloned
}

func apiKeyMetadata(record APIKeyRecord) APIKeyMetadata {
	return APIKeyMetadata{
		WorkspaceID: record.WorkspaceID, ID: record.ID, Subject: record.Subject,
		CreatedAt: record.CreatedAt, ExpiresAt: record.ExpiresAt,
	}
}
