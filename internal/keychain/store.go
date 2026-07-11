// SPDX-License-Identifier: Apache-2.0

package keychain

import (
	"context"
	"errors"
	"fmt"

	platformkeyring "github.com/zalando/go-keyring"
)

const (
	serviceName    = "io.ardur.sith"
	maxKeyBytes    = 128
	maxSecretBytes = 2048
)

var (
	// ErrNotFound reports that the named secret is absent from the OS keychain.
	ErrNotFound = errors.New("secret not found in OS keychain")
	// ErrUnavailable reports that the OS keychain could not complete an operation.
	ErrUnavailable = errors.New("OS keychain is unavailable")
)

// Store persists local Sith secrets without a plaintext filesystem fallback.
type Store interface {
	Put(ctx context.Context, key string, secret []byte) error
	Get(ctx context.Context, key string) ([]byte, error)
	Delete(ctx context.Context, key string) error
}

// SystemStore uses macOS Keychain, Windows Credential Manager, or freedesktop Secret Service.
type SystemStore struct {
	backend backend
}

// NewSystemStore constructs the production keychain store without performing keychain I/O.
func NewSystemStore() *SystemStore {
	return newSystemStore(systemBackend{})
}

func newSystemStore(backend backend) *SystemStore {
	return &SystemStore{backend: backend}
}

// Put creates or replaces one secret in the OS keychain.
func (store *SystemStore) Put(ctx context.Context, key string, secret []byte) error {
	if err := validateContext(ctx); err != nil {
		return err
	}
	if err := validateKey(key); err != nil {
		return err
	}
	if len(secret) == 0 || len(secret) > maxSecretBytes {
		return fmt.Errorf("store keychain secret %q: value must contain between 1 and %d bytes", key, maxSecretBytes)
	}
	if err := store.backend.Set(serviceName, key, string(secret)); err != nil {
		return classifyBackendError("store", key, err)
	}
	return nil
}

// Get retrieves a copy of one secret from the OS keychain.
func (store *SystemStore) Get(ctx context.Context, key string) ([]byte, error) {
	if err := validateContext(ctx); err != nil {
		return nil, err
	}
	if err := validateKey(key); err != nil {
		return nil, err
	}
	secret, err := store.backend.Get(serviceName, key)
	if err != nil {
		return nil, classifyBackendError("read", key, err)
	}
	if len(secret) == 0 || len(secret) > maxSecretBytes {
		return nil, fmt.Errorf(
			"read keychain secret %q: stored value is outside the supported size boundary: %w",
			key,
			ErrUnavailable,
		)
	}
	return []byte(secret), nil
}

// Delete removes one secret from the OS keychain.
func (store *SystemStore) Delete(ctx context.Context, key string) error {
	if err := validateContext(ctx); err != nil {
		return err
	}
	if err := validateKey(key); err != nil {
		return err
	}
	if err := store.backend.Delete(serviceName, key); err != nil {
		return classifyBackendError("delete", key, err)
	}
	return nil
}

func validateContext(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("keychain operation: context is nil")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("keychain operation: %w", err)
	}
	return nil
}

func validateKey(key string) error {
	if len(key) == 0 || len(key) > maxKeyBytes {
		return fmt.Errorf("keychain secret name must contain between 1 and %d bytes", maxKeyBytes)
	}
	for index := range len(key) {
		character := key[index]
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || character == '.' || character == '_' ||
			character == '/' || character == '-' {
			continue
		}
		return fmt.Errorf("keychain secret name %q contains an unsupported character", key)
	}
	return nil
}

func classifyBackendError(operation, key string, err error) error {
	if errors.Is(err, platformkeyring.ErrNotFound) {
		return fmt.Errorf("%s keychain secret %q: %w", operation, key, ErrNotFound)
	}
	return fmt.Errorf("%s keychain secret %q: %w", operation, key, ErrUnavailable)
}

type backend interface {
	Set(service, user, password string) error
	Get(service, user string) (string, error)
	Delete(service, user string) error
}

type systemBackend struct{}

func (systemBackend) Set(service, user, password string) error {
	return platformkeyring.Set(service, user, password)
}

func (systemBackend) Get(service, user string) (string, error) {
	return platformkeyring.Get(service, user)
}

func (systemBackend) Delete(service, user string) error {
	return platformkeyring.Delete(service, user)
}

var _ Store = (*SystemStore)(nil)
