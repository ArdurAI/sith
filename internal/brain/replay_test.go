// SPDX-License-Identifier: Apache-2.0

package brain

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/ArdurAI/sith/internal/fleet"
)

const maxReplayFixtureBytes = 1 << 20

type replayFixture struct {
	Version       int               `json:"version"`
	Name          string            `json:"name"`
	Sanitized     bool              `json:"sanitized"`
	Investigation Investigation     `json:"investigation"`
	Expect        replayExpectation `json:"expect"`
}

type replayExpectation struct {
	TopRule          RuleID                    `json:"top_rule"`
	Status           Status                    `json:"status"`
	Scope            string                    `json:"scope"`
	FleetWide        bool                      `json:"fleet_wide"`
	Clusters         []string                  `json:"clusters"`
	CauseOf          []RuleID                  `json:"cause_of"`
	CitationEvidence []replayCitationEvidence  `json:"citation_evidence"`
	MissingLenses    []fleet.Lens              `json:"missing_lenses"`
	Advisory         replayAdvisoryExpectation `json:"advisory"`
}

type replayCitationEvidence struct {
	Lens      fleet.Lens `json:"lens"`
	Predicate string     `json:"predicate"`
	Observed  string     `json:"observed"`
}

type replayAdvisoryExpectation struct {
	Command   *bool `json:"command"`
	PRDiff    *bool `json:"pr_diff"`
	Sensitive *bool `json:"sensitive"`
}

func TestIncidentReplayFixtures(t *testing.T) {
	for _, path := range replayFixturePaths(t, "testdata/replays") {
		fixture := loadReplayFixture(t, path)
		t.Run(fixture.Name, func(t *testing.T) {
			result, err := Evaluate(fixture.Investigation)
			if err != nil {
				t.Fatalf("Evaluate() error = %v", err)
			}
			assertReplayExpectation(t, fixture, result)
		})
	}
}

func TestIncidentReplayFixturesAreDeterministic(t *testing.T) {
	for _, path := range replayFixturePaths(t, "testdata/replays") {
		fixture := loadReplayFixture(t, path)
		t.Run(fixture.Name, func(t *testing.T) {
			first, err := Evaluate(fixture.Investigation)
			if err != nil {
				t.Fatalf("first Evaluate() error = %v", err)
			}
			second, err := Evaluate(fixture.Investigation)
			if err != nil {
				t.Fatalf("second Evaluate() error = %v", err)
			}
			firstJSON, err := json.Marshal(first)
			if err != nil {
				t.Fatalf("marshal first result: %v", err)
			}
			secondJSON, err := json.Marshal(second)
			if err != nil {
				t.Fatalf("marshal second result: %v", err)
			}
			if !bytes.Equal(firstJSON, secondJSON) {
				t.Fatalf("replay output is not deterministic\nfirst:  %s\nsecond: %s", firstJSON, secondJSON)
			}
		})
	}
}

func TestIncidentReplayCorpusCoversRequiredSafetyCases(t *testing.T) {
	seenRules := make(map[RuleID]struct{})
	var hasCauseChain, hasFleetWide, hasNonFleet, hasUnconfirmed bool
	for _, path := range replayFixturePaths(t, "testdata/replays") {
		fixture := loadReplayFixture(t, path)
		seenRules[fixture.Expect.TopRule] = struct{}{}
		hasCauseChain = hasCauseChain || len(fixture.Expect.CauseOf) > 0
		hasFleetWide = hasFleetWide || fixture.Expect.FleetWide
		hasNonFleet = hasNonFleet || !fixture.Expect.FleetWide
		hasUnconfirmed = hasUnconfirmed || fixture.Expect.Status == StatusUnconfirmed
	}
	for _, ruleID := range []RuleID{RuleBadDeploy, RuleOOMKilled, RuleCrashLoop, RuleConfigDrift, RuleCertExpiry, RuleNodePressure, RuleImagePull, RuleArgoSyncFail} {
		if _, found := seenRules[ruleID]; !found {
			t.Fatalf("replay corpus does not cover %s", ruleID)
		}
	}
	if !hasCauseChain || !hasFleetWide || !hasNonFleet || !hasUnconfirmed {
		t.Fatalf("replay corpus lacks required safety cases: cause_chain=%t fleet_wide=%t non_fleet=%t unconfirmed=%t", hasCauseChain, hasFleetWide, hasNonFleet, hasUnconfirmed)
	}
}

func TestIncidentReplayFixturesRejectMalformedInput(t *testing.T) {
	for _, path := range replayFixturePaths(t, "testdata/replays-invalid") {
		path := path
		t.Run(filepath.Base(path), func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			if _, err := parseReplayFixture(data); err == nil {
				t.Fatal("parseReplayFixture() error = nil, want malformed fixture rejection")
			}
		})
	}
}

