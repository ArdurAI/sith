# Session — 2026-07-22 — E14 immutable Git source snapshot

**Builder:** Gnani Rahul Nutakki · **Branch:** `gnanirahulnutakki/git-source-snapshot-20260722`
**Slice:** [#303](https://github.com/ArdurAI/sith/issues/303), E14
[#46](https://github.com/ArdurAI/sith/issues/46) · **Status:** complete local proof; hosted proof pending

## [G] Goal

Implement the owner-approved first half of the F14.6 provenance split: one immutable
`GitSourceSnapshot` containing only canonical observed Git state. Defer `DesiredChange` to a later
separately reviewed transformer/renderer and keep R2/R4 advisory-only.

## [S] Scope

- Add a versioned source snapshot outside `internal/brain`, bound to one workspace, affected
  resource, pinned GitHub source identity, repository, configured base ref, exact resolved commit,
  one path, exact current bytes, matching blob identity, evidence set, and short validity interval.
- Keep validated fields private, copy mutable inputs, canonicalize evidence ordering, and expose
  only the contract version plus a trusted-time freshness classification.
- Exclude desired bytes, PR metadata, handler binding, actor, intent, policy, approval, credential,
  endpoint, persistence, dispatch, mutation, and execution behavior.

## [A] Decision and implementation

- The owner decision is recorded on E14 in
  [issue comment 5051813734](https://github.com/ArdurAI/sith/issues/46#issuecomment-5051813734),
  and child issue 303 locks this bounded acceptance contract as an E14 sub-issue.
- `git-source-snapshot/v1` accepts exactly one
  `github-git-source-snapshot/2026-03-10` source whose native repository identity must match the
  separately validated host/owner/repository tuple.
- The configured base ref rejects symbolic `HEAD`, full `refs/...`, object-ID-shaped,
  option-shaped, reflog, lock, traversal-like, whitespace, and malformed Git ref names. Commit and
  blob identities are canonical lowercase 40-hex SHA-1 or 64-hex SHA-256 values and must use the
  same object format.
- The snapshot recomputes the exact Git blob identity over
  `blob <byte-count>\0<current-content>` and rejects a blob/content mismatch. SHA-1 is used only for
  current GitHub Git-object interoperability; SHA-256 is supported under Git's transition format.
- Current content is an exact non-NUL UTF-8 byte sequence capped at 64 KiB. No Unicode,
  line-ending, whitespace, or YAML normalization occurs; empty files and CRLF are preserved.
- The repository-relative path rejects absolute, unclean, traversal, backslash-ambiguous, and Git
  metadata paths while retaining otherwise valid Unicode file names.
- Evidence is capped at 32 unique stable references, defensively copied, and canonically sorted.
  It must attach both the affected resource and the exact repository blob.
- Construction normalizes observation times to UTC and permits at most five minutes. `Freshness`
  uses only a supplied trusted time: before observation is future, at/after expiry is stale, and the
  half-open interval between is fresh. Zero time or a forged invalid snapshot fails closed.
- Reflection tests lock the exact input, private snapshot, and two-method public shapes. A recursive
  import guard prevents I/O, policy, persistence, authority, connector-runtime, or Brain imports.

## [T] Proof

- Focused remediation race tests pass; 50 repeated snapshot runs remain green and the package
  reaches 94.0% statement coverage in the full race suite.
- Snapshot fuzzing passes 50,000 executions. Adversarial tests cover zero/multiple/mismatched
  sources, invalid workspace/subject/repository/evidence, symbolic and malformed refs, malformed or
  mixed-format object IDs, blob/content mismatch, unsafe paths, invalid/oversized content, invalid
  validity windows, future/stale/zero clocks, input alias mutation, deterministic ordering, and 64
  concurrent readers.
- `make ci` passes formatting, vet, zero-issue lint, current vulnerability scanning, every race
  test, shell/tooling policy, nine Prometheus rules, performance, binary end-to-end, and production
  build gates.
- `make e2e-isolation` passes PostgreSQL 18.4 forced RLS and two 50,000-execution cross-workspace
  fuzzers.
- One intervening repeat exposed an existing host-clock/database-clock exact-boundary race in the
  approval-expiry test fixture (`expired approval consume error = <nil>`). The production predicate
  remained database-clock authoritative, a clean repeat passed, and follow-up issue
  [#304](https://github.com/ArdurAI/sith/issues/304) tracks the test-only repair outside this PR.
- `make release-check` passes module verification, two reproducible four-platform builds, SPDX
  SBOMs, formula generation, and the amd64/arm64 distroless OCI layout.
- The pinned Kubernetes 1.36.1 Kind gate passes two-cluster fleet fan-out, OCI image, and Argo
  Application projection under the race detector in 242.424 seconds. Teardown leaves no Kind
  cluster or isolated release builder.
- Final isolated-GOPATH module verification and `govulncheck` v1.6.0 report no failures or reachable
  vulnerabilities.

## [S] Security, reliability, and cost

The snapshot is pure and offline. It stores neither secrets nor authority and adds no API request,
egress, storage, cloud resource, telemetry cardinality, or recurring cost. A later live adapter
must separately own least-privilege contents-read credentials, GitHub rate limits, egress,
content-size policy, and remote-state freshness. A later `DesiredChange` contract must bind this
snapshot version and evidence without gaining implicit authorization.

## [R] Primary references

- [GitHub REST Git references](https://docs.github.com/en/rest/git/refs?apiVersion=2026-03-10)
- [GitHub REST Git commits](https://docs.github.com/en/rest/git/commits?apiVersion=2026-03-10)
- [GitHub REST Git blobs](https://docs.github.com/en/rest/git/blobs?apiVersion=2026-03-10)
- [Git hash transition](https://git-scm.com/docs/hash-function-transition)

## [N] Next

Create one signed DCO/GSTACK commit, open a PR to `dev`, and require exact-head hosted CI, CodeQL,
and review plus exact post-merge `dev` proof. Close only child issue 303. F14.6 and E14 remain open;
`DesiredChange`, live Git reads, Hub composition, PEP, and runtime execution are separate slices.
After this snapshot lands, repair the independently tracked approval-expiry test flake in #304.

## [C] Checkpoint #1

The snapshot contract, hash binding, adversarial tests, import/public-shape guards, README/spec
documentation, local review, and complete local gate matrix are frozen on exact base
`430eea3faff4c889c8435b155b042e2104b1aeda`. README was reviewed and updated before commit because
the public architecture now includes the observed-only provenance stage. Remaining gates are the
signed commit, exact-head hosted proof, merge without rewriting the signed feature commit, and exact
post-merge `dev` proof.
