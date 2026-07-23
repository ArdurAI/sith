# E10 F10.1g bounded authentication-refusal metric

**Builder:** Gnani Rahul · **Branch:** `gnanirahulnutakki/feat/e10-auth-refusal-metric`

**Slice:** E10 F10.1g / [#266](https://github.com/ArdurAI/sith/issues/266) · **Status:** in progress

**Base:** `origin/dev` at `15656aaf47331775e5619fc11d431bfd3fdf5dcf`

## [G] Goal

Add the smallest truthful E10 security-observability substrate: one process-wide count of requests
refused by the existing sanitized bearer/session middleware boundary.

## [S] Scope and decision

- `sith_auth_refusals_total` is a preinitialized Prometheus counter with zero labels.
- One valid `hubserver.AuthEvent{Outcome: refused}` produces one increment.
- Runtime fanout delivers independently to the existing process-local audit observer and the metric
  observer, with panic isolation per destination.
- The existing uniform HTTP 401 response and structured local refusal delivery remain unchanged.
- No credential mode, failure reason, tenant, workspace, actor, principal, token, IP, path, request,
  trace, or correlation data enters the metric.

## [A] Analysis and nonclaims

- Authentication refusal precedes a trusted principal and workspace, so adding identity or request
  labels would create both a sensitive-data boundary and attacker-controlled cardinality.
- A refusal ratio needs a trustworthy success or total-attempt denominator. The current observer
  deliberately emits only a sanitized refusal, so this slice does not invent a ratio or threshold.
- This does not cover successful authentication, OIDC provider exchange/callback failures,
  authorization denials, or every future authentication mode.
- This is not a brute-force detector, alert, SLO, error budget, page, or complete security-monitoring
  control. It adds no listener, Service, exporter, persistence, remote write, cloud resource, or
  retention of other systems' telemetry.
- Increment and fanout overhead is constant. Operators retain responsibility for existing scrape
  collector and storage cost.

## [T] Verification plan

- Unit tests: closed event validation, defensive fanout copy, configuration rejection, per-observer
  panic isolation, zero-at-start, exact increment, invalid-event silence, and forbidden-label scan.
- Handler tests: bearer/API and browser-session/console refusals remain uniform; valid auth is silent.
- Runtime review: the same composite observer is passed to fleet, audit-export, and console handlers;
  only the process observer owns shutdown.
- Gates: focused and race suites, full CI, vulnerability analysis, release/Helm/OCI/e2e gates,
  CodeRabbit complete-diff review, signed DCO/GSTACK commit, exact-head CI/CodeQL, empty review and
  security queues, merge, and exact post-merge `dev` CI/CodeQL.

## Sources

- [Prometheus instrumentation](https://prometheus.io/docs/practices/instrumentation/)
- [Prometheus metric naming](https://prometheus.io/docs/practices/naming/)
- [OWASP Logging Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Logging_Cheat_Sheet.html)

## [C] Local verification checkpoint

- Focused package tests and focused race tests pass for `internal/hubserver`,
  `internal/observability`, and `internal/hubruntime`.
- `make ci` passes with the pinned EXTENDED-hosted Prometheus 3.13.1 binary: formatting,
  golangci-lint with zero issues, vet, `govulncheck` with no reachable vulnerabilities, repository
  race/coverage, all safety policies, eight portable alert rules, latency guard, tagged binary e2e,
  and build. `internal/observability` coverage is 94.4%.
- The first `make ci` attempt reached the alert-rule gate after code and security checks, then
  stopped because `promtool` was absent from that shell's PATH. The corrected run used the verified
  EXTENDED binary and passed without a source change.
- `make e2e-isolation` passes PostgreSQL 18.4 forced-RLS suites plus both 50,000-execution workspace
  fuzz campaigns.
- `make release-check` passes reproducible Darwin/Linux amd64/arm64 archives, SPDX SBOMs, checksums,
  Homebrew metadata, and the release-derived multi-platform OCI layout.
- Pinned Helm 4.2.3 and cross-platform OCI contract gates pass.
- The Kubernetes v1.36.1 two-cluster Kind gate passes in 241.386 seconds; `kind get clusters` is
  empty afterward.
- Two complete CodeRabbit passes found only overview wording ambiguity. Both findings were fixed by
  naming the current signal a sanitized authentication-refusal count and keeping future derived
  rates conditional on trustworthy denominators. The final complete ten-file review has no
  findings.
