# Hub spoke worker pool

- Builder: gnanirahulnutakki
- Effort: deep
- Branch: `gnanirahulnutakki/fix/hub-spoke-worker-pool`
- Issue: `#194`
- Status: ready for review

## Goal

Bound and parallelize one workspace refresh so unavailable spokes do not delay healthy peers serially or create unbounded proxy/API pressure.

## Scope

- Add a validated `MaxConcurrentSpokes` collector setting with a default of four and a hard maximum of 64.
- Parallelize transport and snapshot validation only; keep store writes, coverage mutation, metrics, and trace observations serialized.
- Preserve independent per-spoke deadlines, failure categories, retained-stale accounting, and sorted coverage.
- Stop admission on parent cancellation or the first store failure, cancel in-flight work, and wait for every worker before returning.
- Keep refresh coalescing (#195), repeatable-read database snapshots (#193), and OCM transport authorization (#104) out of this slice.

## Progress

[G] Repair serial N-times-timeout hub refresh latency tracked by #194.
[S] Limit changes to collector fan-out, adversarial race tests, and operator/roadmap evidence.
[A] Revalidated the canonical remote and live issue/PR inventory at `origin/dev` `ca45dc3`, then created an isolated EXTENDED worktree.
[T] Added deterministic worker-cap, timeout-wave, healthy-peer, cancellation, store-failure, coverage-order, and closed-observability regressions.

## Verification

- Focused `go test -race ./internal/hubfleet` passed.
- Synchronization-heavy cancellation and admission tests passed 100 repeated race-detector runs.
- The four-spoke timeout regression completes in one one-second parallel wave. Its one-worker negative control failed as intended at 4.004 seconds.
- Red-team review found and repaired a worker-goroutine panic escape and panic-path worker leak; transport and store panic regressions now prove closed errors, peer cancellation/join, and clean later refreshes.
- Independent CodeRabbit review completed twice; the exact final tree returned zero findings across all six changed files.
- `make ci` passed formatting, vet, lint with zero findings, vulnerability scanning with no findings, the full race suite, safety scripts, and build.
- `make e2e-isolation` passed PostgreSQL RLS packages and both 50,000-execution workspace-isolation fuzzers.
- `make e2e-kind` passed the real two-cluster and immutable OCI image contracts in 159.378 seconds with kind v0.32.0.
- `make release-check` passed module verification, two reproducible GoReleaser snapshots, SPDX SBOM validation, Homebrew formula generation, and the multi-platform OCI layout.
- Notion decision log: `https://app.notion.com/p/3a02637edb078131aea9d5452a51caca`
- Notion session checkpoint: `https://app.notion.com/p/3a02637edb0781d4b7c2c0d6f50ee8a6`
- Hosted PR and exact post-merge `dev` gates remain pending.

## Checkpoint

- `2026-07-16/hub-spoke-worker-pool#1`
- `2026-07-16/hub-spoke-worker-pool#2`

## Open questions

- None. The packaged runtime keeps the conservative default; embedded construction may choose only a validated finite limit.
