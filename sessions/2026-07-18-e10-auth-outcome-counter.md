# E10 F10.1h bounded authentication outcomes

**Builder:** Gnani Rahul · **Branch:** `gnanirahulnutakki/feat/e10-auth-outcome-counter`

**Slice:** E10 F10.1h / [#272](https://github.com/ArdurAI/sith/issues/272) · **Status:** local gates clean; landing pending

**Base:** `origin/dev` at `89cffa5cb6f4e51c8c5f4ef9410f323ee044f493`

## [G] Goal

Add the smallest trustworthy authentication-attempt denominator for Sith's existing local bearer
and browser-session verifier boundaries without expanding the audit or privacy surface.

## [S] Scope and decision

- `sith_auth_attempts_total{outcome="accepted|refused"}` has exactly two preinitialized series.
- `accepted` is emitted immediately after local verifier success, before workspace authorization or
  the protected handler. A valid credential forbidden from a workspace is still accepted authn.
- `refused` is emitted exactly once on each existing uniform HTTP 401 path and also increments the
  legacy unlabeled `sith_auth_refusals_total` counter exactly once.
- The process-supervised audit observer and slog adapter remain refusal-only. Accepted events cannot
  log, write a datagram, increment a delivery-drop counter, or start child work.
- The event and metric contain no credential, reason, tenant, workspace, identity, token, IP, path,
  method, request, error, trace, correlation, authorization, or handler-result dimension.

## [A] Analysis and nonclaims

- Authentication success and workspace authorization are separate decisions. Emitting accepted
  before `Principal.Scope` preserves that boundary and avoids a misleading denominator.
- A single bounded `outcome` label follows Prometheus guidance to expose one logical counter family
  whose known series are initialized at startup.
- This does not cover provider exchange/callback failures, authorization denials, handler outcomes,
  or every future authentication mode.
- This slice publishes counters only. It does not define a ratio, threshold, brute-force detector,
  alert, SLO, error budget, page, listener, Service, exporter, persistence, remote write, retention,
  or cloud resource.
- Runtime cost is one fixed counter increment per completed verifier decision. Existing scraper and
  time-series retention costs remain operator-owned.

## [T] Verification plan

- Unit tests: closed event outcomes, accepted/refused increments, zero-at-start series, invalid-event
  silence, legacy refusal compatibility, and forbidden-label inspection.
- Boundary tests: bearer success, browser success before authorization, all existing refusal paths,
  and observer panic isolation preserve governed HTTP behavior.
- Refusal-sink tests: accepted events produce no log, datagram, or delivery-drop increment.
- Gates: focused and race suites, full CI, forced-RLS/isolation fuzz, release/Helm/OCI/two-cluster
  Kind validation, complete-diff CodeRabbit review, signed DCO/GSTACK commit, exact-head hosted CI
  and CodeQL, empty review/security queues, merge, and exact post-merge `dev` proof.

## Sources

- [Prometheus instrumentation](https://prometheus.io/docs/practices/instrumentation/)
- [Prometheus metric naming](https://prometheus.io/docs/practices/naming/)

## [C] Local verification checkpoint

- Focused package tests and focused race tests pass for `internal/hubserver`,
  `internal/observability`, `internal/auditdelivery`, and `internal/hubruntime`.
- `make ci` passes: formatting, golangci-lint with zero issues, vet, `govulncheck` with no
  reachable vulnerabilities, repository-wide race/coverage, safety policies, eight portable alert
  rules, latency guard, tagged binary e2e, and build. `internal/observability` coverage is 94.7%.
- `make e2e-isolation` passes PostgreSQL 18.4 forced-RLS suites at 76.2% `hubdb` coverage plus both
  50,000-execution cross-workspace fuzz campaigns.
- `make release-check` passes two reproducible Darwin/Linux amd64/arm64 builds, SPDX SBOMs,
  checksums, Homebrew metadata, and the release-derived two-platform OCI layout.
- Pinned Helm 4.2.3 and standalone two-platform OCI contract gates pass.
- The Kubernetes v1.36.1 real two-cluster Kind gate passes in 238.997 seconds. Independent cleanup
  checks find no Kind clusters and no Sith/Kind test containers afterward.
- README review is complete and documents the exact verifier-before-authorization boundary, legacy
  counter compatibility, refusal-only sinks, privacy exclusions, and nonclaims.
- Repeated complete 16-file CodeRabbit reviews have zero findings. The final checkpointed
  secret-signature scan found zero candidates.
