# Session — 2026-07-22 — E14 immutable desired change

**Builder:** Gnani Rahul Nutakki · **Branch:** `gnanirahulnutakki/desired-change-20260722`
**Slice:** [#307](https://github.com/ArdurAI/sith/issues/307), E14
[#46](https://github.com/ArdurAI/sith/issues/46) · **Status:** complete local proof; hosted proof pending

## [G] Goal

Implement the owner-approved second half of the F14.6 provenance split: one immutable
`DesiredChange` that binds exact proposed file bytes to an exact `GitSourceSnapshot`, cited
evidence, and a transformer version without enabling R2/R4 writes.

## [S] Scope

- Add a versioned opaque desired-change contract outside `internal/brain`.
- Bind one validated snapshot, one canonical transformer version, exact desired bytes, and a
  bounded unique evidence set.
- Keep construction package-private until a concrete deterministic transformer or declarative
  renderer policy receives separate review.
- Exclude R2/R4 transformation logic, PR metadata, handler binding, actor, intent, policy,
  approval, credential, endpoint, persistence, dispatch, mutation, and execution behavior.

## [A] Decision and implementation

- The owner selected the snapshot-plus-change decomposition in
  [issue comment 5051813734](https://github.com/ArdurAI/sith/issues/46#issuecomment-5051813734).
  Snapshot child #303 landed first through #305; child #307 locks this separate output contract.
- `desired-change/v1` privately contains a defensive copy of one valid
  `git-source-snapshot/v1`, a lowercase canonical `<transformer>/<version>` identity, exact desired
  content, and copied evidence references.
- Desired content is a non-NUL UTF-8 sequence capped at 64 KiB. Empty output, CRLF, tabs, Unicode,
  and trailing whitespace remain exact bytes; no Unicode, line-ending, whitespace, YAML, Helm, or
  Kustomize normalization occurs.
- Exact output equal to the snapshot current bytes is rejected as a no-op.
- Evidence is capped at 32 unique stable references, canonically sorted, and must attach both the
  affected resource and the exact observed Git blob. The snapshot's own nested resource and
  evidence values are deep-copied.
- The only exported operation is `Version`. The constructor and validator remain package-private;
  an AST boundary test rejects an exported construction function.
- No transformer implementation or allowlisted R2/R4 policy exists in this slice. A future
  reviewed transformer must live at the trusted package boundary before it can construct the
  contract.

## [T] Proof

- Focused remediation tests pass under the race detector, including 50 consecutive repetitions.
- Adversarial coverage rejects zero and forged snapshots, malformed transformer versions,
  invalid/NUL/oversized/no-op output, missing/duplicate/unsafe/foreign evidence, private-field
  forgery, input alias mutation, and nondeterministic evidence ordering.
- The dedicated constructor fuzzer passes 50,000 executions. Fuzz and 64-reader concurrent tests
  preserve exact bytes and source binding without mutation.
- Reflection and AST tests lock the five-field private shape, single-method public surface, and
  absence of an exported constructor. The recursive production import guard continues to exclude
  I/O, policy, persistence, and runtime authority.
- `make ci` passes formatting, vet, zero-issue lint, current vulnerability scanning, every race
  test, shell/tooling policies, all nine Prometheus rules, performance, binary end-to-end, and the
  production build. The remediation package reaches 94.8% statement coverage.
- `make e2e-isolation` passes PostgreSQL 18.4 forced RLS and both 50,000-execution cross-workspace
  fuzzers.
- `make release-check` passes module verification, two reproducible four-platform builds, SPDX
  SBOMs, formula generation, and the amd64/arm64 distroless OCI layout.
- The pinned Kubernetes 1.36.1 Kind gate passes two-cluster fleet fan-out, OCI image, and Argo
  Application projection under the race detector in 295.351 seconds. Teardown leaves no Kind
  cluster or isolated release builder.
- The independent CodeRabbit loop reviewed all six files. Two documentation/boundary findings and
  one concurrent-test proof finding were incorporated; the final full-diff pass reports zero
  findings.
- Hosted exact-head proof and exact post-merge proof remain required before closure.

## [S] Security, reliability, and cost

The contract is pure and offline. It cannot be constructed by request/runtime packages, stores no
secret or authority, and adds no API request, egress, storage, cloud resource, telemetry
cardinality, or recurring cost. Before a transformer is approved, its review must cover parser
ambiguity, multi-document YAML, Helm/Kustomize semantics, file mapping, safety bounds, rollback,
and deterministic evidence-to-output binding.

## [R] Primary references

- [GitHub REST Git blobs](https://docs.github.com/en/rest/git/blobs?apiVersion=2026-03-10)
- [GitHub REST Git trees](https://docs.github.com/en/rest/git/trees?apiVersion=2026-03-10)
- [GitHub REST Git commits](https://docs.github.com/en/rest/git/commits?apiVersion=2026-03-10)

## [N] Next

Complete local fuzz, full CI, isolation, release, and real Kind gates; run independent review;
then create one signed DCO/GSTACK commit and require exact-head hosted CI/CodeQL plus exact
post-merge `dev` proof. Close only child #307. F14.6 and E14 remain open; R2/R4 transformer policy,
live Git reads, resolver composition, Hub identity, PEP, approval, dispatch, and execution are
separate slices.

## [C] Checkpoint #1

The desired-change contract, package-private construction boundary, adversarial tests, docs, and
complete local gate matrix are frozen on exact base
`01b5ae7a0d329e19d11696bb24a8f73babb0449b`. README was reviewed and updated before commit because
the public architecture now includes the separately gated output contract. The self-managed QA
kubeconfig was not used; this slice is pure/offline and the real-cluster requirement was satisfied
by disposable local Kind clusters. Remaining gates are the signed commit, exact-head hosted proof,
merge without rewriting the signed feature commit, and exact post-merge `dev` proof.
