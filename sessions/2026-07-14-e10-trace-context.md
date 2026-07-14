# E10 F10.2a sanitized local trace context

Issue: [#137](https://github.com/ArdurAI/sith/issues/137)

Branch: `gnanirahulnutakki/feat/e10-trace-context`

Base: `origin/dev` at `710a428dba4939a749c1a78f39bb97eb3cb4231c`

## [G] Goal

Deliver the smallest independently shippable F10.2 foundation for governed reads: a locally minted,
privacy-preserving trace context across the authenticated hub, PEP audit, and present snapshot
transport. It must create no telemetry product or remote data path.

## [S] Scope

- Add an internal trace contract with a cryptographically minted opaque 128-bit ID, two closed
  stages (`pep.decision`, `spoke.snapshot`), four closed outcomes, and a one-hour duration bound.
- Mint the root only after the hub has verified the session and derived its signed workspace scope;
  direct governed callers also receive a root before PEP audit.
- Strip `traceparent`, `tracestate`, B3, `X-B3-*`, and common request/correlation headers before
  the authenticated downstream handler. Do not accept, echo, or forward caller correlation data.
- Carry the ID into the PEP audit event and structured local trace recorder. The recorder emits only
  trace ID, stage, outcome, and duration milliseconds; malformed events do not emit and observer
  faults cannot affect authorization or collection.
- Carry the same context into every bounded current OCM snapshot transport call. Do not record a
  workspace, actor, role, verb, spoke identifier, endpoint, resource, selector, digest, credential,
  raw error, returned data, or arbitrary attributes.

## [A] Analysis and red-team checks

- The repository privacy boundary explicitly rejects OpenTelemetry and other telemetry SDK imports.
  The implementation therefore uses an internal typed contract, not an SDK-shaped façade; it owns no
  network exporter, listener, queue, persistence, trace store, background worker, sampling sink, or
  wire propagation.
- The full F10.2 requirement says `trace_id == intent_id`, but Phase 1 has no typed action intent or
  E6 decision-ledger schema. This issue is explicitly F10.2a and does not fabricate that equality;
  E4/E6 must define it when proposal/approval/dispatch semantics exist.
- Potential carrier injection is handled twice: authentication removes known carrier headers and
  tracing never reads HTTP headers. Trace events cannot accept a generic attribute map, blocking
  accidental leakage through a future caller-supplied label.
- PEP audit remains fail-closed. Trace observation is passive and panic-isolated, so a logger or
  observer fault cannot widen access or stop independent spoke collection.

## [T] Tests and evidence

- Focused package suite: PASS — `go test ./internal/tracing ./internal/observability ./internal/pep ./internal/hubfleet ./internal/hubserver ./internal/hubruntime`.
  It proves ID validation and preservation, event vocabulary/duration rejection, observer panic
  isolation, local slog emission field allowlisting, audit correlation, hostile-header stripping,
  authenticated hub root minting, and PEP-to-real-collector-to-transport propagation.
- Static supply-chain checks: PASS — `go vet ./...`, `go mod verify`, and `govulncheck ./...`.
- `git diff --check`: PASS at the first review checkpoint.

## [C] Checkpoint #1

- CodeRabbit reviewed the complete uncommitted diff against `710a428` after a credential-pattern
  scan and returned zero findings across all 22 changed files. Its explicit review scope covered
  carrier injection, trace privacy, observer fault isolation, and accidental `intent_id` claims.
- `make ci`: PASS after one test-only staticcheck correction. It ran gofmt, golangci-lint (0 issues),
  `govulncheck` (no vulnerabilities), the full race/coverage suite, static source boundaries,
  binary E2E, latency check, and reproducible build. Relevant final coverage: tracing 86.8%,
  observability 92.7%, PEP 83.9%, hubfleet 69.6%, and hubserver 89.5%.
- `make e2e-isolation`: PASS. It ran race-enabled pinned PostgreSQL isolation/destructive suites
  (hubauth 85.2%, hubserver 89.5%, fleetcache 87.0%, hubdb 73.5%) and the fixed 50,000-execution
  workspace selector fuzz campaign.
- `make release-check`: PASS. GoReleaser reproduced Darwin/Linux amd64/arm64 snapshot archives,
  generated SPDX SBOMs/checksums twice, and generated `dist/sith.rb`.
- `make e2e-kind KIND=/Volumes/EXTENDED/MacData/tools/bin/kind`: PASS (165.233s) against temporary
  real two-cluster fan-out and OCI image contract gates. `kind get clusters` is empty after test
  cleanup.
- Final local cleanup: `docker system prune -af` reclaimed 2.226 GB of unused test/build artifacts;
  active unrelated `elated_antonelli` and `ardur-191-baseline` containers remain running.
- Final GitHub security queues before commit: Dependabot 0, code-scanning 0, secret-scanning 0.

## [C] Checkpoint #2

- Source is ready for README recheck, signed/DCO/GSTACK commit `2026-07-14/e10-trace-context#1`,
  PR, review/CI, exact post-merge CI, and issue/roadmap updates.
