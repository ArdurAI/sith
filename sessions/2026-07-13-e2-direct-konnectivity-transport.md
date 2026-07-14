# Session — 2026-07-13 — e2-direct-konnectivity-transport

**Builder:** Gnani Rahul · **Branch:** `gnanirahulnutakki/feat/e2-direct-konnectivity-transport`
**Slice(s):** E2 / [#123](https://github.com/ArdurAI/sith/issues/123) · **Status:** ready-to-commit

---

[G] Goal: deliver #123, a safe Phase-1 alternative to the blocked ClusterGateway adapter, while
preserving OCM registration, ClusterProxy reverse tunnels, and managed-serviceaccount identity.
No caller authorization header, endpoint, kubeconfig, token, CA, or raw Kubernetes object may
cross the `hubfleet.Transport` seam.

[S] Scope: `internal/hubocm`, the M0 experiment and its safety suite, narrowly reviewed privacy
boundary exceptions, dependencies, and operator-facing documentation. The ClusterGateway-specific
#103/#104 route remains out of scope and blocked pending an official upstream release.

[A] Action: chose the released ClusterProxy `0.10.0`-matched Konnectivity client `v0.31.2`, rather
than an unreleased ClusterGateway fix or a custom agent/tunnel. The adapter treats
`ocm/<managed-cluster-name>` as a pinned identity, reads only the exact rotating
`Secret/<managed-cluster>/sith-reader` projection per snapshot, validates exactly `token` and
`ca.crt`, and never caches or logs credential material.

[A] Action: implemented proxy mTLS with explicit CA/server name, TLS 1.2 minimum, and one client
certificate. The spoke connection requires its CA and a Kubernetes server name; insecure TLS is
rejected. Each TCP dial gets one direct Konnectivity tunnel and closes its private HTTP transport
after the bounded snapshot. The adapter normalizes only Pods, Deployments, and optional Rollouts;
the pagination/resource boundary fails rather than silently returning a partial oversized result.

[A] Action: added `make e2e-ocm` and its direct M0 integration test. The M0 reader RBAC permits
only cluster-wide `list` on Pods, Deployments, and Rollouts; Secrets and Nodes remain denied. The
M0 harness now waits for asynchronously placed addon objects to exist before waiting for
`Available=True`; its safety suite simulates initial `NotFound` responses.

[T] Test: focused race-safe adapter tests cover exact Secret `get`, unsafe projection/configuration
rejection, TLS pinning, credential replacement, no-detail failures, fixed target dialing, tunnel
close, credential-buffer clearing, and normalized snapshot validation. The direct M0 gate passed
repeatedly; its final 152-second run reached both scopes, proved Secrets/Nodes denial and
outbound-only controls, took TLS-verified snapshots, and exercised MSA replacement. The target
removed all kind clusters and its owned scratch directory afterward.

[T] Test: baseline gates passed: `go mod verify`, `govulncheck ./...`, repository-wide
`go test -race`, `make e2e-isolation` (including the fixed 50,000x cross-workspace fuzz campaign),
and `make e2e-kind`. The exact final tree passed `make ci` (including all 16 M0 safety assertions),
`make release-check`, and the direct M0 gate after the addon-race hardening. GitHub Dependabot,
code-scanning, and secret-scanning queues each report zero open alerts.

[R] Review: manual red-team review found and closed two substantive risks before final validation:
the resource limit now fails before another list can be issued, and the addon lifecycle now handles
asynchronous creation. The narrow governed-only privacy allowlist is exercised by the real M0 gate;
no raw credentials or response bodies are present in tests, logs, or this journal.

[C] Checkpoint #1: record the direct ClusterProxy alternative and evidence; next: create the
SSH-signed DCO commit with `GSTACK-Checkpoint: 2026-07-13/e2-direct-konnectivity-transport#1`, then
open a small PR into `dev`.

---

**Session close:** direct alternative ready for review and merge · **Open questions touched:** none
