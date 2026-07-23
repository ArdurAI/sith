# Session — 2026-07-21 — deep quality audit

**Builder:** Gnani Rahul Nutakki · **Branch:** `gnanirahulnutakki/audit-hardening-20260721`
**Scope:** repository-wide deep audit and open-issue completion · **Status:** in progress

## [G] Goal

Audit exact `dev` with current primary-source tooling, fix every confirmed defect in narrow signed
changes, then complete every independently buildable open issue without weakening fail-closed
boundaries.

## [S] Scope

- This checkpoint covers malformed UTF-8 rejection at the tenancy and policy-audit boundary,
  audit-hash fuzz-oracle correctness, and lifecycle ownership for coalesced spoke refreshes.
- Release-policy drift, dependency/toolchain updates, desktop-build version drift, approval expiry,
  and remaining open-issue slices stay separate so each can be reviewed and reverted independently.
- No credential, authorization, schema, cloud resource, external package, or externally visible API
  surface changes. The collector lifecycle context change below is limited to `internal/hubfleet`.

## [A] Action

- Repaired the audit-hash fuzz target so it compares two valid records with the same unframed bytes
  split at different actor/reason boundaries instead of comparing validation-error sentinels.
- Added explicit UTF-8 validation for policy actors and tenancy identities. This aligns the durable
  writer with the portable audit verifier and fails before malformed bytes reach hashing or storage.
- Made the collector lifecycle context mandatory. Shared refreshes remain detached from individual
  callers but now inherit hub shutdown cancellation and deadlines through a value-free boundary.
- Updated every collector construction site and the README's current refresh-lifecycle contract.

## [T] Proof

- `go test -race -count=10 ./internal/hubfleet ./internal/pep ./internal/tenancy ./internal/hubdb`
  passed.
- `FuzzPolicyAuditEntryHashUsesLengthFraming` passed 250,000 executions after the regression fix;
  all 23 repository fuzz targets separately passed 50,000 executions each.
- The first full local CI run exposed a stale release-policy assertion and a hosted coverage gap.
  The separately signed prerequisite landed through PR #294, and exact merge SHA `f0e071a` passed
  expanded CI, reproducible release, real kind, and all three CodeQL analyses.
- After rebasing onto that verified SHA, complete local CI passed formatting, vet, zero-issue lint,
  `govulncheck`, every race test and shell policy, nine alert rules, performance, binary end-to-end,
  and production build.
- `make e2e-isolation` passed PostgreSQL 18.4 forced-RLS coverage and both workspace-isolation
  fuzzers at 50,000 executions each.
- `make release-check` passed dual four-platform reproducibility, SPDX SBOMs, checksums, Homebrew
  formula generation, and release-derived amd64/arm64 OCI verification.
- The Kubernetes 1.36.1 kind gate passed fleet fan-out, OCI image, and Argo Application projection
  under the race detector in 260.143 seconds; teardown left no kind cluster or Buildx builder.
- `README.md` was reviewed and updated because refresh lifetime is now bounded by hub shutdown.

## [S] Security, reliability, and cost

Malformed identities now fail consistently across online and offline audit paths. Detached work no
longer survives process cancellation, while lifecycle values and request credentials remain outside
the refresh context. The change adds no cloud resource, API call, storage, telemetry cardinality,
or recurring cost.

## [N] Next

Push the rebased signed branch, obtain exact-head hosted CI and CodeQL proof, merge without rewriting
the signed commit, and verify the exact post-merge `dev` SHA.

## [C] Checkpoint #1

The validation alignment, lifecycle boundary, adversarial tests, fuzz repair, construction-site
migration, README correction, prerequisite merge, and complete local gate matrix are frozen for
signed review. Hosted exact-head and exact post-merge `dev` proof remain mandatory.
