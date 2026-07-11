// SPDX-License-Identifier: Apache-2.0

// Package localops defines direct, single-context Kubernetes conveniences that use the local
// user's kubeconfig identity. These interfaces are intentionally separate from governed intents,
// plans, policy enforcement, and the connector action vocabulary.
package localops

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/ArdurAI/sith/internal/fleet"
)

// ErrInvalidTarget reports an incomplete or ambiguous local Kubernetes target.
var ErrInvalidTarget = errors.New("local operation target is invalid")

// Target is one resource in one explicitly named local kubeconfig context.
type Target struct {
	Context   string `json:"context"`
	Namespace string `json:"namespace,omitempty"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
}

// Validate rejects implicit contexts and incomplete resource identities.
func (target Target) Validate() error {
	if strings.TrimSpace(target.Context) == "" {
		return fmt.Errorf("%w: context is required", ErrInvalidTarget)
	}
	if strings.TrimSpace(target.Kind) == "" || strings.TrimSpace(target.Name) == "" {
		return fmt.Errorf("%w: kind and name are required", ErrInvalidTarget)
	}
	return nil
}

// ResourceRef converts the local target into the source-abstract evidence address.
func (target Target) ResourceRef(sourceKind string) fleet.ResourceRef {
	return fleet.ResourceRef{
		SourceKind: sourceKind,
		Scope:      target.Context,
		Kind:       target.Kind,
		Namespace:  target.Namespace,
		Name:       target.Name,
	}
}

// ObjectView is one raw read rendered as safe YAML.
type ObjectView struct {
	Evidence fleet.Evidence `json:"evidence"`
	YAML     []byte         `json:"-"`
}

// Description composes one object read with its related Kubernetes events.
type Description struct {
	Object ObjectView       `json:"object"`
	Events []fleet.Evidence `json:"events"`
}

// LogOptions selects one bounded or streaming pod log request.
type LogOptions struct {
	Container  string
	Follow     bool
	Previous   bool
	Timestamps bool
	TailLines  *int64
	Since      time.Duration
}

// TerminalSize is one remote TTY resize observation.
type TerminalSize struct {
	Width  uint16
	Height uint16
}

// TerminalSizeQueue provides resize events to an interactive remote command.
type TerminalSizeQueue interface {
	Next() *TerminalSize
}

// ExecOptions selects one command in one pod/container.
type ExecOptions struct {
	Container string
	Command   []string
	Stdin     bool
	TTY       bool
}

// Streams carries the caller-owned terminal streams for a remote command.
type Streams struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	Sizes  TerminalSizeQueue
}

// ForwardRequest selects one pod/service tunnel. Addresses must remain loopback-only.
type ForwardRequest struct {
	Target    Target
	Addresses []string
	Ports     []string
	Out       io.Writer
	ErrOut    io.Writer
}

// ForwardedPort reports one established local-to-remote port mapping.
type ForwardedPort struct {
	Local  uint16 `json:"local"`
	Remote uint16 `json:"remote"`
}

// ForwardSession owns one live port-forward and its cancellation boundary.
type ForwardSession interface {
	Ready() <-chan struct{}
	Done() <-chan error
	Ports() ([]ForwardedPort, error)
	Close() error
}

// ApplyPreview is the server-validated dry-run shown before a local edit is persisted.
type ApplyPreview struct {
	CurrentYAML []byte
	DryRunYAML  []byte
}

// Reader provides local read-side detail without any governed action path.
type Reader interface {
	View(ctx context.Context, target Target, revealSecrets bool) (ObjectView, error)
	Describe(ctx context.Context, target Target) (Description, error)
	Logs(ctx context.Context, target Target, options LogOptions) (io.ReadCloser, error)
}

// Streamer provides local interactive sessions using the user's own identity.
type Streamer interface {
	Exec(ctx context.Context, target Target, options ExecOptions, streams Streams) error
	PortForward(ctx context.Context, request ForwardRequest) (ForwardSession, error)
}

// Applier provides the deliberately separate local YAML edit boundary.
type Applier interface {
	PreviewApply(ctx context.Context, target Target, manifest []byte) (ApplyPreview, error)
	Apply(ctx context.Context, target Target, manifest []byte) (fleet.Evidence, error)
}

// Client is the complete local per-resource operation surface.
type Client interface {
	Reader
	Streamer
	Applier
}
