# Session — 2026-07-14 — release tag identity

**Issue:** [#146](https://github.com/ArdurAI/sith/issues/146)
**Branch:** `gnanirahulnutakki/docs/release-tag-identity`
**Base:** `origin/dev` at `aa168bf203e9c75815cf460f1754c7d1c3d4881d`

## [G] Goal

Document and test the release-tag identity preflight so a locally valid SSH signature cannot be
mistaken for GitHub-verifiable release authorization.

## [S] Scope

- Add a privacy-preserving maintainer preflight and post-push GitHub verification command to the
  release guide.
- Add a focused documentation-contract test to the existing operator-script gate.
- Keep the release verification gate unchanged; do not alter tags, release assets, or cloud access.

## [A] Analysis and decision

- The stable release workflow correctly rejected an annotated tag whose local SSH signature was
  valid but whose tagger identity was not recognized by GitHub. No draft or public artifacts were
  created for that rejected tag.
- The recovery used a new immutable patch tag on the same reviewed commit with an account identity
  GitHub verified. The resulting release passed its signature, provenance, checksum, and SBOM
  checks.
- The guide now separates local signature verification from GitHub's tag-object verification and
  explicitly prohibits deleting, force-pushing, or retagging a published name.
- Red-team review found that a direct paginated-API-to-`grep -q` pipeline could surface a producer
  SIGPIPE under `pipefail`. The guide captures the verified-email list before the exact match.
- A second review identified that verified email alone cannot bind the configured local SSH key to
  the GitHub account. The preflight now compares normalized public key material against the
  account's public signing-key endpoint without requesting broader token scope or printing values.

## [T] Tests and evidence

- PASS — `bash -n tests/scripts/release_tag_identity_guide_test.sh` and the focused guide
  contract suite; its six assertions cover email and SSH-key identity preflight, local tag
  verification, GitHub tag-object verification, no-rewrite rule, and patch-version recovery.
- PASS — `make ci`, including formatting, vet, static analysis with zero findings, dependency
  vulnerability scanning with no findings, race tests, the existing 19 M0 safety assertions, the
  new guide-contract assertions, latency budget, and tagged binary e2e.
- PASS — `make e2e-isolation`: PostgreSQL forced-RLS/destructive coverage plus the fixed
  50,000-execution workspace fuzz campaign.
- PASS — `make release-check`: two complete four-platform snapshot builds, archive digest
  comparison, SPDX SBOM generation, and formula rendering.
- PASS — final `KIND=/Volumes/EXTENDED/MacData/tools/bin/kind make e2e-kind` in 163.851 seconds.
- Red-team review: fixed the `pipefail`/SIGPIPE preflight issue and the missing registered
  signing-key check. A final reviewer retry became nonresponsive after a bounded wait; manual
  staged-diff, whitespace, focused-contract, and live public-key-match checks found no remaining
  issue.
- Pending signed commit, pull-request CI, and exact post-merge verification.

## [C] Checkpoint #1

- Pending signed commit. Open questions touched: none; the existing immutable-release and
  GitHub-verification defaults remain unchanged.
