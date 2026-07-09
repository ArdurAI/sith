# Sith — what it should be, and the shape it should take

**Status:** research synthesis · **Date:** 2026-07-09

This is the synthesis document. It answers one question the owner posed: *what should Sith
be so that DevOps / Platform / SRE / MLOps engineers reach for it by default, and what form
should it take?* It pulls together five workstreams of deep research (all cited in the
companion files) and turns them into a decision: an executive answer, a form-factor call, a
ruthlessly prioritized roadmap that places **every** capability the owner named, a
carry/discard/net-new reckoning against the predecessor (`devops-portal`), and concrete edits
to Sith's charter, architecture, roadmap, and epics.

**Evidence base (companion files, every load-bearing claim carries a primary URL):**
- [market-and-form-factor.md](market-and-form-factor.md) — practitioner pains (global, China,
  India), the Lens/form-factor story, the OSS-and-paid tool landscape, cost, and multi-cloud.
- [identity-connections-security.md](identity-connections-security.md) — the four connection
  modes, brokered-access prior art, short-lived-credential consensus, custody, and the
  supply-chain/audit bar.
- [integrations-and-ai-governance.md](integrations-and-ai-governance.md) — connector-framework
  design, per-tool integration surfaces, MCP protocol state, and who governs agents on
  clusters today.

The predecessor reviews referenced below live at
`/Volumes/EXTENDED/checkpoints/devops-portal/review-01..10.md` (read-only, vendor-neutral).

---

## 1. Executive answer

Sith should be the tool an engineer reaches for the moment they operate **more than one**
Kubernetes cluster — first as a fast local client that shows their whole fleet from the
kubeconfigs already on their laptop, and then, for a team, as a self-hosted control plane that
lets many operators and their AI agents see and act across that fleet without anyone holding
standing admin credentials. It is **one product with two faces**: a single-user local "fleet
IDE" that installs in one command and needs no account, no server, and no telemetry; and a
self-hosted hub that federates the same fleet over outbound-only in-cluster agents, gates every
write through policy, and records who did what and why. The thing Sith *owns* — the position no
one else holds — is **governance of action across a fleet**: typed, signed, approval-gated
cluster operations applied identically to a human and to an AI agent. The thing that gets Sith
*adopted* is the local mode, because the empty slot in today's market is precisely a
no-account, no-telemetry, aggregated multi-cluster client, and every engineer who installs that
is a candidate to turn the hub on later.

