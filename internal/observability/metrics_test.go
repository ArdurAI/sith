// SPDX-License-Identifier: Apache-2.0

package observability

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/ArdurAI/sith/internal/hubfleet"
	"github.com/ArdurAI/sith/internal/hubserver"
	"github.com/ArdurAI/sith/internal/pep"
)

func TestMetricsExposeOnlyBoundedSelfObservability(t *testing.T) {
	registry := prometheus.NewPedanticRegistry()
	metrics, err := New(Config{Registry: registry, Version: "v1.2.3", Commit: "0123456789abcdef"})
	if err != nil {
		t.Fatal(err)
	}
	metrics.ObserveDecision(pep.VerbFleetRead, pep.DecisionOutcomeAllow, 125*time.Millisecond)
	metrics.ObserveDecision(pep.Verb("workspace-a/token=secret"), pep.DecisionOutcome("untrusted"), -time.Second)
	metrics.ObservePolicyAudit(pep.AuditSinkDurable, pep.AuditOutcomeSuccess, 15*time.Millisecond)
	metrics.ObservePolicyAudit(pep.AuditSinkProcess, pep.AuditOutcomeError, -time.Second)
	metrics.ObservePolicyAudit(pep.AuditSink("workspace-a/token=secret"), pep.AuditOutcome("untrusted"), time.Second)
	metrics.ObserveSpokeSnapshot(hubfleet.SnapshotOutcomeSuccess, 25*time.Millisecond)
	metrics.ObserveSpokeSnapshot(hubfleet.SnapshotOutcome("spoke-a/token=secret"), -time.Second)
	metrics.ObserveFleetRead(hubfleet.FleetReadObservation{
		Outcome: hubfleet.FleetReadOutcomeComplete, Freshness: hubfleet.FleetFreshnessOutcomeFresh,
	})
	metrics.ObserveFleetRead(hubfleet.FleetReadObservation{
		Outcome: hubfleet.FleetReadOutcomeDegraded, Freshness: hubfleet.FleetFreshnessOutcomeStale,
	})
	metrics.ObserveFleetRead(hubfleet.FleetReadObservation{
		Outcome: hubfleet.FleetReadOutcomeDegraded, Freshness: hubfleet.FleetFreshnessOutcomeUnknown,
	})
	metrics.ObserveFleetRead(hubfleet.FleetReadObservation{
		Outcome: hubfleet.FleetReadOutcomeEmpty, Freshness: hubfleet.FleetFreshnessOutcomeEmpty,
	})
	metrics.ObserveFleetRead(hubfleet.FleetReadObservation{
		Outcome: hubfleet.FleetReadOutcomeError, Freshness: hubfleet.FleetFreshnessOutcomeError,
	})
	metrics.ObserveFleetRead(hubfleet.FleetReadObservation{
		Outcome:   hubfleet.FleetReadOutcome("workspace-a/token=secret"),
		Freshness: hubfleet.FleetFreshnessOutcome("spoke-a/token=secret"),
	})
	metrics.ObserveFleetRead(hubfleet.FleetReadObservation{
		Outcome: hubfleet.FleetReadOutcomeComplete, Freshness: hubfleet.FleetFreshnessOutcomeStale,
	})
	metrics.ObserveFleetRead(hubfleet.FleetReadObservation{
		Outcome:   hubfleet.FleetReadOutcomeComplete,
		Freshness: hubfleet.FleetFreshnessOutcome("spoke-a/token=secret"),
	})
	metrics.ObserveReadiness(hubserver.ReadinessOutcomeReady, 10*time.Millisecond)
	metrics.ObserveReadiness(hubserver.ReadinessOutcomeUnavailable, -time.Second)
	metrics.ObserveReadiness(hubserver.ReadinessOutcome("database endpoint secret"), time.Second)
	metrics.ObserveAuth(hubserver.AuthEvent{Outcome: hubserver.AuthOutcomeAccepted})
	metrics.ObserveAuth(hubserver.AuthEvent{Outcome: hubserver.AuthOutcomeRefused})
	metrics.ObserveAuth(hubserver.AuthEvent{Outcome: "token=secret"})
	metrics.ObserveAuthRefusalDeliveryDrop()

	response := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://metrics.invalid/metrics", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("metrics status = %d, body = %s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, metric := range []string{
		"sith_build_info",
		"sith_policy_decisions_total",
		"sith_policy_decision_duration_seconds",
		"sith_policy_audit_attempts_total",
		"sith_policy_audit_duration_seconds",
		`sith_policy_audit_attempts_total{outcome="success",sink="durable"} 1`,
		`sith_policy_audit_attempts_total{outcome="error",sink="durable"} 0`,
		`sith_policy_audit_attempts_total{outcome="success",sink="process"} 0`,
		`sith_policy_audit_attempts_total{outcome="error",sink="process"} 1`,
		"sith_federation_spoke_snapshot_attempts_total",
		"sith_federation_spoke_snapshot_duration_seconds",
		`sith_federation_fleet_read_results_total{outcome="complete"} 1`,
		`sith_federation_fleet_read_results_total{outcome="degraded"} 2`,
		`sith_federation_fleet_read_results_total{outcome="empty"} 1`,
		`sith_federation_fleet_read_results_total{outcome="error"} 1`,
		`sith_federation_fleet_read_freshness_total{outcome="fresh"} 1`,
		`sith_federation_fleet_read_freshness_total{outcome="stale"} 1`,
		`sith_federation_fleet_read_freshness_total{outcome="unknown"} 1`,
		`sith_federation_fleet_read_freshness_total{outcome="empty"} 1`,
		`sith_federation_fleet_read_freshness_total{outcome="error"} 1`,
		`sith_hub_readiness_checks_total{outcome="ready"} 1`,
		`sith_hub_readiness_checks_total{outcome="unavailable"} 1`,
		`sith_hub_readiness_check_duration_seconds_count{outcome="ready"} 1`,
		`sith_hub_readiness_check_duration_seconds_count{outcome="unavailable"} 1`,
		`sith_auth_attempts_total{outcome="accepted"} 1`,
		`sith_auth_attempts_total{outcome="refused"} 1`,
		"sith_auth_refusals_total 1",
		"sith_auth_refusal_delivery_drops_total 1",
		`verb="fleet.read"`,
		`verb="invalid"`,
		`outcome="allow"`,
		`outcome="error"`,
		`outcome="success"`,
		`outcome="store-error"`,
		`sink="durable"`,
		`sink="process"`,
	} {
		if !strings.Contains(body, metric) {
			t.Fatalf("metrics output missing %q: %s", metric, body)
		}
	}
	for _, forbidden := range []string{"workspace-a", "spoke-a", "token=secret", "untrusted", "database endpoint secret"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("metrics output leaked %q: %s", forbidden, body)
		}
	}
	assertSithMetricLabels(t, metrics)
}

