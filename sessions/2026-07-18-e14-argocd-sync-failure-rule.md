# E14 R8 honest Argo CD sync-operation failure rule

**Builder:** Gnani Rahul · **Branch:** `gnanirahulnutakki/feat/e14-argocd-sync-rule`

**Slice:** E14 R8 / [#270](https://github.com/ArdurAI/sith/issues/270) · **Status:** local gates and review complete

**Base:** `origin/dev` at `22c6caa834442120d61add38009bacc154eda4fd`

## [G] Goal

Add a deterministic, evidence-cited rule for an Argo CD Application sync operation whose reviewed
operation phase is `Failed` or `Error`, without turning drift or health into a guessed failure.

## [S] Boundary

- Consume only attached, workspace-valid Argo TIMELINE `FactChange` evidence from projector
  protocol `1.0.0`, with matching source/provenance/entity identity and internally consistent
  change kind, phase, and event time.
- Project only canonical `change.kind=sync-failed`; discard revision, repository, operation
  message, conditions, raw CRD data, credentials, and all other payload fields.
- Preserve caller-declared coverage without inferring it from fact presence. Missing, unavailable,
  stale, or observation-stale TIMELINE evidence remains `unconfirmed`.
- State only that Argo CD reported a failed operation. Do not select a rendering, validation,
  authorization, hook, network, Kubernetes API, resource, health, or other root cause.
- Emit only a sensitive-marked, shell-quoted, read-only Application `kubectl describe` advisory.
- Do not fetch Argo, add a client or network call, retain data, alert, create an SLO, correlate
  fleet-wide, emit an intent, enter the PEP, dispatch, mutate, execute, complete F14.6, or claim the
  E12 connector framework is complete.

## [T] Verification plan

- Exercise the real Argo projector for both failed phases plus drift, successful/running,
  malformed, ambiguous, oversized, stale, unavailable, shell-hostile, and unrelated inputs.
- Require R8 in the sanitized deterministic replay corpus and round-trip text/JSON without
  discarded source fields.
- Run focused race tests, full CI/vulnerability checks, PostgreSQL forced-RLS and fuzz isolation,
  release/SBOM, Helm/OCI, and real two-cluster Kind gates.
- Secret-scan the complete diff, complete CodeRabbit review to zero actionable findings, sign the
  DCO/GSTACK commits, pass exact-head hosted checks, merge into `dev`, and verify exact post-merge
  CI/CodeQL plus empty review and security queues.

## [A] Primary-source decision

- Argo CD's stable trigger contract uses `app.status?.operationState.phase in ['Error', 'Failed']`
  for failed synchronization:
  <https://argo-cd.readthedocs.io/en/stable/operator-manual/notifications/triggers/>.
- The stable notification catalog describes `on-sync-failed` as Application synchronization
  failure and exposes operation details for investigation:
  <https://argo-cd.readthedocs.io/en/stable/operator-manual/notifications/catalog/>.
- Argo CD describes `OutOfSync` as live state differing from desired state; it is not by itself
  proof that a sync operation was attempted and failed:
  <https://argo-cd.readthedocs.io/en/stable/>.

These contracts support the failed-operation symptom and read-only inspection boundary. They do
not identify a root cause, so R8 retains the complete uncertainty set.

## Cost and operability

R8 reuses an existing bounded projector, workspace graph, and pure in-memory evaluator. It adds no
cloud resource, API request, watch, credential, storage, telemetry volume, or egress cost. A human
who runs the advisory may see sensitive Application status and must review it under their existing
kubeconfig authorization; Sith marks the command sensitive and never executes it.

## [V] Local implementation checkpoint — 2026-07-18

- Both `Failed` and `Error` pass through the real Argo projector and produce one canonical,
  entity-local R8 observation and verdict; revision and phase are absent from brain output.
- `OutOfSync`, `Unknown`, `Synced`, successful/running/terminating operations, near-miss values,
  malformed/oversized payloads, unknown fields, mismatched identity/provenance/protocol/time,
  history-shaped failure facts, and unattached facts fail closed.
- Coverage is cloned rather than aliased or inferred. Stale evidence and missing, unavailable, or
  stale TIMELINE coverage produce one cited `unconfirmed` R8 verdict with the gap named.
- Shell-hostile identifiers stay inert; the advisory remains sensitive, read-only, and has no PR
  diff. Two identical cross-cluster signals remain separate and never become fleet-wide.
- Focused `go test -race ./internal/brain ./internal/cli` passes.

## [V2] Full local and review proof — 2026-07-18

- `make ci` passes on the final source tree: formatting, vet, zero lint findings, `govulncheck`
  with no known reachable vulnerabilities, full repository race coverage, shell/pin policies,
  eight pinned Prometheus rules, performance, compiled E2E, and binary build.
- Digest-pinned PostgreSQL 18.4 forced-RLS tests pass with 76.2% `hubdb` coverage. Both fixed
  50,000-case workspace-isolation fuzz campaigns pass with four workers.
- Pinned Helm 4.2.3 and standalone linux/amd64 plus linux/arm64 OCI contracts pass.
- `make release-check` passes twice on the final source: module verification, two reproducible
  Darwin/Linux amd64/arm64 snapshots, archive and SPDX SBOM validation, Homebrew formula, and the
  immutable two-platform hub OCI layout built from release archives.
- The real digest-pinned Kubernetes v1.36.1 two-cluster Kind gate passes in 233.419 seconds under
  the race detector, including fleet fan-out, immutable OCI, and live Argo Application projection.
  Cleanup confirms zero Kind clusters and no Sith test containers.
- The first complete CodeRabbit CLI 0.6.5 review of all 13 changed/new files against exact base
  `22c6caa834442120d61add38009bacc154eda4fd` found one valid provenance edge case: a native ID that
  ended exactly at `#operation/` passed the prefix check without identifying an operation.
- The bridge now requires a non-empty operation identifier after the prefix while continuing to
  discard that identifier. A focused regression, full CI, isolation/fuzz, Helm/OCI, reproducible
  release, and real Kind reruns all pass after the fix. The second complete review reports zero
  findings.
- The corrected 13-file tree scans 225,127 bytes with zero recognized credential, private-key,
  token, JWT, authenticated-URL, or generic secret-assignment candidates.
- README review is complete. It accurately distinguishes cache-backed R1-R7 behavior from the
  graph-fed R8 surface, preserves every cause/non-execution nonclaim, and states that the current
  CLI does not fetch Argo or infer TIMELINE coverage.
- A later documentation-inclusive review found two minor README precision gaps: it abbreviated the
  advisory and did not enumerate every fail-closed graph gate. README now names the exact
  target-bound command and the workspace, provenance, protocol, identity, closed-payload,
  phase/change-kind, event-time, and explicit-coverage requirements. Final clean review remains
  required before staging.

## [C] Checkpoint #1

`2026-07-18/e14-argocd-sync-failure-rule#1` records the review-clean implementation and complete
local proof above on exact base `22c6caa834442120d61add38009bacc154eda4fd`. Next: create the
SSH-signed DCO commit, push one narrow PR into `dev`, require exact-head CI/CodeQL and empty review
and security queues, merge preserving the tested head, verify exact post-merge `dev` proof, close
the child issue, and synchronize the landed Notion and Obsidian checkpoints.
