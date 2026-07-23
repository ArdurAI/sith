// SPDX-License-Identifier: Apache-2.0

package prometheus

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ArdurAI/sith/internal/fleet"
)

func TestProjectAlertsEmitsSanitizedTelemetryFacts(t *testing.T) {
	t.Parallel()
	input := Projection{
		Workspace: "workspace-a", Scope: "cluster-a",
		ObservedAt: time.Date(2026, 7, 16, 23, 0, 0, 0, time.FixedZone("CDT", -5*60*60)),
		Response: alertsResponse(t,
			sourceAlert{
				ActiveAt: "2026-07-16T20:00:00-05:00", State: "firing", Value: "1e+01",
				Labels: map[string]string{
					"alertname": "DeploymentUnavailable", "namespace": "payments", "deployment": "api",
					"severity": "critical", "cluster": "attacker-cluster", "secret_label": "label-secret",
				},
				Annotations: map[string]string{"summary": "annotation-secret"},
			},
			sourceAlert{
				ActiveAt: "2026-07-16T21:00:00Z", State: "pending", Value: "2",
				Labels: map[string]string{"alertname": "NodePressure", "node": "worker-a"},
			},
			sourceAlert{
				ActiveAt: "2026-07-16T22:00:00Z", State: "firing", Value: "3",
				Labels: map[string]string{"alertname": "ExternalDependencyDown", "service": "payments-db"},
			},
		),
	}
	facts, err := ProjectAlerts(input)
	if err != nil {
		t.Fatalf("ProjectAlerts() error = %v", err)
	}
	if len(facts) != 3 {
		t.Fatalf("fact count = %d, want 3", len(facts))
	}

	byName := make(map[string]fleet.GraphFact, len(facts))
	for _, fact := range facts {
		var observation alertObservation
		if err := json.Unmarshal(fact.Fact.Observed, &observation); err != nil {
			t.Fatalf("decode observation: %v", err)
		}
		byName[observation.AlertName] = fact
		if fact.Fact.Kind != fleet.FactAlert || fact.Lens != fleet.LensTelemetry ||
			fact.Fact.Ref.SourceKind != Kind || fact.Fact.Ref.Scope != "cluster-a" ||
			fact.Fact.Provenance.Adapter != Kind || fact.Fact.Provenance.ProtocolV != ProtocolVersion ||
			!strings.HasPrefix(fact.Fact.Provenance.NativeID, "sha256:") ||
			fact.Fact.ObservedAt != input.ObservedAt.UTC() {
			t.Fatalf("unexpected fact contract: %#v", fact)
		}
	}

	deployment := byName["DeploymentUnavailable"]
	if deployment.Entity == nil || *deployment.Entity != (fleet.EntityRef{
		Cluster: "cluster-a", Namespace: "payments", Kind: "Deployment", Name: "api",
	}) || deployment.Fact.Ref.Namespace != "payments" {
		t.Fatalf("deployment fact = %#v", deployment)
	}
	var deploymentObservation alertObservation
	if err := json.Unmarshal(deployment.Fact.Observed, &deploymentObservation); err != nil {
		t.Fatalf("decode deployment observation: %v", err)
	}
	if deploymentObservation.Value != "10" || !deploymentObservation.ActiveAt.Equal(time.Date(2026, 7, 17, 1, 0, 0, 0, time.UTC)) ||
		deploymentObservation.Labels["severity"] != "critical" || deploymentObservation.Labels["namespace"] != "payments" {
		t.Fatalf("deployment observation = %#v", deploymentObservation)
	}
	encoded, err := json.Marshal(facts)
	if err != nil {
		t.Fatalf("marshal facts: %v", err)
	}
	for _, forbidden := range []string{"annotation-secret", "label-secret", "attacker-cluster", "annotations", "secret_label", `\"cluster\"`} {
		if bytes.Contains(encoded, []byte(forbidden)) {
			t.Fatalf("projected facts retained forbidden value %q: %s", forbidden, encoded)
		}
	}

	node := byName["NodePressure"]
	if node.Entity == nil || *node.Entity != (fleet.EntityRef{Cluster: "cluster-a", Node: "worker-a"}) {
		t.Fatalf("node fact = %#v", node)
	}
	if byName["ExternalDependencyDown"].Entity != nil {
		t.Fatalf("unresolvable alert was attached: %#v", byName["ExternalDependencyDown"])
	}
	graph, err := fleet.NewGraph(input.Workspace, facts)
	if err != nil {
		t.Fatalf("NewGraph() error = %v", err)
	}
	if len(graph.Nodes) != 2 || len(graph.Unattached) != 1 {
		t.Fatalf("graph nodes/unattached = %d/%d, want 2/1", len(graph.Nodes), len(graph.Unattached))
	}
}