func TestMetricsUseIndependentRegistriesAndNormalizeBuildLabels(t *testing.T) {
	first, err := New(Config{Version: "v9.9.9", Commit: "abcdef0"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := New(Config{Version: "token=secret", Commit: "workspace-a"})
	if err != nil {
		t.Fatal(err)
	}
	first.ObserveDecision(pep.VerbFleetRead, pep.DecisionOutcomeAllow, time.Millisecond)

	response := httptest.NewRecorder()
	second.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://metrics.invalid/metrics", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("second registry status = %d", response.Code)
	}
	body := response.Body.String()
	if strings.Contains(body, "sith_policy_decisions_total") || strings.Contains(body, "token=secret") || strings.Contains(body, "workspace-a") {
		t.Fatalf("isolated metrics registry leaked another registry or unsafe metadata: %s", body)
	}
	if !strings.Contains(body, `sith_build_info{commit="unknown",version="unknown"} 1`) {
		t.Fatalf("unsafe build metadata was not normalized: %s", body)
	}
	for _, preinitialized := range []string{
		`sith_federation_fleet_read_freshness_total{outcome="fresh"} 0`,
		`sith_federation_fleet_read_freshness_total{outcome="stale"} 0`,
		`sith_federation_fleet_read_freshness_total{outcome="unknown"} 0`,
		`sith_federation_fleet_read_freshness_total{outcome="empty"} 0`,
		`sith_federation_fleet_read_freshness_total{outcome="error"} 0`,
		`sith_hub_readiness_checks_total{outcome="ready"} 0`,
		`sith_hub_readiness_checks_total{outcome="unavailable"} 0`,
		`sith_hub_readiness_check_duration_seconds_count{outcome="ready"} 0`,
		`sith_hub_readiness_check_duration_seconds_count{outcome="unavailable"} 0`,
		`sith_auth_attempts_total{outcome="accepted"} 0`,
		`sith_auth_attempts_total{outcome="refused"} 0`,
		`sith_auth_refusals_total 0`,
	} {
		if !strings.Contains(body, preinitialized) {
			t.Fatalf("metrics output missing preinitialized readiness series %q: %s", preinitialized, body)
		}
	}
}

func TestMetricsRejectDuplicateRegistrations(t *testing.T) {
	registry := prometheus.NewPedanticRegistry()
	conflicting := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "sith", Subsystem: "hub", Name: "readiness_checks_total",
		Help: "Total completed Sith Hub database readiness checks by closed outcome.",
	}, []string{"outcome"})
	if err := registry.Register(conflicting); err != nil {
		t.Fatal(err)
	}
	if _, err := New(Config{Registry: registry}); err == nil {
		t.Fatal("New() accepted a duplicate metric registration")
	}
	if !registry.Unregister(conflicting) {
		t.Fatal("remove conflicting collector")
	}
	if _, err := New(Config{Registry: registry}); err != nil {
		t.Fatalf("New() left partial registration state after failure: %v", err)
	}
}

