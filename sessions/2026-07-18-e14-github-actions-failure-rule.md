# Session — 2026-07-18 — E14 GitHub Actions workflow-run failure rule

**Builder:** Gnani Rahul · **Branch:** `gnanirahulnutakki/feat/e14-github-actions-failure-rule`
**Slice:** [#278](https://github.com/ArdurAI/sith/issues/278), E14
[#46](https://github.com/ArdurAI/sith/issues/46) · **Status:** complete local proof; ready for signed commit

## [G] Goal

Add an honest adjacent R9 rule that recognizes a GitHub Actions workflow-run failure from one
already-authorized GitHub REST response without inventing a root cause or adding a fetch, credential,
write, alert, or execution path.

## [D] Design

- Keep host, owner, repository, run ID, collection time, response retrieval, and TIMELINE coverage
  caller-owned.
- Normalize only an internally consistent `Get a workflow run` response under GitHub REST API
  version `2026-03-10`.
- Emit one unattached source-native TIMELINE fact only for exact status `completed` and conclusion
  `failure`, `timed_out`, or `startup_failure`.
- Abstain for incomplete and completed non-failure runs. Fail closed for unknown states, duplicate
  JSON members, malformed or oversized responses, invalid timestamps, and identity disagreement.
- Bridge only exact `github` / `WorkflowRun` / `workflow-runs/2026-03-10` facts whose closed payload,
  run/attempt/native identity, source, and event time agree.
- Expose only canonical `change.kind=workflow-run-failed` to the deterministic evaluator. R9 stays
  entity-local and requires explicit caller-declared TIMELINE coverage.

## [S] Security and non-claims

The projection retains no job, step, log, actor, branch, commit, URL, token, annotation, unknown
field, or raw response. It does not infer repository-to-workload identity, correlate across hosts,
diagnose code/configuration/credential/permission/capacity/dependency causes, create an alert or SLO,
load a token, call GitHub, persist data, form a typed intent, cross the PEP, mutate, dispatch, rerun,
or execute. The advisory is sensitive human inspection guidance only.

## [T] Focused proof

- Positive, abstention, malformed/duplicate/oversized/invalid-UTF-8, identity, status, conclusion,
  timestamp, graph-boundary, stale-coverage, exact applicability, replay, renderer, and non-correlation
  tests pass under the race detector.
- A 50,000-execution workflow-projector fuzz campaign preserves bounded valid-output invariants.
- Complete repository CI passes with zero lint findings, no reachable vulnerabilities, 87.1% brain
  coverage, 92.8% GitHub connector coverage, policy scripts, alert contracts, performance, e2e smoke,
  and production build.
- PostgreSQL 18.4 forced-RLS isolation passes, as do both 50,000-execution cross-workspace fuzzers.
- Helm and multi-platform OCI contracts pass. Four-platform release archives rebuild identically;
  SPDX SBOM, Homebrew, and release-derived hub OCI verification passes.
- The pinned Kubernetes 1.36.1 two-cluster fleet/image/Argo suite passes in 231.116 seconds; teardown
  leaves no Kind clusters or Kind containers.
- CodeRabbit's first 17-file review found two valid issues: ambiguous correlation wording and an
  exact workflow-protocol fact that could bypass provenance validation when both source fields were
  malformed. A later synchronized-diff review found Go's case-insensitive JSON field matching could
  accept mixed-case aliases. All three are fixed in the response projector and graph bridge with
  regression tests; the final complete review reports zero findings.
- Manual red-team review additionally makes exact workflow-protocol facts with non-TIMELINE fact
  types fail explicitly. README review is complete.
- A high-signal scan of the complete changed-file set reports zero credential or private-key
  candidates.

## [O] Operability and cost

The slice adds bounded in-memory JSON validation and one small normalized fact per accepted response.
It introduces no network request, retained store, queue, controller, hosted service, telemetry series,
or recurring cloud cost. A future reader would still incur GitHub API quota and any log-download
egress; neither is implemented here.

## [P] Primary sources

- [GitHub REST workflow runs, API 2026-03-10](https://docs.github.com/en/rest/actions/workflow-runs?apiVersion=2026-03-10)
- [GitHub Checks status and conclusion contract](https://docs.github.com/en/rest/guides/using-the-rest-api-to-interact-with-checks)
- [GitHub workflow-run history](https://docs.github.com/en/actions/monitoring-and-troubleshooting-workflows/monitoring-workflows/viewing-workflow-run-history)

## [N] Next

Create one signed DCO/GSTACK commit, publish a PR to `dev`, require
exact-head CI/CodeQL/hosted CodeRabbit, merge without rewriting the signed head, prove exact
post-merge `dev` checks, close #278, update E14 #46 without claiming the epic is complete, and recheck
the GitHub security queues.

## [C] Checkpoint #1

Pending signed implementation commit — bounded R9, documentation, complete local proof, repeated
zero-finding final review, and knowledge checkpoints are frozen; next: verify the signed commit,
push, and require the exact hosted gates before merge.
