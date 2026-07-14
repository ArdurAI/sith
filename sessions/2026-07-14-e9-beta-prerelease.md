# E9 beta prerelease policy — 2026-07-14

Issue: #154
Branch: `gnanirahulnutakki/feat/e9-beta-prerelease`
Base: `origin/dev` at `877f37fb13948eb755da41c3edadd7daa78dd22a`

## Scope

Enable exactly one additional release tag form: `vMAJOR.MINOR.PATCH-beta.N`. A beta stays
main-ancestry-only, annotated, GitHub-verified, and signed. It uses the existing archive, SPDX
SBOM, Sigstore, and attestation pipeline, publishes as a GitHub prerelease, and never replaces
the immutable latest stable release. No mutable release, tag rewrite, dev release, alternate
registry, or Homebrew beta channel is introduced.

## [G] Goal

Deliver a truthful signed macOS-arm64 beta release without weakening the existing stable-release
boundary.

## [S] Safety and design checks

- A repository-owned classifier is the sole tag-shape authority used by the workflow and hermetic
  policy test.
- The classifier accepts canonical numeric stable and beta forms only; leading zero components,
  non-beta prerelease labels, build metadata, and trailing input are rejected.
- Existing main-ancestry, annotated-tag, and GitHub signature-verification gates remain in the
  workflow after classification.
- The beta publication API request explicitly sets both `prerelease=true` and `make_latest=false`;
  the stable publication path is unchanged.

## [A] Evidence

- Focused policy test: passed accepted stable/beta forms, rejected malformed forms, and asserted
  the workflow, the raw-string `make_latest` field, and maintainer-guide constraints.
- Existing release tag identity-guide test: passed all six assertions.
- Final-diff `make ci`: passed formatting, vet, lint, reachable-vulnerability scan, full Go race
  suite, shell safety suites, performance check, subprocess E2E, and binary build.
- Final-diff `make release-check`: passed two reproducible four-platform GoReleaser snapshots,
  archive/SBOM verification, formula rendering, and digest comparison.
- `make e2e-kind`: passed the real two-cluster fleet and OCI contract tests. The explicitly named
  disposable Kind clusters were deleted after the check; no unrelated Docker workload was pruned.
- Manual red-team review found that `gh api -F make_latest=false` would send a boolean even though
  the Releases API requires a string enum. The workflow now uses raw `-f make_latest=false`, and
  the policy test requires that exact form.
- CodeRabbit accepted the uncommitted diff and reached analysis, but returned no review within the
  bounded attempt. Repository status reports that reviews are disabled for the `dev` base branch,
  so this is not recorded as an approval. Hosted CI and publication evidence remain pending.

## [T] Test plan

1. Run the normal CI and reproducible release-snapshot gates.
2. Red-team the classifier boundary, publication branch, and preservation of signature/ancestry
   controls.
3. Land only after green PR and exact post-merge `dev` CI, then make a separate reviewed
   `dev -> main` release PR.
4. Create and verify one signed annotated beta tag, its public prerelease state, latest-stable
   preservation, macOS-arm64 archive, checksum, provenance, and local binary/UI smoke.

## [C] Completion criteria

The issue closes only after the beta artifact has passed the release workflow, its macOS-arm64
archive has been verified and launched locally, the prior stable remains latest, and security
queues are clean.
