# Session — 2026-07-12 — P1 observability metrics

**Builder:** Gnani Rahul · **Branch:** `gnanirahulnutakki/feat/p1-observability-metrics`
**Slice:** [#119](https://github.com/ArdurAI/sith/issues/119), E10 [#28](https://github.com/ArdurAI/sith/issues/28) · **Status:** in progress

---

## [G] Goal

Add the first F10.1 self-observability boundary: bounded Prometheus exposition for Sith's own
policy and federated-snapshot behavior. It must be embeddable by the future hub composition root,
without starting a listener, exporting remotely, persisting telemetry, or creating a telemetry
lake.

## [D] Design

- `internal/observability` will own a caller-supplied, non-global Prometheus registry and expose an
  `http.Handler` only. The future hub owns bind address, scrape authorization, readiness, and TLS.
- The PEP and snapshot collector will depend on tiny passive observer interfaces defined at their
  existing package boundaries. A no-op observer is the default, so metrics cannot alter
  authorization, auditing, transport, or persistence behavior.
- Labels are closed vocabulary only: policy verb/outcome and snapshot outcome. Workspace, actor,
  role, cluster, spoke, resource, selector, endpoint, credential, raw error, request body, and
  policy argument digest are forbidden from exposition.
- Registry setup uses explicit error-returning registration rather than global state or
  `MustRegister`, so duplicate or inconsistent metric definitions fail construction predictably.

## [R] Primary sources and constraints

- The Prometheus Go client supports custom registries and `promhttp.HandlerFor`, avoiding default
  global state and enabling isolated tests: <https://pkg.go.dev/github.com/prometheus/client_golang/prometheus>.
- Prometheus recommends low-cardinality labels and specifically rejects user IDs and other unbounded
  sets as labels: <https://prometheus.io/docs/practices/instrumentation/>.
- `docs/EPICS.md` F10.1 requires metrics about Sith itself only; it explicitly excludes retaining
  other systems' series. `internal/privacy/boundary_test.go` forbids telemetry SDKs and makes any
  new HTTP surface an explicit, reviewed boundary.

## [T] Planned evidence

- Unit and race tests for policy allow/refuse/error and snapshot success/closed-failure outcomes.
- A real `httptest` scrape proving the declared exposition, fixed labels, no global registry
  cross-test leakage, and hostile-input non-leakage.
- Repository CI, forced-RLS isolation, release reproducibility, and the real two-cluster kind gate
  after implementation.

## [S] Out of scope

No hub listener, bind address, readiness/health claim, database probe, remote write/exporter,
OpenTelemetry tracing SDK, alert rule, persistent metric store, or external telemetry data.

## [V] Validation evidence

- Focused race tests cover the new observability adapter, PEP observer seam, snapshot observer seam,
  and privacy boundary. The full `go test -race -count=1 ./...`, `go mod verify`, and
  `govulncheck ./...` pass; `internal/observability` coverage is 91.8% in `make ci`.
- `make ci` passes format, lint, vet, module verification, vulnerability scanning, and the complete
  race suite. `make e2e-isolation` passes the forced PostgreSQL RLS suite and its fixed 50,000-case
  cross-workspace fuzz campaign. `make release-check` passes two reproducible four-platform builds,
  SPDX SBOM verification, distribution validation, and Homebrew formula rendering.
- The real two-cluster `make e2e-kind KIND=/Volumes/EXTENDED/MacData/tools/bin/kind` gate passes:
  `TestKindFleetFanout` completed successfully in 79.815 seconds. A first attempt used a nonexistent
  `$GOPATH/bin/kind` path and never reached the test; it is a local tool-path setup failure, not a
  product signal.
- Manual red-team review verified closed-label normalization, no global Prometheus registry, no
  listener/exporter/persistence, and panic isolation. It found a partial-registration rollback risk;
  construction now unregisters only its own prior collectors on an error and a regression proves a
  later retry succeeds. CodeRabbit CLI is not installed, so no external diff was sent.
- Final cleanup leaves zero kind clusters. A non-volume Docker prune removed only unused test
  artifacts and reclaimed 1.217 GB; the two pre-existing active user containers remained running.

## [C] Commit readiness

- `README.md` was reviewed after implementation. It correctly describes `sith hub` as a staged
  runtime and no user-reachable command, listener, or installation behavior changed, so no README
  edit is warranted for this embeddable-only metrics slice.
- Final GitHub security queues are Dependabot `0`, code-scanning `0`, and secret-scanning `0`.
  The separate ClusterGateway authorization fix remains clean and mergeable upstream, but is not
  merged or released; #103/#104 stay open until that exact release can pass the real Sith M0 test.