func TestMetricsRejectPrometheusGlobalRegistry(t *testing.T) {
	registry, ok := prometheus.DefaultRegisterer.(*prometheus.Registry)
	if !ok {
		t.Fatal("Prometheus default registerer is not a concrete registry")
	}
	if _, err := New(Config{Registry: registry}); err == nil {
		t.Fatal("New() accepted Prometheus global registry")
	}
}

func assertSithMetricLabels(t *testing.T, metrics *Metrics) {
	t.Helper()
	families, err := metrics.gatherer.Gather()
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]map[string]bool{
		"sith_build_info":                                 {"commit": true, "version": true},
		"sith_policy_decisions_total":                     {"outcome": true, "verb": true},
		"sith_policy_decision_duration_seconds":           {"outcome": true, "verb": true},
		"sith_policy_audit_attempts_total":                {"outcome": true, "sink": true},
		"sith_policy_audit_duration_seconds":              {"outcome": true, "sink": true},
		"sith_federation_spoke_snapshot_attempts_total":   {"outcome": true},
		"sith_federation_spoke_snapshot_duration_seconds": {"outcome": true},
		"sith_federation_fleet_read_results_total":        {"outcome": true},
		"sith_federation_fleet_read_freshness_total":      {"outcome": true},
		"sith_hub_readiness_checks_total":                 {"outcome": true},
		"sith_hub_readiness_check_duration_seconds":       {"outcome": true},
		"sith_auth_attempts_total":                        {"outcome": true},
		"sith_auth_refusals_total":                        {},
		"sith_auth_refusal_delivery_drops_total":          {},
	}
	for _, family := range families {
		labels, sithMetric := allowed[family.GetName()]
		if !sithMetric {
			continue
		}
		for _, metric := range family.Metric {
			for _, label := range metric.Label {
				if !labels[label.GetName()] {
					t.Fatalf("metric %s exposed forbidden label %q", family.GetName(), label.GetName())
				}
			}
		}
	}
}
