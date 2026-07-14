# Session — 2026-07-12 — Release branch safety

**Builder:** Gnani Rahul · **Branch:** gnanirahulnutakki/chore/release-branch-safety
**Slice:** [#117](https://github.com/ArdurAI/sith/issues/117) · **Status:** ready for review

---

## [G] Goal

Prevent a `dev → main` release PR cleanup from deleting or rewriting Sith's durable integration or
release branch.

## [D] Design

- GitHub branch protection is intentionally minimal: both `dev` and `main` reject deletion and
  force-pushes, without changing the existing required-check, review, or merge workflow.
- The release procedure now treats `dev` as a durable source branch. `--delete-branch` is permitted
  only for a merged feature branch, never a release PR headed by `dev`.

## [T] Evidence

- Before correction, both branch-protection reads returned `404 Branch not protected`; the repository
  had no rulesets.
- The GitHub branch-protection read-back now reports `allow_deletions=false` and
  `allow_force_pushes=false` for both `dev` and `main`, with required status checks and review
  policy unchanged (`null`).
- The exact restored `dev` history was preserved; this slice makes no history rewrite, runtime, or
  dependency change.
- `make ci` passed formatting, lint, vulnerability scanning, race tests, the M0 safety suite,
  latency, and standard e2e coverage. `make release-check` passed two reproducible four-platform
  snapshots, distribution verification, and Homebrew formula generation.
- Manual red-team review confirmed the protection payload narrows only destructive operations, the
  release wording cannot be mistaken for feature-branch cleanup, and no secret, runtime, or
  telemetry path changed. GitHub Dependabot, code-scanning, and secret-scanning queues were each
  zero open alerts before this documentation-only change.

## [S] Scope and safety

This is a release-integrity correction only. It does not add a review quorum, change CI status
requirements, broaden repository access, alter a feature path, or emit telemetry.

## [N] Next

Check README, create the signed/DCO/GSTACK checkpoint, then open a narrow PR into `dev`. Merge only
after hosted CI is green, verify the exact post-merge `dev` CI and GitHub security queues, and close
the corrective issue.
