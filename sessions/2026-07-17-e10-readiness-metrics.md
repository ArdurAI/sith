# Session — 2026-07-17 — E10 Hub database readiness metrics

**Builder:** Gnani Rahul · **Branch:** `gnanirahulnutakki/feat/e10-readiness-metrics`
**Slice:** E10 F10.1e / [#246](https://github.com/ArdurAI/sith/issues/246) · **Status:** ready for commit
**Decision record:** [Notion](https://app.notion.com/p/3a12637edb0781baa05dcddc674bf9bb)

## [G] Goal

Expose bounded attempts, failures, and latency for the Hub's existing database-aware readiness
check without changing the probe response, widening the scrape boundary, or creating
tenant-proportional metrics.

## [D] Design

- A probe-owned observer receives exactly one completed valid readiness check as `ready` or
  `unavailable` plus duration.
- Database error, server deadline, caller cancellation, and recovered checker panic all collapse to
  `unavailable`. Rejected request variants never reach the checker or observer.
- The existing isolated registry preinitializes two counter series and two matching histogram
  outcomes. Invalid observation values are discarded.
- Observer panic is recovered at the instrumentation seam and cannot change the HTTP status, empty
  body, database timeout, or checker call count.
- Production injects the already-owned registry; no new metrics listener or dependency is created.

## [S] Security, privacy, and cost boundary

No tenant, workspace, spoke, actor, request, endpoint, credential, error, panic, or dependency-class
label is emitted. The slice adds no Service, ServiceMonitor, PrometheusRule, exporter, queue,
persistence, remote write, cloud resource, or spoke egress. Cost is limited to two fixed counter
series and histogram buckets for two outcomes per Hub process plus bounded local metric work on the
existing readiness Ping.

## [T] Verification plan

- Focused race tests for probe completion, error, timeout, cancellation, checker panic, observer
  panic, invalid requests, exposition, label allowlists, independent registries, and registration
  rollback.
- Full `make ci`, forced PostgreSQL/RLS isolation, release/SBOM reproducibility, standalone Helm and
  OCI contracts, and the digest-pinned real two-cluster Kind gate.
- Independent diff/secret review and CodeRabbit review before signed DCO/GSTACK commit; exact hosted
  CI, CodeQL, security queues, and post-merge `dev` proof before issue closure.

## [V] Current evidence

- Focused race tests pass for `internal/hubserver`, `internal/observability`, `internal/hubruntime`,
  and the production privacy boundary. Fixtures cover success, dependency error, timeout, caller
  cancellation, checker panic, observer panic, invalid requests, exact call/observation counts,
  preinitialized series, invalid-value suppression, label allowlists, independent registries, and
  registration rollback.
- Full `make ci` passes formatting, vet, zero-finding lint, vulnerability scanning with no reachable
  vulnerabilities, the complete race suite, shell policies, Prometheus rule fixtures, performance,
  binary e2e, and build. `internal/observability` coverage is 93.1%.
- Forced PostgreSQL/RLS isolation passes with `hubdb` coverage at 75.1%; both 50,000-case
  cross-workspace fuzz campaigns pass.
- `make release-check` passes after command-scoping `GOPATH` to the configured module-cache root:
  module integrity, GoReleaser configuration, two reproducible four-platform snapshots, SPDX SBOM,
  distribution verification, Homebrew formula, and release-derived multi-arch OCI layout are green.
  No global Go setting was changed.
- Standalone OCI passes. The Helm contract correctly rejects machine-default Helm v4.1.4 and passes
  with the repository-pinned v4.2.3 binary.
- The digest-pinned Kubernetes v1.36.1 two-cluster Kind gate passes in 235.949 seconds.
- CodeRabbit CLI v0.6.5 completes a 10-file uncommitted-diff review with no findings.
- Primary design guidance: [Prometheus instrumentation](https://prometheus.io/docs/practices/instrumentation/)
  and [metric/label naming](https://prometheus.io/docs/practices/naming/).
