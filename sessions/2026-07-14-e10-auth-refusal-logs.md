# E10 F10.3a sanitized hub authentication-refusal logs

Issue: [#139](https://github.com/ArdurAI/sith/issues/139)

Branch: `gnanirahulnutakki/feat/e10-auth-refusal-logs`

Base: `origin/dev` at `1ecfb56be314e7d01a9c2d2eec38dad2ddc56496`

## [G] Goal

Deliver the smallest independently shippable F10.3 foundation for a security-relevant hub
authentication signal: one sanitized local structured log on pre-principal credential refusal.
It must create no request logging, authentication oracle, telemetry product, or remote data path.

## [S] Scope

- Add a closed `hubserver` authentication event with exactly one outcome (`refused`) and a passive,
  panic-isolated observer seam.
- Emit the same event for every missing, malformed, ambiguous, or invalid bearer credential before
  a verified principal exists. Valid authentication emits no event.
- Add the `slog` adapter only at the hub runtime composition root. It emits WARN with the fixed
  `hub-auth` surface and `refused` outcome.
- Keep all request metadata, token/header text, URL/path/query, remote address, workspace,
  principal, verifier error, caller correlation carrier, trace ID, metric, listener, exporter,
  persistence, rate limiter, and audit-ledger change out of scope.

## [A] Analysis and red-team checks

- A rejected request has not established a signed workspace scope. The implementation deliberately
  neither accepts nor mints a trace/correlation ID there; F10.2a trace context remains strictly
  post-authentication.
- The event has no generic attributes, error string, or metadata field. Its validator accepts only
  the fixed outcome, preventing a future caller-controlled logging path through this seam.
- The event does not disclose credential failure mode. Missing credentials, wrong scheme,
  ambiguity, and verifier failure each produce the existing identical HTTP 401 response plus the
  same one-value observer event, preventing a logging-based authentication oracle.
- The observer is passive: invalid events are dropped and observer panics are recovered before the
  existing uniform response is written. The hub runtime owns the sink; collectors and business
  handlers receive no logging dependency.
- CodeRabbit reviewed the complete staged ten-file diff. It suggested a server-generated receipt
  correlation ID and a bounded asynchronous delivery queue. The receipt conflicts with the
  explicit pre-signed-scope no-correlation rule and was rejected. The queue would add an out-of-
  scope worker, queue, capacity, and shutdown contract; it is recorded separately as #140 rather
  than being hidden inside this narrow slice.

## [T] Tests and evidence

- Focused suites: PASS — `go test ./internal/hubserver ./internal/observability ./internal/hubruntime`
  and `go test -race -count=1 ./internal/hubserver ./internal/observability ./internal/hubruntime`.
  They prove uniform refusal events, valid-session silence, hostile token-like input exclusion,
  malformed-event rejection, observer panic isolation, JSON/text allowlisted log fields, and
  handler configuration propagation.
- Repository race suite: PASS — `go test -race -count=1 ./...`.
- Supply-chain checks: PASS — `go mod verify` and `govulncheck ./...` (no vulnerabilities found).
- `make ci`: PASS — gofmt, golangci-lint (0 issues), vet, vulnerability scan, full race/coverage,
  M0 safety checks, UI latency guard, and tagged e2e. Relevant coverage: hubserver 90.1% and
  observability 93.4%.
- `make e2e-isolation`: PASS — race-enabled pinned PostgreSQL/RLS suites (hubauth 85.2%, hubserver
  90.1%, fleetcache 87.0%, hubdb 73.5%) and fixed 50,000-execution workspace fuzz campaign.
- `make release-check`: PASS — reproducible Darwin/Linux amd64/arm64 snapshot archives, SPDX SBOMs,
  checksums, and release distribution validation.
- `make e2e-kind KIND=/Volumes/EXTENDED/MacData/tools/bin/kind`: PASS — the controlled rerun exited
  0 in 156.669s for real two-cluster fan-out and OCI image contract gates. `kind get clusters` is
  empty after cleanup; only unrelated `elated_antonelli` and `ardur-191-baseline` containers remain.
- GitHub security queues at slice start: Dependabot 0, code-scanning 0, secret-scanning 0.
- CodeRabbit staged-diff review: completed with two suggestions. Both were independently reviewed;
  no in-scope defect remains. The bounded nonblocking-delivery follow-up is [#140](https://github.com/ArdurAI/sith/issues/140).

## [C] Checkpoint #1

- Source, README, tests, local/red-team review, and full validation are ready for the signed
  `2026-07-14/e10-auth-refusal-logs#1` commit. External CodeRabbit review is in progress and must
  be resolved before PR creation.