func TestProjectAlertsIsDeterministicAcrossSourceOrder(t *testing.T) {
	t.Parallel()
	firstAlert := sourceAlert{
		ActiveAt: "2026-07-16T20:00:00Z", State: "firing", Value: "1",
		Labels: map[string]string{"alertname": "A", "namespace": "ns-a", "pod": "pod-a", "severity": "warning"},
	}
	secondAlert := sourceAlert{
		ActiveAt: "2026-07-16T21:00:00Z", State: "pending", Value: "2.0",
		Labels: map[string]string{"alertname": "B", "namespace": "ns-b", "statefulset": "db"},
	}
	base := Projection{Workspace: "workspace-a", Scope: "cluster-a", ObservedAt: time.Date(2026, 7, 16, 22, 0, 0, 0, time.UTC)}
	base.Response = alertsResponse(t, firstAlert, secondAlert)
	first, err := ProjectAlerts(base)
	if err != nil {
		t.Fatalf("first ProjectAlerts() error = %v", err)
	}
	base.Response = alertsResponse(t, secondAlert, firstAlert)
	second, err := ProjectAlerts(base)
	if err != nil {
		t.Fatalf("second ProjectAlerts() error = %v", err)
	}
	firstJSON, _ := json.Marshal(first)
	secondJSON, _ := json.Marshal(second)
	if !slices.Equal(firstJSON, secondJSON) {
		t.Fatalf("projection depends on response order\nfirst:  %s\nsecond: %s", firstJSON, secondJSON)
	}
}

func TestProjectAlertsPreservesValidUTF8LabelValues(t *testing.T) {
	t.Parallel()
	input := Projection{
		Workspace: "workspace-a", Scope: "cluster-a", ObservedAt: time.Now(),
		Response: alertsResponse(t, sourceAlert{
			ActiveAt: "2026-07-16T20:00:00Z", State: "firing", Value: "1",
			Labels: map[string]string{
				"alertname": "UnicodeSeverity", "severity": " 警告 ", "unknown_utf8": "正常 ✅",
			},
		}),
	}
	facts, err := ProjectAlerts(input)
	if err != nil {
		t.Fatalf("ProjectAlerts() error = %v", err)
	}
	var observation alertObservation
	if err := json.Unmarshal(facts[0].Fact.Observed, &observation); err != nil {
		t.Fatalf("decode observation: %v", err)
	}
	if observation.Labels["severity"] != " 警告 " {
		t.Fatalf("severity = %q", observation.Labels["severity"])
	}
	if encoded, _ := json.Marshal(facts); bytes.Contains(encoded, []byte("unknown_utf8")) || bytes.Contains(encoded, []byte("正常")) {
		t.Fatalf("unknown UTF-8 label leaked: %s", encoded)
	}
}

func TestProjectAlertsAbstainsWhenThereAreNoActiveAlerts(t *testing.T) {
	t.Parallel()
	facts, err := ProjectAlerts(Projection{
		Workspace: "workspace-a", Scope: "cluster-a", ObservedAt: time.Now(), Response: alertsResponse(t),
	})
	if err != nil {
		t.Fatalf("ProjectAlerts() error = %v", err)
	}
	if len(facts) != 0 {
		t.Fatalf("facts = %#v, want no fabricated alert evidence", facts)
	}
}

