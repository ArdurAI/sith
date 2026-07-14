# Session — 2026-07-14 — E2 coverage assessment

**Issue:** [#148](https://github.com/ArdurAI/sith/issues/148)
**Branch:** `gnanirahulnutakki/feat/e2-coverage-assessment`
**Base:** `origin/dev` at `0704cf37c042aba05b6644b710647971951dbc00`

## [G] Goal

Export a conservative, typed assessment of existing fleet coverage so a future policy layer can
abstain on stale, unreachable, unaccounted, or inconsistent evidence without reinterpreting raw
counters.

## [S] Scope

- Add a pure `fleet.Coverage` assessment contract and table-driven unit coverage.
- Prove the retained stale result from the existing real two-spoke read-federation test is
  incomplete with explicit gaps.
- Do not add policy wiring, actions, endpoints, persistence, connectors, credentials, telemetry,
  or any ClusterGateway change.

## [A] Analysis and decision

- `Coverage.Complete` previously only compared requested and reachable counts plus stale length;
  it could not explain a gap and could report contradictory unreachable metadata as complete.
- The new assessment is evidence only, not an authorization decision. Empty requested coverage is
  complete evidence; a future typed intent still owns target validation.
- A stale scope may overlap an unreachable scope because retained stale evidence is expected after
  a failed refresh. Negative counts, contradictory accounting, blank names, and duplicate names
  are inconsistent and fail closed.
- The assessment also rejects more stale names than requested scopes; it cannot prove that the
  extra names belong to the requested set, so treating them as harmless would be fail-open.

## [T] Tests and evidence

- PASS — focused `go test -race -count=1 ./internal/fleet ./internal/hubfleet` after each contract
  change; table coverage includes complete, empty, stale, unreachable, unaccounted,
  contradictory, duplicate, blank, and surplus-stale scope cases.
- PASS — `make ci`: formatting, static analysis with zero findings, dependency vulnerability scan
  with no findings, full race suite, the existing safety contracts, and binary integration tests.
- PASS — `make e2e-isolation`: PostgreSQL forced-RLS/destructive coverage plus the fixed
  50,000-execution workspace-isolation fuzz campaign.
- PASS — `make release-check`: two four-platform snapshot builds, identical archive digests, SPDX
  SBOM generation, and formula rendering.
- PASS — final `KIND=/Volumes/EXTENDED/MacData/tools/bin/kind make e2e-kind` in 169.870 seconds;
  the two real spokes retained stale evidence after one failed collection and the assessment
  reported explicit unreachable and stale gaps.
- Red-team review: the first peer-review attempt reached analysis but returned no completion after
  a bounded wait. Manual review found and fixed the surplus-stale-cardinality consistency gap.
  The final CodeRabbit review completed with zero findings across all four staged files.

## [C] Checkpoint #1

- Conservative F2.5 coverage assessment implementation and all local validation are complete;
  the signed commit for this checkpoint carries the matching GSTACK trailer. Open questions
  touched: none; F2.5 exports evidence for a future policy layer but does not make or record a
  policy decision.
