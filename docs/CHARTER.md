# Sith — Charter

**Status:** planning · **Date:** 2026-07-08 · **License:** Apache-2.0

This charter states *why Sith exists*, *who it is for*, *what specifically it owns*, and
*how we will know it worked*. It is deliberately narrow. The single greatest risk to a
project in this space is scope drift into territory that larger, better-funded, or
CNCF-blessed efforts already own; the charter's job is to hold the line.

---

## 1. The problem

An organization runs **many** Kubernetes clusters — across teams, regions, cloud
accounts, VPCs, and network boundaries. Each cluster already has excellent *local*
operational tooling: Argo CD, Argo Rollouts, Prometheus/Grafana, its own RBAC. What is
missing is a **governed place to operate across the fleet**:

- **Seeing across clusters is hard.** Answering "which of my clusters have `payments`
  Degraded right now?" or "which clusters run image X with CVE Y?" means logging into N
  consoles or building a bespoke aggregator. Single-cluster tools structurally cannot
  answer fleet-wide questions.
- **Acting across clusters is dangerous.** The moment you build a central place that can
  *act* on many clusters, you have built the highest-value attack surface and the largest
  blast radius in the estate. Most teams either (a) don't build it and operate by hand, or
  (b) build it without the governance to make it safe.
- **Deep cluster access does not belong in the center.** Centralizing cluster-admin
  kubeconfigs so a hub can act is exactly the anti-pattern that turns one compromise into
  a fleet-wide breach. Yet "reach into the cluster from the center" is the naive design
  everyone reaches for.
- **AI raises the stakes.** Teams now want agents (their own, and third-party agents like
  Claude Code / Codex / kagent) to operate infrastructure. An agent on top of an
  ungoverned control plane is a loaded gun pointed at the fleet.

The gap is therefore **not** "another multi-cluster tool." It is a **governed federation
of *see* and *act* that keeps deep access local and makes every cross-cluster action
policy-gated, scoped, signed, and audited** — including when the actor is an AI agent.

## 2. The thesis

Three claims, each independently defensible, together define Sith:

1. **The transport is commodity — adopt it, do not build it.** An outbound-only
   per-cluster agent plus a central hub that reaches cluster-local services is *already*
   shipped, hardened, and maintained by CNCF and vendors. Building it bespoke re-invents
   security-sensitive infrastructure worse than the incumbents.
   → **Decision: build on Open Cluster Management (OCM).** See
   [ADR-0001](adr/0001-adopt-ocm-vs-bespoke-tunnel.md).
2. **The differentiator is governance of *action federation*, not visibility.** Read
   federation is necessary and is the first vertical, but it is increasingly commoditized.
   The durable, hard, valuable thing is **safely fanning a *typed intent* out to N
   clusters under policy** — with environment gates, wave ordering, partial-failure
   semantics, and honest abstention. This is a *policy* problem, and policy over
   distributed action is genuinely under-served as an adoptable primitive.
3. **AI is a client of the governance, not a bypass of it.** Sith exposes the fleet as a
   **governed MCP server**. Its own agent and any external agent go through the *same*
   policy decision point, the *same* closed verb vocabulary, the *same* scoped identity
   broker, the *same* audit + decision ledger. The AI never holds a cluster credential and
   never gets a shell.

## 3. Target user

**Primary:** the **platform / SRE / fleet-operations engineer** at an organization
running **tens to hundreds** of Kubernetes clusters who is accountable for *safe*
cross-cluster operations and for *who did what, where, and why*.

**Secondary:**
- **Security / compliance owners** who need a defensible answer to "prove what your
  operators — human and agent — are allowed to do and did do across the fleet."
- **AI-forward platform teams** who want to let agents operate infrastructure but only
  behind hard governance.

**Explicitly not the target:** application developers wanting a self-service catalog
(that is an IDP), or a single-cluster operator (local tools already serve them well).

## 4. The wedge (what Sith owns)

Sith owns the **governed access + action federation layer**, expressed as three
federations built on OCM-brokered connectivity:

- **Read federation.** A tenant-scoped, normalized **fleet model** (inventory, health,
  alerts, drift) assembled from OCM-brokered reads, with **cross-cluster correlation** as
  a first-class primitive.