func TestProjectAlertsRejectsMalformedOrAmbiguousEvidence(t *testing.T) {
	t.Parallel()
	validAlert := sourceAlert{
		ActiveAt: "2026-07-16T20:00:00Z", State: "firing", Value: "1",
		Labels: map[string]string{"alertname": "DeploymentUnavailable", "namespace": "payments", "deployment": "api"},
	}
	valid := Projection{Workspace: "workspace-a", Scope: "cluster-a", ObservedAt: time.Now(), Response: alertsResponse(t, validAlert)}

	tests := []struct {
		name   string
		mutate func(*Projection)
	}{
		{"missing workspace", func(input *Projection) { input.Workspace = "" }},
		{"invalid scope", func(input *Projection) { input.Scope = " cluster-a" }},
		{"unsafe scope", func(input *Projection) { input.Scope = "cluster/a" }},
		{"zero observed time", func(input *Projection) { input.ObservedAt = time.Time{} }},
		{"empty response", func(input *Projection) { input.Response = nil }},
		{"invalid UTF-8", func(input *Projection) { input.Response = []byte{'{', 0xff, '}'} }},
		{"malformed JSON", func(input *Projection) { input.Response = []byte(`{"status":`) }},
		{"duplicate JSON member", func(input *Projection) {
			input.Response = []byte(`{"status":"success","status":"success","data":{"alerts":[]}}`)
		}},
		{"trailing JSON", func(input *Projection) {
			input.Response = []byte(`{"status":"success","data":{"alerts":[]}} {}`)
		}},
		{"excessive JSON nesting", func(input *Projection) {
			input.Response = []byte(strings.Repeat("[", maxJSONDepth+1) + strings.Repeat("]", maxJSONDepth+1))
		}},
		{"non-success status", func(input *Projection) {
			input.Response = []byte(`{"status":"error","data":{"alerts":[]}}`)
		}},
		{"missing data", func(input *Projection) { input.Response = []byte(`{"status":"success"}`) }},
		{"missing alerts", func(input *Projection) { input.Response = []byte(`{"status":"success","data":{}}`) }},
		{"null alerts", func(input *Projection) { input.Response = []byte(`{"status":"success","data":{"alerts":null}}`) }},
		{"missing labels", mutateAlertResponse(t, func(alert *sourceAlert) { alert.Labels = nil })},
		{"missing alertname", mutateAlertResponse(t, func(alert *sourceAlert) { delete(alert.Labels, "alertname") })},
		{"invalid alertname", mutateAlertResponse(t, func(alert *sourceAlert) { alert.Labels["alertname"] = " bad-name" })},
		{"invalid state", mutateAlertResponse(t, func(alert *sourceAlert) { alert.State = "inactive" })},
		{"invalid active time", mutateAlertResponse(t, func(alert *sourceAlert) { alert.ActiveAt = "yesterday" })},
		{"zero active time", mutateAlertResponse(t, func(alert *sourceAlert) { alert.ActiveAt = "0001-01-01T00:00:00Z" })},
		{"invalid value", mutateAlertResponse(t, func(alert *sourceAlert) { alert.Value = "NaN" })},
		{"infinite value", mutateAlertResponse(t, func(alert *sourceAlert) { alert.Value = "+Inf" })},
		{"control label", mutateAlertResponse(t, func(alert *sourceAlert) { alert.Labels["severity"] = "critical\nsecret" })},
		{"oversized label name", mutateAlertResponse(t, func(alert *sourceAlert) { alert.Labels[strings.Repeat("x", maxLabelNameBytes+1)] = "x" })},
		{"oversized label value", mutateAlertResponse(t, func(alert *sourceAlert) { alert.Labels["severity"] = strings.Repeat("x", maxLabelValueBytes+1) })},
		{"ambiguous identity", mutateAlertResponse(t, func(alert *sourceAlert) { alert.Labels["pod"] = "api-123" })},
		{"pod without namespace", mutateAlertResponse(t, func(alert *sourceAlert) {
			delete(alert.Labels, "deployment")
			delete(alert.Labels, "namespace")
			alert.Labels["pod"] = "api-123"
		})},
		{"node with namespace", mutateAlertResponse(t, func(alert *sourceAlert) {
			delete(alert.Labels, "deployment")
			alert.Labels["node"] = "worker-a"
		})},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := valid
			input.Response = slices.Clone(valid.Response)
			test.mutate(&input)
			if facts, err := ProjectAlerts(input); err == nil {
				t.Fatalf("ProjectAlerts() facts = %#v, error = nil", facts)
			}
		})
	}
}

