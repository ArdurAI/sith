# Sith — Roadmap

**Status:** planning · **Date:** 2026-07-10 · **Revision:** consolidated with the July-2026 market
research (E14 Investigation Brain, integration waves, standards-alignment gates, Phase-L build sequence)

The roadmap is **falsification-first**: each phase must cheaply *disprove* its key
assumption before the next is funded. The first thing we build is not product code — it is
an experiment designed to delete scope.

Sequencing discipline (never violated): **local before hub · read before write · PR before
mutation · exec never · prod never auto.**

**Two tracks.** The *local track* (day-0 adoption wedge) ships a single binary that federates
the user's own kubeconfig contexts — it needs no OCM and does not gate on Milestone-0. The
*hub track* (day-N governance) is what Milestone-0 gates. They share **one** fleet-model engine
and **one** enforcement pipeline; the local track is Phase L below, the hub track is
M0 → P1 → P2 → P3. Lead with adoption (Phase L); layer governance on top of the same engine.

---

## Milestone-0 — the OCM falsification test  ✅ **PASS**

**Verdict (revalidated 2026-07-11).** One hub plus two spokes passed registration, pinned
addon health, spoke-local reach through projected scoped tokens, real-token negative RBAC
controls, active hub-ingress denial, and outbound-only conntrack assertions in 151 seconds. The
executable evidence and dependency caveats are in
[`experiments/M0-ocm-falsification.md`](experiments/M0-ocm-falsification.md). The bespoke
transport/agent scope is deleted; the hub track proceeds to Phase 1.

> **Lifecycle-safe add-on convergence (2026-07-16).** #198 replaces the creation poll plus
> one-shot availability wait with one absolute deadline and current-object checks. Transient
> NotFound and delete/recreate transitions remain retryable, but the runner accepts availability
> only after the same current UID reports `Available=True` twice. Authorization, API, duplicate or
> malformed condition, and malformed identity failures remain terminal and do not print response
> bodies.

> **Pinned Helm alignment (2026-07-16).** #197 aligns CI, the hub chart contract, and the M0
> falsification runner on the official Helm `v4.2.3` patch release and its verified Linux amd64
> archive checksum. Cross-file policy coverage rejects divergent pins, while both runtime gates
> reject prefix lookalikes and accept only the exact release or Helm's `+g<hex-commit>` build
> metadata.

