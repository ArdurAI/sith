// SPDX-License-Identifier: Apache-2.0

package keychain

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	platformkeyring "github.com/zalando/go-keyring"
)

func TestSystemStoreRoundTrip(t *testing.T) {
	t.Parallel()
	backend := newMemoryBackend()
	store := newSystemStore(backend)
	ctx := t.Context()
	secret := []byte("short-lived-local-token")
	if err := store.Put(ctx, "mcp/local-token", secret); err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	secret[0] = 'X'
	got, err := store.Get(ctx, "mcp/local-token")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if string(got) != "short-lived-local-token" {
		t.Fatalf("Get() = %q", got)
	}
	if err := store.Delete(ctx, "mcp/local-token"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := store.Get(ctx, "mcp/local-token"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get(deleted) error = %v", err)
	}
}

func TestSystemStoreFailsLoudWithoutPlaintextFallback(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv("XDG_DATA_HOME", root)
	backend := newMemoryBackend()
	backend.err = errors.New("backend unavailable and echoed super-secret-value")
	store := newSystemStore(backend)
	err := store.Put(t.Context(), "mcp/local-token", []byte("super-secret-value"))
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Put() error = %v", err)
	}
	if strings.Contains(err.Error(), "super-secret-value") {
		t.Fatalf("Put() error exposed secret: %v", err)
	}
	entries, readErr := os.ReadDir(root)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("fallback files = %v", entries)
	}
}

func TestSystemStoreClassifiesNotFound(t *testing.T) {
	t.Parallel()
	backend := newMemoryBackend()
	backend.err = platformkeyring.ErrNotFound
	store := newSystemStore(backend)
	if _, err := store.Get(t.Context(), "mcp/local-token"); !errors.Is(err, ErrNotFound) || errors.Is(err, ErrUnavailable) {
		t.Fatalf("Get() error = %v", err)
	}
}

func TestSystemStoreRejectsInvalidInputBeforeBackend(t *testing.T) {
	t.Parallel()
	backend := newMemoryBackend()
	store := newSystemStore(backend)
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	tests := []struct {
		name   string
		ctx    context.Context
		key    string
		secret []byte
	}{
		{name: "nil context", key: "valid", secret: []byte("value")},
		{name: "canceled context", ctx: canceled, key: "valid", secret: []byte("value")},
		{name: "empty key", ctx: t.Context(), secret: []byte("value")},
		{name: "unsupported key character", ctx: t.Context(), key: "token:prod", secret: []byte("value")},
		{name: "oversized key", ctx: t.Context(), key: strings.Repeat("a", maxKeyBytes+1), secret: []byte("value")},
		{name: "empty secret", ctx: t.Context(), key: "valid"},
		{name: "oversized secret", ctx: t.Context(), key: "valid", secret: make([]byte, maxSecretBytes+1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := store.Put(test.ctx, test.key, test.secret); err == nil {
				t.Fatal("Put() error = nil")
			}
		})
	}
	if backend.calls != 0 {
		t.Fatalf("backend calls = %d", backend.calls)
	}
}

func TestSystemStoreUsesFixedServiceNamespace(t *testing.T) {
	t.Parallel()
	backend := newMemoryBackend()
	store := newSystemStore(backend)
	if err := store.Put(t.Context(), "mcp/local-token", []byte("value")); err != nil {
		t.Fatal(err)
	}
	if backend.lastService != serviceName || backend.lastUser != "mcp/local-token" {
		t.Fatalf("service/user = %q/%q", backend.lastService, backend.lastUser)
	}
}

type memoryBackend struct {
	values      map[string]string
	err         error
	calls       int
	lastService string
	lastUser    string
}

func newMemoryBackend() *memoryBackend {
	return &memoryBackend{values: make(map[string]string)}
}

func (backend *memoryBackend) Set(service, user, password string) error {
	backend.record(service, user)
	if backend.err != nil {
		return backend.err
	}
	backend.values[service+"\x00"+user] = password
	return nil
}

func (backend *memoryBackend) Get(service, user string) (string, error) {
	backend.record(service, user)
	if backend.err != nil {
		return "", backend.err
	}
	value, exists := backend.values[service+"\x00"+user]
	if !exists {
		return "", platformkeyring.ErrNotFound
	}
	return value, nil
}

func (backend *memoryBackend) Delete(service, user string) error {
	backend.record(service, user)
	if backend.err != nil {
		return backend.err
	}
	key := service + "\x00" + user
	if _, exists := backend.values[key]; !exists {
		return platformkeyring.ErrNotFound
	}
	delete(backend.values, key)
	return nil
}

func (backend *memoryBackend) record(service, user string) {
	backend.calls++
	backend.lastService = service
	backend.lastUser = user
}