func TestProjectAlertsEnforcesBudgetsAndUniqueIdentity(t *testing.T) {
	t.Parallel()
	base := Projection{Workspace: "workspace-a", Scope: "cluster-a", ObservedAt: time.Now()}

	t.Run("response bytes", func(t *testing.T) {
		input := base
		input.Response = bytes.Repeat([]byte{' '}, maxResponseBytes+1)
		if _, err := ProjectAlerts(input); err == nil {
			t.Fatal("ProjectAlerts() accepted oversized response")
		}
	})

	t.Run("alert count", func(t *testing.T) {
		alerts := make([]sourceAlert, maxAlerts+1)
		for index := range alerts {
			alerts[index] = sourceAlert{
				ActiveAt: "2026-07-16T20:00:00Z", State: "firing", Value: "1",
				Labels: map[string]string{"alertname": fmt.Sprintf("Alert-%d", index)},
			}
		}
		input := base
		input.Response = alertsResponse(t, alerts...)
		if _, err := ProjectAlerts(input); err == nil {
			t.Fatal("ProjectAlerts() accepted too many alerts")
		}
	})

	t.Run("label count", func(t *testing.T) {
		alert := sourceAlert{ActiveAt: "2026-07-16T20:00:00Z", State: "firing", Value: "1", Labels: map[string]string{"alertname": "ManyLabels"}}
		for index := 0; index < maxLabelsPerAlert; index++ {
			alert.Labels[fmt.Sprintf("unknown_%d", index)] = "x"
		}
		input := base
		input.Response = alertsResponse(t, alert)
		if _, err := ProjectAlerts(input); err == nil {
			t.Fatal("ProjectAlerts() accepted too many labels")
		}
	})

	t.Run("encoded payload", func(t *testing.T) {
		alert := sourceAlert{
			ActiveAt: "2026-07-16T20:00:00Z", State: "firing", Value: "1",
			Labels: map[string]string{
				"alertname": "LargeAlert", "container": strings.Repeat("a", maxLabelValueBytes),
				"job": strings.Repeat("b", maxLabelValueBytes), "service": strings.Repeat("c", maxLabelValueBytes),
				"severity": strings.Repeat("d", maxLabelValueBytes),
			},
		}
		input := base
		input.Response = alertsResponse(t, alert)
		if _, err := ProjectAlerts(input); err == nil {
			t.Fatal("ProjectAlerts() accepted oversized encoded fact")
		}
	})

	t.Run("duplicate identity", func(t *testing.T) {
		alert := sourceAlert{
			ActiveAt: "2026-07-16T20:00:00Z", State: "firing", Value: "1",
			Labels: map[string]string{"alertname": "Duplicate"},
		}
		input := base
		input.Response = alertsResponse(t, alert, alert)
		if _, err := ProjectAlerts(input); err == nil {
			t.Fatal("ProjectAlerts() accepted duplicate alert identity")
		}
	})
}

func alertsResponse(t *testing.T, alerts ...sourceAlert) []byte {
	t.Helper()
	if alerts == nil {
		alerts = make([]sourceAlert, 0)
	}
	encoded, err := json.Marshal(map[string]any{
		"status": "success",
		"data":   map[string]any{"alerts": alerts},
	})
	if err != nil {
		t.Fatalf("encode alerts response: %v", err)
	}
	return encoded
}

func mutateAlertResponse(t *testing.T, mutate func(*sourceAlert)) func(*Projection) {
	t.Helper()
	return func(input *Projection) {
		alert := sourceAlert{
			ActiveAt: "2026-07-16T20:00:00Z", State: "firing", Value: "1",
			Labels: map[string]string{"alertname": "DeploymentUnavailable", "namespace": "payments", "deployment": "api"},
		}
		mutate(&alert)
		input.Response = alertsResponse(t, alert)
	}
}
