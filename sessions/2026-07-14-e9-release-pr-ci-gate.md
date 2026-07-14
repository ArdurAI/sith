# E9 release-PR CI gate — 2026-07-14

Issue: #157
Branch: `gnanirahulnutakki/ci/release-pr-gate`
Base: `origin/dev` at `42d395135b034bcfb531b5657f3eb01b95ea7bb5`

## [G] Goal

Require the complete Sith CI workflow for `dev` to `main` release PRs and for the exact resulting
`main` push, so a release boundary has independent build, reproducibility, security, and real
two-cluster evidence.

## [S] Scope and safety

- The change expands only the existing CI branch trigger allowlist from `dev` to `dev, main`.
- No job, pin, credential, artifact, release tag, or branch-protection policy is weakened or
  removed.
- The maintainer guide requires green release-PR CI and exact post-merge `main` CI before tagging.

## [A] Evidence

- The hermetic release-PR policy test confirms CI listens to both push and pull-request events for
  `dev` and `main`, and that the guide requires release-PR CI.
- Existing beta-tag policy test remains green.
- Final-diff `make ci` passed formatting, vet, lint, reachable-vulnerability scan, full Go race
  suite, all shell policy tests, performance, subprocess E2E, and build.
- Final-diff `make release-check` passed two reproducible four-platform snapshots, archive/SBOM
  verification, formula rendering, and digest comparison.
- Manual red-team review confirmed that the only workflow delta is adding `main` to the existing
  `push` and `pull_request` branch allowlists. Existing jobs, permissions, pinned actions/tools,
  concurrency, and `dev` coverage are unchanged.
- CodeRabbit accepted the uncommitted diff and reached analysis but returned no review in the
  bounded attempt; it is not recorded as approval. Hosted PR CI, exact post-merge `dev` CI, and
  proof that release PR #156 runs its own full CI after this lands remain pending.

## [T] Test plan

1. Run `make ci` and `make release-check` on the final diff.
2. Review that no trigger or workflow permission broadening was introduced and that all existing
   job gates remain unchanged.
3. Land only after green PR CI and exact post-merge `dev` CI, then observe a new full CI run on
   #156 before promoting `main`.

## [C] Completion criteria

#157 closes only after the resulting `dev` to `main` release PR has an independently green full CI
run and a later exact `main` push CI record is available for the beta release commit.
