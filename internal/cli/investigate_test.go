// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"bytes"
	"encoding/json"
	"reflect"
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

func TestInvestigationSurfaceRendersBoundedImagePullFailure(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	ref := fleet.ResourceRef{SourceKind: "fixture", Scope: "alpha", Kind: "Pod", Namespace: "prod", Name: "payments-0"}
	result, err := brain.Evaluate(brain.Investigation{
		Workspace: fleet.LocalWorkspace,
		Coverage:  map[fleet.Lens]brain.LensCoverage{fleet.LensLive: {Available: true}},
		Observations: []brain.Observation{{
			Ref: ref, Lens: fleet.LensLive, Key: "pod.reason", Value: "ErrImagePull", ObservedAt: now, Source: "fixture",
		}},
	})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}

	var textOutput bytes.Buffer
	textCommand := &cobra.Command{}
	textCommand.SetOut(&textOutput)
	if err := writeInvestigation(textCommand, "text", result); err != nil {
		t.Fatalf("writeInvestigation(text) error = %v", err)
	}
	for _, expected := range []string{
		"R7 image pull failure [confirmed]",
		"evidence: live pod.reason=ErrImagePull",
		"suggested: kubectl --context 'alpha' describe pod 'payments-0' -n 'prod'",
		"sensitive: human review required",
	} {
		if !strings.Contains(textOutput.String(), expected) {
			t.Errorf("text output = %q, want %q", textOutput.String(), expected)
		}
	}

	var jsonOutput bytes.Buffer
	jsonCommand := &cobra.Command{}
	jsonCommand.SetOut(&jsonOutput)
	if err := writeInvestigation(jsonCommand, "json", result); err != nil {
		t.Fatalf("writeInvestigation(json) error = %v", err)
	}
	var decoded brain.Result
	if err := json.Unmarshal(jsonOutput.Bytes(), &decoded); err != nil {
		t.Fatalf("unmarshal R7 JSON: %v", err)
	}
	if !reflect.DeepEqual(decoded, result) {
		t.Fatalf("decoded JSON = %#v, want %#v", decoded, result)
	}
}

func TestInvestigationSurfaceRendersBoundedArgoSyncFailure(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 18, 15, 45, 0, 0, time.UTC)
	ref := fleet.ResourceRef{SourceKind: "argocd", Scope: "alpha", Kind: "Application", Namespace: "argocd", Name: "payments"}
	result, err := brain.Evaluate(brain.Investigation{
		Workspace: fleet.LocalWorkspace,
		Coverage:  map[fleet.Lens]brain.LensCoverage{fleet.LensTimeline: {Available: true}},
		Observations: []brain.Observation{{
			Ref: ref, Lens: fleet.LensTimeline, Key: "change.kind", Value: "sync-failed", ObservedAt: now, Source: "argocd",
		}},
	})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}

	var textOutput bytes.Buffer
	textCommand := &cobra.Command{}
	textCommand.SetOut(&textOutput)
	if err := writeInvestigation(textCommand, "text", result); err != nil {
		t.Fatalf("writeInvestigation(text) error = %v", err)
	}
	for _, expected := range []string{
		"R8 Argo CD sync failure [confirmed]",
		"evidence: timeline change.kind=sync-failed",
		"suggested: kubectl --context 'alpha' describe application.argoproj.io 'payments' -n 'argocd'",
		"sensitive: human review required",
	} {
		if !strings.Contains(textOutput.String(), expected) {
			t.Errorf("text output = %q, want %q", textOutput.String(), expected)
		}
	}

	var jsonOutput bytes.Buffer
	jsonCommand := &cobra.Command{}
	jsonCommand.SetOut(&jsonOutput)
	if err := writeInvestigation(jsonCommand, "json", result); err != nil {
		t.Fatalf("writeInvestigation(json) error = %v", err)
	}
	var decoded brain.Result
	if err := json.Unmarshal(jsonOutput.Bytes(), &decoded); err != nil {
		t.Fatalf("unmarshal R8 JSON: %v", err)
	}
	if !reflect.DeepEqual(decoded, result) {
		t.Fatalf("decoded JSON = %#v, want %#v", decoded, result)
	}
	if strings.Contains(jsonOutput.String(), "operationState") || strings.Contains(jsonOutput.String(), "revision") {
		t.Fatalf("R8 JSON exposed discarded Argo payload fields: %s", jsonOutput.String())
	}
}
