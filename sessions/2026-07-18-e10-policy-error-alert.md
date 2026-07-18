# Session — 2026-07-18 — E10 policy-decision error warning

**Builder:** Gnani Rahul · **Branch:** `gnanirahulnutakki/feat/e10-policy-error-alert`
**Slice:** [#264](https://github.com/ArdurAI/sith/issues/264), E10 [#28](https://github.com/ArdurAI/sith/issues/28) · **Status:** local proof complete

---

## [G] Goal

Add one portable aggregate warning for sustained fail-closed PEP decision errors without claiming
external Ardur PDP latency, dispatch success, an SLO, an error budget, or a page.

## [A] Decision and implementation

- Alert when `error / (allow + deny + require-approval + error) > 0.05` over 15 minutes.
- Require at least 20 eligible `allow|deny|require-approval|error` decisions and a continuous
  10-minute hold.
- Treat `deny` and `require-approval` as valid policy results in the denominator only.
- Aggregate away `verb` and every source label; emit only fixed component and severity labels.
- Keep the existing single-event critical policy-audit alert as the immediate sink-failure signal.

## [S] Security, operability, and cost boundary

The warning exposes no tenant, workspace, actor, identity, intent, trace, request, verb, reason,
credential, endpoint, selector, or raw-error label. It adds one expression evaluated once per
minute over existing fixed-cardinality series and at most one warning instance. It creates no
runtime path, recording series, listener, exporter, Service, monitoring CRD, storage, remote-write
path, receiver, network request, credential path, or cloud resource.

## [A] Primary references

- [Prometheus alerting practices](https://prometheus.io/docs/practices/alerting/)
- [Prometheus recording-rule aggregation guidance](https://prometheus.io/docs/practices/rules/)

## [T] Proof

Behavioral fixtures cover sustained firing and resolution, hostile-label aggregation, missing and
low-volume data, strict-threshold and inclusive-volume boundaries, ordinary deny/approval outcomes,
transient recovery, counter resets, and exclusion of a high-volume unknown outcome from the closed
denominator. Pinned Prometheus 3.13.1 validates all eight rules and fixtures.

- Focused rule, Go contract, and tooling-policy checks: passed.
- The immutable-commit CodeRabbit review found two minor fixture gaps: the ordinary-denial case did
  not prove those valid outcomes affected the denominator, and the transient case never entered
  the pending state. Both adversarial controls are now explicit; the correction review reports
  zero findings and the full CI gate passes again.
- `make ci`: passed, including formatting, lint, vet, `govulncheck`, race tests, policy tests, and
  the tagged end-to-end suite.
- PostgreSQL 18.4 forced-RLS, both 50,000-case workspace-isolation fuzz campaigns, reproducible
  release/SPDX SBOM, Helm 4.2.3, and cross-platform OCI gates: passed.
- Digest-pinned Kubernetes v1.36.1 Kind fleet, OCI, and Argo projection gate: passed in 239.180s.

Hosted exact-head CI/CodeQL, empty review/security queues, merge, and exact post-merge `dev` proof
remain required before closure.

## [C] Checkpoint #1

`2026-07-18/e10-policy-error-alert#1` records the complete local proof above on exact base
`00a9489e257c131fa734b9a672c2b1f0af552748` before the signed DCO/GSTACK commit.
