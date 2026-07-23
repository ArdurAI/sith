# Session — 2026-07-17 — E10 policy-audit metrics

**Builder:** Gnani Rahul · **Branch:** `gnanirahulnutakki/feat/policy-audit-metrics`
**Slice:** E10 F10.1c / [#234](https://github.com/ArdurAI/sith/issues/234) · **Status:** ready for review

## [G] Goal

Expose the fail-closed policy-audit sinks through Sith's existing isolated, loopback-only
Prometheus registry so operators can distinguish database and process-log failures and latency
without identity-bearing or caller-controlled labels.

## [D] Design

- `pep` owns a typed passive audit-observer contract and an auditor decorator. The decorator calls
  the authoritative sink first, reports its exact success/error result afterward, and isolates an
  observer panic without changing the returned audit error.
- The runtime decorates the durable database and structured process-log sinks separately before
  passing them to the existing database-first ordered auditor. A durable failure therefore emits no
  false process attempt.
- `observability.Metrics` implements the contract with two fixed metric families and only the
  closed `durable|process` and `success|error` vocabularies. Invalid values are discarded.
- The registry remains non-global and pull-only. No listener, Service, ingress, exporter, remote
  write, persistence, tenant label, or external-system telemetry is added.

## [R] Research and trade-offs

- Prometheus recommends application-prefixed names, base-unit suffixes, and avoiding high-cardinality
  labels: https://prometheus.io/docs/practices/naming/
- Prometheus instrumentation guidance treats cardinality as a primary operating cost:
  https://prometheus.io/docs/practices/instrumentation/
- Histograms preserve aggregatable latency distributions; Sith retains the repository's existing
  classic-histogram convention: https://prometheus.io/docs/practices/histograms/
- The bounded cost is four counter label combinations plus four fixed histogram combinations. No
  tenant-proportional series or new infrastructure cost is introduced.

## [T] Planned evidence

- Unit and race tests for success, error preservation, invalid configuration, and observer panic.
- Ordered integration tests proving durable failure suppresses the process attempt and process
  failure follows durable success.
- A real scrape and label allowlist proving hostile values do not create series or leak data.
- Full CI, forced PostgreSQL RLS isolation, vulnerability scan, reproducible release/SBOM, Helm,
  and real two-cluster fan-out gates before review and merge.

## [V] Local validation

- Focused race tests and `go vet` pass for `internal/pep`, `internal/observability`, and
  `internal/hubruntime`.
- Full `make ci` passes with the repository-pinned golangci-lint v2.12.2: zero lint issues, no
  vulnerabilities, the complete race suite, script policy checks, performance budget, binary e2e,
  and build.
- `make e2e-isolation` passes the PostgreSQL forced-RLS suite at 75.2% `hubdb` coverage and both
  fixed 50,000-case cross-workspace fuzz campaigns.
- The canonical local release target reaches the known machine-wide `go mod verify` discovery
  anomaly even though `go env GOMOD` resolves this checkout. Its substantive steps pass
  independently: two four-platform GoReleaser snapshots, distribution and SPDX SBOM verification,
  Homebrew formula, multi-architecture OCI layout, and identical artifact digests. Clean hosted CI
  remains the authoritative module-verification gate.
- Helm v4.2.3 was downloaded from `get.helm.sh` and verified against its official checksum; the
  pinned Helm chart contract and cross-platform OCI contract pass. The real two-cluster Kind fleet,
  OCI, and Argo projection suite passes in 238.583 seconds.

## [C] Review checkpoint

- Manual red-team review confirmed that the observer receives no event contents, error value,
  tenant identity, or request metadata; invalid typed values create no series; and a panic cannot
  replace the authoritative sink result.
- The runtime wraps each sink before the existing ordered composition, so durable failure still
  prevents process delivery and its metric, while process failure remains distinguishable after a
  successful durable append.
- CodeRabbit reviewed all eight implementation and validation files in the uncommitted diff and
  reported zero findings.
