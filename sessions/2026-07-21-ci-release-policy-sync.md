# Session — 2026-07-21 — CI release policy sync

**Builder:** Gnani Rahul Nutakki · **Branch:** `gnanirahulnutakki/ci-release-policy-sync-20260721`
**Scope:** release-policy enforcement prerequisite · **Status:** local proof complete

## [G] Goal

Restore one trustworthy local and hosted CI contract after GitHub Actions maintenance changed the
release action commits without updating a policy test that hosted CI did not execute.

## [S] Scope

- Run the complete operator shell-policy suite in hosted CI.
- Test immutable Docker action commit pins without duplicating version-specific commit values.
- Fail the release-image verifier immediately when repository or distribution path
  canonicalization fails.
- Exclude dependencies, desktop tooling, product behavior, release publication, and cloud changes.

## [A] Action

- Added `make test-scripts` to the hosted build job and removed a duplicate Prometheus policy call.
- Replaced four stale hard-coded action commits with a structural assertion requiring exactly one
  full 40-character commit pin and version comment for each expected Docker action.
- Split command substitutions from `readonly` declarations so `set -e` observes failed `cd`
  operations, and added a dynamic regression proving an invalid dist path stops before archive or
  Docker work.

## [T] Proof

- `make test-scripts` passed every Helm, M0, release-tag, release-PR, release-image, and Prometheus
  policy assertion.
- ShellCheck is clean for the changed scripts, with intentional literal workflow expressions
  explicitly classified as `SC2016` test fixtures.
- Actionlint is clean for the changed workflow.
- Complete local CI passed formatting, vet, zero-issue lint, `govulncheck`, the repository-wide race
  suite, all operator policy scripts, nine Prometheus rules, performance, binary end-to-end, and
  production build.
- `make e2e-isolation` passed PostgreSQL 18.4 forced-RLS coverage and both cross-workspace fuzzers
  at 50,000 executions each.
- `make release-check` passed dual four-platform reproducibility, SPDX SBOMs, checksums, formula
  generation, and release-derived amd64/arm64 OCI verification through the changed script.
- Exact-head hosted CI and CodeQL plus exact post-merge `dev` proof remain mandatory.
- `README.md` was reviewed. No update is needed because this changes CI enforcement and verifier
  failure handling, not an operator or product contract.

## [S] Security, reliability, and cost

Hosted CI can no longer report green while skipping the aggregate release/operator policy suite.
The verifier fails before using an unresolved path. The change adds no API call, cloud resource,
runtime privilege, artifact, storage, telemetry, or recurring cost.

## [N] Next

Create one SSH-signed DCO/GSTACK commit, obtain exact-head hosted proof, merge into `dev`, and
rebase the dependent audit-hardening branch onto the verified post-merge SHA.

## [C] Checkpoint #1

The hosted/local policy alignment, durable action-pin assertion, fail-fast verifier change,
regression tests, and local focused proof are frozen for signed review.
