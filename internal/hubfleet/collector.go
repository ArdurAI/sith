// SPDX-License-Identifier: Apache-2.0

// Package hubfleet collects bounded, tenant-scoped snapshots from registered OCM spokes.
package hubfleet

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/ArdurAI/sith/internal/fleet"
	"github.com/ArdurAI/sith/internal/tenancy"
)

const (
	// SourceKind is the fixed fleet-model source stamp for an OCM-brokered spoke.
	SourceKind = "ocm-spoke"

	protocolVersion     = "1.0.0"
	defaultSpokeTimeout = 5 * time.Second
	defaultSnapshotAge  = 5 * time.Minute
	maxSpokeTimeout     = 30 * time.Second
	maxSnapshotAge      = time.Hour
	maxSnapshotFacts    = 1_000
	maxObservedBytes    = 256 * 1024
	maxFutureSkew       = 30 * time.Second
)

var observedKeys = map[fleet.FactKind]map[string]struct{}{
	fleet.FactInventory: {
		"resource":           {},
		"status":             {},
		"replicas":           {},
		"available_replicas": {},
		"ready":              {},
		"generation":         {},
	},
	fleet.FactHealth: {"status": {}},
}

// FailureKind is the closed, non-sensitive status persisted for a failed collection attempt.
type FailureKind string

// Persisted collection failure kinds.
const (
	FailureTransport       FailureKind = "transport"
	FailureDeadline        FailureKind = "deadline"
	FailureInvalidSnapshot FailureKind = "invalid-snapshot"
)

// Spoke is registered tenancy metadata. It intentionally carries no endpoint, token, or kubeconfig.
type Spoke struct {
	ID                string            `json:"id"`
	ManagedClusterRef string            `json:"managed_cluster_ref"`
	Labels            map[string]string `json:"labels,omitempty"`
}

// Validate rejects ambiguous or unsafe registered-spoke metadata.
func (spoke Spoke) Validate() error {
	if err := validateText("spoke ID", spoke.ID, 256, false); err != nil {
		return err
	}
	if err := validateText("managed cluster reference", spoke.ManagedClusterRef, 1024, false); err != nil {
		return err
	}
	if len(spoke.Labels) > 64 {
		return fmt.Errorf("spoke labels exceed 64 entries")
	}
	for key, value := range spoke.Labels {
		if err := validateText("spoke label key", key, 256, false); err != nil {
			return err
		}
		if err := validateText("spoke label value", value, 256, true); err != nil {
			return err
		}
	}
	return nil
}

// Snapshot is one bounded normalized response from a registered spoke.
//
// Facts are restricted to inventory and health. The transport may acquire a projected credential
// internally, but neither it nor endpoint material crosses this package boundary.
type Snapshot struct {
	ObservedAt time.Time        `json:"observed_at"`
	Facts      []fleet.Evidence `json:"facts"`
}

// Transport obtains one snapshot through a previously registered spoke path.
type Transport interface {
	Snapshot(ctx context.Context, workspaceID tenancy.WorkspaceID, spoke Spoke) (Snapshot, error)
}

// Store owns registered-spoke lookup and tenant-scoped snapshot persistence.
type Store interface {
	RegisteredSpokes(ctx context.Context, scope tenancy.Scope) ([]Spoke, error)
	ReplaceSnapshot(ctx context.Context, scope tenancy.Scope, spoke Spoke, snapshot Snapshot, attemptedAt time.Time) error
	MarkSnapshotFailure(
		ctx context.Context,
		scope tenancy.Scope,
		spoke Spoke,
		failure FailureKind,
		attemptedAt time.Time,
	) (retainedSnapshot bool, err error)
}

// CollectorConfig defines bounded collection behavior.
type CollectorConfig struct {
	Store          Store
	Transport      Transport
	SpokeTimeout   time.Duration
	MaxSnapshotAge time.Duration
	Now            func() time.Time
}

// Collector collects independent, per-spoke snapshots without allowing one failure to suppress peers.
type Collector struct {
	store          Store
	transport      Transport
	spokeTimeout   time.Duration
	maxSnapshotAge time.Duration
	now            func() time.Time
}

