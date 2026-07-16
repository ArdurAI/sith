# Fleet-cache workspace mutation boundaries

- Builder: gnanirahulnutakki
- Effort: deep
- Branch: `gnanirahulnutakki/fix/fleetcache-workspace-mutations`
- Issue: `#187`
- Status: ready for review

## Goal

Qualify every fleet-cache data mutation and its operational state by an explicit workspace boundary.

## Scope

- Key records, coverage, warm state, sync state, update time, and errors by workspace.
- Carry an explicit workspace on watch snapshots, upserts, deletes, and errors.
- Reject missing or mixed workspace mutations atomically.
- Preserve `QueryScoped` ownership revalidation and workspace-scope the local-UI pause control.
- Keep pagination issues #185 and #190, connector framework work, and external release blockers out of scope.

## Progress

[G] Prove and repair the cross-workspace mutation collisions tracked by #187.
[S] Limit changes to connector watch envelopes, the in-memory fleet cache, local hydration callers, and focused unit/integration coverage.
[A] Revalidated `origin/dev` at `a54ebe4`, confirmed #187 open, and preserved the isolated EXTENDED worktree across the #185 merge.
[T] Added race/fuzz regressions for identical resource identity collisions, cross-workspace watch-error contamination, and change-notification isolation; the real two-cluster kind suite now replays a live Pod identity through two independent workspaces.

## Verification

- `make ci` passed on the final tree: formatting, vet, lint, vulnerability scan, race tests, safety scripts, performance, subprocess e2e, and build.
- `make e2e-isolation` passed with PostgreSQL RLS and 50,000 executions for each scoped-read and foreign-mutation fuzzer.
- `make e2e-kind` passed against two real kind clusters in 153.102 seconds.
- `make release-check` passed two reproducible snapshot builds, archive/SBOM verification, formula generation, and multi-platform OCI layout verification.
- Red-team review confirmed workspace validation occurs before lock acquisition or mutation; record keys, kind aliases, per-kind watch errors, replace, snapshot, upsert, delete, pause, sync, version, and change-wait paths cannot alter or signal a foreign workspace.
- `README.md` was reviewed; its public workspace-scoped cache description remains accurate, so the implementation evidence belongs in `docs/ROADMAP.md` rather than duplicating internals in the README.
- Notion decision log: `https://app.notion.com/p/39f2637edb078163b98ef0ac07b992e2`
- Notion session checkpoint: `https://app.notion.com/p/39f2637edb0781cfb6a1fa1dd2f91d48`

## Checkpoint

- `2026-07-16/fleetcache-workspace-mutations#1`
- `2026-07-16/fleetcache-workspace-mutations#2`

## Open questions

- None. Missing or mixed workspace identity fails closed; this is a security invariant, not a configurable policy.
