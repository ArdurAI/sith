# ADR 0008: Deterministic local advisory brain and evidence contract

**Status:** Accepted
**Date:** 2026-07-11
**Decision owners:** E14 / F14.5 (#48)

## Context

The Phase-L client has a workspace-scoped, cache-first view of Pods, Deployments, Events, and
Nodes across kubeconfig contexts. F14.5 must turn supported degraded signals into ranked,
evidence-cited hypotheses without adding an LLM, telemetry lake, write path, account, or data
egress. The authoritative behavior is defined in
[`../specs/E2-readfed-brain-integrations.md`](../specs/E2-readfed-brain-integrations.md) sections
2.6 and 3.2-3.6.

Raw Kubernetes object shapes are not a stable rule API. For example, Kubernetes 1.36 may expose a
rapidly restarting nonzero-exit container as repeated `terminated=Error` snapshots instead of a
durable `waiting=CrashLoopBackOff` string. Conversely, a completed Job must not be diagnosed as a
crash loop. Fleet-wide correlation must also use a content-addressed image digest, never a tag or
bare workload name.

## Decision

1. `internal/brain` consumes a closed normalized observation envelope: entity reference, evidence
   lens, key, value, source, time, and staleness. It performs no I/O. Kubernetes decoding stays in
   `fleetcache`; cache-to-rule projection stays in one adapter.
2. The catalog contains stable canonical R1-R6 rules plus bounded adjacent R7 image-pull, R8 Argo
   sync-operation failure, and R9 GitHub Actions workflow-run failure rules. Matching is exact and
   deterministic. Verdict ordering is fleet-wide correlation, score, rule ID, then scope. R1 can
   compose as the cause of R3 when a recent change and repeated failure are attached to the same
   entity.
3. Required and strengthening lenses are checked per entity. Fleet-level connector availability
   cannot satisfy a workload's evidence gate. Missing or stale required evidence yields
   `unconfirmed`; missing variant evidence yields `detected` plus named missing lenses.
4. R3 accepts either the native `CrashLoopBackOff` condition or repeated nonzero `Error`
   termination with at least two restarts. Exit-zero `Completed` is not a match. Citations expose
   the normalized form used.
5. Cross-cluster correlation requires the same `sha256:` image digest on at least two contexts.
   The correlated hypothesis is ranked ahead of its per-context findings and names all contexts.
6. Local output is an inert advisory command or PR-change description. A source boundary test
   rejects imports from connector, local-operation, and MCP packages; the brain has no plan,
   execute, intent, PEP, or dispatch seam.

### 2026-07-18 extension: adjacent R7 image-pull failure

R7 is the first adjacent rule admitted by the existing observation schema. It matches only exact,
case-insensitive `ImagePullBackOff` or `ErrImagePull` values from the sanitized LIVE
`pod.reason` projection. The cited waiting reason proves an image-pull failure or backoff but does
not identify registry authentication, image-reference, reachability, rate-limit, platform, or any
other underlying cause. R7 therefore emits only a sensitive-marked, read-only
`kubectl describe pod` advisory and is explicitly excluded from fleet-wide image-digest
correlation. It adds no registry probe, credential access, Event-message retention, connector,
write path, storage, or network egress.

This boundary follows Kubernetes' documented container-state contract: a waiting container is
still completing startup operations such as pulling its image, its `Reason` summarizes that state,
and `kubectl describe pod` is the documented inspection surface. See the upstream
[Pod lifecycle](https://kubernetes.io/docs/concepts/workloads/pods/pod-lifecycle/#container-states)
and [Pod debugging](https://kubernetes.io/docs/tasks/debug/debug-application/debug-pods/) guides.

### 2026-07-18 extension: adjacent R8 Argo sync-operation failure

R8 consumes the already-reviewed graph contract rather than raw Argo CRDs. `FromGraphFacts`
first validates the workspace-bounded graph and then admits only an attached TIMELINE
`FactChange` whose source kind and provenance adapter are `argocd`, protocol is `1.0.0`, and source
and entity identify the same Application. Its closed payload must pair `change_kind=sync-failed`
with operation phase `Failed` or `Error`, and its event time must equal the fact observation time.
Only canonical `change.kind=sync-failed` enters the evaluator; revision, repository, messages,
conditions, raw CRD data, and operation payload fields are discarded.

Graph projection copies caller-declared lens coverage but never infers it from fact presence.
Missing, unavailable, stale, or observation-stale TIMELINE evidence therefore produces an
`unconfirmed` R8 verdict. `OutOfSync` is desired-state drift and is not a failed-operation signal.
R8 states only that Argo CD reported a failed operation and leaves rendering, validation,
authorization, hooks, networking, Kubernetes API, resource, and other causes unresolved. Its only
advisory is a sensitive-marked, read-only `kubectl describe application.argoproj.io` command.

This boundary follows Argo CD's stable notification trigger, where failed synchronization is
defined by `app.status?.operationState.phase` being `Error` or `Failed`, and the stable catalog's
`on-sync-failed` operation-failure contract. See the upstream
[notification triggers](https://argo-cd.readthedocs.io/en/stable/operator-manual/notifications/triggers/)
and [notification catalog](https://argo-cd.readthedocs.io/en/stable/operator-manual/notifications/catalog/).

### 2026-07-18 extension: adjacent R9 GitHub Actions workflow-run failure

R9 consumes one already-authorized GitHub REST `Get a workflow run` response through a pure
projector. Trusted host, owner, repository, run ID, and collection time remain caller-supplied. The
response must agree with that identity, contain a positive workflow ID and attempt, use a recognized
status and conclusion, and provide a bounded event timestamp. Only exact status `completed` paired
with `failure`, `timed_out`, or `startup_failure` emits an unattached TIMELINE `FactChange`;
incomplete and completed non-failure runs abstain, while unknown or internally inconsistent input
fails closed. Unknown source fields are deliberately discarded, but duplicate JSON members are
rejected before decoding.

`FromGraphFacts` admits that fact only when source kind and provenance adapter are `github`, protocol
is `workflow-runs/2026-03-10`, the resource is an unattached `WorkflowRun`, host/owner/run/attempt and
native identities agree, the closed payload carries an accepted failure conclusion, and its event
time equals the fact observation time. The evaluator sees only canonical
`change.kind=workflow-run-failed`; conclusion, workflow ID, job and step data, logs, actors, branches,
commits, URLs, unknown response fields, and raw JSON do not enter its observation or output.

R9 states only that GitHub reported a completed failed run. It does not choose among code,
configuration, credential, permission, capacity, dependency, or other causes, and it does not infer
repository-to-workload or cross-host relationships. Its only advisory is sensitive human guidance
to inspect failed jobs and logs before considering a rerun. GitHub documents the REST response
contract in [Workflow runs](https://docs.github.com/en/rest/actions/workflow-runs?apiVersion=2026-03-10)
and the closed conclusion vocabulary in
[Checks](https://docs.github.com/en/rest/guides/using-the-rest-api-to-interact-with-checks).

## Consequences

- Investigations are offline, reproducible, replayable, and inspectable. The same observation
  corpus always produces the same ranked verdicts.
- LIVE-only evidence can report an observed OOM or repeated failure while refusing to guess the
  telemetry-dependent cause variant. This is intentionally less confident than an opaque guess.
- Phase L does not auto-port-forward to Prometheus/Loki, retain telemetry series, read Git desired
  state, or infer absent evidence. Those connectors can later emit the same observation contract.
- The existing cache-backed CLI does not fetch Argo Applications or GitHub workflow runs. R8 and R9
  are available only to callers that already possess validated graph facts and explicitly declare
  TIMELINE coverage.
- There is no hosted or cloud cost. Runtime cost is one existing tier-1 hydration pass plus
  in-memory rule evaluation over the returned records or an existing bounded graph. No extra
  Kubernetes watch, Argo/GitHub request, storage, or network egress is introduced.
- Advisory strings are not shell execution. Operators remain responsible for reviewing and
  running a suggestion with their own kubeconfig identity.

## Alternatives considered

- **Parse raw Kubernetes JSON inside every rule:** rejected because schema/presentation details
  would couple the reasoner to client-go and make replay fixtures brittle.
- **Treat missing telemetry as a negative signal:** rejected because absence of a connector is not
  evidence that a hypothesis is false.
- **Correlate image tags or workload names:** rejected because mutable tags and user-controlled
  names can collide across tenants and clusters.
- **Call a model or hosted diagnosis API:** rejected because it breaks determinism, offline use,
  and the permanent local no-egress invariant.
- **Emit or dispatch typed intents locally:** rejected because F14.5 is advisory only; governed
  plans belong to the future hub write path.