// NewCollector constructs a fail-closed collector with bounded per-spoke work.
func NewCollector(config CollectorConfig) (*Collector, error) {
	if config.Store == nil || config.Transport == nil {
		return nil, fmt.Errorf("new spoke collector: store and transport are required")
	}
	if config.SpokeTimeout == 0 {
		config.SpokeTimeout = defaultSpokeTimeout
	}
	if config.SpokeTimeout < time.Second || config.SpokeTimeout > maxSpokeTimeout {
		return nil, fmt.Errorf("new spoke collector: timeout must be between 1s and %s", maxSpokeTimeout)
	}
	if config.MaxSnapshotAge == 0 {
		config.MaxSnapshotAge = defaultSnapshotAge
	}
	if config.MaxSnapshotAge < time.Second || config.MaxSnapshotAge > maxSnapshotAge {
		return nil, fmt.Errorf("new spoke collector: maximum snapshot age must be between 1s and %s", maxSnapshotAge)
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &Collector{
		store:          config.Store,
		transport:      config.Transport,
		spokeTimeout:   config.SpokeTimeout,
		maxSnapshotAge: config.MaxSnapshotAge,
		now:            config.Now,
	}, nil
}

// Collect refreshes every registered spoke independently and returns honest coverage.
// A transport or validation failure is recorded as a closed status and does not fail the peer loop.
func (collector *Collector) Collect(ctx context.Context, scope tenancy.Scope) (fleet.Coverage, error) {
	if collector == nil || collector.store == nil || collector.transport == nil || ctx == nil {
		return fleet.Coverage{}, fmt.Errorf("collect spoke snapshots: collector and context are required")
	}
	if err := scope.Authorize(tenancy.ActionRead); err != nil {
		return fleet.Coverage{}, fmt.Errorf("collect spoke snapshots: %w", err)
	}
	if tenancy.ValidateWorkspaceID(scope.WorkspaceID()) != nil {
		return fleet.Coverage{}, fmt.Errorf("collect spoke snapshots: validated workspace scope is required")
	}
	spokes, err := collector.store.RegisteredSpokes(ctx, scope)
	if err != nil {
		return fleet.Coverage{}, fmt.Errorf("collect spoke snapshots: list registered spokes: %w", err)
	}
	spokes = cloneSpokes(spokes)
	if err := validateSpokes(spokes); err != nil {
		return fleet.Coverage{}, fmt.Errorf("collect spoke snapshots: %w", err)
	}

	coverage := fleet.Coverage{Requested: len(spokes)}
	for _, spoke := range spokes {
		if err := ctx.Err(); err != nil {
			return coverage, fmt.Errorf("collect spoke snapshots: %w", err)
		}
		attemptedAt := collector.now().UTC()
		spokeContext, cancel := context.WithTimeout(ctx, collector.spokeTimeout)
		snapshot, collectionErr := collector.transport.Snapshot(spokeContext, scope.WorkspaceID(), cloneSpoke(spoke))
		deadlineErr := spokeContext.Err()
		cancel()
		if err := ctx.Err(); err != nil {
			return coverage, fmt.Errorf("collect spoke snapshots: %w", err)
		}
		if collectionErr == nil && deadlineErr != nil {
			collectionErr = deadlineErr
		}
		if collectionErr != nil {
			if err := collector.recordFailure(ctx, scope, spoke, failureFor(collectionErr), attemptedAt, &coverage); err != nil {
				return coverage, err
			}
			continue
		}
		if err := validateSnapshot(spoke, snapshot, attemptedAt, collector.maxSnapshotAge); err != nil {
			if failureErr := collector.recordFailure(ctx, scope, spoke, FailureInvalidSnapshot, attemptedAt, &coverage); failureErr != nil {
				return coverage, failureErr
			}
			continue
		}
		if err := collector.store.ReplaceSnapshot(ctx, scope, spoke, cloneSnapshot(snapshot), attemptedAt); err != nil {
			return coverage, fmt.Errorf("collect spoke snapshots: persist %q: %w", spoke.ID, err)
		}
		coverage.Reachable++
	}
	sort.Strings(coverage.Unreachable)
	sort.Strings(coverage.Stale)
	return coverage, nil
}

func (collector *Collector) recordFailure(
	ctx context.Context,
	scope tenancy.Scope,
	spoke Spoke,
	failure FailureKind,
	attemptedAt time.Time,
	coverage *fleet.Coverage,
) error {
	retained, err := collector.store.MarkSnapshotFailure(ctx, scope, spoke, failure, attemptedAt)
	if err != nil {
		return fmt.Errorf("collect spoke snapshots: persist %s failure: %w", spoke.ID, err)
	}
	coverage.Unreachable = append(coverage.Unreachable, spoke.ID)
	if retained {
		coverage.Stale = append(coverage.Stale, spoke.ID)
	}
	return nil
}

func failureFor(err error) FailureKind {
	if errors.Is(err, context.DeadlineExceeded) {
		return FailureDeadline
	}
	return FailureTransport
}

// ValidateSnapshot validates an externally obtained snapshot using Sith's standard freshness limit.
func ValidateSnapshot(spoke Spoke, snapshot Snapshot, now time.Time) error {
	return validateSnapshot(spoke, snapshot, now, defaultSnapshotAge)
}

func validateSpokes(spokes []Spoke) error {
	seen := make(map[string]struct{}, len(spokes))
	for index := range spokes {
		if err := spokes[index].Validate(); err != nil {
			return fmt.Errorf("invalid registered spoke at index %d: %w", index, err)
		}
		if _, exists := seen[spokes[index].ID]; exists {
			return fmt.Errorf("duplicate registered spoke %q", spokes[index].ID)
		}
		seen[spokes[index].ID] = struct{}{}
	}
	sort.Slice(spokes, func(left, right int) bool { return spokes[left].ID < spokes[right].ID })
	return nil
}

func validateSnapshot(spoke Spoke, snapshot Snapshot, now time.Time, maximumAge time.Duration) error {
	if err := spoke.Validate(); err != nil {
		return fmt.Errorf("invalid spoke: %w", err)
	}
	if snapshot.ObservedAt.IsZero() || snapshot.ObservedAt.After(now.Add(maxFutureSkew)) ||
		now.Sub(snapshot.ObservedAt) > maximumAge {
		return fmt.Errorf("snapshot observation time is outside the accepted freshness window")
	}
	if len(snapshot.Facts) > maxSnapshotFacts {
		return fmt.Errorf("snapshot contains more than %d facts", maxSnapshotFacts)
	}
	seenFacts := make(map[string]struct{}, len(snapshot.Facts))
	for index := range snapshot.Facts {
		if err := validateEvidence(spoke, snapshot.Facts[index], snapshot.ObservedAt, now, maximumAge); err != nil {
			return fmt.Errorf("invalid snapshot fact at index %d: %w", index, err)
		}
		factKey := string(snapshot.Facts[index].Kind) + "\x00" + snapshot.Facts[index].Ref.String()
		if _, exists := seenFacts[factKey]; exists {
			return fmt.Errorf("snapshot contains duplicate normalized fact %q", snapshot.Facts[index].Ref.String())
		}
		seenFacts[factKey] = struct{}{}
	}
	return nil
}

func validateEvidence(spoke Spoke, evidence fleet.Evidence, snapshotObservedAt, now time.Time, maximumAge time.Duration) error {
	if evidence.Kind != fleet.FactInventory && evidence.Kind != fleet.FactHealth {
		return fmt.Errorf("fact kind %q is not allowed for a spoke snapshot", evidence.Kind)
	}
	if evidence.Ref.SourceKind != SourceKind || evidence.Ref.Scope != spoke.ID || evidence.Source != spoke.ID {
		return fmt.Errorf("fact source does not match registered spoke")
	}
	if err := validateText("resource kind", evidence.Ref.Kind, 128, false); err != nil {
		return err
	}
	if strings.EqualFold(evidence.Ref.Kind, "secret") {
		return fmt.Errorf("secret inventory is not allowed in a spoke snapshot")
	}
	if err := validateText("resource name", evidence.Ref.Name, 256, false); err != nil {
		return err
	}
	if err := validateText("resource namespace", evidence.Ref.Namespace, 256, true); err != nil {
		return err
	}
	if len(evidence.Ref.Attributes) != 0 {
		return fmt.Errorf("resource attributes are not allowed in a spoke snapshot")
	}
	if evidence.ObservedAt.IsZero() || evidence.ObservedAt.After(snapshotObservedAt.Add(maxFutureSkew)) ||
		now.Sub(evidence.ObservedAt) > maximumAge {
		return fmt.Errorf("fact observation time is outside the accepted freshness window")
	}
	if evidence.Provenance.Adapter != SourceKind || evidence.Provenance.ProtocolV != protocolVersion ||
		evidence.Provenance.DeepLink != "" {
		return fmt.Errorf("fact provenance is not an allowed OCM spoke snapshot profile")
	}
	if evidence.Provenance.NativeID != "" || evidence.Provenance.Collector != "" {
		return fmt.Errorf("fact provenance contains unsupported raw metadata")
	}
	if err := validateObserved(evidence.Kind, evidence.Observed); err != nil {
		return err
	}
	return validateDisplay(evidence.Display)
}

func validateDisplay(display []fleet.DisplayField) error {
	if len(display) != 0 {
		return fmt.Errorf("display fields are not allowed in a normalized spoke snapshot")
	}
	return nil
}

func validateObserved(kind fleet.FactKind, observed json.RawMessage) error {
	if len(observed) == 0 || len(observed) > maxObservedBytes {
		return fmt.Errorf("observed payload must be between 1 and %d bytes", maxObservedBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(observed))
	first, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("observed payload is not valid JSON: %w", err)
	}
	if delimiter, ok := first.(json.Delim); !ok || delimiter != '{' {
		return fmt.Errorf("observed payload must be a JSON object")
	}
	if err := validateJSONObject(decoder, observedKeys[kind]); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("observed payload has trailing JSON tokens")
		}
		return fmt.Errorf("observed payload is not valid JSON: %w", err)
	}
	return nil
}

