// SPDX-License-Identifier: Apache-2.0

// Package tracing provides a local, privacy-preserving trace context for governed hub work.
// It deliberately provides no wire propagation, listener, exporter, persistence, or telemetry
// SDK integration. Those boundaries require a separate review once a typed action protocol exists.
package tracing

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

const idBytes = 16

type contextKey struct{}

// ID is one opaque, locally minted trace correlation identifier. It is not an intent identifier:
// the future E4/E6 typed-intent schema owns that binding.
type ID string

// Valid reports whether an identifier has the fixed lower-case hexadecimal representation minted
// by NewID. Restricting the vocabulary prevents trace records from accepting caller text.
func (id ID) Valid() bool {
	if len(id) != idBytes*2 {
		return false
	}
	decoded := make([]byte, idBytes)
	if _, err := hex.Decode(decoded, []byte(id)); err != nil {
		return false
	}
	return string(id) == hex.EncodeToString(decoded)
}

// NewID mints an opaque identifier from the operating system's cryptographic random source.
func NewID() (ID, error) {
	raw := make([]byte, idBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("mint trace identifier: %w", err)
	}
	return ID(hex.EncodeToString(raw)), nil
}

// FromContext returns the locally minted trace identifier, if one is present and valid.
func FromContext(ctx context.Context) (ID, bool) {
	if ctx == nil {
		return "", false
	}
	id, ok := ctx.Value(contextKey{}).(ID)
	return id, ok && id.Valid()
}

// Ensure returns a context with one locally minted trace identifier. It preserves an existing
// valid identifier and never reads request metadata, so hostile correlation headers are ignored.
func Ensure(ctx context.Context) (context.Context, ID, error) {
	if ctx == nil {
		return nil, "", fmt.Errorf("establish trace context: context is required")
	}
	if id, ok := FromContext(ctx); ok {
		return ctx, id, nil
	}
	id, err := NewID()
	if err != nil {
		return nil, "", err
	}
	return context.WithValue(ctx, contextKey{}, id), id, nil
}