The de-facto wedge is therefore a two-step land-and-expand, not a single feature. **Land** with
the local fleet client: `brew install sith && sith ui` detects every kubeconfig context and
renders one aggregated, searchable view across all of them in under ten minutes — with pod
logs, exec, and port-forward in the core (their removal from the open-source build is what drove
the Lens exodus, [lensapp/lens#6823](https://github.com/lensapp/lens/issues/6823)) and with
cross-cluster search that single-cluster tools structurally cannot do. That slot is open: k9s
shows one context at a time (34k stars, but `:ctx` to switch), Headlamp centers on the
per-cluster view, Lens sits behind an account wall, and the only tool that already aggregates
many clusters into one view — Aptakube — is closed and paid ([aptakube.com](https://aptakube.com/)).
**Expand** to the hub when a team needs shared visibility and real approvals: the same binary
becomes a control plane where clusters graduate from "my kubeconfig" to outbound
[OCM](https://open-cluster-management.io/) minions, writes become typed intents adjudicated by an
external policy decision point (Ardur), and the fleet is exposed as a governed **MCP server** so
Claude Code, Codex, and kagent inherit the same governance for free. Governed action federation
is the durable moat — the research found it empty across every incumbent — and the local client
is the funnel that fills it.

---

## 2. The two wedges (the reframe the plan needs)

Sith's current charter names one wedge — governed action federation — and explicitly puts a
local console *out of scope* ("A single-cluster console / IDE … owned by Headlamp, k9s, Lens",
[SCOPE.md](../SCOPE.md)). The research says that is half right and half a missed on-ramp. There
are two distinct wedges, and conflating them is what killed the predecessor:

- **The adoption wedge — the local aggregated fleet client.** How you get ten thousand
  individual engineers to install the thing and like it. This is won by form and trust, not by
  governance features. The evidence is unambiguous: the top barriers to adopting a new OSS tool
  in 2024 were *fear of abandonment (46%)*, *too complex to run (46%)*, and *thin docs (45%)* —
  security scanners ranked far lower ([CNCF Annual Survey 2024](https://www.cncf.io/wp-content/uploads/2025/04/cncf_annual_survey24_031225a.pdf)).
  The Lens revolt was about an account wall, a trust break, telemetry-by-default, and logs/shell
  removed from the OSS build — not a missing feature. So the adoption wedge is won by a
  single-binary, ten-minute-wow, no-account, no-telemetry, permissively-licensed client that
  answers a fleet-wide question on first run.

- **The durable wedge — governed action federation with AI as a client.** What makes Sith
  defensible and, eventually, what an organization pays to self-host and standardize on. This is
  the position the research confirms is *empty*: OCM ships rollout mechanics but no
  approvals/typed-verbs/audit; ACM and Rancher have platform-coupled policy; Kargo gates artifact
  promotion only; Komodor audits its own AI's remediation inside a closed SaaS; the AI-SRE
  incumbents (Komodor, Rancher "Liz", HolmesGPT) stop at advise/diagnose or go autonomy-first with
  no approval primitives; the MCP gateways (Kong, Solo agentgateway, MintMCP, Permit.io) enforce
  auth and tool-allowlists but have no fleet-aware, blast-radius-conscious, approval-gated action
  ([integrations-and-ai-governance.md](integrations-and-ai-governance.md) §4). The KubeCon EU
  2026 read of the room — "nobody has quite figured out how to manage and secure [agents] inside
  Kubernetes yet" — is the market saying the door is open.

The predecessor `devops-portal` had **neither** wedge. It had no adoption on-ramp (its onboarding
was helm-install-a-platform-then-configure-SSO, and it 500'd on a fresh install — review-01,
review-05), and it had no durable moat (it reverse-proxied and iframed tools that were already
better, which review-01 correctly called *negative value*). Sith wins by holding both: lead with
the adoption wedge, monetize/defend on the durable one, and — critically — build them on **one
shared engine** so the local client and the hub are the same fleet model rendered two ways.

---

## 3. Recommended form factor

**One Go binary, one web frontend, three run modes.** This is the shape the research points to,
and it is exactly what let Headlamp win the Kubernetes-Dashboard succession
([kubernetes.io, 2026-06-01](https://kubernetes.io/blog/2026/06/01/dashboard-to-headlamp/)):

| Mode | Command | What it is | Who runs it |
|---|---|---|---|
| **CLI** | `sith …` | Scriptable fleet verbs; the substrate power-users script against | Everyone |
| **Local ("fleet IDE")** | `sith ui` | Local web UI on `localhost`, reads existing kubeconfigs, single-user, zero config, **no account, no telemetry** | The individual engineer — the top of the funnel |
| **Hub (federated)** | `sith hub` | The *same* UI served multi-user, clusters joined as outbound OCM minions, workspaces + governance + audit | The platform/SRE team |

The load-bearing form-factor decisions, each backed by evidence in
[market-and-form-factor.md § Part 2](market-and-form-factor.md#part-2):

- **Single artifact, no server or account to install.** k9s (`brew install`, one binary) and
  Freelens define the funnel; "helm-install a platform, then SSO, then see value" is the adoption
  cliff the predecessor fell off. The wow is `brew install sith && sith ui` → every context
  detected → one aggregated fleet view with cmd-K search, in minutes, offline-capable, nothing
  leaving the machine.
- **Cache-first render, never spinner-first.** The single most important DX decision. Kubernetes
  hands you the perfect substrate — watch streams into a local informer cache — so every view
  renders from a local store in tens of milliseconds and reconciles deltas in the background (the
  Linear local-first mechanic; the 0.1s/1s/10s
  [response-time limits](https://www.nngroup.com/articles/response-times-3-important-limits/)).
- **Center of gravity is the fleet, not the pod.** The local mode is *not* "another single-cluster
  console" — that slot is taken and is correctly out of scope. It is the aggregated,
  cross-cluster view Headlamp/k9s/Lens don't center on: all-clusters resource views, fleet search
  and correlation ("which clusters run image X", "where is `payments` degraded"), staleness
  stamps, and the same typed-verb actions with dry-run/diff (self-approved locally, but the same
  intent model that later gains real governance). Per-pod table stakes (logs, exec, port-forward,
  YAML edit) exist because their absence drove the Lens exodus — but they are commodity K8s API
  calls, not a place to out-feature Headlamp.
- **Go binary with an embedded frontend first; Tauri desktop shell as a fast-follow.** Ship the
  web UI embedded in the single binary (k9s-grade install friction, no code-signing tax on day
  one). Wrap it later in **Tauri, not Electron** — the memory/footprint evidence and Lens-refugee
  sensitivity to Electron bloat both point that way, and Aptakube already proves Tauri in this
  exact product class. The wrapper is additive; the web UI has to exist in both modes anyway.

---

## 4. The ruthlessly prioritized roadmap

Feature sprawl is what killed `devops-portal` (12 providers, 92 API routes, ~7 of 10 pillars pure
pass-through — review-01). The discipline here is: **every capability the owner named is placed
in exactly one of four buckets, and the "not now" bucket is as important as the wedge.** A
capability earns a higher bucket only if it is part of a wedge, not merely "useful".

### 4.1 Wedge — build first; this is the reason to exist plus the on-ramp

| Capability | Why it is wedge, not later |
|---|---|
| **Local aggregated "fleet IDE" mode** (`sith ui`, kubeconfig auto-detect, cache-first render, cmd-K fleet search, logs/exec/port-forward/YAML in core, no account, no telemetry) | The adoption wedge. Occupies the one empty OSS slot (§2). Every install is a hub candidate. |
| **Read federation + normalized fleet model + cross-cluster correlation** | The shared engine behind *both* faces. Correlation is the thing single-cluster tools structurally cannot do; it is the first-run wow and the hub's core. Build the read source **abstract** from day one: local kubeconfig contexts *or* OCM-brokered spokes feed the same model. |
| **Minions (outbound OCM agents) + multi-auth** (kubeconfig for local; API keys / JWT / OIDC and short-lived cloud IAM for the hub) | The connection substrate. Minions are how a "my kubeconfig" cluster graduates to a governed one without the operator relearning anything. Multi-auth is table stakes because a cloud kubeconfig alone is not even a working credential ([identity §1](identity-connections-security.md)). |
| **Governance spine, day one:** Workspace tenancy, signed-token authn (never header trust), least-privilege RBAC, forced Postgres RLS backstop, audit-log (*what-happened*) + decision-ledger (*why-allowed*) | These are the predecessor's exact failure points (dead RLS, header-trust IDOR, single god-key). They are day-one requirements, not later hardening — a control plane that leaks across tenants has no reason to exist. |
| **First governed typed write — `gitops.open-pr` through the Ardur PDP** | The safest possible proof of the action-federation path: a proposal a human merges, zero cluster mutation, zero new standing trust. It lights up the whole PEP pipeline and the two ledgers. |
| **No-god-key custody:** no central admin kubeconfig; KMS-envelope per-tenant DEKs in the hub; OS keychain in local mode; **cosign-signed releases + SLSA L2 provenance + SBOM from the first tag** | Custody is a wedge property here because the predecessor's single `TOKEN_ENCRYPTION_KEY` (one leak decrypts every tenant) is a disqualifying foundation, and signing is now cheap — SLSA L2 via cosign + GitHub attestation is "an afternoon". Evaluators run these scorecards before a human reads the code ([identity §6](identity-connections-security.md)). |

### 4.2 Fast-follow — right after the wedge proves out

| Capability | Placement rationale |
|---|---|
| **Governed MCP server** (annotated read tools + elicitation-gated write tools on the *same* PEP) | The amplifier, not a separate bet. Because Sith enforces server-side, every external agent (Claude Code, Codex, kagent) inherits the governance for free. MCP is now vendor-neutral LF infrastructure ([donated 2025-12-09](https://blog.modelcontextprotocol.io/posts/2025-12-09-mcp-joins-agentic-ai-foundation/)); the protocol is settled, the governance on top is not. This *is* "Claude/Codex compatibility". |
| **Policy federation** — waves/canary, environment gates, multi-approver for prod, partial-failure/auto-rollback, abstention; the live-mutation verbs (`argocd.sync`, `rollout.promote\|abort`, `deployment.scale\|restart`) behind all of it | The genuinely novel, genuinely empty part of the durable wedge. Fast-follow because it rides the PEP the wedge already builds and needs the `gitops.open-pr` path proven first. |
| **Connector framework** — out-of-process gRPC, SDK-first, **three fixed kinds** (read adapter / brokered read-through / typed-action adapter), one canonical connector per tool | Generalizes the six hand-written day-1 adapters (**Argo CD, Flux, Helm, Prometheus, Loki, GitHub** — the ones that feed the model cheaply or host the first write). Fast-follow, not wedge: build adapters by hand first, generalize once the shape is proven — the opposite of Backstage's premature-ecosystem trap ([integrations §1](integrations-and-ai-governance.md)). |
| **Cost analyzer as a read-overlay** — deploy/read OpenCost per cluster, aggregate at the hub into per-workspace/per-team fleet rollups, GPU columns where DCGM exists | A read integration, *not a build*. The fleet rollup is the exact documented gap: OpenCost is per-cluster by design and its multi-cluster ask was triaged P3 and closed unresolved; Kubecost's unified multi-cluster view is Enterprise-tier ([market § Part 3 §5](market-and-form-factor.md#part-3)). Building a metering engine would re-fight OpenCost/Kubecost/CAST AI on their ground. |
| **Multi-cloud enumeration + short-lived token minting** (thin per-cloud adapters: EKS `get-token`, AKS Entra+kubelogin, GKE plugin, plus **ACK/CCE/TKE** for China) | The Kubernetes API is uniform across US and China clouds (all conformance-certified), so cluster-*inside* views work day one with no cloud code. Only enumeration and credential-minting differ per cloud — a thin adapter, minting short-lived tokens, storing no long-lived cloud keys. |
| **Air-gap / multi-arch / registry-relocatable packaging** (Zarf-style single bundle, no phone-home, `linux/amd64`+`arm64` images from the first build, cosign-signed) | Mandatory for China and regulated estates, and cheap only if designed in early. Multi-arch images are day-one; the offline bundle is fast-follow. |
| **Tauri desktop shell** wrapping the same binary | Dock presence and deep-OS integration for the local mode. Additive. |

### 4.3 Later — real, but not until the above lands

| Capability | Why later |
|---|---|
| **Long-tail read connectors** — Datadog, Splunk, Elastic/OpenSearch/Kibana, Terraform/OpenTofu state-and-drift | Demand-ranked, not wedge. Each is a read adapter through the framework; none is on the critical path. Cost/observability pain pushes teams *toward* self-hosting, which is a distribution tailwind, not a reason to embed these. |
| **ITSM typed actions** — Jira / Zendesk / ServiceNow change-ticket linkage for a fleet action | Real demand (change-management linkage), but it is a convenience verb, not the wedge. |
| **Sith as an MCP *client*** (calling kagent, Grafana MCP, GitHub MCP) | A convenience that consumes other servers; the value is Sith *being* the governed server, not calling others. |
| **Governing LangChain / LangGraph agents** | Resolves to: those agents connect to Sith's MCP server as clients, get a scoped identity with a ceiling below the human's, and every cluster action they attempt goes through the same PEP + decision-ledger. Sith **governs** agents; it does not **orchestrate** them (LangGraph/kagent do that). Downstream of the MCP server and policy federation, hence later. |
| **OpenShift-specific views** (Routes / SCC-aware) | Conformant-API coverage is guaranteed day one; deep OpenShift-isms are later, and Sith never competes for ACM-committed estates. |

### 4.4 Explicitly not now — say no, loudly (this list is the anti-sprawl contract)

- **Re-skinning or proxying tool UIs** (iframing Grafana, thin ArgoCD/GitHub re-implementations).
  This was the predecessor's core negative-value pattern (review-01). Brokered read-through means
  *deep-link to the tool's own UI*; never re-build it.
- **A telemetry lake / metrics or log store.** Sith reads health through Prometheus/Loki; it never
  stores series. Drifting here re-fights Grafana/Datadog and violates the non-goal.
- **A metering / billing / cost-optimization engine.** The cost overlay reads OpenCost; it does
  not meter or auto-mutate clusters (that is CAST AI's ground).
- **An agent-orchestration framework.** LangGraph and kagent orchestrate; Sith governs. Building an
  orchestrator is scope Sith cannot win and does not need.
- **A developer portal / IDP / service catalog / scorecards / DORA.** The exact thesis that lost
  across 2020–2026 and that the predecessor died on (review-01, review-10). Portals can *consume*
  Sith's API/MCP; Sith is not a portal.
- **A GitOps controller / reconciler** (Sith opens PRs) and **a multi-cluster scheduler** (Karmada,
  OCM Placement).
- **Fluentd / Fluent Bit as data sources** — they ship logs *to* Loki/Elastic; read those sinks
  instead. **Kustomize / Helm as action targets** in v1 — read adapters for inventory only.
- **`exec` / free-form `apply` / Secret mutation / RBAC mutation** — not "not yet", but permanently
  inexpressible in the action model. This does not move buckets, ever.
- **Running SPIRE / forcing a workload-identity platform on users.** SPIFFE is the right *identity
  model* (support SPIFFE IDs and mTLS), but SPIRE is operationally heavy — a dedicated engineer and
  a 6–24-month rollout ([identity §2](identity-connections-security.md)). Support the identity type;
  do not make users run the platform.

---

## 5. Carry / discard / net-new vs `devops-portal`

The predecessor is a lessons-learned artifact, not a starting codebase. The reviews are blunt:
"a technically impressive solution in search of a problem" with "no identified user … and no
wedge" (review-01), whose one salvageable idea is "a governed AI-mutation layer … exposed as an
MCP-governance component" (review-10). Concretely:

| Carry (the good bones) | Discard (the failure modes) | Net-new (what the research says Sith needs and the predecessor never had) |
|---|---|---|
| **Action/exec broker service-layer** — the clean 1:1 tool→service mapping (review-08) — **redesigned as the PEP** with a closed verb vocabulary and no shell | **Shared central admin kubeconfig / inbound-god-kubeconfig** model — replaced by outbound OCM minions + scoped MSA tokens + no central admin credential | **Local aggregated fleet client** as the adoption wedge (empty OSS slot) |
| **Per-org encrypted credential vault** (AES-256-GCM key-ring, review-08) — **re-architected** as KMS-envelope per-tenant DEKs | **Single god key** (`TOKEN_ENCRYPTION_KEY` decrypts every tenant) | **Cross-cluster correlation** as a first-class query (single-cluster tools can't) |
| **RBAC + audit spine** (review-08) — kept and **hardened** with signed-token authz and a separate decision-ledger | **Dead/inert RLS + `x-user-role` header-trust IDOR** — replaced by FORCE RLS (non-owner role) + signed-token-only authz | **Typed-intent action model** with signed dispatch + per-minion local allowlist re-validation (two independent blast-radius bounds) |
| **Governed AI / MCP ambition** — review-10's "salvage at most one idea": this becomes the **core**, done right (real MCP server, elicitation gates, AI-as-client) | **All-heavy monolith** (~48k LOC, 92 routes, 24 pages; iframes/reverse-proxies better tools) — replaced by one narrow Go binary + embedded UI, no re-skinning | **Policy federation** — waves / multi-approver / abstention (genuinely novel, empty) |
| **Workspace tenancy** (the Tenant→Workspace rename) — kept as the isolation anchor with a **real** RLS backstop | **Feature sprawl** (12 providers, ~7/10 pillars pass-through) — replaced by the closed vocabulary + three fixed connector kinds + a hard scope gate | **Ardur** as external PDP + identity broker + decision-ledger (*why-allowed* split from *what-happened*) |
| | **Broken onboarding** (platform-install cliff; 500s on fresh install) — replaced by `brew install && sith ui`, ten-minute wow | **Air-gap / multi-arch / registry-relocatable** distribution for China/regulated |
| | **Features that never ran** (dead write path, `allowDestructive` never set, mock MCP page, qwen2.5:3b as the only tool-capable model) — replaced by falsification-first, ship-what's-verified discipline | **Cost read-overlay** with fleet rollup + GPU columns (empty in OSS, paywalled commercially) |

---

## 6. Concrete changes to the plan

These are the edits the research implies. They are surgical: the governance thesis is *validated*
by the evidence (annotations are hints, enforcement must be server-side, no incumbent occupies
governed fleet action) — the change is to add the adoption wedge and hang the named capabilities
off it without diluting the anti-drift contract.

### CHARTER.md
- **§3 Target user** — add the **individual operator** as the top-of-funnel entry (the local-mode
  user), distinct from the platform/SRE team who runs the hub. The primary buyer is unchanged; the
  first user is new.
- **§4 The wedge** — reframe from one wedge to **two**: the *adoption wedge* (the local aggregated
  fleet client) and the *durable wedge* (governed action federation). State explicitly that they
  share one fleet-model engine and that the local mode is the funnel for the governed hub, not a
  second product.
- **§6 Success criteria** — add an adoption-side criterion for the local mode (e.g. "a new user
  goes from `brew install` to a populated cross-cluster answer in under ten minutes, offline, with
  nothing leaving the machine"), sitting alongside the existing P1–P3 governance criteria.

### SCOPE.md
- **The single most important scope edit.** Refine the out-of-scope row "A single-cluster console /
  IDE" to "**Another single-cluster** console" and add an **in-scope** line: "**an aggregated
  multi-cluster local fleet client** (the adoption on-ramp), distinguished by fleet
  aggregation/correlation and staleness — not per-pod parity with Headlamp/k9s." This resolves the
  apparent contradiction with the owner's "Lens IDE better than Lens" ask: Sith does not build
  another per-cluster console; it builds the *fleet* view none of them center on.
- Add to the non-goals table (make the §4.4 "not now" list contractual): tool-UI re-skinning /
  proxying, a telemetry lake, a metering/optimization engine, an agent-orchestration framework, and
  Fluentd/Fluent-bit as data sources.

### ARCHITECTURE.md
- Add a **run-modes** section up front: one Go binary + one embedded web frontend, three modes
  (`sith` / `sith ui` / `sith hub`); cache-first local render via the K8s informer/watch cache;
  Tauri shell as a fast-follow.
- Make the **read-federation service source-abstract**: the fleet model is populated from **local
  kubeconfig contexts (direct)** *or* **OCM-brokered spokes (federated)** — same model, two sources.
  This is what makes local and hub one engine.
- Add the **four-mode connection/identity model** from [identity §3](identity-connections-security.md)
  (local / minion / cloud-IAM / API-key-JWT-OIDC) and the **connector framework** (three fixed
  kinds, out-of-process gRPC, SDK-first) as named components.
- Add the **cost read-overlay** as a fleet-fact kind, and note **cosign + SLSA L2 + SBOM** as
  release requirements and **SPIFFE IDs/mTLS supported without requiring SPIRE**.

### ROADMAP.md
- Insert an early **Local-mode track** that ships the local fleet client on the **same P1
  read-federation engine**, in parallel with the hub's P1 — the adoption artifact ships early
  without breaking falsification-first, because the local client *is* read federation against local
  kubeconfigs.
- Explicitly require **P1 read federation to be source-agnostic** (local *or* OCM) from the start.
- Keep M0 (passed), P2 (`gitops.open-pr`), and P3 (policy federation + MCP) as they are; attach the
  **cost overlay**, **connector-framework generalization**, and **multi-cloud/air-gap packaging** as
  P3-adjacent fast-follows, with **multi-arch images required from the first release**.

### EPICS.md
- **Amend E2 (Read federation)** to treat **local kubeconfig contexts as a first-class read source**
  alongside OCM spokes.
- **New E11 — Local fleet client (the adoption on-ramp):** single Go binary, `sith ui`, kubeconfig
  auto-detect, cache-first render, cmd-K fleet search, logs/exec/port-forward/YAML in core,
  aggregated cross-cluster views, no account/telemetry; Tauri shell fast-follow. Reuses E2's model.
- **New E12 — Connector framework (fast-follow):** out-of-process gRPC SDK, three fixed kinds, one
  canonical connector per tool; generalizes the six day-1 adapters.
- **New E13 — Cost read-overlay (fast-follow):** OpenCost per-cluster read + hub rollup + GPU
  columns; explicitly *not* a metering engine.
- **Amend E9 (Deployment & packaging)** to require multi-arch (`amd64`+`arm64`) images day one,
  registry-relocatable, a Zarf-style air-gap bundle with no phone-home, and cosign-signed releases
  with SLSA L2 provenance + SBOM.
- **Amend E7 (MCP server)** to note that the MCP *client* role and agent-management are **later**,
  downstream of the server; and **E8 (Console)** to note the console is the one web frontend served
  by both `sith ui` and `sith hub`.

---

## 7. The one-line test for every future feature request

Before anything is added: *does it serve the adoption wedge (get an individual to install and
love the local fleet client) or the durable wedge (governed action across the fleet, humans and
agents alike)?* If neither, it belongs in §4.4 — no matter how useful it sounds. That question,
applied ruthlessly, is the difference between Sith and the predecessor.
