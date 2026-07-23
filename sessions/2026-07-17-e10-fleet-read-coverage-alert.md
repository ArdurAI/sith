# Session — 2026-07-17 — E10 fleet-read coverage alert

**Builder:** Gnani Rahul · **Branch:** `gnanirahulnutakki/feat/e10-fleet-read-alert`
**Slice:** E10 F10.4b / [#242](https://github.com/ArdurAI/sith/issues/242) · **Status:** ready for review

## [G] Goal

Turn the bounded F10.1d fleet-read outcome counter into one actionable aggregate warning without
claiming snapshot freshness, selecting an SLO target, or adding tenant-proportional observability.

## [D] Design

- The numerator is `degraded|error`; the eligible denominator is `complete|degraded|error`.
- `empty` is excluded from both sides because a consistent zero-scope read is legitimate and must
  neither fire nor dilute the alert.
- The warning requires more than 5% adverse outcomes among at least 20 eligible
  `complete|degraded|error` reads over 15 minutes, sustained for 10 minutes.
- A top-level aggregate removes the closed outcome and arbitrary scrape labels before alert
  creation. Output labels and annotations are fixed strings.
- The existing operator-owned loopback scrape, rule evaluator, Alertmanager, receiver, and
  metamonitoring boundaries remain unchanged.

## [R] Research and trade-offs

- Prometheus recommends a small number of symptom-oriented, actionable alerts with slack for small
  blips: https://prometheus.io/docs/practices/alerting/
- Google SRE treats an SLO target as a stakeholder/product decision and calls out low-traffic error
  budget alerting as a special case: https://sre.google/workbook/alerting-on-slos/
- A warning symptom alert with a minimum-volume guard was selected instead of a page or formal burn
  alert. There is no negotiated target, traffic baseline, or response owner for a defensible SLO.
- Treating `empty` as an error was rejected because an internally consistent zero-scope workspace
  is valid. Including it only in the denominator was rejected because it could hide degradation.

## [S] Scope boundary

No workspace, tenant, spoke, resource, selector, principal, trace, endpoint, age, or raw-error label
is added. There is no Service, ServiceMonitor, PrometheusRule CRD, exporter, remote-write path,
receiver, dashboard, freshness guarantee, dispatch/PDP signal, SLO target, or error budget.

## [T] Verification plan

- Promtool fixtures for sustained mixed degradation/error, legitimate empty reads, absent series,
  low traffic, exact 5%, exact minimum volume, transient recovery, resolution, counter reset, and
  hostile source labels.
- Repository contract tests for rule count/limit, canonical expression, fixed output labels, static
  annotations, runbook anchor, and forbidden dynamic/CRD patterns.
- Full CI/race, vulnerability, forced RLS isolation, release/SBOM reproducibility, Helm/OCI, real
  two-cluster Kind, independent review, hosted CI/CodeQL, security queues, and exact post-merge
  `dev` proof before closeout.

## [O] Security, operability, and cost

The blast radius is one additional aggregate warning. Four existing counter series feed one short
expression per minute and at most one new alert instance. There is no cloud resource, spoke egress,
listener, storage, or tenant-proportional cost.

## [K] Knowledge checkpoint

Notion decision record:
https://app.notion.com/p/3a12637edb0781f2b206fd00d891ba31

## [V] Local verification

Local verification is complete on base `be442ff44630ac4ba80a3ebc7869a6ec591d5ffa`; hosted PR checks
and exact post-merge `dev` proof remain pending until the reviewed commit is pushed.

- `make test-alert-rules PROMTOOL=/tmp/sith-prometheus-3.13.1/promtool` validates four rules and all
  fixtures. The new fixtures cover
  mixed degradation/error, hostile input labels, high empty traffic, empty-only and absent series,
  low eligible volume, exact 5%, exact 20, transient recovery, resolution, and counter reset.
- `make ci` passes with pinned golangci-lint v2.12.2, govulncheck v1.6.0, and promtool v3.13.1:
  formatting, vet, zero lint findings, no reachable vulnerabilities, race/coverage, shell policies,
  performance, binary e2e, OCI instruction contract, and build are green. Observability coverage is
  92.5%.
- `make e2e-isolation` passes with 75.2% `hubdb` coverage. Both cross-tenant fuzz campaigns complete
  exactly 50,000 executions; the mutation campaign found two additional interesting corpus inputs
  and remained green.
- The live official Helm v4.2.3 darwin-arm64 archive matches SHA-256
  `048ecf5ad3160f83d918f9fe945238d2132b079640f7b106175331c25f242c64`; the fail-closed Helm
  contract passes through `make e2e-helm`. `make e2e-oci` passes standalone multi-architecture OCI
  validation.
- `make e2e-kind KIND=/tmp/sith-tools/kind` passes with Kind v0.32.0 against the digest-pinned
  Kubernetes v1.36.1 node image, covering the fleet, OCI, and Argo two-cluster gate in 240.563
  seconds.
- The aggregate release target reaches the known machine-local `go mod verify` discovery anomaly.
  Direct GoReleaser and releasecheck commands pass the substantive gates independently: config,
  two clean four-platform snapshots, archive and SPDX SBOM validation, Homebrew formula,
  release-derived multi-architecture OCI layout, and exact first/second artifact digest equality.
- CodeRabbit's first complete review identified two documentation issues: shared ratio-alert volume
  terminology and an ambiguous local-versus-hosted verification status. Both were corrected; the
  second complete review reports zero findings across all eight changed files.
