# Deterministic isolation fuzz budget

Issue: [#87](https://github.com/ArdurAI/sith/issues/87)

Branch: `gnanirahulnutakki/ci/isolation-fuzz-budget`

## [G] Goal

Make the destructive tenant-isolation gate deterministic under runner load without weakening its
native Go mutation coverage.

## [S] Scope

- Replace wall-clock fuzz completion with Go's native fixed-iteration `Nx` mode.
- Execute exactly 50,000 mutations with four workers in local and CI gates.
- Keep the existing corpus, mutation engine, target, and all PostgreSQL negative controls.
- Retain an independent two-minute process timeout to detect genuine hangs.

## [A] Failure analysis

PR #86 run `29179500950` passed every deterministic PostgreSQL and isolation test, then completed
32,103 mutations before the five-second fuzz context expired during shutdown. Re-running until a
faster runner passed would hide a timing-dependent required-gate failure. Go's documented
`-fuzztime=Nx` form separates the coverage budget from elapsed time and removes that cancellation
boundary.

## [T] Tests

- Direct fixed-count campaign under `GOMAXPROCS=2`: PASS, exactly 50,000 mutations in `2.926s`.
- Five consecutive constrained `make e2e-isolation` runs: PASS, exactly 250,000 total mutations;
  each run also passed the digest-pinned PostgreSQL 18.4 RLS suite with hubdb coverage `71.1%`.
- `make ci`: PASS (format, vet, lint, govulncheck, race/coverage, operator-script safety, latency,
  binary e2e, and build).
- `make e2e-kind`: PASS against two real Kubernetes 1.36.1 clusters in `90.902s`.
- `make release-check`: PASS with two reproducible Darwin/Linux amd64/arm64 builds, SPDX SBOMs,
  and generated Homebrew formula.
- GitHub open queues before publication: Dependabot `0`, code scanning `0`, secret scanning `0`.
- Cleanup: zero kind clusters; Docker prune reclaimed `2.043 GB`.

## [C] Checkpoint

- Signed/DCO/GSTACK feature commit: `eb1692d312252ba93b559bc1d7dd8c4ceae7777b`.
- Feature PR [#88](https://github.com/ArdurAI/sith/pull/88) passed CI run `29179821061`; its server
  log records exactly 50,000 mutations with four workers in `8.48s` before all remaining gates
  passed.
- PR #88 merged to `dev` as `1fa9375bb75c8642f0ac1d37e32858610177b422`.
- Exact post-merge run `29180191466` passed the main job, including deterministic isolation and
  real two-cluster kind. Its release job initially failed before repository execution when the
  Syft installer received an unhandled GitHub asset HTTP 302; rerunning only that failed job on the
  same SHA passed the full reproducible archive/SBOM/formula gate in `59s`.
- Issue #87 is closed. PR #86 is rebased onto the fixed `dev` history and must pass a fresh run.
- Final GitHub open queues: Dependabot `0`, code scanning `0`, secret scanning `0`.
