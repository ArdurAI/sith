# Hub refresh coalescing

- Builder: gnanirahulnutakki
- Effort: deep
- Branch: `gnanirahulnutakki/fix/hub-refresh-coalescing`
- Issue: `#195`
- Status: ready for review

## Goal

Collapse duplicate concurrent hub refreshes without crossing workspace, authorization, cancellation, request-context, or trace boundaries.

## Scope

- Authorize every caller through the existing role and PEP pipeline before coordination.
- Keep at most one active refresh flight per validated workspace while allowing different workspaces to progress independently.
- Run shared work on a fresh internal context that contains no caller cancellation, credential, request value, or request trace.
- Return defensive copies of the closed coverage result and one closed error result.
- Remove completed, failed, and panicking flights without retaining per-request state.
- Keep spoke worker-pool behavior (#194) and database snapshot isolation (#193) out of this slice.

## Progress

[G] Repair duplicate same-workspace refresh work and last-writer timing races tracked by #195.
[S] Limit the change to collector coordination, adversarial race tests, and operator/roadmap documentation.
[A] Revalidated `origin/dev` at `566fc96`, confirmed no open PR collision, and created an isolated EXTENDED worktree.
[T] Added deterministic authorization, same/cross-workspace, leader/waiter cancellation, shared failure, panic cleanup, result-copy, and trace/context isolation regressions.

## Verification

- Focused `go test -race -count=100 ./internal/hubfleet` passed.
- Focused hubfleet, hubserver, and hubruntime race suites passed.
- `make ci` passed formatting, vet, lint with zero findings, vulnerability scanning with no findings, the full race suite, safety scripts, performance, subprocess e2e, and build.
- `make e2e-isolation` passed PostgreSQL RLS packages and 50,000 executions for each fleet-cache workspace-isolation fuzzer.
- `make e2e-kind` passed the real two-cluster and OCI image contracts in 157.014 seconds with the pinned kind v0.32.0 toolchain.
- `make release-check` passed module verification, two reproducible GoReleaser snapshots, SPDX SBOM validation, Homebrew formula generation, and the multi-platform OCI layout.
- Red-team review confirmed denied callers never join, caller cancellation/values/traces cannot reach shared work, coverage slices do not alias, and completion/failure/panic paths remove coordinator state.
- `README.md` documents the operator-visible authorization, workspace, cancellation, and request-context boundaries.
- Hosted PR checks and exact post-merge `dev` gates remain pending.
- Notion decision log: `https://app.notion.com/p/3a02637edb0781f3933ef6d6f52268b6`
- Notion session checkpoint: `https://app.notion.com/p/3a02637edb0781fb82ddd9dc2d20408e`

## Checkpoint

- `2026-07-16/hub-refresh-coalescing#1`
- `2026-07-16/hub-refresh-coalescing#2`

## Open questions

- None. Coordination is intentionally per workspace and occurs only after each caller's own policy decision.
