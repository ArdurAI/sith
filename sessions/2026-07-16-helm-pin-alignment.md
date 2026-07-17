# Helm tooling pin alignment

- Builder: gnanirahulnutakki
- Effort: standard
- Branch: `gnanirahulnutakki/fix/helm-pin-alignment`
- Issue: `#197`
- Status: ready for review

## Goal

Align every live Helm tool boundary on the official v4.2.3 patch release and reject version
lookalikes that could bypass a prefix comparison.

## Scope

- Pin CI, the hub chart contract, the M0 OCM runner, and current experiment documentation to
  `v4.2.3`.
- Verify CI's Linux amd64 archive against the official SHA-256
  `e9b88b4ee95b18c706839c28d3a0220e5bc470e9cd9262410c90793c45ff8b7c`.
- Accept only the exact semantic release or Helm's real `+g<hex-commit>` build metadata.
- Add policy coverage that fails if executable and documented pins diverge.
- Leave historical session records unchanged; this session is the dated evidence for the new pin.

## Progress

[G] Repair the stale and divergent Helm pins tracked by #197.
[S] Limit the slice to tool-version admission, checksum verification, policy coverage, current
experiment/roadmap documentation, and this session checkpoint.
[A] Revalidated `origin/dev`, confirmed there is no active #197 owner or PR, and created a separate
EXTENDED worktree.
[A] Queried the official Helm GitHub release API: v4.2.3 is a non-draft, non-prerelease release
published 2026-07-09. The official Linux amd64 checksum endpoint returns the pinned digest above.
[A] Downloaded the official Darwin arm64 archive over TLS, verified its published SHA-256, and
observed the real short-version output `v4.2.3+g43e8b7f`.
[A] Aligned CI, the M0 runner, the real Helm chart contract, and current experiment evidence on
v4.2.3. Both shell and Go validators now accept only the exact release or lowercase
`+g<7-40 hex>` commit metadata.
[T] Added a cross-file policy gate plus positive and adversarial version cases covering patch
lookalikes, prereleases, vendor suffixes, short hashes, uppercase hashes, whitespace, and extra
output.
[A] Rebasing before commit incorporated #194's disjoint worker-pool merge (`231c89e`) so the final
signed slice starts from the current live `dev` integration head.

## Verification

- `bash -n`, focused ShellCheck (excluding the runner's pre-existing SC2174), and
  `git diff --check` pass.
- `tests/scripts/helm_tooling_policy_test.sh` passes all 7 cross-file pin and checksum assertions.
- `tests/scripts/m0_ocm_falsification_safety_test.sh` passes all 29 assertions, including the
  exact-version and five lookalike rejection cases.
- `make e2e-helm HELM=/Volumes/EXTENDED/MacData/tools/bin/helm-v4.2.3` passes the real chart
  contract in 2.374 seconds on the final rebased tree with the checksum-verified upstream binary.
- Final `make ci` on the rebased live-`dev` tree passes with zero lint findings, no known
  vulnerabilities, the full race suite, all shell policy suites, performance, subprocess e2e,
  and build.
- `make e2e-isolation` passes PostgreSQL/RLS controls and both 50,000-execution cross-workspace
  fuzz campaigns.
- `make e2e-kind` passes the two-cluster fan-out and OCI contract in 153.604 seconds.
- `make release-check` passes two reproducible snapshots, SPDX SBOM verification, Homebrew formula
  generation, and the multi-platform release OCI layout. The first invocation inherited the
  machine's stale `/Users/nutakki/go` global GOPATH and stopped before artifact creation; the
  verified rerun used the canonical EXTENDED GOPATH.
- `make e2e-ocm` passes the real hub-plus-two-spoke lab with the verified Helm v4.2.3 binary:
  all four add-ons converged, M0 passed in 220 seconds, both direct ClusterProxy tests passed, and
  cleanup removed all three clusters.
- Red-team review confirmed there is no prefix fallback: malformed pins fail the cross-file gate,
  shell command substitution admits no extra output, and the Go path removes only the one expected
  trailing newline before applying the anchored release pattern.
- The README was reviewed before commit. No user command changes; the experiment and roadmap docs
  are the authoritative surfaces for this tool-policy correction.

## Checkpoint

- `2026-07-16/helm-pin-alignment#1`

## Open questions

- None. Custom vendor suffixes are deliberately rejected because they are not the verified
  upstream release artifact.