- **Action federation.** Writes are **typed intents** from a **closed verb vocabulary**
  (`argocd.sync|rollback`, `rollout.promote|abort`, `deployment.scale|restart`,
  `gitops.open-pr`). Intents are **signed**; each spoke validates against a **local
  allowlist** and executes with its **own scoped identity**. No shell, no free-form
  `apply`, no secret/RBAC mutation. First write shipped = `gitops.open-pr`.
- **Policy federation.** Fan-out reasoning: environment gates, wave/canary ordering with a
  per-wave gate, partial-failure/auto-rollback, idempotency, and **federation-specific
  abstention** when the fleet view is incomplete or stale.

**Ardur is the PDP + identity broker + decision-ledger.** Sith's enforcement points ask
Ardur *"may this actor issue this intent across these clusters now?"* → allow / deny /
require-approval(s). See [ADR-0005](adr/0005-ai-mcp-ardur-pdp.md).

## 5. Why now

Verified against the July-2026 landscape (see [`COMPETITIVE.md`](../COMPETITIVE.md) for
citations):

- **The substrate matured.** OCM's `cluster-proxy` and `managed-serviceaccount` addons
  reached **v0.10.0 (Feb 2026)** and deliver the exact outbound-only, cross-VPC,
  reach-cluster-local-services design as adoptable addons — so the year we would have
  spent building transport is now spent on governance instead.
- **AI operations went mainstream — and so did the danger.** Komodor shipped an
  extensible multi-agent architecture (Mar 2026) with MCP/OpenAPI bring-your-own-tools;
  SUSE Rancher Prime shipped an agentic "crew" + external MCP at KubeCon EU 2026. The
  market has validated *agents operating fleets*; what remains under-served is
  **vendor-neutral, OSS, governed action federation** as a primitive rather than a feature
  of one vendor's platform.
- **MCP gained the safety primitives to build on.** The 2025-03-26 spec added tool
  annotations (`readOnlyHint`/`destructiveHint`/…, explicitly *hints, enforce
  server-side*), and the 2025-06-18 spec added **Elicitation** (a server can request
  structured user input/approval mid-flow) — the native shape for human-in-the-loop
  approval gates.

## 6. Success criteria

Success is defined by **falsification-first** discipline: each milestone must be able to
*disprove* an assumption cheaply before the next is funded.

- **M0 (falsification).** We can reach a spoke's in-cluster service (Grafana / Argo CD)
  from an OCM hub via `cluster-proxy` + `managed-serviceaccount` in **≤ 1 day of setup**.
  *If yes → the "build the transport" scope is deleted.* *If no → the whole premise is
  wrong and we stop before writing product code.*
- **P1 (read federation).** From one place, correctly answer a cross-cluster question
  ("every cluster where deployment X is unhealthy") across **≥ 2 spokes**, tenant-scoped,
  with staleness surfaced per cluster.
- **P2 (first governed write).** A `gitops.open-pr` intent flows end-to-end through the
  Ardur PDP and opens a real PR on a target repo — **with a complete decision-ledger +
  audit-log entry** and *zero* cluster credentials ever reaching the center or the AI.
- **P3 (policy federation + MCP).** A wave-ordered, approval-gated intent fans out across
  spokes with a gate per wave and correct **abstention** when a spoke is stale; the same
  governance applies identically to an external MCP client.

**Non-criteria (we will resist measuring these):** number of integrations, breadth of
read surface, UI polish, agent autonomy level. Depth of *governance correctness* beats
breadth of *features* for this product.

## 7. Guardrails (anti-drift contract)

1. If a capability is available as a maintained OCM addon or upstream project, **adopt it**
   rather than building it.
2. If a proposed feature belongs to "developer portal", "GitOps controller", "scheduler",
   or "telemetry lake", it is **out of scope** — full stop (see [`docs/SCOPE.md`](SCOPE.md)).
3. The write path may only ever grow **typed verbs in a reviewed closed vocabulary**.
   Adding a verb is an ADR-level decision. `exec` and free-form `apply` are permanently
   excluded.
4. Multi-tenant isolation, signed intents, per-spoke local enforcement, and scoped
   identity are **day-one requirements**, not later hardening.
