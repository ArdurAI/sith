# Session — 2026-07-20 — E14 remediation candidate contract

**Builder:** Gnani Rahul · **Branch:** `gnanirahulnutakki/feat/e14-remediation-candidate`
**Slice:** [#292](https://github.com/ArdurAI/sith/issues/292), E14
[#46](https://github.com/ArdurAI/sith/issues/46) · **Status:** local proof complete; hosted proof pending

## [G] Goal

Implement GR-approved F14.6 option 1 as a contract-only child: let reviewed Brain rules emit an
inert typed remediation requirement template without resolving a target, constructing handler
arguments, proposing to PEP, approving, dispatching, mutating, or executing anything.

## [S] Scope

- Add a `RemediationCandidate` containing only one closed `intent.Verb` and ordered closed
  provenance requirements.
- Map R1 only to `argocd.rollback`, requiring an authoritative Argo Application target and exact
  revision.
- Map R2 and R4 only to `gitops.open-pr`, requiring repository, base ref, expected base commit,
  file path, observed blob identity, and exact bounded desired content.
- Keep R3, R5, R6, R7, R8, and R9 advisory-only and preserve every existing advisory.
- Update deterministic replay expectations, public documentation, and the no-write package
  boundary.
- Exclude PEP imports or proposals, provenance resolution, handler invocation, approval,
  persistence, credentials, signatures, network, dispatch, mutation, execution, and cloud changes.

## [A] Decision and implementation

- GR approved the two-stage structured-candidate design on 2026-07-20. The decision is recorded on
  [parent issue 46](https://github.com/ArdurAI/sith/issues/46#issuecomment-5017157418), bounded child
  issue 292, Notion, and the EXTENDED-backed Obsidian Sith decision trail.
- Added a rule-owned candidate with exactly two public fields: the existing closed verb type and a
  slice of closed provenance requirement identifiers.
- Encoded the two approved verb-to-requirements mappings in a closed switch. Unsupported or empty
  verbs produce no candidate.
- Candidate JSON marshaling and unmarshaling fail closed on missing, reordered, duplicated,
  unknown-field, unknown-verb, forged, and trailing forms.
- Each evaluation returns fresh candidate state, and fleet correlation deep-copies its candidate
  so a caller cannot mutate an entity verdict through a fleet-wide alias.
- Replay fixtures now lock R1, R2, and R4 to the approved mappings and prove every other canonical
  rule remains candidate-free.
- The Brain import boundary now rejects connector, Hub DB, local-ops, MCP, PEP, SQL, network, OS,
  process, and gRPC paths. The package imports only the side-effect-free intent vocabulary.
- Updated the E2 source specification, README, this GSTACK journal, Notion, and Obsidian. F14.6
  intentionally remains open for a separately reviewed authoritative provenance and governed-handoff child.

## [T] Proof

- `go mod verify` passed.
- `go test -race -count=20 ./internal/brain`, focused vet, and the exact linter passed with zero
  issues. Adversarial tests cover public shape, exact catalog mappings, strict JSON, repeated-call
  mutation isolation, fleet/entity alias isolation, unconfirmed R1 behavior, and advisory-only R3.
- `make ci` passed formatting, vet, zero-issue lint, `govulncheck ./...` with no vulnerabilities,
  the complete race suite, script policies, nine Prometheus rules, performance, binary end-to-end,
  and production build. Brain coverage was 88.2%.
- `make e2e-isolation` passed PostgreSQL 18.4 forced-RLS tests and both cross-workspace fuzz targets
  at exactly 50,000 executions each.
- `make release-check` passed module verification, dual four-platform release reproducibility,
  archive checks, SPDX SBOMs, checksums, Homebrew formula generation, and the release-derived
  amd64/arm64 distroless OCI layout.
- The isolated EXTENDED Docker configuration initially hid Docker Desktop's Buildx plugin. A
  plugin-only symlink restored Buildx without copying credentials or auth state; the complete gate
  then passed. One later registry TLS timeout was treated as external and the full gate passed on retry.
- The pinned Kubernetes 1.36.1 Kind gate passed fleet fan-out, OCI image contract, and Argo
  Application projection under the race detector in 238.591 seconds. Cleanup left no matching Kind
  cluster or Buildx builder.
- `README.md` was reviewed before commit and updated because verdict JSON now exposes an optional
  public candidate field and the prior no-intent-import statement would have become false.
- The complete diff received a manual security and correctness review. Hosted review, exact-head
  CI/CodeQL, and exact post-merge `dev` proof remain mandatory.

## [S] Security, reliability, and cost

The candidate is a requirement template, not evidence readiness or authorization. It carries no
target, handler arguments, actor, workspace, credential, signature, policy decision, or execution
state. Human advisory prose never becomes an implicit action contract. The slice performs no I/O
and adds no privilege, API request, egress, storage, telemetry cardinality, cloud resource, or
recurring cost.

## [N] Next

Create one SSH-signed DCO/GSTACK commit, push the branch, open a PR to `dev`, require exact-head
hosted review, CI, and CodeQL, merge without rewriting the signed feature commit, and prove the exact
post-merge `dev` SHA. Then close child issue 292, update parent issue 46 without claiming F14.6 or
E14 complete, recheck all GitHub security queues, and synchronize Notion and Obsidian.

## [C] Checkpoint #1

The approved decision, bounded issue, implementation, adversarial tests, replay contract,
import-boundary guard, README/spec updates, Notion/Obsidian records, and complete local gate matrix
are frozen on exact base `76f8f8fb3c630d755eb47d4cc006fa1decae551a`. Remaining gates are the signed
commit, exact-head hosted proof and review, merge, and exact post-merge `dev` proof.

## [C] Checkpoint #2

The complete committed-diff review found two valid fail-closed gaps. Candidate JSON now rejects
case-variant field aliases before Go's case-insensitive struct matching, and the Brain side-effect
import classifier now rejects every listed package tree, including `net/rpc`, `os/user`,
`database/sql/driver`, and gRPC subpackages. Dedicated regressions cover both boundaries. The
remediation-only independent review reports zero findings across all three changed files.

The complete post-remediation matrix is green: twenty focused race repetitions, focused vet and
zero-issue lint, full CI with no vulnerability findings and Brain coverage at 88.3%, PostgreSQL
forced-RLS plus both 50,000-execution isolation fuzzers, reproducible release/SBOM/formula/OCI
proof, and pinned Kubernetes 1.36.1 Kind in 237.037 seconds with clean teardown. A new signed
checkpoint commit, fresh exact-head hosted CI/CodeQL/review, merge, and exact post-merge `dev` proof
remain required.
