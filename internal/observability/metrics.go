// SPDX-License-Identifier: Apache-2.0

// Package observability exposes bounded, pull-only metrics about Sith's own process behavior.
// It deliberately owns no listener, remote exporter, persistence, or external telemetry data.
package observability

import (
	"fmt"
	"net/http"
	"regexp"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/ArdurAI/sith/internal/hubfleet"
	"github.com/ArdurAI/sith/internal/pep"
)

var (
	versionLabelPattern = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)
	commitLabelPattern  = regexp.MustCompile(`^(?:none|unknown|[0-9a-f]{7,64})$`)
)

// Config supplies non-sensitive build metadata and an optional isolated registry. A nil Registry
// creates a fresh pedantic registry; the Prometheus global registry is never used.
type Config struct {
	Registry *prometheus.Registry
	Version  string
	Commit   string
}

// Metrics records low-cardinality control-plane observations and exposes the matching handler.
// All label normalization occurs before a metric is created, so caller-controlled values cannot
// increase cardinality or cross the privacy boundary.
type Metrics struct {
	gatherer            prometheus.Gatherer
	policyDecisions     *prometheus.CounterVec
	policyDuration      *prometheus.HistogramVec
	policyAuditAttempts *prometheus.CounterVec
	policyAuditDuration *prometheus.HistogramVec
	snapshotAttempts    *prometheus.CounterVec
	snapshotDuration    *prometheus.HistogramVec
	authDeliveryDrops   prometheus.Counter
}

var (
	_ pep.DecisionObserver      = (*Metrics)(nil)
	_ pep.AuditObserver         = (*Metrics)(nil)
	_ hubfleet.SnapshotObserver = (*Metrics)(nil)
)

// New constructs metrics against a caller-owned or fresh isolated registry. Registration errors
// are returned rather than panicking, making duplicate or incompatible metrics a startup failure.
func New(config Config) (*Metrics, error) {
	registry := config.Registry
	if registry == nil {
		registry = prometheus.NewPedanticRegistry()
	}
	if registry == prometheus.DefaultRegisterer || registry == prometheus.DefaultGatherer {
		return nil, fmt.Errorf("construct metrics: Prometheus global registry is not allowed")
	}

	metrics := &Metrics{
		gatherer: registry,
		policyDecisions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "sith", Subsystem: "policy", Name: "decisions_total",
			Help: "Total completed Sith policy-read decisions by closed verb and outcome.",
		}, []string{"verb", "outcome"}),
		policyDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "sith", Subsystem: "policy", Name: "decision_duration_seconds",
			Help: "Duration of completed Sith policy-read decisions by closed verb and outcome.",
		}, []string{"verb", "outcome"}),
		policyAuditAttempts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "sith", Subsystem: "policy", Name: "audit_attempts_total",
			Help: "Total completed Sith policy-audit sink attempts by closed sink and outcome.",
		}, []string{"sink", "outcome"}),
		policyAuditDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "sith", Subsystem: "policy", Name: "audit_duration_seconds",
			Help: "Duration of completed Sith policy-audit sink attempts by closed sink and outcome.",
		}, []string{"sink", "outcome"}),
		snapshotAttempts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "sith", Subsystem: "federation", Name: "spoke_snapshot_attempts_total",
			Help: "Total completed Sith federated spoke snapshot attempts by closed outcome.",
		}, []string{"outcome"}),
		snapshotDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "sith", Subsystem: "federation", Name: "spoke_snapshot_duration_seconds",
			Help: "Duration of completed Sith federated spoke snapshot attempts by closed outcome.",
		}, []string{"outcome"}),
		authDeliveryDrops: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "sith", Subsystem: "auth", Name: "refusal_delivery_drops_total",
			Help: "Total dropped bounded local authentication-refusal delivery records.",
		}),
	}
	buildInfo := prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "sith", Name: "build_info", Help: "Sith build metadata with safe release identifiers only.",
		ConstLabels: prometheus.Labels{
			"version": normalizedVersion(config.Version),
			"commit":  normalizedCommit(config.Commit),
		},
	})
	buildInfo.Set(1)

	registered := make([]prometheus.Collector, 0, 10)
	for _, collector := range []struct {
		name      string
		collector prometheus.Collector
	}{
		{name: "Go runtime", collector: collectors.NewGoCollector()},
		{name: "process", collector: collectors.NewProcessCollector(collectors.ProcessCollectorOpts{})},
		{name: "build info", collector: buildInfo},
		{name: "policy decisions", collector: metrics.policyDecisions},
		{name: "policy duration", collector: metrics.policyDuration},
		{name: "policy audit attempts", collector: metrics.policyAuditAttempts},
		{name: "policy audit duration", collector: metrics.policyAuditDuration},
		{name: "snapshot attempts", collector: metrics.snapshotAttempts},
		{name: "snapshot duration", collector: metrics.snapshotDuration},
		{name: "authentication-refusal delivery drops", collector: metrics.authDeliveryDrops},
	} {
		if err := registry.Register(collector.collector); err != nil {
			for index := len(registered) - 1; index >= 0; index-- {
				registry.Unregister(registered[index])
			}
			return nil, fmt.Errorf("register %s metrics: %w", collector.name, err)
		}
		registered = append(registered, collector.collector)
	}
	for _, sink := range []pep.AuditSink{pep.AuditSinkDurable, pep.AuditSinkProcess} {
		for _, outcome := range []pep.AuditOutcome{pep.AuditOutcomeSuccess, pep.AuditOutcomeError} {
			metrics.policyAuditAttempts.WithLabelValues(string(sink), string(outcome))
			metrics.policyAuditDuration.WithLabelValues(string(sink), string(outcome))
		}
	}

	return metrics, nil
}

