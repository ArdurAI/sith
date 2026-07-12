# Session — 2026-07-12 — Release-history sync

**Builder:** Gnani Rahul · **Branch:** gnanirahulnutakki/chore/sync-main-release-history
**Slice:** release maintenance · **Status:** ready for review

---

## [G] Goal

Reconcile `main`'s already released history into `dev` before the next coherent dev-to-main release
PR, without dropping either line of verified work.

## [D] Decision

- The common ancestor is `1588efc`; `main` contains release PR #99 while `dev` contains subsequent
  verified E2 work (#101, #102, #107, #108).
- `git merge-tree --write-tree origin/dev origin/main` produced a clean tree. The actual history
  merge is content-neutral; the only file in this maintenance slice is this audit journal.
- The merge commit is SSH-signed, DCO-signed, and GSTACK-stamped. It preserves release ancestry for
  a truthful future dev-to-main PR rather than force-pushing or rewriting either branch.

## [T] Evidence

- `git merge --no-ff --no-commit origin/main` completed with no conflicts and no staged content
  changes; merge commit `76fe5d6` concludes the ancestry sync.
- Current `origin/dev` at the branch point is `466cec1`, whose exact post-merge CI `29191343824`
  passed build, race, RLS/fuzz, real two-kind integration, reproducible archive, SBOM, and formula
  gates.

## [S] Scope and safety

No product code, configuration, source behavior, credential, connector, endpoint, or release asset
changes here. This PR exists solely to make the branch graph safe for the next release promotion.

## [N] Next

Run the normal hosted CI on the sync PR, merge it into `dev` only when green, then open a coherent
dev-to-main release PR from the reconciled integration branch.
