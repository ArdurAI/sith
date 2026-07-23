# OCM add-on wait lifecycle

- Builder: gnanirahulnutakki
- Effort: deep
- Branch: `gnanirahulnutakki/fix/ocm-addon-wait-lifecycle`
- Issue: `#198`
- Status: ready for review

## Goal

Make the M0 OCM add-on convergence wait tolerate asynchronous creation and delete/recreate
transitions without granting separate creation and availability timeout windows.

## Scope

- Use one absolute deadline for each `ManagedClusterAddOn` from first creation check through
  confirmed `Available=True`.
- Query only machine-formatted UID and `Available` condition fields from the current object.
- Treat NotFound, deletion, recreation, missing conditions, `False`, and `Unknown` as bounded
  progress states.
- Fail closed on authorization or API errors, malformed identity, malformed or duplicate
  `Available` conditions, and deadline exhaustion.
- Keep cluster registration waits, chart versions, OCM transport behavior, and Helm pin alignment
  outside this slice.

## Progress

[G] Repair the lifecycle race and doubled timeout window tracked by #198.
[S] Limit the implementation to the M0 experiment runner, deterministic safety harness, experiment
and roadmap evidence, and this session checkpoint.
[A] Revalidated current `dev`, confirmed #194 is owned by another active task, reserved a separate
EXTENDED worktree, and checked the official kubectl get/wait machine-output contracts.
[A] Replaced the two-phase get/wait sequence with a shared-deadline polling loop. Each get is bounded
by the remaining budget, suppresses only NotFound, validates closed UID/condition output, and
requires two consecutive ready reads for the same UID.
[T] The deterministic safety harness now covers delayed creation, old-object deletion plus new UID
recreation, terminal read failures without response-body leakage, duplicate/malformed conditions,
and shared-deadline exhaustion.

## Verification

- `bash -n` passes for the runner and its safety harness.
- `bash tests/scripts/m0_ocm_falsification_safety_test.sh` passes all 23 assertions.
- `git diff --check` passes.
- The repository README was reviewed; no user-facing command or product behavior changed, so the
  experiment and roadmap are the authoritative documentation for this harness-only correction.
- Focused safety passed five consecutive runs; the final post-review safety run also passed all 23
  assertions with `bash -n`, focused ShellCheck, and `git diff --check` green.
- `make ci` passed twice; the final run includes zero lint findings, no known vulnerabilities, the
  complete race suite, all shell policy suites, warm-cache performance, subprocess e2e, and build.
- `make e2e-isolation` passed the PostgreSQL/RLS suites and both 50,000-execution cross-workspace
  fuzzers.
- `make e2e-kind` passed the real two-cluster fan-out and OCI image contract in 159.546 seconds.
- `make e2e-ocm` passed the affected real hub-plus-two-spoke lab in 163 seconds: all four add-ons
  converged through the lifecycle-safe wait, topology/credential/RBAC/outbound-only controls passed,
  both direct ClusterProxy tests passed, and cleanup removed all clusters.
- `make release-check` passed two reproducible GoReleaser snapshots, SPDX SBOM verification,
  Homebrew formula generation, and the multi-platform release OCI layout.
- Red-team review added an explicit post-request absolute-deadline check and confirmed that only UID
  plus the closed Available status are parsed; kubectl stderr and object bodies remain suppressed.
- Hosted PR and exact post-merge `dev` evidence remain pending.

## Checkpoint

- `2026-07-16/ocm-addon-wait-lifecycle#1`
- `2026-07-16/ocm-addon-wait-lifecycle#2`

## Open questions

- None. A transient absent/missing condition remains retryable; malformed or duplicate condition
  values remain terminal.
