# Session — 2026-07-17 — E10 bounded fleet-read outcomes

**Builder:** Gnani Rahul · **Branch:** `gnanirahulnutakki/feat/e10-fleet-read-outcomes`
**Slice:** E10 F10.1d / [#240](https://github.com/ArdurAI/sith/issues/240) · **Status:** ready for review

## [G] Goal

Create the smallest production signal needed to classify authorized Hub fleet-read coverage
outcomes, without retaining tenant, spoke, resource, selector, or raw-error dimensions and without
claiming freshness or a complete F10.4 SLO.

## [D] Design

- `hubfleet.Source` emits one result only after PEP authorization and the tenant-scoped reader call.
- The closed outcomes are `complete`, `degraded`, `empty`, and `error`. Coverage assessment and the
  result/count consistency check run before the empty check, so malformed zero-request coverage
  fails closed as `degraded`.
- A reader error remains wrapped with its original cause and emits only `error`.
- The optional observer is panic-isolated; metrics cannot change authorization, database behavior,
  response data, or availability.
- `observability.Metrics` preinitializes exactly four aggregate counter series and the production
  runtime injects the same observer into API and console fleet reads.

## [S] Scope boundary

No workspace, spoke, resource, selector, route, principal, trace, snapshot age, or raw error becomes
a label. There is no new listener, Service, ServiceMonitor, PrometheusRule, exporter, remote-write
path, receiver, SLO target, error budget, or alert. Dispatch and PDP signals remain future work.

## [T] Verification plan

- Unit fixtures for complete, stale/incomplete, malformed, empty, and reader-error results.
- Explicit tests that policy refusal emits nothing and observer panic cannot affect success or the
  original reader error.
- Prometheus exposition tests for exact preinitialized series, invalid-value normalization, label
  allowlisting, independent registries, and rollback after a late duplicate registration.
- API, console, and real two-cluster runtime wiring tests.
- Full CI/race, vulnerability, forced PostgreSQL RLS/isolation, release/SBOM reproducibility,
  Helm/OCI, and real two-cluster gates before review and merge.

## [O] Security, operability, and cost

The blast radius is a process-local counter increment after an authorized read. Cardinality is
constant at four series, so scrape and storage cost do not grow with tenants or spokes. The signal
cannot authorize, route, dispatch, or identify a tenant, and the existing loopback-only scrape
boundary remains unchanged.

## [R] Research and trade-offs

- Prometheus metric naming guidance requires one logical quantity per metric and a `_total` suffix
  for accumulating counts: https://prometheus.io/docs/practices/naming/
- Prometheus instrumentation guidance warns that every labelset consumes RAM, CPU, disk, and network
  and recommends keeping most metrics unlabeled or below ten series:
  https://prometheus.io/docs/practices/instrumentation/
- One closed `outcome` label was selected instead of per-gap, tenant, or spoke dimensions. It keeps
  the series count at four while preserving a meaningful aggregate coverage-outcome ratio.
- `complete` means the existing coverage contract reported no gaps; it is not a snapshot-age
  guarantee. Snapshot ages were rejected for this slice because a histogram would require a reviewed
  bucket and sampling contract. The existing tenant-scoped response remains the diagnostic source
  for exact stale and unreachable scopes.

## [V] Local verification

- Focused and repository-wide race suites pass. Full `make ci` passes with pinned golangci-lint
  v2.12.2, govulncheck v1.6.0, and Prometheus promtool v3.13.1: formatting, vet, zero lint findings,
  no reachable vulnerabilities, coverage, shell policies, portable alert tests, performance,
  binary e2e, and build are green. Observability coverage is 92.5%.
- Forced PostgreSQL RLS/isolation passes with 75.2% `hubdb` coverage. Both cross-tenant fuzz campaigns
  complete 50,000 executions; the mutation campaign found one additional interesting corpus input
  and remained green.
- The official Helm v4.2.3 darwin-arm64 archive checksum was verified before the Helm contract ran.
  Standalone multi-architecture OCI validation passes.
- Kind v0.32.0 against the digest-pinned Kubernetes v1.36.1 node image passes the full fleet, OCI,
  and Argo two-cluster gate in 237.454 seconds.
- The aggregate release target reaches the known machine-local `go mod verify` discovery anomaly.
  Its substantive gates pass independently: GoReleaser configuration, two reproducible four-platform
  snapshots, archive and SPDX SBOM validation, Homebrew formula, multi-architecture release OCI
  layout, and identical first/second artifact digests.
- Independent CodeRabbit review first reported zero code findings, then identified one documentation
  ambiguity that conflated complete coverage with freshness. The wording now explicitly rejects that
  guarantee; final review reports zero findings across all 14 changed files. Manual red-team review
  added a result/count consistency guard so corrupt cross-field coverage cannot inflate the complete
  or empty outcomes.
- Notion decision and validation checkpoint:
  https://app.notion.com/p/3a12637edb0781c7b611da38e74f6e30
