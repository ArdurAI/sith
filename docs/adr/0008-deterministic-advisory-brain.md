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
2. The catalog contains stable R1-R6 rules. Matching is exact and deterministic. Verdict ordering
   is fleet-wide correlation, score, rule ID, then scope. R1 can compose as the cause of R3 when a
   recent change and repeated failure are attached to the same entity.
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

## Consequences

- Investigations are offline, reproducible, replayable, and inspectable. The same observation
  corpus always produces the same ranked verdicts.
- LIVE-only evidence can report an observed OOM or repeated failure while refusing to guess the
  telemetry-dependent cause variant. This is intentionally less confident than an opaque guess.
- Phase L does not auto-port-forward to Prometheus/Loki, retain telemetry series, read Git desired
  state, or infer absent evidence. Those connectors can later emit the same observation contract.
- There is no hosted or cloud cost. Runtime cost is one existing tier-1 hydration pass plus
  in-memory rule evaluation over the returned records. No extra Kubernetes watch is introduced.
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
