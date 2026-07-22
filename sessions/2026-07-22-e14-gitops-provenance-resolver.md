# Session — 2026-07-22 — E14 GitOps provenance resolver

**Builder:** Gnani Rahul Nutakki · **Branch:** `gnanirahulnutakki/gitops-provenance-20260722`
**Slice:** [#301](https://github.com/ArdurAI/sith/issues/301), E14
[#46](https://github.com/ArdurAI/sith/issues/46) · **Status:** complete local proof; hosted proof pending

## [G] Goal

Implement the owner-approved F14.6b contract-only GitOps stage: resolve one confirmed canonical
Brain candidate against one fresh, source-owned provenance bundle without allowing a caller to
invent target or handler arguments and without creating a PEP proposal or write capability.

## [S] Scope

- Add one versioned immutable provenance bundle outside `internal/brain`, owned by the canonical
  GitHub source contract and bound to one workspace, cited subject, repository, exact Git objects,
  desired content, evidence set, and bounded validity interval.
- Pin provenance to the exact planning handler adapter version and argument-schema digest.
- Resolve confirmed entity-local R2/R4 `gitops.open-pr` candidates only; keep R1 and all other rules
  candidate-only or advisory-only.
- Reuse the GitHub planner's pure schema and semantic validation boundary and verify that its
  canonical output preserves every source-owned value.
- Exclude endpoint, PEP, persistence, database, credential, network, Git access, PR creation,
  dispatch, shell, filesystem, cluster mutation, or execution behavior.

## [A] Decision and implementation

- Parent issue 46 records GR's approval of the source-owned-bundle plus pure-resolver design; child
  issue 301 locks the bounded acceptance contract.
- `GitOpsProvenanceBundle` has private fields and is constructible only after exact source count,
  source version, workspace, subject, repository, object-ID, validity, content-bound, and evidence
  validation. Mutable references and slices are defensively copied and evidence ordering compares
  the complete resource-identity tuple.
- The bundle expires no later than five minutes after observation and rejects symbolic `HEAD`, full
  `refs/...`, option-shaped, and commit-shaped base branch identities. The shared GitHub planner now
  rejects those ambiguous configured base branches too.
- `OpenPRPlanner.CanonicalizeOpenPRArgs` exposes the planner's existing I/O-free schema and semantic
  checks without constructing an action plan. `Plan` now calls the same internal seam, so policy
  cannot drift between resolver and planner.
- The resolver requires one confirmed non-fleet R2/R4 verdict with an attached fresh citation and
  exactly one fresh matching bundle. It verifies the live descriptor, wire version, adapter
  version, schema digest, repository target, canonical schema, and exact commit/blob/content output,
  then rechecks the descriptor to close an in-process time-of-check/time-of-use gap.
- Ready output is limited to normalized target, canonical arguments, SHA-256 argument digest, and
  copied evidence references. All expected refusals use bounded closed abstention reasons.
- A recursive import guard prevents I/O, policy, persistence, or authority packages from entering
  the remediation package, and reflection tests lock authority-free public shapes plus opaque bundle
  fields.

## [T] Proof

- Focused package tests pass under the race detector for the GitHub planner and remediation resolver.
- Twenty consecutive focused resolver race runs pass. The exact focused linter passes with zero
  issues, and the resolver fuzz target passes 50,000 executions.
- Positive R2/R4 tests prove stable canonical arguments and digests across repeated calls and
  evidence input ordering. Sixty-four concurrent resolutions remain byte-identical.
- Adversarial tests cover missing/mutated/unsupported candidates; stale/unattached verdict evidence;
  zero/multiple/stale/future/foreign/unattached/multi-source provenance; unsafe/floating inputs;
  validity, content, repository, base, commit, and blob failures; descriptor, wire, adapter, and
  schema drift; handler target/output mutation; nil dependencies; cancellation before and after
  handler validation; output aliases; and fuzzed paths/content/base refs.
- `make ci` passes formatting, vet, zero-issue lint, `govulncheck` with no vulnerabilities, every
  race test, shell/tooling policies, nine Prometheus rules, performance, binary end-to-end, and the
  production build. The new resolver package reaches 94.1% statement coverage.
- `make e2e-isolation` passes PostgreSQL 18.4 forced-RLS coverage and both cross-workspace fuzzers at
  50,000 executions each.
- `make release-check` passes module verification, two reproducible four-platform snapshots, SPDX
  SBOMs, checksums, formula generation, and the release-derived amd64/arm64 distroless OCI layout.
- The pinned Kubernetes 1.36.1 Kind gate passes two-cluster fleet fan-out, OCI image, and Argo
  Application projection under the race detector in 243.913 seconds. Teardown leaves no Kind
  cluster, Sith container, or isolated release builder.
- The first CodeRabbit pass found two valid documentation omissions: evidence references in the
  README result list and exact byte/digest/freshness semantics in the spec. Both were corrected.
  A second whole-diff review covering all nine changed files reports zero findings.
- A final manual execution trace then found a last-check time-of-check/time-of-use gap: cancellation
  or expiry during the post-canonicalization descriptor check could otherwise return ready. The
  resolver now rechecks both immediately before output, with dedicated regressions.
- The exact post-hardening focused fuzz, full CI, isolation, release, and real-cluster matrices all
  pass again, and the third whole-diff CodeRabbit review reports zero findings.
- Hosted review, exact-head CI/CodeQL, and post-merge `dev` proofs remain pending.

## [S] Security, reliability, and cost

The resolver is pure and offline. Actor, role, authenticated scope, server-owned intent ID, policy
decision, approval state, credential, endpoint, and execution state are absent from the bundle and
result. A ready result is provenance-complete, not authorized. This slice adds no API request,
egress, storage, cloud resource, telemetry cardinality, or recurring cost. A future GitHub read
adapter must separately account for credential custody, rate limits, egress, and remote-state
freshness.

## [R] Primary references

- [GitHub REST references](https://docs.github.com/en/rest/git/refs?apiVersion=2026-03-10)
- [GitHub REST commits](https://docs.github.com/en/rest/git/commits?apiVersion=2026-03-10)
- [GitHub REST trees](https://docs.github.com/en/rest/git/trees?apiVersion=2026-03-10)
- [Argo CD Application specification](https://argo-cd.readthedocs.io/en/latest/user-guide/application-specification/)

## [N] Next

Create one SSH-signed DCO/GSTACK commit, open a PR to `dev`, and require exact-head hosted
CI/CodeQL/review plus exact post-merge proof. Close only child issue 301. F14.6 and E14 remain open
for the separately reviewed authenticated Hub-to-PEP composition and the live canonical provenance
adapter.

## [C] Checkpoint #1

The resolver contract, shared handler canonicalization seam, ambiguous-base hardening, adversarial
tests, import/public-shape guards, README/spec correction, session record, clean independent review,
and complete local gate matrix are frozen on exact base
`4b562abe6a16cf5f7ba77b6b16b682361d43f23b`. README was reviewed and updated before commit because
the public architecture now includes a provenance-resolution stage. Remaining gates are the signed
commit, exact-head hosted CI/CodeQL/review, merge without rewriting the signed feature commit, and
exact post-merge `dev` proof.
