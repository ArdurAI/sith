# Session — 2026-07-17 — E10 database-aware Hub readiness

**Builder:** Gnani Rahul · **Branch:** `gnanirahulnutakki/feat/e10-database-readiness`
**Slice:** E10 F10.5a / [#244](https://github.com/ArdurAI/sith/issues/244) · **Status:** ready for review
**Decision record:** [Notion](https://app.notion.com/p/3a12637edb0781cea875d0e5aba58aa2)

## [G] Goal

Replace the Hub chart's TCP-only readiness claim with a bounded application-level decision that
detects PostgreSQL loss without turning a dependency outage into a restart storm.

## [D] Design

- `GET /healthz` is dependency-free process liveness on the existing TLS listener.
- `GET /readyz` calls only the existing least-privilege application pool's `Ping` under a
  server-owned one-second deadline.
- Both routes are unauthenticated because kubelet probes have no Sith identity, but they accept
  only an exact GET with no query or encoded-path variation and emit only fixed empty responses.
- Database errors, endpoints, credentials, tenant state, and panics collapse to one empty `503`.
- OCM and spoke reachability are excluded: partial reachability belongs in fleet coverage and must
  not make the whole Hub unavailable.
- The chart uses HTTPS `httpGet` probes on the named `https` port with explicit timeout, success,
  and failure thresholds. It adds no listener, Service, secret, metric, or cloud resource.

## [R] Research and trade-offs

- Kubernetes distinguishes readiness, which removes a Pod from Service traffic, from liveness,
  which restarts it. The current primary guide also documents HTTPS probes and minimal dedicated
  endpoints: https://kubernetes.io/docs/tasks/configure-pod-container/configure-liveness-readiness-startup-probes/
- pgxpool `Ping` acquires a connection and executes an empty SQL statement, which directly tests
  the existing pool without crossing a tenant query boundary:
  https://pkg.go.dev/github.com/jackc/pgx/v5/pgxpool#Pool.Ping
- Coupling PostgreSQL to liveness was rejected because a shared database outage could restart every
  Hub replica. A separate plaintext probe listener was rejected because Kubernetes supports HTTPS
  probes and the Hub already owns the required TLS listener.

## [S] Scope boundary

No OCM call, spoke call, tenant query, RLS scope, error body, dependency label, listener, Service,
Ingress, exporter, credential, database topology change, or cloud resource is added.

## [T] Verification plan

- Focused race tests for `internal/hubserver`, `internal/hubruntime`, and `internal/hubdb`.
- Helm v4.2.3 lint/render contract for both profiles and optional browser/metrics combinations.
- Full `make ci`, forced PostgreSQL/RLS isolation, release checks, standalone OCI/Helm gates, and
  the digest-pinned Kubernetes v1.36.1 two-cluster Kind test.
- Independent secret/diff review and CodeRabbit review before merge; exact post-merge `dev` CI,
  CodeQL, and GitHub security queues before issue closure.

## [V] Local verification

- Focused race tests pass for `internal/hubserver`, `internal/hubruntime`, `internal/hubdb`, and
  the production privacy boundary. Probe fixtures cover ready, dependency error, timeout, caller
  cancellation, panic, wrong method, query, trailing path, encoded path, nil construction, and
  authenticated fleet fallback isolation.
- Official Helm v4.2.3 lint/render validation passes for light/heavy, browser OIDC, loopback
  metrics, and combined browser-plus-metrics modes. The rendered contract requires HTTPS
  `/healthz` and `/readyz`, the named `https` port, explicit one-second timeouts, and no TCP probe.
- Full `make ci` passes with zero lint findings, no reachable vulnerabilities, all race tests,
  privacy boundaries, shell policies, Prometheus rules, warm-cache performance, binary smoke,
  normal e2e, and build.
- Forced PostgreSQL/RLS isolation passes with 75.1% hub-db coverage; both cross-workspace fuzz
  campaigns complete 50,000 executions with no new failure.
- The digest-pinned Kubernetes v1.36.1 two-cluster gate passes in 240.5 seconds. Standalone
  multi-architecture OCI validation also passes.
- The aggregate `make release-check` wrapper reproduces the known machine-local `go mod verify`
  module-discovery anomaly. Every substantive stage passes in isolated invocations: GoReleaser
  config, two clean four-platform snapshots, archive/SPDX SBOM validation, Homebrew formula,
  release-derived linux/amd64+linux/arm64 OCI layout, and exact digest equality.
- CodeRabbit's complete independent review reports no findings across all thirteen changed files.

Hosted PR checks, review, and exact post-merge `dev` proof remain pending.
