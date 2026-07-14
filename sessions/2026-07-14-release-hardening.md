# Session — 2026-07-14 — release hardening

**Issue:** [#143](https://github.com/ArdurAI/sith/issues/143)
**Branch:** `gnanirahulnutakki/fix/release-hardening`
**Base:** `origin/dev` at `ba102f467415db3e309cbf35fe7b02f88929c593`

## [G] Goal

Resolve the portable-scratch, OCI assertion, and operator-documentation findings discovered during
the governed-read release promotion before cutting a stable public tag.

## [S] Scope

- Replace the machine-specific M0 scratch default with a canonical private per-UID temporary path.
- Preserve the explicit opt-in for arbitrary non-EXTENDED roots, private-parent validation, held
  directory identity, marker-owned cleanup, and local-Docker requirement.
- Decode the OCI Job's JSON and require a non-empty `version` field.
- Correct the rendered issue reference and document the complete combined M0 reader grants.

## [A] Analysis and red-team checks

- The first real M0 run exposed macOS `$TMPDIR` aliasing through `/var` to `/private/var`. The
  harness correctly rejected that non-canonical path, so the default is canonicalized rather than
  weakening the no-symlink rule.
- The second run exposed that the default private parent was prepared after the first entry
  validation. Preparation now happens before entry; the helper remains idempotent for focused
  safety tests.
- The generated default is the only non-EXTENDED root accepted without an opt-in. Any other
  non-EXTENDED override still requires `SITH_M0_ALLOW_NON_EXTENDED=1` and then must pass the same
  canonical, owner, mode, and inode checks.
- `CREATE INDEX` in migration `0006` remains transactional by design: the indexed table is created
  empty in that same migration, so no pre-existing workload can be blocked by that index creation.
- Authentication refusal delivery remains separately tracked in [#140](https://github.com/ArdurAI/sith/issues/140): the existing arbitrary `slog` sink contract cannot guarantee both nonblocking delivery and leak-free shutdown without a dedicated lifecycle-owned transport.

## [T] Tests and evidence

- Shell safety suite: PASS — `bash -n hack/experiments/m0-ocm-falsification.sh` and
  `make test-scripts` (19 assertions), including the generated default lifecycle and a symlinked
  temporary-directory canonicalization case.
- OCI assertion unit: PASS — `go test -race -count=1 -tags='e2e kind' -run
  '^TestValidateVersionOutput$' ./tests/e2e`; valid JSON without `version` and empty versions fail.
- Real M0 integration: PASS — `KIND=/Volumes/EXTENDED/MacData/tools/bin/kind make e2e-ocm` created
  a hub and two spokes under the portable default, proved scoped reads, Secrets/Nodes denial,
  outbound-only controls, credential replacement, direct adapter and runtime tests, then removed
  all three clusters. M0 elapsed time was 186 seconds.
- Real multi-cluster Kind integration: PASS — `KIND=/Volumes/EXTENDED/MacData/tools/bin/kind make
  e2e-kind` in 177.961 seconds.
- Repository CI: PASS — `make ci`, including race tests, static analysis (0 issues), dependency
  vulnerability scan (no vulnerabilities), shell safety, UI latency, and tagged binary e2e.
- Isolation and release: PASS — `make e2e-isolation` with PostgreSQL/RLS coverage and the fixed
  50,000-execution workspace fuzz campaign; `make release-check` rebuilt and verified all four
  Darwin/Linux archives twice, produced SPDX SBOMs, and rendered the formula.
- Independent local staged-diff review: one stale error-message finding, fixed and rerun through
  focused syntax, safety, OCI unit, formatting, and whitespace checks.

## [C] Checkpoint

- README review found no required change. The source, documentation, tests, session evidence, and
  validations are ready for a signed, DCO, GSTACK commit and a narrow PR into `dev`.
