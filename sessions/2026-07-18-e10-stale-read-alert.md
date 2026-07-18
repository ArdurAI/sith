# Session — 2026-07-18 — E10 proven-stale fleet-read warning

**Builder:** Gnani Rahul · **Branch:** `gnanirahulnutakki/feat/e10-stale-read-alert`
**Slice:** [#262](https://github.com/ArdurAI/sith/issues/262), E10 [#28](https://github.com/ArdurAI/sith/issues/28) · **Status:** locally verified

---

## [G] Goal

Add one portable aggregate warning over the bounded F10.1f request-time freshness counter without
claiming continuous monitoring, a per-spoke series, an SLO, an error budget, a page, a receiver, or
a new scrape path.

## [A] Decision and implementation

- Alert when `stale / (fresh + stale) > 0.05` over 15 minutes.
- Require at least 20 eligible `fresh|stale` reads and a continuous 10-minute hold.
- Exclude `unknown`, `error`, and `empty` from numerator and denominator because none proves age.
- Aggregate away every source label and emit only the fixed `component` and `severity` labels.
- Keep this warning distinct from coverage degradation: a complete result can still be stale, and
  an unknown result is not evidence of staleness.

## [S] Security, operability, and cost boundary

The rule exposes no tenant, workspace, spoke, cluster, resource, principal, trace, endpoint, age,
credential, or raw-error label. It adds one expression evaluated once per minute over five existing
fixed-cardinality series and at most one warning instance per evaluator. It creates no recording
series, listener, exporter, Service, monitoring CRD, storage, remote-write path, receiver, network
request, credential path, or cloud resource.

## [A] Primary references

- [Prometheus alerting practices](https://prometheus.io/docs/practices/alerting/)
- [Prometheus recording rules](https://prometheus.io/docs/practices/rules/)

## [T] Verification

Behavioral fixtures cover sustained firing and resolution, hostile-label aggregation, missing and
excluded-only data, minimum-volume and strict-threshold boundaries, transient recovery, and counter
reset behavior.

- Pinned Prometheus 3.13.1 accepts all seven rules and every deterministic fixture.
- Full CI passes with zero lint findings and no reachable vulnerabilities.
- PostgreSQL 18.4 forced-RLS coverage is 72.8%; the broader isolation suite is 76.2%.
- Both 50,000-case cross-workspace fuzz campaigns pass.
- Reproducible archives, SPDX SBOMs, Helm 4.2.3, and cross-platform OCI checks pass.
- Kubernetes v1.36.1 two-cluster Kind passes in 236.710 seconds.
- CodeRabbit reviewed all eight changed files and returned zero findings.

Hosted exact-head and post-merge gates remain required before closure.

## [C] Checkpoint #1

Create the signed implementation commit with
`GSTACK-Checkpoint: 2026-07-18/e10-stale-read-alert#1`; next require hosted exact-head CI, CodeQL,
empty review and security queues, merge into `dev`, and exact post-merge proof before closing #262.
