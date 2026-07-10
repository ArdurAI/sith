# Sith — Market & Landscape Research (July 2026)

**Status:** research · **Date:** 2026-07-10 · **License:** Apache-2.0 · **Author identity:** ArdurAI

**Purpose.** A current (2026) competitive-and-landscape pass to *feed epic creation*. It builds on
the existing research set — [`market-and-form-factor.md`](market-and-form-factor.md),
[`USE-CASE-AND-SHAPE.md`](USE-CASE-AND-SHAPE.md),
[`integrations-and-ai-governance.md`](integrations-and-ai-governance.md),
[`identity-connections-security.md`](identity-connections-security.md) (PRs #15/#16/#17) — and
does **not** re-derive what they already establish (the Lens exodus, the Electron/Tauri decision,
the China/air-gap map, the cost-rollup gap). Where this pass overlaps, it *refreshes and links*;
where the prior set was thin — the **AI-SRE / auto-triage category**, a single **cross-category
comparison matrix**, the **standards bibliography**, and a **findings → epic-theme mapping** — it
goes deep.

> **Grounding.** Sith = a single Go binary, local-first: a k9s/Lens-class **cross-cluster fleet
> client** (`sith` / `sith ui`, kubeconfig-based, cache-first, offline, no account) that grows into
> a governed **hub** (`sith hub`) for typed-intent action federation, with a **source-abstract
> connector contract** (7 verbs: discover/read/query/diff/plan/execute/verify, PR #41/#43) and a
> **rule-based Investigation Brain** (proposed epic **E14**, PR #43: R1 bad-deploy · R2 OOMKilled ·
> R3 CrashLoopBackOff · R4 config-drift · R5 cert-expiry · R6 node-pressure). Epics **E0–E13** are
> GitHub issues **#18–#31**; feature issues **#32–#38**; roadmap epic **#39**.

---

## 0. Method & honesty notes

- **Sourcing.** Every load-bearing claim below carries a link. Facts were gathered by web search
  and primary-source fetch during 2026-07-10. GitHub issue states, CVE details, CNCF maturity
  dates, and spec-release status were fetched from the primary page where practical.
- **Adversarial verification.** Section 8 re-checks the six claims most likely to be wrong or
  overstated. Where a source is vendor-authored, competitor-authored, or a secondary blog, it is
  labelled inline and kept out of load-bearing conclusions.
- **Known discrepancy carried forward (unresolved).** This pass read the OCM release page as
  **v1.2.0 (2026-02-02)**; prior repo research
  ([`market-and-form-factor.md`](market-and-form-factor.md) §3) cited **v1.3.1 (2026-05-19)**. The
  addon pins Sith depends on (`cluster-proxy` / `managed-serviceaccount` **v0.10.0**) are
  consistent across both. Treat the exact OCM core version as *verify-before-pinning*; it is not
  load-bearing for any conclusion here. See [OCM releases](https://open-cluster-management.io/docs/release/).
- **What this pass did not do.** No pricing was negotiated or quoted beyond public pages; no China
  clouds were re-verified (prior pass hand-verified ACK/CCE/TKE conformance — reused by reference).

---

## 1. Executive summary

**Top competitors, by the lane they actually threaten:**

1. **k9s** (34k★, the operator's terminal default) and **Headlamp** (CNCF, the Kubernetes-Dashboard
   successor) own the *local read* lane Sith enters — both are **per-cluster-centric**; neither
   ships an aggregated cross-cluster fleet view. **Aptakube** is the only tool that does, and it is
   closed and paid.
2. The **AI-SRE / auto-triage** wave — **k8sgpt**, **HolmesGPT** (both CNCF Sandbox), **Robusta**,
   **Botkube**, **Komodor/Klaudia**, **Cleric** — is the fastest-moving adjacent category and the
   one most likely to be *confused* with Sith's Investigation Brain. Decisive finding: **every one
   of them is investigate/advise — read-only or action-gated — and LLM-agentic.** None ships
   deterministic rule-based root-cause, and none ships governed *typed* action.
3. The **fleet/platform incumbents** — **Rancher/Prime** (price-hike churn), **OpenShift+ACM**,
   **OCM**, **Komodor**, **Devtron**, **Portainer** — own governance-at-scale but none exposes a
   *neutral, typed-intent, multi-approver, PR-first* action vocabulary; ACM/Rancher policy is
   platform-coupled.
4. **Backstage/IDPs** are a different category (catalog/self-service), and their own community
   concedes the **Kubernetes tile "shows little more than metadata… developers hit a wall during
   incidents."** Not a competitor; a *consumer* of Sith's API/MCP.

**The wedge verdict (confirmed, and now sharper).** The prior research's two-wedge thesis holds:
**(a)** the adoption wedge is *"k9s for your whole fleet"* — an OSS, no-account, no-telemetry,
**cross-cluster-by-default** local client, a slot that is still **empty** (closest occupant closed
+ paid); **(b)** the durable moat is **governed typed-intent action federation with AI as a
governed client**, a position **empty across every incumbent and every AI-SRE tool.** This pass adds
a **third, newly-evidenced wedge inside the moat**: a **deterministic, offline, rule-based
Investigation Brain** that advises locally and proposes governed PR-based remediation in the hub —
occupying the gap *between* dumb dashboards and non-deterministic LLM SRE-agents, and directly
answered by the July-2026 evidence that (i) LLM SRE agents are read-only/gated on action and (ii)
naively-shipped Kubernetes MCP servers are actively vulnerable ([CVE-2026-46519](https://neuraltrust.ai/blog/kubernetes-mcp),
CVSS 8.8).

**Standards to align with (Section 4):** OCM addon framework; CNCF landscape placement; **MCP
2026-07-28 spec RC** (OAuth 2.1 + RFC 8707 audience binding, elicitation); OpenTelemetry
**Kubernetes semantic conventions** (the join keys for the four-lens graph); **client-go
ExecCredential** (`client.authentication.k8s.io/v1`) for kubeconfig exec-plugin auth;
**SLSA + Sigstore/cosign + SBOM** supply-chain trio; **Kubernetes API conventions** for the
normalized fleet model.

**Recommended epic themes (Section 7):** they map cleanly onto the existing epic set — the biggest
*new* signal is that **E14 (Investigation Brain)** should be promoted from "proposed" to a
first-class epic with a **"deterministic vs LLM" positioning contract**, and that **E7 (Governed
MCP)** should adopt the MCP-2026 authorization hardening and an explicit *enforce-at-execution*
guard (the exact bug class behind CVE-2026-46519).

---

## 2. Competitive landscape

Category order: local fleet clients → GitOps/action layer → fleet/platform incumbents → **AI-SRE /
auto-triage (deep)** → IDP/Backstage → observability/cost (by reference). Each entry: what it does ·
gaps · real user complaints with links.

### 2A. Local / single-operator fleet clients (Sith's day-0 lane)

| Tool | What it is | Gap vs Sith | Real complaints / signal |
|---|---|---|---|
| [**k9s**](https://github.com/derailed/k9s) | Terminal UI, single Go binary, 34.1k★, Apache-2.0 | **One context at a time** (`:ctx` to switch); no aggregated fleet view; no correlation; no governed action | Multi-cluster asks keep being filed and **closed *not planned***: [#3374 "Multi-cluster mode?"](https://github.com/derailed/k9s/issues/3374) (opened 2025-06-02, closed not-planned/stale), [#1430 "Multiple Tabs"](https://github.com/derailed/k9s/issues/1430), plus [#1006/#2730](https://github.com/derailed/k9s/issues/1006) cited in prior research. **Three-plus distinct, unmet requests** = a durable convenience gap |
| [**Lens**](https://lenshq.io/pricing) (Mirantis) | Electron K8s IDE; multi-cluster mgmt, metrics, logs, terminal | Account wall; telemetry-by-default (opt-out); closed source since 2024-03; Plus $25/user/mo | The exodus was about **trust** (account + closed-source + telemetry + logs/shell moved out of OSS build), fully documented in [`market-and-form-factor.md`](market-and-form-factor.md) §Part 2 — not re-derived here |
| [**OpenLens**](https://github.com/MuhammedKalkan/OpenLens) | Login-free Lens build | **Dead since 2023-06** | 4.4k★ for a build repo whose only feature was deleting the login = raw "no-account" demand |
| [**Freelens**](https://github.com/freelensapp/freelens) | MIT fork of OpenLens, no account/telemetry | Inherits Lens's **one-cluster-at-a-time** UX + Electron weight; a continuation, not a fleet rethink | Healthy (5.3k★, ~monthly cadence) — proves the audience, not the fleet answer |
| [**Headlamp**](https://github.com/kubernetes-sigs/headlamp) | **CNCF/SIG-UI** web + desktop, plugin system; the [Kubernetes-Dashboard successor](https://kubernetes.io/blog/2026/06/01/dashboard-to-headlamp/) | **Per-cluster-centric**; multi-cluster registration exists but ClusterProfile inventory is alpha; no fleet correlation, no staleness semantics, no governed typed actions | CNCF-blessed and improving monthly — the *strongest* reason Sith's local mode must lead with **fleet aggregation/correlation**, not "another general console" |
| [**Aptakube**](https://aptakube.com/) | Closed-source **Tauri** desktop client | Closed + paid ($9/mo personal), no free tier | Its headline claim is being *"the **only** Kubernetes UI that can connect to multiple clusters simultaneously"* — **direct proof the aggregated-fleet slot is real and, in OSS, empty** |
| [**Kubevious**](https://github.com/kubevious/kubevious) | Apache-2.0 app-centric config **validation + introspection**; CLI validates manifests in CI | Config-lens only; not a fleet client; no cross-cluster correlation | Actively maintained; **adjacent to Sith's R4 config-drift rule** — a reference for rule design, not a competitor |

**Lane verdict (unchanged, reinforced):** OSS, no-account, no-telemetry, **cross-cluster-by-default**
local client with fleet search/correlation is **unoccupied**. k9s won't add it (repeatedly declined);
Headlamp centers per-cluster; Aptakube is closed+paid. This is the on-ramp.

### 2B. GitOps / deploy layer (integrate, never replace)

Covered in [`market-and-form-factor.md`](market-and-form-factor.md) §Part 3.2. Refresh: **Argo CD**
(CNCF-graduated; its own multi-cluster story is being agent-ified by
[argocd-agent](https://github.com/argoproj-labs/argocd-agent)) federates *its own* surface only;
**Flux** is CRD-only; **Kargo** ([akuity/kargo](https://github.com/akuity/kargo)) gates *artifact
promotion through environments* via GitOps — the closest OSS analog to "governed promotion" but
Argo-coupled and not typed live operations across heterogeneous fleets. Sith rides these as targets
for `gitops.open-pr` / `argocd.sync|rollback` / `rollout.promote|abort` — it does not reconcile.

### 2C. Fleet / platform incumbents

Full table in [`market-and-form-factor.md`](market-and-form-factor.md) §Part 3.3 (Rancher/Prime,
OpenShift+ACM, OCM, Karmada, Clusterpedia, KubeSphere, Spectro Cloud, Loft/vCluster, kagent). 2026
refresh of the two most action-relevant:

- **Rancher / Prime (SUSE).** 2026 "Liz → agentic crew" + MCP server integration
  ([SUSE, KubeCon EU 2026](https://www.suse.com/c/kubecon-eu-2026-first-agentic-ecosystem-platform/)).
  Still **subscription-gated**; the 2025 vCPU repricing drove alternatives-shopping (rival-authored
  account, [Portainer](https://www.portainer.io/blog/suse-rancher-price-hike-why-enterprises-are-searching-for-alternatives-in-2025) — treat as directional). No typed-intent vocabulary, no multi-approver
  fan-out gates, no abstention.
- **OCM.** CNCF **Sandbox, applying to Incubation** ([CNCF project page](https://www.cncf.io/projects/open-cluster-management/); [TOC incubation issue #1884](https://github.com/cncf/toc/issues/1884)). `cluster-proxy` now supports a
  ["Service Proxy" for reaching in-cluster services](https://open-cluster-management.io/blog/2025/cluster-proxy-now-supports-service-proxy-an-easy-way-to-access-services-in-managed-clusters/)
  — *directly useful to Sith's day-N reach path*. Confirmed absent from OCM: approval gates,
  multi-approver, typed/closed action vocabulary (ManifestWork = arbitrary YAML), operation-level
  audit ledger, abstention. **Governance above OCM remains the gap.**

### 2D. AI-SRE / auto-triage — the deep pass (new)

This is the category most likely to be *confused with* Sith's Investigation Brain, and the one the
prior research covered least. The 2026 field, and where each sits on the **investigate ↔ act** axis:

| Tool | What it does | Reasoning engine | Action stance (2026) | Gaps vs Sith's brain + governed action | Signal |
|---|---|---|---|---|---|
| [**k8sgpt**](https://k8sgpt.ai/) | Scans clusters, explains issues in plain English; CLI + in-cluster Operator writing `Result` CRs | **Analyzers** (structured pre-analysis per resource) **+ LLM** backend for the explanation | **Advise-only** (diagnosis + remediation *suggestions*) | Requires an AI backend for the useful output; per-cluster; no cross-cluster correlation; no governed action path | [CNCF Sandbox, accepted 2023-12-19](https://www.cncf.io/projects/k8sgpt/); the reference "AI K8s troubleshooting" OSS |
| [**HolmesGPT**](https://github.com/HolmesGPT/holmesgpt) | Agentic RCA over observability data + runbooks | **LLM agentic loop** — "actively decides what data to fetch, runs targeted queries, iteratively refines hypotheses" | **Read-only. "Investigates and advises rather than taking autonomous remedial actions… returns a natural-language diagnosis and remediation steps but doesn't execute fixes"** ([CNCF blog, 2026-01-07](https://www.cncf.io/blog/2026/01/07/holmesgpt-agentic-troubleshooting-built-for-the-cloud-native-era/)) | Non-deterministic; needs an LLM + observability stack; single-incident, not fleet correlation; no typed governed action | [CNCF Sandbox, Oct 2025](https://github.com/HolmesGPT/holmesgpt); by **Robusta + Microsoft** |
| [**Robusta**](https://docs.robusta.dev/) | K8s monitoring/automation + Robusta AI (managed HolmesGPT); change tracking, KRR right-sizing | Playbooks (rule-triggered automations) + LLM (Holmes) | **Automations exist** but are pre-authored playbooks; AI layer is advise | Playbooks are per-cluster and hand-wired; no neutral typed vocabulary, no multi-approver/abstention; SaaS AI tier | Production cost anecdote: *~$0.04/investigation* — LLM-per-incident is cheap but non-deterministic |
| [**Botkube**](https://botkube.io/) | ChatOps: alerts + AI Assistant (GPT-4o) in Slack/Teams/Mattermost/Discord; compiles RCA/post-mortems | LLM assistant + `kubectl`-exec plugins from chat | **Executes commands from chat** (ChatOps) — trust boundary is chat RBAC | Free-form `kubectl` from chat is exactly the surface Sith **permanently excludes**; no typed closed vocabulary, no signed dispatch, no per-spoke re-validation | The "act from chat" model is the *anti-pattern* Sith's threat model rejects |
| [**Komodor / Klaudia**](https://komodor.com/) | SaaS K8s ops + multi-agent AI-SRE (50+ agents), MCP/OpenAPI extensibility, **sandboxed + audited remediation** ([2026-03-18](https://www.globenewswire.com/news-release/2026/03/18/3258257/0/en/komodor-introduces-extensible-autonomous-multi-agent-architecture-for-ai-driven-site-reliability-engineering.html)) | Multi-agent LLM | Remediation is **sandboxed + audited** but inside a **closed SaaS** | Closed, SaaS (China/air-gap excluded), diagnosis-first; no vendor-neutral typed vocabulary, no cross-tool multi-approver canary as a primitive | Closest *product* to "operate the fleet with AI on top"; $72M funded ([Crunchbase](https://www.crunchbase.com/organization/komodor)) |
| [**Cleric**](https://cleric.ai/) | Autonomous AI-SRE teammate; investigates in Slack with evidence links; continuous learning | LLM + operational memory | **"Today's AI SREs are most effective at investigation and diagnosis… they're gated in their ability to make production changes"** (Cleric's own framing) | Autonomy-first *aspiration*, action-gated *reality*; closed SaaS; observability-tool-centric, not fleet-governance | $9.8M seed; Gartner Cool Vendor 2025 — VC momentum, not a governance primitive |

**Category verdict (the sharpest new finding).** The entire AI-SRE wave converges on the same
shape: **LLM-agentic, investigate/advise, and either read-only or explicitly gated on production
action** (HolmesGPT read-only; Cleric "gated"; Komodor sandboxed-inside-SaaS; k8sgpt advise-only;
Botkube pushes the risk onto chat-RBAC + free-form `kubectl`). Two structural openings fall out:

1. **Deterministic root-cause is unoccupied in OSS.** Every serious tool routes root-cause through
   an LLM. Sith's **rule-based** brain (R1–R6) is *offline, reproducible, explainable, and needs no
   model or data egress* — a different and complementary product, credible to security-conscious /
   air-gapped / China estates that cannot send cluster telemetry to an LLM. It should be positioned
   *as* deterministic, not as "our AI is better."
2. **The action gap the AI-SRE tools admit to is Sith's moat.** They all stop at "here's the fix";
   Sith's answer to "and now safely do it" is **typed-intent + PR-first + multi-approver +
   abstention + signed dispatch + per-spoke re-validation** — and Sith can be the **governed MCP
   server these very agents call** to act safely (they become clients of Sith's governance, per
   [`integrations-and-ai-governance.md`](integrations-and-ai-governance.md) §4).

### 2E. IDP / Backstage (different category, potential consumer)

**Backstage** is the de-facto OSS IDP, but its community concedes the Kubernetes surface is shallow:
*"the Kubernetes 'tile' often shows little more than metadata, raw manifests, or links to external
dashboards, leaving developers hitting a wall… during incidents, deployments, or debugging"*
([Komodor on Backstage+K8s](https://komodor.com/blog/komodor-backstage-kubernetes-visibility-into-open-source-idp/)),
plus the well-worn *build-vs-buy* cost complaint (*"6 months building what commercial tools deliver
in 2 weeks"*). This is **not a competitor** — it is the predecessor's failed race (portal) — but it
is a **distribution channel**: Sith's read federation + brain can be the data behind a Backstage
tile via Sith's API/MCP. Explicit non-goal per [`EPICS.md`](../EPICS.md) §Non-goals; reaffirmed.

### 2F. Observability / cost (integrate-only)

Unchanged from prior research and not re-derived: Prometheus/Grafana/Loki, Elastic/OpenSearch,
Fluentd/Fluent-bit, Datadog/Splunk are **read-through** surfaces, never embedded; **OpenCost**
(CNCF incubating) is the per-cluster standard and the **fleet rollup is the gap** (paywalled at
Kubecost Enterprise; unsolved in OpenCost — [opencost#2638](https://github.com/opencost/opencost/issues/2638)).
See [`market-and-form-factor.md`](market-and-form-factor.md) §Part 3.4–3.5. → **E13**.

---

## 3. The comparison matrix

Columns are the properties that define Sith's wedges. `Y` = yes/first-class; `~` = partial/limited;
`N` = no; `$` = paid/enterprise-tier only; `SaaS` = closed SaaS only. "X-cluster read" =
*aggregated* cross-cluster view (not "can switch context"). "Typed action" = closed, schema-validated
verb vocabulary (not free-form `kubectl`/YAML). "Multi-approver / abstention" = governed fan-out
gates. "Local, no-acct" = runs offline from kubeconfig with no account/telemetry. "Det. RCA" =
deterministic (non-LLM) root-cause.

| Tool | Local, no-acct | X-cluster read | Correlation + staleness | Typed action | PR-first remediation | Multi-approver / abstention | Governed MCP | Det. RCA | OSS |
|---|---|---|---|---|---|---|---|---|---|
| **Sith** (target) | **Y** | **Y** | **Y** | **Y** | **Y** | **Y** | **Y** | **Y** | **Y** |
| k9s | Y | N | N | N | N | N | N | N | Y |
| Lens | ~ (acct) | ~ (switch) | N | N | N | N | N | N | N |
| Freelens | Y | N | N | N | N | N | N | N | Y |
| Headlamp | Y | ~ (register) | N | N | N | N | ~ (MCP) | N | Y |
| Aptakube | Y | **Y** | N | N | N | N | N | N | N ($) |
| Kubevious | ~ | N | ~ (config) | N | N | N | N | ~ (rules) | Y |
| Rancher/Prime | N | Y | ~ | N | N | N | ~ (MCP) | N | ~ ($) |
| OpenShift+ACM | N | Y | ~ | N (YAML) | N | ~ (policy) | N | N | ~ ($) |
| OCM | N | ~ (inventory) | ~ | N (YAML) | N | N | N | N | Y |
| Clusterpedia | N | **Y** (search) | ~ | N | N | N | N | N | Y |
| Komodor | SaaS | Y | Y | N | N | N (sandboxed) | ~ (MCP) | N | N (SaaS) |
| Devtron | ~ | Y | ~ | N | ~ (GitOps) | ~ | N | N | ~ ($) |
| Portainer | Y | Y | ~ | N | N | ~ (RBAC) | N | N | ~ ($) |
| k8sgpt | Y (needs LLM) | N | N | N | N | N | ~ | N | Y |
| HolmesGPT | ~ (needs LLM) | ~ | ~ | N | N | N | ~ | N | Y |
| Robusta | ~ | ~ | Y | N (playbooks) | N | N | ~ | N | ~ ($) |
| Botkube | ~ | ~ | ~ | N (kubectl) | N | ~ (chat RBAC) | ~ | N | ~ ($) |
| Cleric | SaaS | Y | Y | N (gated) | N | N | ~ | N | N (SaaS) |
| CAST AI | SaaS | Y | ~ | N (auto-mutate) | N | N | N | N | N (SaaS) |
| Backstage | ~ | ~ (tile) | N | N | N | N | ~ | N | Y |

**Reading the matrix.** No row but Sith's has `Y` in both **cross-cluster read** *and* **typed
governed action**; no OSS row has `Y` in **deterministic RCA**; and the only rows with aggregated
cross-cluster read are either closed/paid (Aptakube, Komodor, Cleric, CAST AI) or read-only inventory
(Clusterpedia). The empty column intersection **{local-no-account} × {cross-cluster} × {typed
governed action} × {deterministic RCA}** is Sith.

---

## 4. Category framing — which fight is Sith in?

Four adjacent categories are frequently conflated. Sith spans two of them and deliberately abstains
from the other two.

| Category | Canonical examples | The job | Sith's relationship |
|---|---|---|---|
| **Fleet client** (read/observe) | k9s, Lens, Headlamp, Aptakube, Clusterpedia | *See* many clusters | **Sith competes here (day-0)** — but on **aggregation + correlation**, the axis the OSS incumbents skip |
| **AI-SRE / auto-triage** | k8sgpt, HolmesGPT, Komodor, Cleric | *Explain* what's wrong | **Sith competes here (E14)** — but **deterministically**, and hands off to governed action instead of stopping at advice |
| **GitOps / action layer** | Argo CD, Flux, Kargo, Rancher Fleet | *Reconcile* desired state | **Sith does NOT compete** — it *proposes* (PR-first) and *rides* their APIs; reconciler is out of scope |
| **IDP / portal** | Backstage, Port, Cortex | *Catalog + self-service* | **Sith does NOT compete** — portals *consume* Sith via API/MCP; this was the predecessor's fatal race |

**Is "local-first, cross-cluster-by-default, no-account" a real wedge? — Yes, with evidence.**

- *Local-first / no-account:* the OpenLens 4.4k★-for-deleting-the-login signal, the Lens trust
  exodus, and the CNCF-survey adoption barriers (complexity/abandonment/docs, not governance) all
  point the same way — form and trust win adoption. (Prior research, reused.)
- *Cross-cluster-by-default:* three-plus **closed-not-planned** k9s multi-cluster requests
  ([#3374](https://github.com/derailed/k9s/issues/3374), [#1430](https://github.com/derailed/k9s/issues/1430),
  [#1006/#2730](https://github.com/derailed/k9s/issues/1006)); Aptakube monetizing exactly this as
  its headline; Headlamp centering per-cluster. The demand is proven and the OSS supply is zero.
- *Honest caveat:* the read wedge is a **convenience** gap, not an emergency — so it is a
  *beachhead*, not the business. The durable value is the **governed action + governed-agent-access
  moat**, which the whole 2026 AI-SRE wave has now made *more* valuable by flooding the "advise"
  half while leaving the "safely act" half empty.

---

## 5. Standards & guidelines to align with

Authoritative, current references an implementer can build against. (Full bibliography in §9.)

### 5.1 Multi-cluster substrate
- **Open Cluster Management** — hub/spoke registration, Placement, ManifestWork, addon framework;
  `cluster-proxy` (konnectivity-based outbound reach, incl. Service Proxy) + `managed-serviceaccount`
  (scoped token projection). [Docs](https://open-cluster-management.io/docs/) ·
  [cluster-proxy](https://github.com/open-cluster-management-io/cluster-proxy) ·
  [managed-serviceaccount](https://github.com/open-cluster-management-io/managed-serviceaccount).
  *Align:* adopt the addon framework; do not re-implement transport (ADR-0001).
- **CNCF Landscape** — for category placement and the "adopt-don't-build" test.
  [landscape.cncf.io/guide](https://landscape.cncf.io/guide) · [cncf/landscape](https://github.com/cncf/landscape).

### 5.2 Agent / AI governance
- **Model Context Protocol** — the **2026-07-28 spec release candidate** is the largest revision
  since launch: authorization aligned to **OAuth 2.1 + OpenID Connect**, clients **required to
  implement RFC 8707 Resource Indicators** (audience-bound, tightly-scoped tokens), a stateless
  core, Extensions, Tasks, MCP Apps, and a formal deprecation policy
  ([MCP blog](https://blog.modelcontextprotocol.io/posts/2026-07-28-release-candidate/);
  [authorization spec](https://modelcontextprotocol.io/specification/draft/basic/authorization)).
  Elicitation (server-initiated user prompts) is the standard hook for Sith's approval gates.
  *Align:* E7 must implement audience-bound tokens and **enforce at execution, not just discovery**
  — see the CVE below.
- **MCP security guidance** — **NSA/CISA MCP Security CSI**
  ([PDF](https://www.nsa.gov/Portals/75/documents/Cybersecurity/CSI_MCP_SECURITY.pdf)) and the
  concrete failure mode: **CVE-2026-46519** in `mcp-server-kubernetes` (20k weekly npm downloads),
  CVSS **8.8** — security controls (`ALLOW_ONLY_READONLY_TOOLS` etc.) were enforced only at the
  `tools/list` **discovery** layer, not the `tools/call` **execution** layer, so a caller who knows
  the tool name bypasses them ([NeuralTrust write-up](https://neuraltrust.ai/blog/kubernetes-mcp)).
  *Align:* Sith's spoke-side **re-validation at execution** (E4 F4.5) and fail-safe allowlist are
  the exact structural fix — make it a named guardrail in E7.

### 5.3 Telemetry / correlation model
- **OpenTelemetry Kubernetes semantic conventions** — `k8s.pod.uid`, `k8s.pod.name`,
  `k8s.namespace.name`, `k8s.cluster.uid` (= UID of `kube-system` as the cluster-ID proxy), etc.
  ([semconv k8s registry](https://opentelemetry.io/docs/specs/semconv/registry/attributes/k8s/);
  [resource/k8s.md](https://github.com/open-telemetry/semantic-conventions/blob/main/docs/resource/k8s.md)).
  *Align:* use these as the **join keys** for the four-lens operational graph (PR #43 §2.3 already
  cites "OTel-style" — this pins the exact attribute names to normalize against).

### 5.4 Cluster access / identity
- **client-go ExecCredential / credential plugins** — `client.authentication.k8s.io/v1` (and
  `v1beta1`); the standard for cloud exec-plugin auth (`aws eks get-token`, `kubelogin`,
  `gke-gcloud-auth-plugin`). Token/ClientKeyData are sensitive — in-memory only.
  ([Client Authentication v1](https://kubernetes.io/docs/reference/config-api/client-authentication.v1/);
  [kubeconfig concepts](https://kubernetes.io/docs/concepts/configuration/organize-cluster-access-kubeconfig/)).
  *Align:* local mode passes exec-plugins through; never persists cloud tokens (E11 F11.6, E3).
- **SPIFFE/SPIFFE-ID + mTLS** — support the IDs, don't force SPIRE (per SCOPE non-goals). Reused
  from [`identity-connections-security.md`](identity-connections-security.md).

### 5.5 Supply chain / release integrity
- **SLSA** (build provenance levels) + **Sigstore/cosign** (keyless signing via Fulcio + Rekor
  transparency log) + **SBOM** (Syft → CycloneDX/SPDX), tied together by **in-toto (ITE-6)**
  attestations. ([SLSA](https://slsa.dev/) · [Sigstore/cosign](https://docs.sigstore.dev/) ·
  practitioner chain: [oneuptime](https://oneuptime.com/blog/post/2026-01-25-sigstore-supply-chain-security/view)).
  *Align:* cosign-signed releases + SLSA L2 + SBOM **from the first tag** (E9), matching the
  supply-chain checklist evaluators actually check
  ([`identity-connections-security.md`](identity-connections-security.md) §6).

### 5.6 API design / normalization
- **Kubernetes API conventions** — for the normalized fleet-fact model's field naming, list/status
  semantics, and label/selector conventions.
  ([api-conventions.md](https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md)).
  *Align:* the fleet model should read as idiomatic Kubernetes, not a bespoke schema.

---

## 6. Differentiation, positioning, risks

### 6.1 What makes Sith a *default* tool
1. **One binary, ten-minute wow, no account** — beats the "helm-install-a-platform" cliff that
   killed the predecessor and gates Backstage/Rancher.
2. **Cross-cluster-by-default** — the one thing every OSS local client refuses to be, and the one
   thing Aptakube charges for.
3. **Deterministic Investigation Brain** — root-cause without an LLM, offline, explainable; a
   category the AI-SRE wave structurally cannot occupy while it depends on model calls + telemetry
   egress.
4. **Governed typed action + governed MCP** — the "safely act" half that every advise-tool leaves
   empty, and the only credible way to let Claude Code / Codex / kagent act on a fleet without a
   cluster-admin token or a shell.

### 6.2 The strongest single wedge
**"k9s for your whole fleet, that can safely do something about what it finds."** Lead with the
free cross-cluster read (adoption), differentiate with the deterministic brain (retention), monetize
/ moat with governed action federation + governed agent access (durability). The AI-SRE flood of
2026 *strengthens* this: it validates demand for "understand my cluster" while proving the market
still has no safe, neutral, typed action layer.

### 6.3 Risks & objections (adversarial)
- **"Headlamp is CNCF and free — why not just use it?"** Because it's per-cluster; Sith's answer is
  the fleet axis + brain + governed action, not feature-parity. *Risk:* if Headlamp ships fleet
  aggregation first, the read wedge erodes — so the brain + action moat must not lag the read view.
- **"AI-SRE tools will add action soon."** They're gated *by design* (autonomy on prod is the hard
  part, and they're closed SaaS that can't enter air-gap/China). Sith's neutral, deterministic,
  PR-first model is the safer path — and Sith can be the *governed executor* they call.
- **"Rule-based RCA is less capable than an LLM."** True for open-ended reasoning; **false** for the
  six highest-frequency, well-understood failure modes (OOMKill/CrashLoop/drift/cert/bad-deploy/node
  pressure), where determinism + offline + explainability are *features*. Position as complementary,
  not competitive, with the LLM tools; let an LLM call Sith, not replace it.
- **"Is the read wedge big enough to build a company on?"** No — it's the on-ramp, explicitly. The
  business is the moat. Stated honestly so it isn't oversold.
- **MCP is a fast-moving RC** — building to the 2026-07-28 RC risks churn. *Mitigation:* target the
  authorization primitives (OAuth 2.1 audience binding, enforce-at-execution) that are stable
  intent even as surface shifts.

### 6.4 What NOT to build (reaffirmed anti-scope)
No portal/IDP/catalog/scorecards/DORA; no GitOps reconciler; no multi-cluster scheduler; no
telemetry/metrics lake; no metering/optimization engine; no agent-orchestration framework; no
free-form `exec`/`apply`/Secret/RBAC mutation (permanently inexpressible); no re-skinning of tools
that are already better; **and specifically new to this pass:** no LLM-in-the-critical-path for
root-cause (the brain is deterministic; an LLM is an optional *client*, never the engine), and no
"act from chat" free-`kubectl` surface (the Botkube anti-pattern).

---

## 7. Recommended epic themes → candidate epics / features

Mapping findings to the existing epic set (issues **#18–#39**). Each theme cites the evidence that
motivates it.

| # | Theme (finding → action) | Maps to | Change vs current plan |
|---|---|---|---|
| T1 | **Aggregated cross-cluster read is the whole on-ramp** — lead the local client with fleet aggregation + correlation, not a per-cluster console (k9s decline ×3+, Aptakube-charges-for-it, Headlamp-per-cluster) | **E2** (#20), **E11** (#29), F2.3/F2.4, F11.1/F11.4 | Reinforces; add "aggregation-first UX" as an E11 acceptance criterion |
| T2 | **Promote the Investigation Brain to a first-class epic with a determinism contract** — the AI-SRE wave is all LLM/advise; deterministic+offline RCA is unoccupied in OSS and credible to air-gap/China | **E14** (proposed, PR #43) → **new issue**; depends on E2 four-lens graph | **Net-new epic issue** — currently only a spec, no #; positioning contract "deterministic, no-LLM-in-path, explainable" |
| T3 | **Governed MCP must adopt MCP-2026 auth + enforce-at-execution** — CVE-2026-46519 is the exact bug class (controls at discovery, not execution); MCP RC mandates RFC 8707 audience binding | **E7** (#25, #37), links E4 F4.5 | Add auth-hardening + execution-time-revalidation guardrails to E7 acceptance criteria |
| T4 | **"AI-SRE tools are clients of Sith, not competitors"** — they advise then stop; Sith is the governed executor they call (HolmesGPT/Cleric/kagent read-only/gated) | **E7** (#25), **E4** (#22) | Add explicit "external agent as governed MCP client" narrative + a `plan→execute` handoff verb path |
| T5 | **Four-lens graph normalizes on OTel semconv join keys** — pin `k8s.*.uid`/`k8s.cluster.uid` as the correlation keys | **E2** (#20), F2.6/F2.7 (PR #43) | Cite exact OTel attribute names in the F2.x contracts |
| T6 | **Config-drift rule (R4) can borrow Kubevious's app-centric validation model**; drift is also the `diff`/`plan` connector verbs | **E14** R4, **E12** (#30) connector `diff`/`plan` | Reference Kubevious rule design; keep drift read+PR, never auto-apply |
| T7 | **Supply-chain integrity from the first tag** — SLSA L2 + cosign + SBOM is table-stakes for evaluators | **E9** (#27), E3 (#21) | Already planned; elevate to a day-one release-gate, not a later hardening |
| T8 | **Cost is a fast-follow read-overlay, not a wedge** — OSS fleet-rollup gap confirmed, but crowded/commoditized; sequence after the moat proves out | **E13** (#31) | Unchanged; keep as fast-follow |
| T9 | **Local exec-plugin passthrough + no-token-persistence** — align to client-go ExecCredential v1; never store cloud tokens | **E11** (#29, #36), **E3** (#21) | Reinforces F11.6; name the standard |
| T10 | **Two-layer cloud abstraction (pure-K8s-API + thin enum/mint adapters)** — conformance is uniform incl. China; only auth/enumeration differs | **E12** (#30), multi-cloud fast-follow | Unchanged from prior research; carried for epic completeness |

**The one structural recommendation:** file **E14 (Investigation Brain)** as a numbered epic issue
(it is currently only a spec in PR #43), with the **deterministic-vs-LLM positioning contract** and
R1–R6 as its features — because the 2026 AI-SRE landscape is the single biggest change since the
prior research pass, and it makes the brain both more *necessary* (to differentiate) and more
*defensible* (determinism is the axis the LLM tools cannot take).

---

## 8. Adversarial verification of key claims

| # | Claim | Verdict | Evidence / caveat |
|---|---|---|---|
| 1 | k9s has repeatedly declined an aggregated multi-cluster view | **CONFIRMED** | [#3374](https://github.com/derailed/k9s/issues/3374) fetched: opened 2025-06-02, **closed not-planned/stale**. Prior research verified #1006/#2730 likewise. *Caveat:* no maintainer prose quote was visible on #3374; the "not planned" close is the signal |
| 2 | Every 2026 AI-SRE tool is investigate/advise, read-only or action-gated | **CONFIRMED for the sampled set** | HolmesGPT "read-only… doesn't execute fixes" ([CNCF](https://www.cncf.io/blog/2026/01/07/holmesgpt-agentic-troubleshooting-built-for-the-cloud-native-era/)); Cleric "gated in ability to make production changes" (own site); Komodor remediation "sandboxed + audited" but closed SaaS; k8sgpt advise-only. *Caveat:* Botkube **does** execute `kubectl` from chat — the exception, and the anti-pattern Sith rejects |
| 3 | CVE-2026-46519 shows enforcement-at-discovery-not-execution, CVSS 8.8 | **CONFIRMED** | [NeuralTrust](https://neuraltrust.ai/blog/kubernetes-mcp): controls at `tools/list` not `tools/call`; fixed in 3.6.0. *Precision:* affects the **npm `mcp-server-kubernetes`** (Flux159), **not** the Go `containers/kubernetes-mcp-server`. Attributed correctly above |
| 4 | Aptakube is the *only* tool doing aggregated multi-cluster (OSS slot empty) | **CONFIRMED as Aptakube's own claim; "only" is marketing** | Aptakube markets "the only Kubernetes UI that can connect to multiple clusters simultaneously." Clusterpedia/Komodor/Cleric also aggregate — but as server/SaaS, not a local no-account client. The **OSS-local-no-account** slot is empty; that is the precise, defensible statement |
| 5 | MCP 2026-07-28 spec mandates audience-bound tokens | **CONFIRMED, with status caveat** | [MCP blog](https://blog.modelcontextprotocol.io/posts/2026-07-28-release-candidate/): clients required to implement RFC 8707 Resource Indicators. *Caveat:* it is a **Release Candidate**, not final — build to the stable primitives |
| 6 | OCM lacks approvals/typed-verbs/audit-ledger/abstention | **CONFIRMED** | Consistent with prior hand-verified research; ManifestWork = arbitrary YAML, rollout strategies exist but no governance gates. OCM version (v1.2.0 vs v1.3.1) discrepancy flagged in §0 — not load-bearing |

---

## 9. Bibliography (cited sources)

**Local clients & form factor**
- k9s repo & multi-cluster issues — https://github.com/derailed/k9s · [#3374](https://github.com/derailed/k9s/issues/3374) · [#1430](https://github.com/derailed/k9s/issues/1430) · [#1006](https://github.com/derailed/k9s/issues/1006)
- Headlamp — https://github.com/kubernetes-sigs/headlamp · Dashboard→Headlamp: https://kubernetes.io/blog/2026/06/01/dashboard-to-headlamp/
- Lens pricing — https://lenshq.io/pricing · OpenLens — https://github.com/MuhammedKalkan/OpenLens · Freelens — https://github.com/freelensapp/freelens
- Aptakube — https://aptakube.com/ · Kubevious — https://github.com/kubevious/kubevious

**AI-SRE / auto-triage**
- k8sgpt — https://k8sgpt.ai/ · CNCF: https://www.cncf.io/projects/k8sgpt/
- HolmesGPT — https://github.com/HolmesGPT/holmesgpt · CNCF blog: https://www.cncf.io/blog/2026/01/07/holmesgpt-agentic-troubleshooting-built-for-the-cloud-native-era/ · auto-diagnosing alerts: https://www.cncf.io/blog/2026/04/21/auto-diagnosing-kubernetes-alerts-with-holmesgpt-and-cncf-tools/
- Robusta — https://docs.robusta.dev/ · Botkube — https://botkube.io/ · Komodor — https://komodor.com/ (multi-agent: https://www.globenewswire.com/news-release/2026/03/18/3258257/0/en/komodor-introduces-extensible-autonomous-multi-agent-architecture-for-ai-driven-site-reliability-engineering.html) · Cleric — https://cleric.ai/

**Fleet / platform incumbents**
- OCM — https://open-cluster-management.io/docs/ · CNCF: https://www.cncf.io/projects/open-cluster-management/ · TOC incubation #1884: https://github.com/cncf/toc/issues/1884 · cluster-proxy Service Proxy: https://open-cluster-management.io/blog/2025/cluster-proxy-now-supports-service-proxy-an-easy-way-to-access-services-in-managed-clusters/
- Rancher agentic — https://www.suse.com/c/kubecon-eu-2026-first-agentic-ecosystem-platform/
- Devtron — https://devtron.ai/ · Portainer — https://www.portainer.io/ · Argo CD agent — https://github.com/argoproj-labs/argocd-agent · Kargo — https://github.com/akuity/kargo

**IDP**
- Backstage Kubernetes — https://backstage.io/docs/features/kubernetes/ · Komodor+Backstage gap: https://komodor.com/blog/komodor-backstage-kubernetes-visibility-into-open-source-idp/

**Standards**
- MCP 2026-07-28 RC — https://blog.modelcontextprotocol.io/posts/2026-07-28-release-candidate/ · authorization — https://modelcontextprotocol.io/specification/draft/basic/authorization · NSA MCP CSI — https://www.nsa.gov/Portals/75/documents/Cybersecurity/CSI_MCP_SECURITY.pdf · CVE-2026-46519 — https://neuraltrust.ai/blog/kubernetes-mcp
- OTel k8s semconv — https://opentelemetry.io/docs/specs/semconv/registry/attributes/k8s/ · https://github.com/open-telemetry/semantic-conventions/blob/main/docs/resource/k8s.md
- client-go ExecCredential — https://kubernetes.io/docs/reference/config-api/client-authentication.v1/ · kubeconfig — https://kubernetes.io/docs/concepts/configuration/organize-cluster-access-kubeconfig/
- SLSA — https://slsa.dev/ · Sigstore/cosign — https://docs.sigstore.dev/ · supply-chain chain — https://oneuptime.com/blog/post/2026-01-25-sigstore-supply-chain-security/view
- Kubernetes API conventions — https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md
- CNCF landscape — https://landscape.cncf.io/guide · OpenCost fleet-rollup gap — https://github.com/opencost/opencost/issues/2638

**Prior repo research this builds on**
- [`market-and-form-factor.md`](market-and-form-factor.md) · [`USE-CASE-AND-SHAPE.md`](USE-CASE-AND-SHAPE.md) · [`integrations-and-ai-governance.md`](integrations-and-ai-governance.md) · [`identity-connections-security.md`](identity-connections-security.md)
</content>
</invoke>
