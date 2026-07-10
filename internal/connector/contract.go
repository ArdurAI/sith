// SPDX-License-Identifier: Apache-2.0

// Package connector defines Sith's capability-scoped source-adapter contract.
package connector

import (
	"context"
	"encoding/json"
	"time"

	"github.com/ArdurAI/sith/internal/fleet"
)

// Connector identifies one canonical integration and its declared capabilities.
type Connector interface {
	Kind() string
	Capabilities() []Capability
	Descriptor() Descriptor
}

// Descriptor is static registry, taxonomy, ownership, and version metadata.
type Descriptor struct {
	Kind         string        `json:"kind"`
	ConnKind     ConnectorKind `json:"connector_kind"`
	ProtocolV    string        `json:"protocol_version"`
	Owner        string        `json:"owner"`
	Capabilities []Capability  `json:"capabilities"`
	Verbs        []string      `json:"verbs,omitempty"`
}

// ConnectorKind is the closed integration taxonomy.
//
//nolint:revive // ConnectorKind is the locked cross-connector contract name from issue #38.
type ConnectorKind string

// Supported connector kinds.
const (
	KindReadAdapter  ConnectorKind = "read-adapter"
	KindBrokeredRead ConnectorKind = "brokered-read-through"
	KindTypedAction  ConnectorKind = "typed-action"
)

// Valid reports whether the connector kind belongs to the closed taxonomy.
func (kind ConnectorKind) Valid() bool {
	switch kind {
	case KindReadAdapter, KindBrokeredRead, KindTypedAction:
		return true
	default:
		return false
	}
}

// Capability names one verb a connector explicitly opts into.
type Capability string

// Supported connector capabilities.
const (
	CapDiscover Capability = "discover"
	CapRead     Capability = "read"
	CapQuery    Capability = "query"
	CapDiff     Capability = "diff"
	CapPlan     Capability = "plan"
	CapExecute  Capability = "execute"
	CapVerify   Capability = "verify"
)

// Valid reports whether the capability belongs to the seven-verb contract.
func (capability Capability) Valid() bool {
	switch capability {
	case CapDiscover, CapRead, CapQuery, CapDiff, CapPlan, CapExecute, CapVerify:
		return true
	default:
		return false
	}
}

// Reader implements the discover, read, and query half of the connector contract.
type Reader interface {
	Connector
	Discover(ctx context.Context) (Discovery, error)
	Read(ctx context.Context, ref fleet.ResourceRef) (fleet.Evidence, error)
	Query(ctx context.Context, query fleet.Query) (fleet.QueryResult, error)
}

// Discovery describes the scopes a reader can currently address.
type Discovery struct {
	Scopes      []Scope  `json:"scopes"`
	Unreachable []string `json:"unreachable,omitempty"`
}

// Scope is one cluster, context, or spoke exposed by a reader.
type Scope struct {
	Name       string    `json:"name"`
	Kinds      []string  `json:"kinds"`
	Reachable  bool      `json:"reachable"`
	ObservedAt time.Time `json:"observed_at,omitempty"`
}

// Differ computes desired-versus-observed state without mutation.
type Differ interface {
	Connector
	Diff(ctx context.Context, request DiffRequest) (fleet.Diff, error)
}

// Planner converts a validated typed intent into an inspectable dry-run plan.
type Planner interface {
	Connector
	Plan(ctx context.Context, intent Intent) (ActionPlan, error)
}

// Executor applies a previously approved action plan through the governed path.
type Executor interface {
	Connector
	Execute(ctx context.Context, plan ActionPlan) (ExecutionResult, error)
}

// Verifier checks post-conditions after an execution.
type Verifier interface {
	Connector
	Verify(ctx context.Context, request VerifyRequest) (Verification, error)
}

// Intent is a validated, signed request from the closed action vocabulary.
type Intent struct {
	ID            string              `json:"id"`
	Workspace     string              `json:"workspace"`
	Actor         string              `json:"actor"`
	Verb          string              `json:"verb"`
	Target        fleet.ResourceRef   `json:"target"`
	Args          json.RawMessage     `json:"args"`
	Justification string              `json:"justification"`
	EvidenceRefs  []fleet.ResourceRef `json:"evidence_refs,omitempty"`
	Signature     string              `json:"signature"`
}

// ActionPlan is the non-mutating, inspectable result of planning an intent.
type ActionPlan struct {
	IntentID   string            `json:"intent_id"`
	Verb       string            `json:"verb"`
	Target     fleet.ResourceRef `json:"target"`
	Diff       fleet.Diff        `json:"diff"`
	Steps      []PlanStep        `json:"steps"`
	Reversible bool              `json:"reversible"`
	Warnings   []string          `json:"warnings,omitempty"`
}

// PlanStep is one ordered typed API call; it is never a shell command.
type PlanStep struct {
	Description string          `json:"description"`
	API         string          `json:"api"`
	Params      json.RawMessage `json:"params"`
}

// ExecutionResult records the observed outcome of an approved plan.
type ExecutionResult struct {
	IntentID   string         `json:"intent_id"`
	Applied    bool           `json:"applied"`
	StepsDone  int            `json:"steps_done"`
	Observed   fleet.Evidence `json:"observed"`
	StartedAt  time.Time      `json:"started_at"`
	FinishedAt time.Time      `json:"finished_at"`
	Err        string         `json:"err,omitempty"`
}

// VerifyRequest is a typed post-condition assertion.
type VerifyRequest struct {
	IntentID string            `json:"intent_id"`
	Target   fleet.ResourceRef `json:"target"`
	Expect   fleet.Selector    `json:"expect"`
}

// Verification is the observed verdict for a post-condition.
type Verification struct {
	Satisfied bool           `json:"satisfied"`
	Observed  fleet.Evidence `json:"observed"`
	Detail    string         `json:"detail,omitempty"`
	CheckedAt time.Time      `json:"checked_at"`
}

// DiffRequest asks a connector to compare desired and observed state.
type DiffRequest struct {
	Target  fleet.ResourceRef `json:"target"`
	Desired json.RawMessage   `json:"desired,omitempty"`
}

// ValidVerb reports whether a verb belongs to the reviewed initial action vocabulary.
func ValidVerb(verb string) bool {
	switch verb {
	case "argocd.sync", "argocd.rollback",
		"rollout.promote", "rollout.abort",
		"deployment.scale", "deployment.restart",
		"gitops.open-pr":
		return true
	default:
		return false
	}
}
