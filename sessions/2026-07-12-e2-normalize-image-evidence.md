# Session — 2026-07-12 — E2 normalized image evidence

**Builder:** Gnani Rahul · **Branch:** gnanirahulnutakki/fix/e2-normalize-image-evidence
**Slice:** [#106](https://github.com/ArdurAI/sith/issues/106), E2 [#20](https://github.com/ArdurAI/sith/issues/20) · **Status:** ready for review

---

## [G] Goal

Prevent a local runtime-only Kubernetes `imageID`, a mutable tag, or an ambiguous multi-image Pod
from creating a fleet-wide Investigation Brain conclusion. Admit correlation evidence only when one
canonical `repository@sha256:<64>` reference is proven from Pod status.

## [D] Design

- Retain existing runtime image digests for local display but give them no graph or brain correlation
  authority.
- Normalize a known runtime prefix only when the remaining value passes the #105
  `ImageDigestFromRepoDigest` contract exactly. Never combine a mutable Pod spec image with a bare
  runtime digest.
- The brain requires exactly one normalized repository digest per record and revalidates it before
  grouping; zero or multiple candidates abstain from fleet-wide image correlation.

## [T] Evidence

- Focused golangci-lint and race tests pass: fleetcache **87.0%**, brain **86.9%** coverage.
- The existing real two-kind `sith investigate` integration passed with actual crashing fixture Pod
  status, proving valid same-image correlation remains bounded and explicit across two local clusters.
- Unit cases reject bare runtime IDs, tag-plus-digest strings, short digests, missing values, direct
  malformed brain observations, and ambiguous multi-image records.
- `make ci` passed: formatting, vet, golangci-lint, govulncheck, full race suite, M0 safety (15
  assertions), warm-cache performance, binary smoke, and standard e2e suite.
- `make e2e-isolation` passed: forced-RLS/destructive PostgreSQL suites (fleetcache **87.0%**;
  hubdb **72.4%**) and the fixed **50,000x** cross-workspace fuzz campaign.
- `make release-check` passed: two reproducible four-platform GoReleaser snapshots, SPDX SBOM
  verification, and generated Homebrew formula.
- Manual red-team review verified runtime-only data cannot reach fleet grouping, every candidate is
  revalidated before grouping, ambiguous multi-image records abstain, and cache cloning preserves
  the new field. CodeRabbit CLI remains unavailable locally, so no external diff was sent; no P0/P1
  finding remains.
- Post-gate cleanup confirmed zero kind clusters; Docker prune reclaimed **1.21 GB**. GitHub
  Dependabot, code-scanning, and secret-scanning queues were each **0** open alerts.

## [S] Scope and safety

This is a local cache/brain normalization change only. It adds no endpoint, account, telemetry
storage, connector verb, list/watch, secret, arbitrary source attribute, or write path.

## [N] Next

Check README applicability, create the signed/DCO/GSTACK checkpoint, publish the narrow PR into
`dev`, and merge only after its CI and exact post-merge CI are green.
