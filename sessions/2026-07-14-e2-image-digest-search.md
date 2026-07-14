# Session — 2026-07-14 — e2-image-digest-search

**Builder:** Gnani Rahul · **Branch:** `gnanirahulnutakki/feat/e2-image-digest-search`
**Slice(s):** E2 / [#127](https://github.com/ArdurAI/sith/issues/127) · **Status:** ready-to-commit

---

[G] Goal: deliver the immutable image-evidence foundation for F2.4: read the runtime-resolved
digest from ordinary Pod container status, persist only a bounded canonical projection, and answer
one exact workspace-scoped fleet lookup with honest coverage.

[S] Scope: exact lowercase SHA-256 digests from `Pod.Status.ContainerStatuses[].ImageID`, direct
OCM snapshot normalization, forced-RLS persistence and GIN lookup, one signed-session hub route,
and two-spoke evidence. Registry calls, image pulls, SBOM/CVE/feed retrieval, tags/prefixes,
arbitrary selectors, writes, credentials, init containers, and ephemeral containers are excluded.

[A] Action: accepted only the known `containerd`, `docker-pullable`, `cri-o`, and `docker` runtime
prefixes (or a bare canonical digest), then retained only `sha256:<64 lowercase hex>`. Unknown,
mutable, malformed, ambiguous, init, and ephemeral values abstain. Inventory validates a sorted,
unique, at-most-64 digest list only for Pod facts; no raw Pod/status payload crosses the store seam.

[A] Action: added a closed `fleet.image.search` PEP verb and a narrow `ImageSearcher`. It authorizes
the signed tenancy scope before its query port, hashes canonical validated arguments for audit, and
issues only `FactInventory + Pod + exact digest` queries. PostgreSQL uses parameterized JSONB array
membership behind forced RLS with a partial GIN expression index; labels, names, prefixes, health,
and CVE selectors remain rejected in this shape.

[A] Action: mounted only `GET /v1/workspaces/{workspace}/fleet/images/{sha256:<64-lowercase-hex>}`.
The parser rejects queries, encoded ambiguity, noncanonical values, foreign workspace scopes, and
wrong methods. The route exposes no target, selector, freshness override, transport, or credential;
errors remain generic and responses are non-cacheable.

[T] Test: focused race suites for `fleet`, `hubocm`, `hubfleet`, `hubdb`, `hubserver`, and
`hubruntime` passed. `make e2e-postgres` passed with 71.5% package coverage, including the new
migration, exact two-spoke result, and cross-workspace negative control. `make e2e-ocm` passed in
the real hub-plus-two-spoke M0 lab: direct adapter and TLS runtime tests observed the fixture's
actual runtime digest on both spokes and the harness deleted every temporary Kind cluster.

[T] Test: final `go mod verify`, `govulncheck ./...`, `make ci`, and `make release-check` passed.
CI covered format, vet, lint, full race suites, safety harness, E2E, build, and a dual Darwin/Linux
amd64/arm64 reproducibility run with SPDX SBOMs. GitHub queues were checked immediately before
publication: Dependabot 0, code scanning 0, secret scanning 0.

[R] Review: manual red-team review covered hostile runtime schemes, mutable strings, untrusted
JSON, parameterized exact lookup, RLS/cross-workspace isolation, policy refusal before query,
fixed-path/query/escape handling, safe errors, and no raw credential/status persistence. CodeRabbit
CLI v0.6.5 was authenticated and submitted the final uncommitted diff twice; each remote review
entered `reviewing` but emitted no finding before continuing to heartbeat, so both runs were stopped
after bounded waits. This external review-service delay is recorded, not treated as a Sith blocker.

[C] Checkpoint #1: next: SSH-signed DCO/GSTACK implementation commit, push, PR into `dev`, exact
post-merge CI, issue/roadmap updates, and the next ordered unblocked backlog slice.

---

**Session close:** exact image evidence ready for independent review and landing · **Open questions
touched:** none
