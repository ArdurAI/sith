# Session — 2026-07-18 — E10 authentication refusal-only warning

**Builder:** Gnani Rahul · **Branch:** `gnanirahulnutakki/feat/e10-auth-refusal-only-alert`
**Slice:** [#274](https://github.com/ArdurAI/sith/issues/274), E10 [#28](https://github.com/ArdurAI/sith/issues/28) · **Status:** local proof complete

**Base:** `origin/dev` at `4096320d42b512c943615b3da8ef5d1a3ce80839`

---

## [G] Goal

Add one portable, aggregate warning for sustained refusal-only Hub authentication traffic without
inventing a workload-independent ratio, attributing an actor, or claiming attack detection.

## [A] Decision and implementation

- Alert when at least 20 aggregate `refused` attempts and zero `accepted` attempts occur over
  15 minutes, with a continuous 10-minute hold.
- Aggregate away the closed outcome and every scrape/source label; emit only fixed component and
  severity labels plus static annotations.
- Stay quiet when the accepted series is missing. Partial telemetry cannot prove refusal-only
  traffic; the separate missing-telemetry rule remains the metamonitoring signal.
- Treat any accepted verifier decision as suppression, even if later workspace authorization
  denies the request.

## [A] Rejected alternative

A generic five-percent refusal ratio was rejected. OWASP says authentication successes and
failures should be monitored but explicitly rejects one-size-fits-all monitoring and alerting.
Prometheus recommends a small set of simple symptom alerts with slack. Sith has no negotiated
authentication objective or traffic baseline that makes five percent meaningful.

## [S] Security, operability, and cost boundary

No tenant, workspace, actor, identity, intent, trace, request, credential, endpoint, verifier
error, or scrape/source label survives aggregation. The warning does not claim brute force,
credential stuffing, account compromise, an SLO, an error budget, a page, OIDC-provider coverage,
authorization-denial coverage, or monitoring-path health. It adds one expression evaluated once
per minute over existing fixed-cardinality series and at most one warning instance, with no runtime
path, listener, Service, exporter, storage, remote write, receiver, credential, network request, or
cloud resource.

## [T] Verification plan

- Go and promtool contracts pin the exact expression, 15-minute window, inclusive 20-refusal
  guard, 10-minute hold, static annotations, fixed labels, and ninth-rule limit.
- Fixtures prove sustained firing/resolution, hostile-label aggregation, the exact boundary, and
  silence for missing/partial data, low volume, accepted traffic, transient bursts, and resets.
- Remaining gates: repeated complete-diff review, signed DCO/GSTACK, exact-head hosted gates, merge,
  empty review/security queues, and exact post-merge `dev` proof.

## Primary sources

- [OWASP Authentication Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Authentication_Cheat_Sheet.html)
- [OWASP Logging Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Logging_Cheat_Sheet.html)
- [Prometheus alerting practices](https://prometheus.io/docs/practices/alerting/)

## [C] Checkpoint

Notion and the curated Obsidian decision are synchronized. The rule, contracts, fixtures, and
operator documentation are implemented locally.

- Pinned Prometheus 3.13.1 validates all nine rules and every fixture.
- The focused observability package passes with the race detector.
- The first wider shell-policy run correctly failed because its bounded alert count remained at
  eight. Updating that explicit contract to nine fixed the only failure; the complete shell-policy
  suite now passes.
- Fixtures directly prove the combined expression, exact 20-refusal boundary, 10-minute pending
  state, accepted-event suppression, hostile-label aggregation, partial-data silence, low volume,
  transient recovery, and counter-reset safety.

## [T] Full local proof

- `make ci` passes formatting, lint with zero findings, vet, reachable-vulnerability analysis with
  no findings, repository-wide race/coverage, shell policies, all nine rules, performance, tagged
  binary end-to-end, and build. `internal/observability` coverage is 94.7%.
- PostgreSQL 18.4 forced-RLS tests pass at 76.2% `hubdb` coverage, followed by both 50,000-case
  cross-workspace isolation fuzz campaigns.
- Two reproducible GoReleaser builds, SPDX SBOMs, checksums, Homebrew metadata, and the
  release-derived two-platform OCI layout pass.
- Pinned Helm 4.2.3 and the standalone Linux amd64/arm64 OCI contract pass.
- The digest-pinned Kubernetes v1.36.1 two-cluster fan-out, OCI, and Argo projection gate passes in
  241.828 seconds. Independent cleanup finds no Kind clusters or Kind containers.
- One unrelated auto-remove `sith-local-ops` container created at `2026-07-18T01:07:24Z` was already
  running and was left untouched; it is not evidence from or residue of this cluster run.

README review is complete. The first 25,756-byte diff/secret inspection found zero signature
candidates. The first complete eight-file CodeRabbit review found one valid minor stale-status list
in this journal; this correction removes gates that the later evidence already proves passed.
Repeated final secret inspection/review, signing, hosted gates, merge, and exact post-merge proof
remain pending.

The corrected 25,907-byte candidate has zero secret-signature candidates, and the second complete
eight-file CodeRabbit review reports zero findings. This journal-only evidence update is included
in one final scan and complete review before the source tree is frozen for signing.
