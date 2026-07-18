// SPDX-License-Identifier: Apache-2.0

package observability

import (
	"os"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

type alertRuleFile struct {
	Groups []alertRuleGroup `yaml:"groups"`
}

type alertRuleGroup struct {
	Name     string      `yaml:"name"`
	Interval string      `yaml:"interval"`
	Limit    int         `yaml:"limit"`
	Rules    []alertRule `yaml:"rules"`
}

type alertRule struct {
	Alert       string            `yaml:"alert"`
	Expr        string            `yaml:"expr"`
	For         string            `yaml:"for"`
	Labels      map[string]string `yaml:"labels"`
	Annotations map[string]string `yaml:"annotations"`
}

func TestPortableAlertRulesStayBoundedAndStatic(t *testing.T) {
	t.Parallel()

	contents, err := os.ReadFile("../../monitoring/sith-hub.rules.yml")
	if err != nil {
		t.Fatalf("read portable alert rules: %v", err)
	}
	var file alertRuleFile
	if err := yaml.Unmarshal(contents, &file); err != nil {
		t.Fatalf("decode portable alert rules: %v", err)
	}
	if len(file.Groups) != 1 {
		t.Fatalf("rule groups = %d, want 1", len(file.Groups))
	}
	group := file.Groups[0]
	if group.Name != "sith-hub.failure-signals" || group.Interval != "1m" || group.Limit != 8 {
		t.Errorf("rule group contract = %#v", group)
	}
	if len(group.Rules) != 8 {
		t.Fatalf("alert rules = %d, want 8", len(group.Rules))
	}

	want := map[string]struct {
		severity    string
		hold        string
		expr        string
		summary     string
		description string
	}{
		"SithHubPolicyAuditFailure": {
			severity: "critical", hold: "2m",
			expr: `sum(increase(sith_policy_audit_attempts_total{outcome="error"}[5m])) > 0`,
		},
		"SithHubPolicyDecisionErrorRatioHigh": {
			severity: "warning", hold: "10m",
			expr:        `( sum(increase(sith_policy_decisions_total{outcome="error"}[15m])) / clamp_min(sum(increase(sith_policy_decisions_total{outcome=~"allow|deny|require-approval|error"}[15m])), 1) ) > 0.05 and sum(increase(sith_policy_decisions_total{outcome=~"allow|deny|require-approval|error"}[15m])) >= 20`,
			summary:     "Sith hub policy decisions are returning sustained errors",
			description: "More than five percent of at least twenty aggregate eligible policy decisions ended in error over fifteen minutes.",
		},
		"SithHubAuthRefusalDeliveryDrop": {
			severity: "warning", hold: "5m",
			expr: `sum(increase(sith_auth_refusal_delivery_drops_total[10m])) > 0`,
		},
		"SithHubFederationSnapshotFailureRatioHigh": {
			severity: "warning", hold: "10m",
			expr: `( sum(increase(sith_federation_spoke_snapshot_attempts_total{outcome!="success"}[15m])) / clamp_min(sum(increase(sith_federation_spoke_snapshot_attempts_total[15m])), 1) ) > 0.05 and sum(increase(sith_federation_spoke_snapshot_attempts_total[15m])) >= 20`,
		},
		"SithHubFleetReadCoverageDegradationHigh": {
			severity: "warning", hold: "10m",
			expr: `( sum(increase(sith_federation_fleet_read_results_total{outcome=~"degraded|error"}[15m])) / clamp_min(sum(increase(sith_federation_fleet_read_results_total{outcome=~"complete|degraded|error"}[15m])), 1) ) > 0.05 and sum(increase(sith_federation_fleet_read_results_total{outcome=~"complete|degraded|error"}[15m])) >= 20`,
		},
		"SithHubFleetReadStalenessHigh": {
			severity: "warning", hold: "10m",
			expr: `( sum(increase(sith_federation_fleet_read_freshness_total{outcome="stale"}[15m])) / clamp_min(sum(increase(sith_federation_fleet_read_freshness_total{outcome=~"fresh|stale"}[15m])), 1) ) > 0.05 and sum(increase(sith_federation_fleet_read_freshness_total{outcome=~"fresh|stale"}[15m])) >= 20`,
		},
		"SithHubDatabaseReadinessDegradationHigh": {
			severity: "warning", hold: "10m",
			expr: `( sum(increase(sith_hub_readiness_checks_total{outcome="unavailable"}[15m])) / clamp_min(sum(increase(sith_hub_readiness_checks_total{outcome=~"ready|unavailable"}[15m])), 1) ) > 0.05 and sum(increase(sith_hub_readiness_checks_total{outcome=~"ready|unavailable"}[15m])) >= 20`,
		},
		"SithHubTelemetryMissing": {
			severity: "warning", hold: "5m",
			expr:        `absent_over_time(sith_build_info[10m])`,
			summary:     "Sith hub telemetry is absent from the rule evaluator",
			description: "No Sith build-info sample reached the rule evaluator during the last ten minutes.",
		},
	}
	for _, rule := range group.Rules {
		expected, ok := want[rule.Alert]
		if !ok {
			t.Errorf("unexpected alert %q", rule.Alert)
			continue
		}
		if rule.For != expected.hold {
			t.Errorf("%s hold = %q, want %q", rule.Alert, rule.For, expected.hold)
		}
		if len(rule.Labels) != 2 || rule.Labels["component"] != "sith-hub" || rule.Labels["severity"] != expected.severity {
			t.Errorf("%s labels = %#v", rule.Alert, rule.Labels)
		}
		wantRunbook := "https://github.com/ArdurAI/sith/blob/dev/docs/runbooks/hub-alerts.md#" +
			strings.ToLower(rule.Alert)
		if len(rule.Annotations) != 3 || rule.Annotations["summary"] == "" ||
			rule.Annotations["description"] == "" || rule.Annotations["runbook_url"] != wantRunbook {
			t.Errorf("%s annotations = %#v", rule.Alert, rule.Annotations)
		}
		if expected.summary != "" && (rule.Annotations["summary"] != expected.summary ||
			rule.Annotations["description"] != expected.description) {
			t.Errorf("%s fixed annotation contract = %#v", rule.Alert, rule.Annotations)
		}
		for key, value := range rule.Annotations {
			if strings.Contains(value, "{{") || strings.Contains(value, "$labels") || strings.ContainsAny(value, "\r\n") {
				t.Errorf("%s annotation %q is dynamic or multiline", rule.Alert, key)
			}
		}
		if normalized := strings.Join(strings.Fields(rule.Expr), " "); normalized != expected.expr {
			t.Errorf("%s normalized expression = %q, want %q", rule.Alert, normalized, expected.expr)
		}
		delete(want, rule.Alert)
	}
	if len(want) != 0 {
		t.Errorf("missing alert contracts: %#v", want)
	}
}