func replayFixturePaths(t *testing.T, directory string) []string {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("read replay fixtures: %v", err)
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		paths = append(paths, filepath.Join(directory, entry.Name()))
	}
	if len(paths) == 0 {
		t.Fatal("replay fixture corpus is empty")
	}
	sort.Strings(paths)
	return paths
}

func loadReplayFixture(t *testing.T, path string) replayFixture {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	fixture, err := parseReplayFixture(data)
	if err != nil {
		t.Fatalf("parse fixture %s: %v", path, err)
	}
	return fixture
}

func parseReplayFixture(data []byte) (replayFixture, error) {
	if len(data) > maxReplayFixtureBytes {
		return replayFixture{}, fmt.Errorf("replay fixture exceeds %d bytes", maxReplayFixtureBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var fixture replayFixture
	if err := decoder.Decode(&fixture); err != nil {
		return replayFixture{}, fmt.Errorf("decode replay fixture: %w", err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return replayFixture{}, fmt.Errorf("replay fixture must contain one JSON value")
		}
		return replayFixture{}, fmt.Errorf("decode replay fixture trailer: %w", err)
	}
	if err := fixture.Validate(); err != nil {
		return replayFixture{}, err
	}
	return fixture, nil
}

func (fixture replayFixture) Validate() error {
	if fixture.Version != 1 {
		return fmt.Errorf("unsupported replay fixture version %d", fixture.Version)
	}
	if fixture.Name == "" || strings.TrimSpace(fixture.Name) != fixture.Name {
		return fmt.Errorf("replay fixture name is required")
	}
	if !fixture.Sanitized {
		return fmt.Errorf("replay fixture must be marked sanitized")
	}
	if err := fixture.Investigation.Validate(); err != nil {
		return fmt.Errorf("invalid replay investigation: %w", err)
	}
	if err := validateReplayInvestigation(fixture.Investigation); err != nil {
		return err
	}
	if !isCanonicalRule(fixture.Expect.TopRule) {
		return fmt.Errorf("replay expectation has an unsupported top rule %q", fixture.Expect.TopRule)
	}
	switch fixture.Expect.Status {
	case StatusConfirmed, StatusDetected, StatusUnconfirmed:
	default:
		return fmt.Errorf("replay expectation has an unsupported status %q", fixture.Expect.Status)
	}
	if fixture.Expect.Scope == "" || strings.TrimSpace(fixture.Expect.Scope) != fixture.Expect.Scope {
		return fmt.Errorf("replay expectation scope is required")
	}
	if len(fixture.Expect.CitationEvidence) == 0 {
		return fmt.Errorf("replay expectation must name cited evidence")
	}
	if err := validateCitationEvidence(fixture.Expect.CitationEvidence); err != nil {
		return err
	}
	if err := validateRuleIDs(fixture.Expect.CauseOf); err != nil {
		return err
	}
	if err := validateLenses(fixture.Expect.MissingLenses); err != nil {
		return err
	}
	if fixture.Expect.Advisory.Command == nil || fixture.Expect.Advisory.PRDiff == nil || fixture.Expect.Advisory.Sensitive == nil {
		return fmt.Errorf("replay expectation must declare every advisory shape field")
	}
	if fixture.Expect.FleetWide {
		if fixture.Expect.Scope != "fleet" || len(fixture.Expect.Clusters) < 2 {
			return fmt.Errorf("fleet-wide replay expectation requires fleet scope and two clusters")
		}
		return validateStrings("cluster", fixture.Expect.Clusters)
	}
	if fixture.Expect.Scope == "fleet" || len(fixture.Expect.Clusters) != 0 {
		return fmt.Errorf("non-fleet replay expectation must not name fleet scope or clusters")
	}
	return nil
}

func validateReplayInvestigation(investigation Investigation) error {
	for lens := range investigation.Coverage {
		if !lens.Valid() {
			return fmt.Errorf("replay coverage lens %q is invalid", lens)
		}
	}
	for _, observation := range investigation.Observations {
		if !observation.Lens.Valid() {
			return fmt.Errorf("replay observation lens %q is invalid", observation.Lens)
		}
		if observation.ObservedAt.IsZero() {
			return fmt.Errorf("replay observation time is required")
		}
		if observation.Source == "" || strings.TrimSpace(observation.Source) != observation.Source {
			return fmt.Errorf("replay observation source is required")
		}
		if observation.Ref.SourceKind == "" || strings.TrimSpace(observation.Ref.SourceKind) != observation.Ref.SourceKind {
			return fmt.Errorf("replay observation source kind is required")
		}
	}
	return nil
}

func isCanonicalRule(id RuleID) bool {
	for _, candidate := range catalog {
		if candidate.id == id {
			return true
		}
	}
	return false
}

func validateStrings(label string, values []string) error {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value == "" || strings.TrimSpace(value) != value {
			return fmt.Errorf("replay %s is invalid", label)
		}
		if _, duplicate := seen[value]; duplicate {
			return fmt.Errorf("replay %s %q is duplicated", label, value)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func validateCitationEvidence(citations []replayCitationEvidence) error {
	for _, citation := range citations {
		if !citation.Lens.Valid() || citation.Predicate == "" || strings.TrimSpace(citation.Predicate) != citation.Predicate || citation.Observed == "" {
			return fmt.Errorf("replay citation evidence is invalid")
		}
	}
	return nil
}

func validateRuleIDs(ruleIDs []RuleID) error {
	seen := make(map[RuleID]struct{}, len(ruleIDs))
	for _, ruleID := range ruleIDs {
		if !isCanonicalRule(ruleID) {
			return fmt.Errorf("replay cause rule %q is invalid", ruleID)
		}
		if _, duplicate := seen[ruleID]; duplicate {
			return fmt.Errorf("replay cause rule %q is duplicated", ruleID)
		}
		seen[ruleID] = struct{}{}
	}
	return nil
}

func validateLenses(lenses []fleet.Lens) error {
	seen := make(map[fleet.Lens]struct{}, len(lenses))
	for _, lens := range lenses {
		if !lens.Valid() {
			return fmt.Errorf("replay missing lens %q is invalid", lens)
		}
		if _, duplicate := seen[lens]; duplicate {
			return fmt.Errorf("replay missing lens %q is duplicated", lens)
		}
		seen[lens] = struct{}{}
	}
	return nil
}

func assertReplayExpectation(t *testing.T, fixture replayFixture, result Result) {
	t.Helper()
	if result.Workspace != fixture.Investigation.Workspace {
		t.Fatalf("result workspace = %q, want %q", result.Workspace, fixture.Investigation.Workspace)
	}
	if len(result.Verdicts) == 0 {
		t.Fatal("result has no verdicts")
	}
	top := result.Verdicts[0]
	want := fixture.Expect
	if top.Rule != want.TopRule || top.Status != want.Status || top.Scope != want.Scope || top.FleetWide != want.FleetWide {
		t.Fatalf("top verdict = %#v, want rule=%s status=%s scope=%q fleet_wide=%t", top, want.TopRule, want.Status, want.Scope, want.FleetWide)
	}
	if !slices.Equal(top.Clusters, want.Clusters) {
		t.Fatalf("top clusters = %q, want %q", top.Clusters, want.Clusters)
	}
	if !slices.Equal(top.CauseOf, want.CauseOf) {
		t.Fatalf("top cause_of = %q, want %q", top.CauseOf, want.CauseOf)
	}
	if got := sortedCitationEvidence(top.Citations); !slices.Equal(got, sortedReplayCitationEvidence(want.CitationEvidence)) {
		t.Fatalf("top citation evidence = %#v, want %#v", got, want.CitationEvidence)
	}
	if got := sortedLenses(top.MissingLenses); !slices.Equal(got, sortedLenses(want.MissingLenses)) {
		t.Fatalf("top missing lenses = %q, want %q", got, want.MissingLenses)
	}
	if got, expected := top.Advisory.Command != "", *want.Advisory.Command; got != expected {
		t.Fatalf("advisory command present = %t, want %t", got, expected)
	}
	if got, expected := top.Advisory.PRDiff != "", *want.Advisory.PRDiff; got != expected {
		t.Fatalf("advisory PR diff present = %t, want %t", got, expected)
	}
	if top.Advisory.Sensitive != *want.Advisory.Sensitive {
		t.Fatalf("advisory sensitive = %t, want %t", top.Advisory.Sensitive, *want.Advisory.Sensitive)
	}
}

func sortedCitationEvidence(citations []Citation) []replayCitationEvidence {
	result := make([]replayCitationEvidence, 0, len(citations))
	for _, citation := range citations {
		result = append(result, replayCitationEvidence{Lens: citation.Lens, Predicate: citation.Predicate, Observed: citation.Observed})
	}
	return sortedReplayCitationEvidence(result)
}

func sortedLenses(lenses []fleet.Lens) []fleet.Lens {
	result := append([]fleet.Lens(nil), lenses...)
	sort.Slice(result, func(left, right int) bool { return result[left] < result[right] })
	return result
}

func sortedReplayCitationEvidence(citations []replayCitationEvidence) []replayCitationEvidence {
	result := append([]replayCitationEvidence(nil), citations...)
	sort.Slice(result, func(left, right int) bool {
		if result[left].Lens != result[right].Lens {
			return result[left].Lens < result[right].Lens
		}
		if result[left].Predicate != result[right].Predicate {
			return result[left].Predicate < result[right].Predicate
		}
		return result[left].Observed < result[right].Observed
	})
	return result
}
