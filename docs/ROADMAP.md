# Sith — Roadmap

**Status:** planning · **Date:** 2026-07-08

The roadmap is **falsification-first**: each phase must cheaply *disprove* its key
assumption before the next is funded. The first thing we build is not product code — it is
an experiment designed to delete scope.

Sequencing discipline (never violated): **read before write · PR before mutation · exec
never · prod never auto.**

---

## Milestone-0 — the OCM falsification test  ⟵ *do this first, before any product code*

**Assumption under test:** OCM `cluster-proxy` + `managed-serviceaccount` really do
deliver outbound-only, cross-network, reach-cluster-local-services connectivity — so we do
**not** need to build a bespoke tunnel/agent.

**Goal.** Stand up an OCM hub and **2 local spokes** (`kind` or `k3d`), enable the two
addons, and reach a spoke's **in-cluster Grafana / Argo CD** from the hub through the
reverse tunnel, using a **scoped `managed-serviceaccount` token** (not a cluster-admin
kubeconfig).

**Steps (lab, not product):**
1. Create hub + spoke-a + spoke-b as local clusters. Keep all scratch on
   `/Volumes/EXTENDED` (system disk is small).
2. Bootstrap OCM (`clusteradm init` on hub; join spokes). Verify `ManagedCluster` objects.
3. Enable `cluster-proxy` (v0.10.0) and `managed-serviceaccount` (v0.10.0) addons.
4. Deploy a trivial in-cluster service (or Grafana / Argo CD) on each spoke.
5. From the hub, reach that spoke-local service **through cluster-proxy**, authenticating
   with an **MSA-projected scoped token**.
6. Confirm the spoke only ever makes **outbound** connections (no inbound hub→spoke port).

**Exit criteria (the deciding experiment):**
- ✅ **If reachable in ≤ ~1 day of setup** → the "build the agent/tunnel" scope is
  **deleted** from Sith. We adopt OCM and spend the saved time on governance. Proceed to
  Phase 1.
- ❌ **If it does not work / needs bespoke transport** → the core premise ([ADR-0001](adr/0001-adopt-ocm-vs-bespoke-tunnel.md))
  is wrong. **Stop.** Re-evaluate before writing any product code. (Cheapest possible
  place to fail.)

**Demo.** A terminal recording: hub curls a spoke-local Grafana/Argo CD endpoint via
cluster-proxy using an MSA token, with `tcpdump`/netstat showing spoke connections are
outbound-only. Write up the result in `docs/adr/0001` as the falsification evidence.

> Milestone-0 is a **lab experiment**, not a feature. Its only artifact is a documented
> yes/no and a short runbook. No Sith product code is written until it passes.

---

## Phase 1 — read-only federation (the first vertical)

**Goal.** From one governed place, assemble a **normalized fleet model** across the 2
spokes and answer a **cross-cluster** question that single-cluster tools cannot.

**In scope.**
- Hub read-federation service: pull inventory + health from both spokes via cluster-proxy
  + MSA tokens; normalize into the fleet model; stamp **freshness + source cluster**.
- `Workspace` tenancy + signed-token authn + RBAC spine (reader/operator roles), with the
  **DB-level RLS backstop present from day one** ([ADR-0003](adr/0003-tenancy-isolation.md)).
- A cross-cluster correlation query (e.g. "every cluster where deployment X is unhealthy").
- The **policy-hook seam** at the (future) intent boundary, returning "allow" for reads.

**Exit criteria.**
- A single query returns a correct, tenant-scoped, cross-cluster answer over **≥ 2 spokes**.
- Per-cluster **staleness is visible** in the result.
- A second workspace **cannot** see the first workspace's clusters — verified at the DB
  layer, not just the app layer (attempt an app-layer bypass; RLS blocks it).

**Demo.** "Show me every cluster where `payments` is Degraded" → one answer spanning both
spokes, with a stale cluster flagged, and a tenant-isolation test showing cross-workspace
access denied by the DB backstop.

