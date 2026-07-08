# Sith

> **Status: planning.** This repository contains **plan-only** artifacts — charter,
> architecture, ADRs, threat model, roadmap, and competitive analysis. There is **no
> product or application code**, by design. The owner reviews this plan before any
> implementation begins.

## What Sith is

**Sith is a governed, multi-tenant, cross-cluster operations *federation* control plane
for generic Kubernetes fleets.**

**The one-sentence job:** *give an operator one governed place to **see** and safely
**act** across many Kubernetes clusters — while deep cluster access stays local to each
cluster.*

Sith is **built on [Open Cluster Management (OCM)](https://open-cluster-management.io/)**
(a CNCF Sandbox project). It does **not** re-implement the cross-cluster transport —
that plumbing already exists and is hardened. Sith is the **governance + federation +
AI/MCP layer** on top of it.

## What Sith is *not* (explicit non-goals)

- ❌ **Not a developer portal / IDP.** (That is Backstage / Port / Cortex / OpsLevel territory.)
- ❌ **Not a GitOps controller.** (That is Argo CD / Flux. Sith *opens PRs*; it does not reconcile desired state.)
- ❌ **Not a multi-cluster scheduler / workload-placement engine.** (That is Karmada / OCM Placement.)
- ❌ **Not a telemetry lake / observability backend.** (That is Prometheus / Grafana / Loki / Datadog. Sith *reads* health; it does not store metrics.)

If Sith drifts into any of these, it has lost its reason to exist.

## The wedge — three federations

The commodity plumbing (an outbound-only per-cluster agent + a central hub reaching
cluster-local services) is **not** the differentiator and is **not** built bespoke — it
is adopted from OCM (`cluster-proxy` + `managed-serviceaccount`). What Sith owns on top:

1. **Read federation** — a normalized fleet model (inventory / health / alerts) built
   from OCM-brokered reads, enabling **cross-cluster correlation** that single-cluster
   tools structurally cannot do ("every cluster where `payments` is Degraded", "which of
   my 40 clusters run image X with CVE Y").
2. **Action federation** — the *only* writes are **typed intents** from a **closed verb
   vocabulary** (`argocd.sync|rollback`, `rollout.promote|abort`, `deployment.scale|restart`,
   `gitops.open-pr`). **Never a shell. Never free-form `apply`. Never secret/RBAC
   mutation.** The safest first write is `gitops.open-pr` — a human merges it. Each spoke
   agent independently validates every intent against a **local allowlist** and executes
   with its **own scoped identity** (defense-in-depth). Intents are **signed**.
3. **Policy federation** — the genuinely novel hard part: one intent can fan out to N
   clusters. Sith reasons about fan-out — environment gates (prod = multi-approver, never
   auto), wave/canary ordering with a gate per wave, partial-failure / auto-rollback,
   idempotency, and **federation-specific abstention** ("37/40 clusters visible, 3 stale
   >10m — refuse the fleet sync").

**[Ardur](https://github.com/ArdurAI/ardur)** is the **policy decision point (PDP)** for
every intent, the broker of scoped execution identity, and the **decision-ledger**
(*why-allowed*) that complements the action **audit-log** (*what-happened*).

Sith is also **exposed as a governed MCP server**, so external agents (Claude Code, Codex,
kagent) become clients that inherit the same governance — *a governed MCP gateway to your
whole fleet.*

## Documents

| Document | What it covers |
|---|---|
| [`docs/CHARTER.md`](docs/CHARTER.md) | Problem, thesis, target user, wedge, why-now, success criteria |
| [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) | Hub + OCM-brokered spokes, the three federations, diagrams, data model, MCP surface, where Ardur plugs in |
| [`docs/SCOPE.md`](docs/SCOPE.md) | In-scope, out-of-scope, defaults, non-goals |
| [`docs/ROADMAP.md`](docs/ROADMAP.md) | Milestone-0 (the OCM falsification test) → Phase 1 → 2 → 3 |
| [`docs/THREAT-MODEL.md`](docs/THREAT-MODEL.md) | Hub as crown-jewel, signed intents, per-spoke allowlist, blast-radius controls, abstention |
| [`COMPETITIVE.md`](COMPETITIVE.md) | Web-verified landscape and where the wedge is defensible |
| [`docs/adr/`](docs/adr/) | Architecture Decision Records 0001–0006 |

## Headline decision

> **Adopt OCM as the substrate; do not build a bespoke tunnel/agent.** The first thing
> we build is not code — it is a **falsification test** (see [Milestone-0](docs/ROADMAP.md)):
> stand up an OCM hub + two `kind`/`k3d` spokes, enable `cluster-proxy` +
> `managed-serviceaccount`, and reach a spoke's in-cluster Grafana/Argo CD from the hub.
> **If that works, the entire "build the agent/tunnel" scope is deleted** and what remains
> is the only thing worth building: the governance + federation + AI/MCP layer.

## Notes on this plan

- **Vendor-neutral throughout.** No customer names, no employer specifics, no secrets.
- **Every external claim is web-verified and cited** in the relevant document (OCM addon
  versions/status, competitor facts). Verified as of **July 2026**.
- License: [Apache-2.0](LICENSE).
