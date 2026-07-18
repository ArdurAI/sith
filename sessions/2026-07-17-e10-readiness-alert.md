# Session — 2026-07-17 — E10 Hub database-readiness alert

**Builder:** Gnani Rahul · **Branch:** `gnanirahulnutakki/feat/e10-readiness-alert`
**Slice:** E10 F10.4c / [#248](https://github.com/ArdurAI/sith/issues/248) · **Status:** ready for commit
**Decision record:** [Notion](https://app.notion.com/p/3a12637edb0781349e9fd9a3487e61c2)

## [G] Goal

Turn sustained database-backed Hub readiness degradation into one actionable portable warning without
inventing a freshness SLO, creating monitoring infrastructure, or widening metric cardinality.

## [D] Design

- Aggregate `unavailable` over all completed `ready|unavailable` checks in a 15-minute window.
- Require more than 5% failures and at least 20 completed checks, continuously for 10 minutes.
- Aggregate away every source label and emit only static `component` and `severity` labels.
- Keep notification routing and the scrape/rule pipeline operator-owned; this is a warning symptom,
  not an error-budget page or metamonitoring substitute.

## [S] Security, privacy, and cost boundary

No tenant, workspace, spoke, actor, request, endpoint, credential, error, panic, or dependency label
is emitted. The slice adds one aggregate alert vector over existing counters and no listener, Service,
ServiceMonitor, PrometheusRule, Alertmanager, receiver, exporter, remote write, persistence, cloud
resource, or spoke egress.

## [T] Verification plan

- `promtool check rules` plus fixtures for missing, all-ready, low-volume, exact-threshold, inclusive
  volume, transient, sustained, recovery, hostile-label aggregation, and counter-reset behavior.
- Static Go/shell contracts for exact expression, labels, annotations, group bound, and pinned tool.
- Full CI, forced PostgreSQL/RLS isolation, reproducible release/SBOM, standalone OCI and pinned Helm,
  real two-cluster Kind, CodeRabbit, CodeQL, security queues, and exact post-merge `dev` proof.

## [R] Primary references

- [Prometheus alerting best practices](https://prometheus.io/docs/practices/alerting/)
- [Prometheus alerting rules](https://prometheus.io/docs/prometheus/latest/configuration/alerting_rules/)

## [V] Current evidence

- Pinned Prometheus 3.13.1 parses all five rules; fixtures prove missing/all-ready/low-volume data,
  the exact threshold, inclusive volume, transient failure, sustained firing after the hold, recovery,
  hostile-label aggregation, and counter-reset behavior.
- The focused race suite and full `make ci` pass formatting, vet, zero-finding lint, vulnerability
  scanning with no reachable vulnerabilities, all race tests, policy scripts, Prometheus fixtures,
  performance, binary smoke, and build. `internal/observability` coverage remains 93.1%.
- Forced PostgreSQL/RLS isolation passes with `hubdb` coverage at 75.1%; both 50,000-case
  cross-workspace fuzz campaigns pass.
- Reproducible release verification passes twice for four platforms, including SPDX SBOMs,
  distribution verification, Homebrew formula, and the release-derived multi-architecture OCI layout.
- Standalone OCI and pinned Helm v4.2.3 contracts pass.
- The digest-pinned Kubernetes v1.36.1 real two-cluster Kind gate passes in 236.012 seconds.
- CodeRabbit CLI v0.6.5 reviews all nine changed files with no findings.