// Handler returns an embeddable Prometheus exposition handler. It does not bind a port or make
// outbound calls; a composition root owns any listener and access boundary.
func (metrics *Metrics) Handler() http.Handler {
	if metrics == nil || metrics.gatherer == nil {
		return http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			http.Error(writer, "metrics unavailable", http.StatusServiceUnavailable)
		})
	}
	return promhttp.HandlerFor(metrics.gatherer, promhttp.HandlerOpts{ErrorHandling: promhttp.HTTPErrorOnError})
}

// ObserveDecision records one completed policy decision using only fixed label vocabularies.
func (metrics *Metrics) ObserveDecision(verb pep.Verb, outcome pep.DecisionOutcome, duration time.Duration) {
	if metrics == nil || metrics.policyDecisions == nil || metrics.policyDuration == nil {
		return
	}
	verbLabel := normalizedVerb(verb)
	outcomeLabel := normalizedDecisionOutcome(outcome)
	metrics.policyDecisions.WithLabelValues(verbLabel, outcomeLabel).Inc()
	metrics.policyDuration.WithLabelValues(verbLabel, outcomeLabel).Observe(normalizedDuration(duration))
}

// ObservePolicyAudit records one completed policy-audit sink attempt using only closed sink and
// outcome vocabularies. Invalid values are discarded instead of creating a caller-controlled
// series.
func (metrics *Metrics) ObservePolicyAudit(sink pep.AuditSink, outcome pep.AuditOutcome, duration time.Duration) {
	if metrics == nil || metrics.policyAuditAttempts == nil || metrics.policyAuditDuration == nil ||
		!sink.Valid() || !outcome.Valid() {
		return
	}
	metrics.policyAuditAttempts.WithLabelValues(string(sink), string(outcome)).Inc()
	metrics.policyAuditDuration.WithLabelValues(string(sink), string(outcome)).Observe(normalizedDuration(duration))
}

// ObserveSpokeSnapshot records one completed federated snapshot attempt using only a fixed outcome
// vocabulary. It intentionally omits all tenant and spoke identifiers.
func (metrics *Metrics) ObserveSpokeSnapshot(outcome hubfleet.SnapshotOutcome, duration time.Duration) {
	if metrics == nil || metrics.snapshotAttempts == nil || metrics.snapshotDuration == nil {
		return
	}
	outcomeLabel := normalizedSnapshotOutcome(outcome)
	metrics.snapshotAttempts.WithLabelValues(outcomeLabel).Inc()
	metrics.snapshotDuration.WithLabelValues(outcomeLabel).Observe(normalizedDuration(duration))
}

// ObserveAuthRefusalDeliveryDrop records one bounded process-local delivery drop. It carries no
// labels because authentication happens before any principal, workspace, or trusted correlation.
func (metrics *Metrics) ObserveAuthRefusalDeliveryDrop() {
	if metrics == nil || metrics.authDeliveryDrops == nil {
		return
	}
	metrics.authDeliveryDrops.Inc()
}

func normalizedVersion(value string) string {
	if value == "dev" || value == "unknown" || versionLabelPattern.MatchString(value) {
		return value
	}
	return "unknown"
}

func normalizedCommit(value string) string {
	if commitLabelPattern.MatchString(value) {
		return value
	}
	return "unknown"
}

func normalizedVerb(verb pep.Verb) string {
	if verb.Valid() {
		return string(verb)
	}
	return "invalid"
}

func normalizedDecisionOutcome(outcome pep.DecisionOutcome) string {
	switch outcome {
	case pep.DecisionOutcomeAllow, pep.DecisionOutcomeDeny, pep.DecisionOutcomeRequireApproval, pep.DecisionOutcomeError:
		return string(outcome)
	default:
		return string(pep.DecisionOutcomeError)
	}
}

func normalizedSnapshotOutcome(outcome hubfleet.SnapshotOutcome) string {
	switch outcome {
	case hubfleet.SnapshotOutcomeSuccess,
		hubfleet.SnapshotOutcomeTransport,
		hubfleet.SnapshotOutcomeDeadline,
		hubfleet.SnapshotOutcomeInvalidSnapshot,
		hubfleet.SnapshotOutcomeStoreError,
		hubfleet.SnapshotOutcomeCanceled:
		return string(outcome)
	default:
		return string(hubfleet.SnapshotOutcomeStoreError)
	}
}

func normalizedDuration(duration time.Duration) float64 {
	if duration < 0 {
		return 0
	}
	return duration.Seconds()
}