---

## Phase 2 — first governed typed-intent write (`gitops.open-pr` end-to-end)

**Goal.** Prove the **action federation + Ardur PDP** path with the **safest possible
write**: `gitops.open-pr` — a proposal a human merges. No cluster mutation yet.

**In scope.**
- The intent model + closed-vocabulary allowlist (fail-safe) + per-verb arg schema
  ([ADR-0004](adr/0004-typed-intent-action-model.md)).
- The PEP enforcement pipeline (authn → membership → role → verb → args → tenant scope →
  **Ardur PDP** → elicited approval → scoped identity → caps → **signed dispatch** →
  audit + decision-ledger).
- **Ardur as PDP** returning real decisions ([ADR-0005](adr/0005-ai-mcp-ardur-pdp.md)); the
  decision-ledger + audit-log both populated.
- `gitops.open-pr` executes by opening a real PR on a target repo. Git credential held via
  **KMS envelope, per-tenant** ([ADR-0006](adr/0006-credential-key-custody.md)).
- Spoke-side (or repo-side) independent re-validation of the signed intent.

**Exit criteria.**
- A `gitops.open-pr` intent flows end-to-end and opens a real PR.
- **Zero** cluster credentials reach the center or any AI/agent at any point.
- Every step is in the audit-log; the allow decision is in Ardur's decision-ledger, bound
  to a hash of the resolved args.
- A denied intent (wrong role / prod without approval / unknown verb) is refused and logged.

**Demo.** An operator (then an MCP client) proposes "open a PR to bump replicas for `web`
in workspace X"; Ardur allows with justification; a PR appears; the full
proposed→approved→executed ledger is shown; the same request from a `reader` is refused.

---

## Phase 3 — policy federation (waves / approvals / abstention) + MCP server

**Goal.** Fan a single intent out to **N clusters** safely, and expose the whole surface as
a **governed MCP server** so external agents inherit the same governance.

**In scope.**
- **Wave/canary ordering** with a **gate per wave** and a health check between waves.
- **Environment gates + multi-approver** for `prod`; max-clusters-per-intent ceiling.
- **Partial-failure semantics**: stop-on-failure, auto-rollback of the failed wave,
  **idempotency/dedupe** on retry.
- **Federation-specific abstention**: refuse fleet-wide action when the targeted set is
  incomplete/stale, with an honest message.
- First live-mutation verbs behind all of the above (`argocd.sync`, `rollout.promote`,
  `deployment.scale`) — still **never** `exec` or free-form `apply`.
- **MCP server**: annotated read tools + write tools gated by **Elicitation**
  (2025-06-18), all onto the same PEP.

**Exit criteria.**
- A wave-ordered intent across ≥ 2 spokes runs dev→canary→rest with a gate per wave; a
  forced mid-rollout failure triggers auto-rollback of that wave and stops.
- With one spoke made stale, a fleet-wide intent **abstains** with the correct message.
- An external MCP client (e.g. Claude Code) issues a read and a write and is subject to
  **identical** governance (approval elicited, decision-ledgered, audited).

**Demo.** "Sync `payments` across all staging + prod, canary first." Sith plans the waves;
prod requires a second approver; canary passes, one prod cluster fails → that wave rolls
back and the rest halts; then a stale-cluster run shows abstention; then the same run is
driven from an MCP client with the same gates.

---

## What is deliberately *not* on this roadmap

- Broad integration count, UI polish, or "autonomy level" as goals in themselves.
- Any verb beyond the closed vocabulary; any `exec`/free-form apply; any secret/RBAC write.
- Re-implementing OCM transport, a scheduler, a portal, or a telemetry store (see
  [`SCOPE.md`](SCOPE.md)).

Each phase's design decisions are recorded as ADRs; each phase's falsification result is
appended to the relevant ADR as evidence.
