# Hub fleet repeatable-read snapshot

- Builder: gnanirahulnutakki
- Effort: deep
- Branch: `gnanirahulnutakki/fix/hubdb-repeatable-read`
- Issue: `#193`
- Status: ready for review

## Goal

Return persisted hub facts, coverage, and staleness from one workspace-scoped PostgreSQL snapshot.

## Scope

- Keep existing workspace write transactions at `READ COMMITTED, READ WRITE`.
- Read cluster state and facts inside one `REPEATABLE READ, READ ONLY` transaction.
- Set and verify the workspace RLS scope inside that same transaction.
- Fail closed if any returned fact lacks a registered spoke in the captured cluster-state snapshot.
- Add a deterministic PostgreSQL regression that commits `ReplaceSnapshot` between the two reads.
- Keep hub collection concurrency, refresh coalescing, and OCM lifecycle findings out of this slice.

## Progress

[G] Repair mixed persisted fact and coverage snapshots tracked by #193.
[S] Limit changes to the hub database transaction boundary, focused PostgreSQL proof, and roadmap evidence.
[A] Revalidated `origin/dev` at `566fc96` and created an isolated EXTENDED worktree.
[T] Added a two-connection deterministic interleaving that verifies repeatable-read, read-only mode, transaction-local RLS, and coherent old-old or new-new results.

## Verification

- Focused package race tests pass.
- The PostgreSQL RLS integration regression passes on the restored repeatable-read tree.
- Red-team negative control: downgrading the query to `READ COMMITTED` returned generation 2 facts with generation 1 `Stale=true` and `StaleFor=2h0m0s`; the regression failed as intended.
- Independent CodeRabbit review completed with zero findings.
- `make ci` passed formatting, vet, lint, vulnerability scanning, the full race suite, safety scripts, performance, subprocess e2e, and build.
- `make e2e-isolation` passed PostgreSQL RLS coverage and both 50,000-execution fleet-cache isolation fuzzers.
- `make e2e-kind` passed against two real kind clusters in 163.456 seconds.
- `make release-check` passed reproducible archives, SPDX SBOM validation, Homebrew formula generation, and the multi-platform OCI layout.
- Notion decision log: `https://app.notion.com/p/3a02637edb0781909fc0d9629635d08c`
- Notion session checkpoint: `https://app.notion.com/p/3a02637edb078148baefddf316ea1469`
- PR and exact post-merge `dev` evidence remain pending.

## Checkpoint

- `2026-07-16/hubdb-repeatable-read#1`
- `2026-07-16/hubdb-repeatable-read#2`

## Open questions

- None. The stronger transaction mode is private to read snapshots and does not alter write-path semantics.
