# Session — 2026-07-17 — E10 portable hub alert rules

**Builder:** Gnani Rahul · **Branch:** `gnanirahulnutakki/feat/e10-portable-alert-rules`
**Slice:** E10 F10.4a / [#238](https://github.com/ArdurAI/sith/issues/238) · **Status:** ready for review

## [G] Goal

Turn the Hub's existing fixed-cardinality self-observability metrics into tested, actionable alerts
without widening the loopback scrape boundary or pretending that missing dispatch/PDP/freshness
signals already satisfy the full F10.4 SLO contract.

## [D] Design

- A native Prometheus rule file remains independent of Helm and the Prometheus Operator. Sith adds
  no Service, ServiceMonitor, PrometheusRule CRD, exporter, remote write, or notification receiver.
- Every expression uses a top-level aggregate, so arbitrary scrape/remote-write labels cannot fan
  out or leak into the resulting alert. Alert labels and annotations are fixed strings only.
- Policy-audit failures are critical because they fail governed reads closed. Authentication log
  drops and sustained aggregate snapshot failures are warnings with explicit hold and volume guards.
- Missing series remain an external metamonitoring concern rather than being misreported as healthy
  or unhealthy by a rule that cannot distinguish disabled metrics from a broken scrape.

## [R] Research and trade-offs

- Prometheus recommends few, symptom-oriented, actionable alerts with slack for small blips:
  https://prometheus.io/docs/practices/alerting/
- Prometheus documents native rule validation and deterministic unit fixtures through `promtool`:
  https://prometheus.io/docs/prometheus/latest/configuration/recording_rules/ and
  https://prometheus.io/docs/prometheus/latest/configuration/unit_testing_rules/
- The latest verified upstream release on 2026-07-17 is Prometheus v3.13.1, published 2026-07-10;
  CI pins its official Linux amd64 archive checksum.
- A chart-rendered PrometheusRule was rejected because Sith intentionally has no remotely scrapeable
  Service or ServiceMonitor. Rendering a rule CR without a data path would be a misleading partial
  integration and would add an optional-CRD admission dependency.

## [T] Validation plan

- `promtool check rules` plus fixture-driven alert tests for thresholds, hold windows, minimum
  volume, missing series, counter resets, and hostile input labels.
- A Go contract test locks the rule/metric allowlist, fixed labels, static annotations, aggregation,
  group cardinality limit, and runbook links.
- Full CI/race, vulnerability, forced PostgreSQL isolation, release/SBOM reproducibility, Helm/OCI,
  and real two-cluster Kind gates before review and merge.

## [O] Security, operability, and cost

The evaluator runs three short-window expressions once per minute and can emit at most three alert
instances. No existing metric series, runtime listener, network path, or storage contract changes.
Prometheus/Alertmanager capacity, external labels, notification routing, and metamonitoring remain
operator-owned.

## [V] Local verification

- Official Prometheus v3.13.1 `promtool check rules` and fixture tests pass. Fixtures cover exact
  5% and exact 20-attempt boundaries, hold windows, minimum volume, missing series, counter reset,
  and hostile source labels that must not reach alerts.
- Full `make ci` passes with the pinned golangci-lint v2.12.2 and govulncheck v1.6.0: formatting,
  vet, zero lint findings, no reachable vulnerabilities, full race/coverage, shell policies,
  performance, binary e2e, and build are green. Observability coverage is 93.1%.
- Forced PostgreSQL RLS/isolation passes with 75.2% `hubdb` coverage; both cross-tenant fuzz
  campaigns complete 50,000+ executions.
- Helm v4.2.3 and the standalone multi-architecture OCI contract pass. Kind v0.32.0 against the
  digest-pinned Kubernetes v1.36.1 node passes the complete fleet/OCI/Argo suite in 236.537 seconds.
- The aggregate release target reaches the known machine-local `go mod verify` discovery anomaly.
  Its substantive gates pass independently: two reproducible four-platform snapshots, archive and
  SPDX SBOM validation, Homebrew formula, multi-architecture OCI layout, and identical digests.
- CodeRabbit's first complete review produced four applicable test-contract findings: exact
  comparator fixtures, canonical expression locking, complete output-label allowlisting, and
  relative `PROMTOOL` path handling. All were fixed; the second complete review reports zero
  findings.
- Notion checkpoint: https://app.notion.com/p/3a02637edb078115a74ac6cd5deb61f9
