# Session — 2026-07-19 — E12 wire and adapter version split

**Builder:** Gnani Rahul · **Branch:** `gnanirahulnutakki/feat/e12-wire-version-split`
**Slice:** [#288](https://github.com/ArdurAI/sith/issues/288), E12
[#30](https://github.com/ArdurAI/sith/issues/30) · **Status:** local proof complete; hosted proof pending

## [G] Goal

Split framework transport compatibility from opaque adapter/evidence provenance and prove a
deterministic fail-closed negotiation seam before adding protobuf, gRPC transport, subprocesses,
credentials, or networking.

## [S] Scope

- Structured framework `WireVersion{Major, Minor}` values.
- Explicit supported-version offers with exact-set negotiation.
- A separately named opaque connector `AdapterVersion`.
- Atomic registry validation and deterministic descriptor inspection.
- Existing fleet evidence `protocol_version` serialization remains unchanged.
- No protobuf, generated code, gRPC dependency, process launch, listener, credentials, network,
  persistence migration, adapter port, execution, package, release, cluster, or cloud change.

## [A] Decision and implementation

- GR approved the recommended E12 version split on 2026-07-19. The decision is recorded on
  [parent issue 30](https://github.com/ArdurAI/sith/issues/30#issuecomment-5016946815), in Notion,
  and in the EXTENDED-backed Obsidian Sith decision trail.
- Opened bounded child issue 288 after confirming no duplicate issue or competing pull request.
- Added a structured wire-version type and initial version 1.0.
- Added exact-set negotiation that selects the highest mutually advertised version and
  distinguishes invalid offers, major mismatch, and unsupported minor overlap.
- Registry validation rejects empty, zero-major, duplicate, or missing wire offers and empty
  adapter versions before registration, then stores a sorted defensive copy.
- Migrated the local kubeconfig and GitHub planning descriptors plus test connectors while leaving
  all evidence `ProtocolV` values untouched.
- Updated the source-adapter spec, E12 mirrors, accepted ADR 0014, and this GSTACK journal.

## [T] Proof

- Connector, kubeconfig, GitHub planner, CLI, hydrator, and fleet package tests pass.
- Negotiation tests cover exact 1.0, explicit minor fallback, highest explicit major, input order,
  empty offers, zero major, duplicates, major mismatch, and same-major/no-common-minor refusal.
- Registry tests cover atomic malformed-version refusal, deterministic sorting, descriptor JSON
  domain separation, and defensive copying.
- A native Go fuzz target asserts that every successful result was explicitly offered by both
  endpoints and that no higher common version was skipped.
- The first complete CodeRabbit review found two documentation/contract clarity gaps: make `Kind`
  normatively the canonical target-tool identity and make `Major > 0` explicit in the source spec.
  Both were fixed, including a cross-taxonomy duplicate-tool registry test.
- Manual red-team review found that future peer-controlled offers needed an explicit cardinality
  bound. Offers are now limited to 32 versions and oversized input is covered by refusal tests.
- `go mod verify` passed and pinned `govulncheck` v1.6.0 reported no vulnerabilities.
- The full `make ci` gate passed formatting, vet, zero-issue lint, race tests, shell-policy tests,
  nine Prometheus rule tests, warm-cache performance, a 30.724-second binary end-to-end run, and
  the production build.
- `make e2e-isolation` passed the forced PostgreSQL RLS suites and both isolation fuzz targets at
  exactly 50,000 executions each.
- `make release-check` passed GoReleaser validation, dual snapshot reproducibility, SBOM and
  checksum generation, Homebrew formula generation, and the multi-architecture OCI layout check.
- The pinned kind gate passed the fleet fan-out, OCI image contract, and Argo Application
  projection tests under the race detector in 244.090 seconds. Teardown left no kind clusters.
- The final whole-diff CodeRabbit review covered all 18 changed files and returned zero findings.
- `README.md` was reviewed before commit. No update is needed because this slice changes an
  internal connector compatibility seam; the operator-visible contract is documented in the
  source-adapter spec and ADR 0014.

## [S] Security, reliability, and cost

The version boundary fails closed and never parses opaque provenance as transport compatibility.
It adds no authority, process, listener, credential, I/O, telemetry cardinality, or recurring cloud
cost. The remaining transport security and supervision design stays separately reviewable.

## [P] Primary sources

- [Protocol Buffers proto3 compatibility rules](https://protobuf.dev/programming-guides/proto3/)
- [Protocol Buffers best practices](https://protobuf.dev/best-practices/dos-donts/)
- [gRPC core concepts](https://grpc.io/docs/what-is-grpc/core-concepts/)
- [ADR 0014](../docs/adr/0014-connector-wire-adapter-version-split.md)

## [N] Next

Create one signed DCO/GSTACK commit, push the branch, and require exact-head review/CI/CodeQL plus
exact post-merge `dev` proof before closing issue 288. E12 remains open for the separately approved
transport and supervision slices.

## [C] Checkpoint #1

The owner decision, child issue, Notion record, EXTENDED Obsidian decision, implementation,
adversarial unit tests, fuzz target, spec correction, E12 mirrors, ADR, and GSTACK journal are
present. Full local and hosted proof remain fail-closed gates.

## [C] Checkpoint #2

All required local gates are green: module integrity, pinned vulnerability scanning, full CI,
isolation fuzzing, reproducible release assembly, and pinned real-cluster end-to-end coverage.
Disposable cluster cleanup and a zero-finding final whole-diff review are verified. The remaining
gates are the signed commit, exact-head hosted proof, merge, and exact post-merge `dev` proof.