> **Phase-1 ClusterGateway authorization gate (2026-07-13).** M0 proves reverse-tunnel
> connectivity and scoped-token RBAC; it does **not** authorize a Sith transport to use a
> ClusterGateway proxy that forwards a hub caller's inbound `Authorization` header. The tracked
> upstream remediation ([oam-dev/cluster-gateway#171](https://github.com/oam-dev/cluster-gateway/pull/171))
> removes that header before client-go applies the selected managed-service-account credential and
> adds a header-precedence regression. It is verified green but remains open, and no official
> ClusterGateway release contains it. Keep [#103](https://github.com/ArdurAI/sith/issues/103)
> blocked by [#104](https://github.com/ArdurAI/sith/issues/104) until an official upstream release
> includes the fix; then rerun the Sith two-spoke negative route and require `403` for the scoped
> Secrets denial without logging credentials or response bodies.

> **Phase-1 direct ClusterProxy alternative (2026-07-13).** [#123](https://github.com/ArdurAI/sith/issues/123)
> delivers the same bounded read contract without consuming ClusterGateway: it uses the released
> ClusterProxy Konnectivity client directly, the exact rotating `sith-reader` MSA projection, and
> a fixed registered managed-cluster target. The adapter does not forward caller authorization,
> disables neither proxy nor Kubernetes TLS verification, and returns only normalized
> Pods/Deployments/Rollouts inventory plus health and, where already present, bounded
> runtime-proven `VulnerabilityReport` CVE facts. `make e2e-ocm` proves the direct route across
> both M0 spokes, its `403` Secrets negative control, an MSA projection replacement, and the
> authenticated TLS runtime refresh/read composition and exact runtime-proven image/CVE queries.
> This does not unblock [#103](https://github.com/ArdurAI/sith/issues/103); that ClusterGateway-specific
> transport remains blocked by [#104](https://github.com/ArdurAI/sith/issues/104) pending an
> official upstream release.

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

## Phase L — Local mode (the adoption wedge, day 0)  ⟵ *ships first / in parallel; needs no OCM*

**Assumption under test:** engineers will install and keep a single-binary local tool that
renders their whole kubeconfig fleet in one view — the "k9s for your whole fleet" wedge — and
that this is the on-ramp to the governed hub. (What is being falsified here is *adoption*, not
transport.)

**Goal.** `brew install sith && sith` → every kubeconfig context detected → one aggregated,
searchable fleet view with cross-cluster correlation, in under 10 minutes, offline, with
nothing leaving the machine.

**In scope.**
- One Go binary: `sith` (CLI + k9s-style TUI) and `sith ui` (local web "fleet IDE" on
  `localhost`). No account, no telemetry, no server, no agents.
- The **source-abstract fleet model**, populated in local mode from the user's kubeconfig
  contexts via client-side fan-out (informer/watch cache), rendered **cache-first**.
- Cross-cluster correlation query and fleet search across all contexts.
- Per-pod table stakes in core: logs, exec, port-forward, YAML view/edit — commodity K8s calls
  present because their absence drove the Lens exodus, not the differentiator.
- **Governed MCP read server** (`sith serve --mcp`): the same fleet as annotated read tools, so
  an AI agent inherits the read surface. The shadow-MCP lesson makes this a hard requirement —
  the sanctioned path must be easier than `npx kubernetes-mcp-server`.
- A **local advisory Investigation Brain** subset (E14) — deterministic, offline hypotheses for
  the day-1 failure modes over the locally-reachable lenses; advisory only (a suggested
  command / PR diff the user runs). *"k9s for your whole fleet that also tells you why payments
  is down."*

**Build sequence (locked slices — `docs/BUILD-SEQUENCE.md`).** Phase L is delivered as ordered,
always-green slices, each leaving the binary more useful than the last:

| Slice | What | Issue(s) |
|---|---|---|
| 0 | Foundation walking-skeleton (`fleet.Source` seam + CI) | #47 |
| 1 | Source-abstract model + local-kubeconfig fan-out | #38, #32 |
| 2 | Cache-first render (CLI + TUI) + cross-cluster search | #33 |
| 3 | Per-pod table stakes (logs/exec/port-forward/YAML) | #35 |
| 4 | Local web fleet IDE (`sith ui`) | #34 |
| 5 | No-account / no-telemetry / keychain custody | #36 |
| 6 | MCP read tools (`sith serve --mcp`) | #37 |
| — | Local advisory Investigation Brain (R1–R6, reachable lenses) | #48 |
| P | Packaging & supply chain (parallel; does not gate 1–6) | #27 |

> **Local fan-out hardening evidence (2026-07-16).** #181 contains client-go operations that
> outlive cancellation; #185 paginates Kubernetes resource lists within a deterministic
> fleet-wide materialization budget and reports incomplete scopes explicitly. #190 applies the
> same opaque-continuation discipline to generic server Tables, caps each response page at 4 MiB
> and each request at 16 MiB, rejects ignored limits and continuation cycles, and retains display
> fields only for selected facts. Unit adversarial coverage and a real second-page kind fixture
> prove that the presentation path remains bounded without dropping late-page server columns.
> #192 pages every list-watch bootstrap at 250 objects under one absolute request deadline and
> accepts a complete snapshot only within 10,000 objects and 128 pages per scope and kind. Empty
> or changed resource versions, ignored limits, continuation failures/cycles, cancellation, and
> budget exhaustion emit `WatchError` without opening a stream; a real late-page ConfigMap proves
> the watch starts from the completed consistent snapshot.
> #187 workspace-qualifies fleet-cache record identity, coverage, sync/pause/error state, and
> change notifications; missing or mixed-workspace replace/watch mutations fail closed, with
> race, fuzz, and real two-cluster kind coverage proving identical resource identities remain
> independent across workspaces.
> #196 anchors kubeconfig directory traversal and file reads to `os.Root`, rejects root or file
> identity replacement before parsing, refuses deferred local credential/plugin paths, and keeps
> all race diagnostics relative and content-free.
> #193 reads persisted cluster state and facts inside one workspace-scoped PostgreSQL
> `REPEATABLE READ, READ ONLY` transaction, so coverage and staleness always describe the same
> fact snapshot while transaction-local RLS remains the backstop.

**Exit criteria.**
- First run to a populated cross-cluster answer in **< 10 minutes**, offline.
- A correlation query returns a correct answer over **≥ 2 kubeconfig contexts**.
- No account, no telemetry, and no credential leaves the machine (verified by an egress test).
- An MCP client (e.g. Claude Code) calls the read tools and gets the same fleet answers.
- The advisory brain surfaces a cited hypothesis + suggested command for a degraded workload,
  and **abstains** (naming the missing lens) when a required lens is unreachable.

**Demo.** `brew install sith && sith` on a laptop with 3 kubeconfig contexts → one fleet view;
"every context where `payments` is Degraded" answered in one query; then Claude Code queries
the same fleet via the MCP read tools; then the brain explains *why* one is degraded.

> Phase L needs no OCM and does not wait on Milestone-0. It is the adoption wedge; the hub
> track (M0 → P1 → P2 → P3) adds federation and governance on top of the same engine.

---

## Phase 1 — read-only federation (the first vertical)

**Goal.** From one governed place, assemble a **normalized fleet model** across the 2
spokes and answer a **cross-cluster** question that single-cluster tools cannot. This is the
**same fleet-model engine as Phase L**, now sourced from OCM-brokered spokes instead of local
kubeconfigs — the read source is abstracted so hub mode and local mode share one code path.

**In scope.**
- The read source is **abstracted** (local kubeconfig *or* OCM spoke); Phase L's local path
  and this hub path are one implementation of the same fleet model.
- Hub read-federation service: pull inventory + health from both spokes via cluster-proxy
  + MSA tokens; normalize into the fleet model; stamp **freshness + source cluster**.
- `Workspace` tenancy + signed-token authn + RBAC spine (reader/operator roles), with the
  **DB-level RLS backstop present from day one** ([ADR-0003](adr/0003-tenancy-isolation.md)).
- A cross-cluster correlation query (e.g. "every cluster where deployment X is unhealthy").
- The **policy-hook seam** at the (future) intent boundary, returning "allow" for reads.

> **Hub refresh hardening evidence (2026-07-16).** #195 independently authorizes every caller,
> coalesces only concurrent refreshes for the same validated workspace, and runs shared work on a
> detached internal trace so leader/waiter cancellation and request context cannot cross caller
> boundaries. Completed, failed, and panicking flights are removed; different workspaces remain
> independent. #193 separately reads persisted coverage and facts from one repeatable-read
> workspace snapshot. #194 admits spoke transports through a validated 1-64 worker pool with a
> conservative default of four, serializes persistence and coverage mutation, and cancels all
> admitted peers before returning a parent-cancellation or store error. These boundaries are kept
> separate from refresh coordination so caller isolation, transport fan-out, and database snapshot
> consistency can each fail closed independently.

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
- The `gitops.open-pr` verb is also exposed as an **MCP write tool** (elicitation-gated), so an
  agent can propose it under the same PEP — the first governed *write* surface for agents.

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
- **MCP server, full write surface**: the live-mutation verbs exposed as MCP write tools gated
  by **Elicitation** (2025-06-18), onto the same PEP. (MCP *read* tools shipped in Phase L; the
  `gitops.open-pr` write tool shipped in P2; here the fan-out write verbs reach external agents.)

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

## The Investigation Brain (E14) — deterministic root-cause across the phases

The July-2026 market pass found the entire **AI-SRE / auto-triage wave** (k8sgpt, HolmesGPT,
Robusta, Botkube, Komodor, Cleric) converging on one shape: **LLM-agentic, investigate/advise,
read-only or action-gated**. None ships deterministic rule-based root-cause; none ships governed
*typed* action. Sith's **E14 — Investigation Brain** occupies both openings: a **rule-based,
transparent, abstaining** reasoner over E2's four-lens graph that *proposes, never executes*.

- **Phase L** — a **local advisory subset** (hypotheses + a suggested command/PR the user runs)
  over the locally-reachable lenses. Determinism + offline + explainability are the features the
  LLM tools structurally cannot offer air-gapped / China / security-conscious estates.
- **P1** — the full deterministic brain over the **four-lens operational graph** (E2 F2.6/F2.7),
  correlated by OpenTelemetry semconv keys; the six canonical rules (R1 bad deploy · R2 OOMKilled
  · R3 CrashLoopBackOff · R4 config drift · R5 cert expiry · R6 node pressure) reach a *confident*
  verdict once the Wave-1 connector core is present, and **abstain** honestly otherwise.
- **P2 / P3** — the **same** rules render a **governed typed-intent proposal** through the PEP:
  advisory in local mode, governed in the hub. One brain, two modes. The AI-SRE tools become
  *clients* of this governance, not competitors — their advice becomes a typed `plan` Sith gates.

## Integration waves (E12) — the connector coverage the brain needs

Connectors ship in four waves (`docs/specs/E2-readfed-brain-integrations.md` §4), each scored by
verb subset, lenses fed, kind (read-adapter / brokered read-through / typed-action), effort tier,
and mode. **Wave 1 is the daily core and is deliberately the exact coverage the six brain rules
need:**

- **W1 — daily core:** Kubernetes (the substrate) · GitHub · ArgoCD · Prometheus · Elasticsearch ·
  AWS. With just this, R1/R2/R4/R5/R6 reach *confident* and R3 reaches *detect*.
  Issue #206 establishes the first ArgoCD contract as a bounded, sanitized `Application`-to-graph
  projector before any network adapter or out-of-process framework is generalized around it.
  Issue #209 establishes the matching Prometheus `/api/v1/alerts` contract: already-fetched active
  alerts become bounded TELEMETRY facts, annotations and unknown labels are discarded, and only one
  unambiguous allowlisted Kubernetes identity can attach a fact to the graph. The endpoint remains
  query-through; this slice adds no network client, series retention, credential loading, or writes.
  Issue #212 establishes the GitHub merge-event contract: one already-fetched, API-versioned pull
  request response becomes a bounded TIMELINE fact only when its merge evidence is internally
  consistent. Caller-provided repository identity remains authoritative, sensitive response fields
  are discarded, and the event stays unattached until an explicit repository-to-workload relation
  exists. This slice adds no HTTP client, token loading, persistence, or GitHub write capability.
  Issue #214 establishes the Elasticsearch log-evidence contract: one already-fetched, complete
  Search API response using the current ECS Kubernetes field profile becomes at most three bounded
  TELEMETRY cause facts for R3. Cluster, namespace, and Pod identity must match the trusted caller.
  A supplied container requires every hit to carry that exact container; an omitted container is a
  deliberate Pod-wide query, accepts hits with any or no container field, and emits no container
  identity. The trusted query window is the inclusive `[start, end]` interval, its duration cannot
  exceed fifteen minutes, and its end cannot be more than five minutes ahead of collection time;
  the duration cap is not a freshness claim. A future live reader must issue these same bounds.
  Raw messages are classified in memory and discarded.
  Missing cluster identity, partial or failed shards, `_source`, unknown fields, and ambiguous values
  fail closed. This slice adds no HTTP client, index discovery, credentials, persistence, or writes.
- **W2 — desired-state/diff:** Helm · Kustomize · kubectl-diff (readers, **not** action targets in v1).
- **W3 — viz/tracing/clouds:** Grafana (deep-link only) · OTel (semconv key backbone) · OpenShift ·
  Azure · GCP.
- **W4 — long-tail:** OpenSearch · Splunk · Fluentd/FluentBit (**health-only**) · Istio/Linkerd
  (mesh → dependency edges) · Docker.

Scope discipline holds throughout: read log **sinks** not shippers; Grafana is brokered, never
re-skinned; Helm/Kustomize expose no action verbs; telemetry is query-through, never retained.

## Standards-alignment gates (cross-cutting)

Not an epic — acceptance gates woven into the epics above (`standards-alignment` label):

- **MCP 2026-07-28 RC** — OAuth 2.1 + **RFC 8707 audience-bound tokens**, and **enforce-at-execution
  not just discovery** (the CVE-2026-46519 bug class, CVSS 8.8). → E7, E4. Build to the stable
  primitives; the RC surface will churn.
- **OpenTelemetry Kubernetes semconv** — the correlation join keys for the four-lens graph. → E2.
- **client-go ExecCredential v1** — kubeconfig exec-plugin auth; cloud tokens never persisted. → E1, E11.
- **SLSA L2 + Sigstore/cosign + SBOM** — from the **first tag**, a day-one release gate. → E9.
- **Kubernetes API conventions** — the fleet model reads as idiomatic Kubernetes. → E2.

## What is deliberately *not* on this roadmap

- Broad integration count, UI polish, or "autonomy level" as goals in themselves.
- Any verb beyond the closed vocabulary; any `exec`/free-form apply; any secret/RBAC write.
- Re-implementing OCM transport, a scheduler, a portal, or a telemetry store (see
  [`SCOPE.md`](SCOPE.md)).
- **No LLM in the critical path for root-cause** — the Investigation Brain (E14) is deterministic;
  an LLM is an optional *client*, never the reasoning engine.
- **No "act from chat" free-`kubectl` surface** — the Botkube anti-pattern the threat model rejects.

Each phase's design decisions are recorded as ADRs; each phase's falsification result is
appended to the relevant ADR as evidence.
