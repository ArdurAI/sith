// SPDX-License-Identifier: Apache-2.0

package brain

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/ArdurAI/sith/internal/fleet"
)

const (
	argoGraphSource          = "argocd"
	argoGraphProtocolVersion = "1.0.0"
	maxArgoChangePayload     = 16 << 10
)

type argoChangePayload struct {
	ChangeKind       string    `json:"change_kind"`
	Revision         string    `json:"revision,omitempty"`
	Phase            string    `json:"phase,omitempty"`
	EventAt          time.Time `json:"event_at"`
	HistoryID        string    `json:"history_id,omitempty"`
	HistoryTruncated bool      `json:"history_truncated,omitempty"`
}

// FromGraphFacts converts reviewed graph facts into normalized brain observations while preserving
// caller-declared coverage. It performs no discovery, I/O, coverage inference, or mutation.
func FromGraphFacts(
	workspace string,
	facts []fleet.GraphFact,
	coverage map[fleet.Lens]LensCoverage,
) (Investigation, error) {
	if strings.TrimSpace(workspace) == "" {
		return Investigation{}, fmt.Errorf("project graph facts: workspace is required")
	}
	graph, err := fleet.NewGraph(workspace, facts)
	if err != nil {
		return Investigation{}, fmt.Errorf("project graph facts: %w", err)
	}

	input := Investigation{
		Workspace: workspace,
		Coverage:  make(map[fleet.Lens]LensCoverage, len(coverage)),
	}
	for lens, state := range coverage {
		if !lens.Valid() {
			return Investigation{}, fmt.Errorf("project graph facts: coverage lens %q is invalid", lens)
		}
		input.Coverage[lens] = state
	}
	for _, node := range graph.Nodes {
		for _, fact := range node.Facts {
			observation, present, err := observationFromGraphFact(fact)
			if err != nil {
				return Investigation{}, err
			}
			if present {
				input.Observations = append(input.Observations, observation)
			}
		}
	}
	for _, fact := range graph.Unattached {
		if _, _, err := observationFromGraphFact(fact); err != nil {
			return Investigation{}, err
		}
	}
	return input, nil
}

func observationFromGraphFact(fact fleet.GraphFact) (Observation, bool, error) {
	if fact.Fact.Kind != fleet.FactChange || fact.Lens != fleet.LensTimeline {
		return Observation{}, false, nil
	}
	provenance := fact.Fact.Provenance
	ref := fact.Fact.Ref
	if ref.SourceKind != argoGraphSource && provenance.Adapter != argoGraphSource {
		return Observation{}, false, nil
	}
	if ref.SourceKind != argoGraphSource || provenance.Adapter != argoGraphSource {
		return Observation{}, false, fmt.Errorf("project Argo change fact: source and provenance must both be %q", argoGraphSource)
	}
	if provenance.ProtocolV != argoGraphProtocolVersion {
		return Observation{}, false, fmt.Errorf("project Argo change fact: unsupported protocol version %q", provenance.ProtocolV)
	}
	if fact.Entity == nil {
		return Observation{}, false, fmt.Errorf("project Argo change fact: Application entity is required")
	}
	entity := *fact.Entity
	if ref.Kind != "Application" || ref.Namespace == "" ||
		entity.Cluster != ref.Scope || entity.Namespace != ref.Namespace ||
		entity.Kind != ref.Kind || entity.Name != ref.Name ||
		entity.Pod != "" || entity.Node != "" || entity.ImageDigest != "" {
		return Observation{}, false, fmt.Errorf("project Argo change fact: source and entity must identify the same Application")
	}
	if fact.Fact.Source != ref.Scope {
		return Observation{}, false, fmt.Errorf("project Argo change fact: evidence source must match the Application scope")
	}
	if len(ref.Attributes) != 0 || len(fact.Fact.Display) != 0 {
		return Observation{}, false, fmt.Errorf("project Argo change fact: unexpected attributes or display fields")
	}
	if len(fact.Fact.Observed) == 0 || len(fact.Fact.Observed) > maxArgoChangePayload {
		return Observation{}, false, fmt.Errorf("project Argo change fact: payload must be between 1 and %d bytes", maxArgoChangePayload)
	}

	payload, err := decodeArgoChangePayload(fact.Fact.Observed)
	if err != nil {
		return Observation{}, false, err
	}
	if payload.EventAt.IsZero() || !payload.EventAt.Equal(fact.Fact.ObservedAt) {
		return Observation{}, false, fmt.Errorf("project Argo change fact: payload event time must equal the fact observation time")
	}
	switch payload.ChangeKind {
	case "argocd-sync":
		switch payload.Phase {
		case "", "Succeeded", "Running", "Terminating":
			return Observation{}, false, nil
		default:
			return Observation{}, false, fmt.Errorf("project Argo change fact: phase %q is inconsistent with an Argo sync change", payload.Phase)
		}
	case "sync-failed":
		if payload.Phase != "Failed" && payload.Phase != "Error" {
			return Observation{}, false, fmt.Errorf("project Argo change fact: phase %q does not prove a failed sync operation", payload.Phase)
		}
		if payload.HistoryID != "" || payload.HistoryTruncated {
			return Observation{}, false, fmt.Errorf("project Argo change fact: failed operation must not carry history metadata")
		}
		nativePrefix := ref.Scope + "/" + ref.Namespace + "/" + ref.Name + "#operation/"
		operationID, found := strings.CutPrefix(provenance.NativeID, nativePrefix)
		if !found || strings.TrimSpace(operationID) == "" {
			return Observation{}, false, fmt.Errorf("project Argo change fact: operation provenance is inconsistent with the Application")
		}
	default:
		return Observation{}, false, fmt.Errorf("project Argo change fact: unsupported change kind %q", payload.ChangeKind)
	}

	return Observation{
		Ref:        ref,
		Lens:       fleet.LensTimeline,
		Key:        "change.kind",
		Value:      "sync-failed",
		ObservedAt: fact.Fact.ObservedAt,
		Source:     fact.Fact.Source,
		Stale:      fact.Fact.Stale,
	}, true, nil
}

func decodeArgoChangePayload(raw json.RawMessage) (argoChangePayload, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var payload argoChangePayload
	if err := decoder.Decode(&payload); err != nil {
		return argoChangePayload{}, fmt.Errorf("project Argo change fact: decode payload: %w", err)
	}
	var trailer json.RawMessage
	if err := decoder.Decode(&trailer); err != io.EOF {
		if err == nil {
			return argoChangePayload{}, fmt.Errorf("project Argo change fact: payload must contain one JSON value")
		}
		return argoChangePayload{}, fmt.Errorf("project Argo change fact: decode payload trailer: %w", err)
	}
	return payload, nil
}
