// SPDX-License-Identifier: Apache-2.0

// Package brain provides deterministic, evidence-cited fleet investigations.
package brain

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ArdurAI/sith/internal/fleet"
)

// RuleID identifies one stable hypothesis rule.
type RuleID string

// Canonical rule identifiers.
const (
	RuleBadDeploy    RuleID = "R1"
	RuleOOMKilled    RuleID = "R2"
	RuleCrashLoop    RuleID = "R3"
	RuleConfigDrift  RuleID = "R4"
	RuleCertExpiry   RuleID = "R5"
	RuleNodePressure RuleID = "R6"
	RuleImagePull    RuleID = "R7"
	RuleArgoSyncFail RuleID = "R8"
)

// Status is the confidence state of a verdict.
type Status string

// Verdict confidence states.
const (
	StatusConfirmed   Status = "confirmed"
	StatusDetected    Status = "detected"
	StatusUnconfirmed Status = "unconfirmed"
)

// LensCoverage records whether a lens can safely support a verdict.
type LensCoverage struct {
	Available bool   `json:"available"`
	Stale     bool   `json:"stale"`
	Reason    string `json:"reason,omitempty"`
}

// Observation is one normalized, immutable signal consumed by the rule engine.
type Observation struct {
	Ref        fleet.ResourceRef `json:"ref"`
	Lens       fleet.Lens        `json:"lens"`
	Key        string            `json:"key"`
	Value      string            `json:"value"`
	ObservedAt time.Time         `json:"observed_at"`
	Source     string            `json:"source"`
	Stale      bool              `json:"stale"`
}

// Investigation is the complete deterministic input for one workspace.
type Investigation struct {
	Workspace    string                      `json:"workspace"`
	Observations []Observation               `json:"observations"`
	Coverage     map[fleet.Lens]LensCoverage `json:"coverage"`
}

// Citation explains the exact signal that contributed to a verdict.
type Citation struct {
	Ref        fleet.ResourceRef `json:"ref"`
	Lens       fleet.Lens        `json:"lens"`
	Predicate  string            `json:"predicate"`
	Observed   string            `json:"observed"`
	Weight     int               `json:"weight"`
	ObservedAt time.Time         `json:"observed_at"`
	Source     string            `json:"source"`
	Stale      bool              `json:"stale"`
}

// Advisory is output for a human to inspect and run; it is never dispatched.
type Advisory struct {
	Command   string `json:"command,omitempty"`
	PRDiff    string `json:"pr_diff,omitempty"`
	Sensitive bool   `json:"sensitive"`
}

// Verdict is one ranked and coverage-honest hypothesis.
type Verdict struct {
	Rule          RuleID            `json:"rule"`
	FailureMode   string            `json:"failure_mode"`
	Status        Status            `json:"status"`
	Hypothesis    string            `json:"hypothesis"`
	Scope         string            `json:"scope"`
	Ref           fleet.ResourceRef `json:"ref"`
	Score         int               `json:"score"`
	FleetWide     bool              `json:"fleet_wide"`
	Clusters      []string          `json:"clusters,omitempty"`
	CauseOf       []RuleID          `json:"cause_of,omitempty"`
	MissingLenses []fleet.Lens      `json:"missing_lenses,omitempty"`
	Citations     []Citation        `json:"citations"`
	Advisory      Advisory          `json:"advisory"`
}

// Result is a deterministic ranked investigation answer.
type Result struct {
	Workspace string    `json:"workspace"`
	Verdicts  []Verdict `json:"verdicts"`
}

// Validate rejects ambiguous or unsafe investigation inputs.
func (input Investigation) Validate() error {
	if strings.TrimSpace(input.Workspace) == "" {
		return fmt.Errorf("investigation workspace is required")
	}
	for index, observation := range input.Observations {
		if observation.Ref.Scope == "" || observation.Ref.Kind == "" || observation.Ref.Name == "" {
			return fmt.Errorf("observation %d has an incomplete resource reference", index)
		}
		if observation.Lens == "" || strings.TrimSpace(observation.Key) == "" || strings.TrimSpace(observation.Value) == "" {
			return fmt.Errorf("observation %d requires lens, key, and value", index)
		}
	}
	return nil
}

func sortVerdicts(verdicts []Verdict) {
	sort.SliceStable(verdicts, func(left, right int) bool {
		if verdicts[left].FleetWide != verdicts[right].FleetWide {
			return verdicts[left].FleetWide
		}
		if verdicts[left].Score != verdicts[right].Score {
			return verdicts[left].Score > verdicts[right].Score
		}
		if verdicts[left].Rule != verdicts[right].Rule {
			return verdicts[left].Rule < verdicts[right].Rule
		}
		if verdicts[left].Scope != verdicts[right].Scope {
			return verdicts[left].Scope < verdicts[right].Scope
		}
		if verdicts[left].Hypothesis != verdicts[right].Hypothesis {
			return verdicts[left].Hypothesis < verdicts[right].Hypothesis
		}
		return verdicts[left].Ref.String() < verdicts[right].Ref.String()
	})
}
