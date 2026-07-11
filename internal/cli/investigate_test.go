// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/ArdurAI/sith/internal/brain"
	"github.com/ArdurAI/sith/internal/fleet"
)

func TestInvestigateRendersCitedAbstainingAdvisory(t *testing.T) {
	stdout, stderr, exitCode := runCLIWithReader(t, []string{"investigate"}, &cacheReader{})
	if exitCode != 0 || stderr != "" {
		t.Fatalf("exit/stderr = %d/%q", exitCode, stderr)
	}
	for _, expected := range []string{"R3 CrashLoopBackOff [detected]", "coverage gap: telemetry", "evidence: live pod.failure=CrashLoopBackOff", "suggested: kubectl --context 'alpha' logs 'payments-0'"} {
		if !strings.Contains(stdout, expected) {
			t.Errorf("stdout = %q, want %q", stdout, expected)
		}
	}
}

func TestInvestigateJSONUsesStableVerdictSchema(t *testing.T) {
	stdout, stderr, exitCode := runCLIWithReader(t, []string{"investigate", "payments", "--context", "alpha", "-o", "json"}, &cacheReader{})
	if exitCode != 0 || stderr != "" {
		t.Fatalf("exit/stderr = %d/%q", exitCode, stderr)
	}
	var result brain.Result
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if len(result.Verdicts) != 1 || result.Verdicts[0].Rule != brain.RuleCrashLoop || result.Verdicts[0].Status != brain.StatusDetected {
		t.Fatalf("result = %#v", result)
	}
}

func TestInvestigateUnknownContextFailsClosed(t *testing.T) {
	stdout, stderr, exitCode := runCLIWithReader(t, []string{"investigate", "--context", "missing", "-o", "json"}, &cacheReader{})
	if exitCode == 0 || !strings.Contains(stderr, "reached 0/1 contexts") {
		t.Fatalf("exit/stdout/stderr = %d/%q/%q", exitCode, stdout, stderr)
	}
}

func TestInvestigationSurfaceRendersR1ThroughR4Fixtures(t *testing.T) {
	now := time.Now().UTC()
	ref := fleet.ResourceRef{SourceKind: "fixture", Scope: "alpha", Kind: "Deployment", Namespace: "prod", Name: "payments"}
	observation := func(lens fleet.Lens, key, value string) brain.Observation {
		return brain.Observation{Ref: ref, Lens: lens, Key: key, Value: value, ObservedAt: now, Source: "fixture"}
	}
	tests := []struct {
		name         string
		rule         brain.RuleID
		observations []brain.Observation
	}{
		{"R1", brain.RuleBadDeploy, []brain.Observation{observation(fleet.LensLive, "workload.status", "Degraded"), observation(fleet.LensTimeline, "change.kind", "deploy"), observation(fleet.LensDesired, "desired.changed", "true"), observation(fleet.LensTelemetry, "error.rate", "increased")}},
		{"R2", brain.RuleOOMKilled, []brain.Observation{observation(fleet.LensLive, "pod.reason", "OOMKilled"), observation(fleet.LensTelemetry, "memory.variant", "limit")}},
		{"R3", brain.RuleCrashLoop, []brain.Observation{observation(fleet.LensLive, "pod.failure", "repeated-error"), observation(fleet.LensTelemetry, "logs.cause", "panic")}},
		{"R4", brain.RuleConfigDrift, []brain.Observation{observation(fleet.LensLive, "workload.status", "Healthy"), observation(fleet.LensDesired, "desired.drift", "true"), observation(fleet.LensTimeline, "change.kind", "kubectl-edit")}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			coverage := map[fleet.Lens]brain.LensCoverage{}
			for _, item := range test.observations {
				coverage[item.Lens] = brain.LensCoverage{Available: true}
			}
			result, err := brain.Evaluate(brain.Investigation{Workspace: fleet.LocalWorkspace, Observations: test.observations, Coverage: coverage})
			if err != nil {
				t.Fatalf("Evaluate() error = %v", err)
			}
			var output bytes.Buffer
			command := &cobra.Command{}
			command.SetOut(&output)
			if err := writeInvestigation(command, "text", result); err != nil {
				t.Fatalf("writeInvestigation() error = %v", err)
			}
			if len(result.Verdicts) == 0 || result.Verdicts[0].Rule != test.rule || !strings.Contains(output.String(), string(test.rule)) || !strings.Contains(output.String(), "evidence:") || !strings.Contains(output.String(), "suggested:") {
				t.Fatalf("result/output = %#v/%q", result, output.String())
			}
		})
	}
}
