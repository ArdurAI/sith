# Sith — Scope & Defaults

**Status:** planning · **Date:** 2026-07-08

This document is the anti-drift contract. When a feature request arrives, it is checked
against this document first. "It would be useful" is not sufficient to be in scope; it must
be *part of the wedge* (see [`CHARTER.md`](CHARTER.md) §4).

---

## In scope

- **Read federation** across many clusters: normalized fleet inventory, health, alerts,
  drift, and image/CVE facts, with **cross-cluster correlation** as a first-class query.
- **Action federation**: a **closed vocabulary of typed intents** dispatched to spokes,
  signed, locally re-validated, executed with local scoped identity.
- **Policy federation**: environment gates, multi-approver flows, wave/canary ordering,
  partial-failure/rollback, idempotency, and abstention.
- **Governance spine**: multi-tenant `Workspace` isolation, RBAC, signed-token authn,
  audit-log (what-happened) + decision-ledger (why-allowed).
- **Governed MCP server** exposing the above so external agents inherit the same governance.
- **Adoption of OCM** (`cluster-proxy`, `managed-serviceaccount`, `ManagedCluster`) as the
  transport/identity substrate.
- **Integration with Ardur** as PDP, identity broker, and decision-ledger.

## Out of scope (non-goals)

| Not this | Because that is | Owned by |
|---|---|---|
| A developer portal / IDP / service catalog | a different product category | Backstage, Port, Cortex, OpsLevel |
| A GitOps controller / desired-state reconciler | Sith *opens PRs*; it does not reconcile | Argo CD, Flux |
| A multi-cluster scheduler / workload placement | Sith governs *operations*, not placement | Karmada, OCM Placement |
| A telemetry lake / metrics-logs backend | Sith *reads* health; it does not store series | Prometheus, Grafana, Loki, Datadog |
| A bespoke cross-cluster tunnel / agent transport | commodity, security-sensitive plumbing | OCM `cluster-proxy`, Konnectivity, remotedialer |
| A general policy engine | Sith *uses* one (Ardur) at the intent boundary | Ardur / OPA-class engines |
| A single-cluster console / IDE | local tools already serve this well | Headlamp, k9s, Lens |

## Permanently excluded from the action model

These are **never** added to the verb vocabulary, at any phase:

- `exec` / arbitrary shell into a pod or node.
- Free-form `kubectl apply` of arbitrary manifests.
- **Secret** creation/mutation/read-through.
- **RBAC** object mutation (Role/ClusterRole/Bindings).

Rationale and the closed vocabulary are in
[ADR-0004](adr/0004-typed-intent-action-model.md).

## Defaults (safe by default)

- **Read-only first.** New tenants and new integrations start read-only.
- **`prod` never auto-acts.** Any intent targeting a `prod`-labelled cluster requires
  approval; multi-cluster `prod` fan-out requires multi-approver.
- **First write is `gitops.open-pr`.** Direct mutations (`argocd.sync`, etc.) are enabled
  per-workspace only after the PR path is proven.
- **Dry-run first** for every verb that supports it; show the plan/diff before execute.
- **Fail-safe.** Unknown verb, unschema'd args, unresolved target, stale fleet view, or
  missing approval → **refuse**, never "best effort".
- **Abstain on incomplete visibility.** Fleet-wide actions require a fresh, complete view
  of the targeted set, or Sith refuses and explains.
- **Least privilege everywhere.** The AI/agent identity ceiling is strictly below the
  human's; the spoke identity is scoped to the verb's needs.
- **Everything audited.** Proposed + approved + dry-run + executed, always — for humans and
  agents alike.

## Scope changes

Adding a verb, adding an OCM addon dependency, or relaxing a default is an **ADR-level
decision** recorded in [`docs/adr/`](adr/). Scope creep is a design defect, not a feature.
