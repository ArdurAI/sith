# Session — 2026-07-14 — e2-hub-direct-runtime

**Builder:** Gnani Rahul · **Branch:** `gnanirahulnutakki/feat/e2-hub-direct-runtime`
**Slice(s):** E2 / [#125](https://github.com/ArdurAI/sith/issues/125) · **Status:** ready-to-commit

---

[G] Goal: deliver the Phase-1 hub runtime for the direct OCM transport: mount only authenticated,
workspace-scoped fleet refresh/read routes, use the existing `hubfleet.Transport` seam, and deploy
only from in-cluster identity and fixed read-only mounts.

[S] Scope: authenticated fleet HTTP routes, runtime composition, direct OCM M0 integration, shared
test fixture, CLI wiring, privacy declarations, release documentation, and the gRPC security update.
Exchange routes and arbitrary caller-supplied cluster targets remain intentionally unmounted.

[A] Action: runtime configuration fails closed unless every listener, database, session verifier,
hub TLS mount, proxy mTLS mount, and Kubernetes API name is explicitly supplied. It accepts only
read-only regular mounted files, uses `rest.InClusterConfig` without kubeconfig fallback, and
constructs the managed-service-account reader, RLS database store, PEP audit logger, direct OCM
adapter, collector, and authenticated handler as one bounded TLS server.

[A] Action: the fleet API derives a fresh server-side `tenancy.Scope` from the verified Sith
session on each request. Fixed paths and methods reject query parameters, encoded path ambiguity,
foreign workspaces, unauthenticated requests, and dependency detail leakage. No credential,
transport target, selector, or cluster endpoint crosses the public boundary.

[A] Action: updated `google.golang.org/grpc` to 1.79.3 for CVE-2026-33186 and verified
`govulncheck` reports no vulnerabilities. The current Konnectivity client, including v0.36.0,
still exposes only a legacy `grpc.DialContext`-based tunnel constructor. A one-expression
`staticcheck` waiver retains `grpc.WithBlock` because removing it allowed an already-cancelled
creation context to attempt a proxy connection. The adjacent regression test proves the bounded
fail-closed behavior; no project-wide lint rule is changed.

[T] Test: targeted `go test -race -count=1 ./internal/hubocm ./internal/hubruntime` and
`govulncheck ./...` passed. The final guarded `make e2e-ocm` run passed in 215 seconds: it registered a
real hub plus two spokes, used scoped managed-service-account identity, actively denied hub node
and pod ingress, observed zero hub-initiated flows, exercised both direct adapter and runtime TLS
tests, emitted both required test-pass markers, and deleted every `sith-m0-*` Kind cluster afterward.

[T] Test: `make ci` passed format, vet, lint, vulnerability scan, repository-wide race/coverage,
17 M0 harness safety assertions, latency, binary E2E, and build. `make release-check` passed
snapshot archives for Darwin/Linux on amd64/arm64, SPDX SBOM generation, checksums, and Homebrew
formula rendering. `kind get clusters` reported none after validation.

[R] Review: red-team checks exercised path encoding, query rejection, workspace spoofing, token
and dependency-error redaction, unsafe mount rejection, and caller cancellation. The local port
8090 collision belongs to an unrelated active user process; the M0 harness defers only its
`clusteradm proxy health` check to the mandatory direct runtime gate, which passed. No user
process or unrelated active Docker container was stopped. CodeRabbit’s first pass found two valid
test-quality gaps, both fixed and rerun. Its follow-up PEP concern was rejected after source
inspection: `hubfleet.Collector.Collect` already authorizes and audits
`VerbSpokeSnapshotRefresh` before any spoke read, so handler duplication would create two policy
decisions. Its provenance documentation concern was fixed in the README.

[C] Checkpoint #1: SSH-signed DCO/GSTACK implementation commit created with
`GSTACK-Checkpoint: 2026-07-14/e2-hub-direct-runtime#1`; next: push, open the reviewed PR into
`dev`, and verify exact post-merge CI.

---

**Session close:** runtime ready for independent review and landing · **Open questions touched:**
none