func validateJSONObject(decoder *json.Decoder, allowed map[string]struct{}) error {
	seen := make(map[string]struct{})
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return fmt.Errorf("read observed object key: %w", err)
		}
		key, ok := token.(string)
		if !ok {
			return fmt.Errorf("observed object key is not a string")
		}
		if _, exists := seen[key]; exists {
			return fmt.Errorf("observed payload contains duplicate key %q", key)
		}
		if isSensitiveKey(key) {
			return fmt.Errorf("observed payload contains disallowed key %q", key)
		}
		if allowed != nil {
			if _, exists := allowed[key]; !exists {
				return fmt.Errorf("observed payload key %q is not part of the normalized profile", key)
			}
		}
		seen[key] = struct{}{}
		if err := validateJSONValue(decoder); err != nil {
			return err
		}
	}
	end, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("close observed object: %w", err)
	}
	if delimiter, ok := end.(json.Delim); !ok || delimiter != '}' {
		return fmt.Errorf("observed payload object is malformed")
	}
	return nil
}

func validateJSONArray(decoder *json.Decoder) error {
	for decoder.More() {
		if err := validateJSONValue(decoder); err != nil {
			return err
		}
	}
	end, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("close observed array: %w", err)
	}
	if delimiter, ok := end.(json.Delim); !ok || delimiter != ']' {
		return fmt.Errorf("observed payload array is malformed")
	}
	return nil
}

func validateJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("read observed value: %w", err)
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		return validateJSONObject(decoder, nil)
	case '[':
		return validateJSONArray(decoder)
	default:
		return fmt.Errorf("observed payload has an unexpected delimiter")
	}
}

func isSensitiveKey(key string) bool {
	normalized := strings.NewReplacer("_", "", ".", "", "-", "", " ", "").Replace(strings.ToLower(strings.TrimSpace(key)))
	switch normalized {
	case "token", "accesstoken", "idtoken", "authorization", "kubeconfig", "server", "endpoint", "password",
		"credential", "credentials", "secret", "data", "stringdata", "binarydata", "clientkeydata", "clientcertificatedata":
		return true
	default:
		return false
	}
}

func validateText(name, value string, maximum int, allowEmpty bool) error {
	if value == "" && allowEmpty {
		return nil
	}
	if value == "" || strings.TrimSpace(value) != value || len(value) > maximum {
		return fmt.Errorf("%s must be a trimmed value of at most %d bytes", name, maximum)
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return fmt.Errorf("%s contains a control character", name)
		}
	}
	return nil
}

func cloneSpoke(spoke Spoke) Spoke {
	cloned := spoke
	if spoke.Labels != nil {
		cloned.Labels = make(map[string]string, len(spoke.Labels))
		for key, value := range spoke.Labels {
			cloned.Labels[key] = value
		}
	}
	return cloned
}

func cloneSpokes(spokes []Spoke) []Spoke {
	cloned := make([]Spoke, len(spokes))
	for index := range spokes {
		cloned[index] = cloneSpoke(spokes[index])
	}
	return cloned
}

func cloneSnapshot(snapshot Snapshot) Snapshot {
	cloned := Snapshot{ObservedAt: snapshot.ObservedAt, Facts: make([]fleet.Evidence, len(snapshot.Facts))}
	for index := range snapshot.Facts {
		cloned.Facts[index] = snapshot.Facts[index]
		cloned.Facts[index].Observed = append(json.RawMessage(nil), snapshot.Facts[index].Observed...)
		cloned.Facts[index].Display = append([]fleet.DisplayField(nil), snapshot.Facts[index].Display...)
		if snapshot.Facts[index].Ref.Attributes != nil {
			cloned.Facts[index].Ref.Attributes = make(map[string]string, len(snapshot.Facts[index].Ref.Attributes))
			for key, value := range snapshot.Facts[index].Ref.Attributes {
				cloned.Facts[index].Ref.Attributes[key] = value
			}
		}
	}
	return cloned
}
