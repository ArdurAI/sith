# E9 beta draft publication repair — 2026-07-14

Issue: #159
Branch: `gnanirahulnutakki/fix/e9-beta-draft-publish`
Base: `origin/dev` at `ff07b60ff934508dd694f338ffa017d3503da171`

## [G] Goal

Repair the beta publication step so a workflow-created draft release is resolved by its database ID
and can be published as a prerelease without replacing the latest stable release.

## [S] Incident boundary

- Signed `v0.3.0-beta.1` passed GitHub tag verification and the release workflow built, signed, and
  attested the draft artifacts.
- The final publication step used `GET /releases/tags/{tag}` and received 404 for the draft,
  leaving the draft unpublished. Stable `v0.2.1` remained latest.
- The failed tag and draft are preserved. No deletion, force-push, retagging, or manual publication
  is permitted. The corrected workflow will publish a new immutable `v0.3.0-beta.2` tag.

## [A] Evidence in progress

- The workflow now resolves `gh release view <tag> --json databaseId`, whose draft-aware view is
  already proven by the existing beta.1 draft.
- The policy test requires that lookup, rejects the draft-invisible tags endpoint, and keeps the
  raw-string `make_latest=false` assertion.
- Pending: focused and full gates, red-team review, hosted PR/post-merge CI, release promotion, and
  beta.2 artifact verification.

## [T] Test plan

1. Run policy scripts, `make ci`, and `make release-check` on the final diff.
2. Confirm stable publication remains unchanged and beta mutation still requires
   `draft=false`, `prerelease=true`, and raw string `make_latest=false`.
3. Land through `dev` and a reviewed release PR, then create a new signed beta.2 tag and verify its
   release state and macOS-arm64 archive.

## [C] Completion criteria

The issue closes only when beta.2 is published as a prerelease, `v0.2.1` remains latest, and the
macOS-arm64 release archive passes checksum, provenance, and local launch verification.
