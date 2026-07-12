# Sith — Market & Landscape Research (July 2026)

**Status:** research · **Date:** 2026-07-10 · **Author identity:** ArdurAI

This is a current (mid-2026) competitive and standards scan for Sith, written to feed epic
creation. It builds on — does not duplicate — the prior research
([`USE-CASE-AND-SHAPE.md`](USE-CASE-AND-SHAPE.md),
[`market-and-form-factor.md`](market-and-form-factor.md),
[`integrations-and-ai-governance.md`](integrations-and-ai-governance.md)) and the just-landed
specs (PRs #40–#43: the Slice-0 foundation, the F2.1 7-verb source-adapter contract, the E2
four-lens read-federation + Investigation Brain, and the F11 local fleet UX). Where those already
established a fact with a primary source, this doc cites forward rather than re-deriving.

**What Sith is (the thing being positioned).** A single Go binary, local-first: `sith` is a
k9s/Lens-class **multi-cluster** Kubernetes fleet client (kubeconfig-based, cache-first, offline,
no account) that grows into a local web "fleet IDE" (`sith ui`) and an optional governed hub
(`sith hub`). Its design bets, from the specs, are four: (1) **cross-cluster-by-default** fleet
views; (2) a **source-abstract 7-verb connector contract** (`discover · read · query · diff ·
plan · execute · verify`); (3) a **four-lens operational graph** (LIVE / DESIRED / TIMELINE /
TELEMETRY) correlated by OpenTelemetry-semconv identity keys; and (4) a **rule-based Investigation
Brain** that turns a symptom into ranked, evidence-cited root-cause hypotheses and a *proposed*
plan — deterministic, transparent, and honest about coverage (it abstains rather than guesses).

**Verification note.** Every external claim carries a URL. Claims drawn from a web-search summary
rather than a page I fetched line-by-line are marked *(search-surfaced)* and should be spot-checked
before they become load-bearing in a pitch; GitHub issue numbers and canonical standards URLs are
stable and treated as verified. Nothing here is invented; unverifiable items are flagged.

---

## 1. Competitive landscape

The field splits into four bands. Sith deliberately touches all four but *is* none of them: it is
a local-first multi-cluster client (band A form factor) with an investigation brain and governed
action (bands C/D value) minus the SaaS/account/LLM-autonomy baggage.

### Band A — Local Kubernetes clients (the form factor Sith adopts)

| Tool | What it does | Gaps vs Sith's thesis | Real-user signal |
|---|---|---|---|
| **k9s** ([repo](https://github.com/derailed/k9s)) | Terminal UI, single Go binary, reads your kubeconfig, ~90% of daily kubectl. The de-facto individually-adopted client. | **One context at a time.** No merged cross-cluster view; no desired/telemetry lenses; no RCA. | Multi-cluster is the single most-requested missing feature, repeatedly, and unbuilt: [#1006 "manage resources of multiple clusters simultaneously"](https://github.com/derailed/k9s/issues/1006), [#2730 "combine resources from multiple clusters in each view"](https://github.com/derailed/k9s/issues/2730), [#3374 "multi-cluster mode?"](https://github.com/derailed/k9s/issues/3374), [#1430 "multiple tabs"](https://github.com/derailed/k9s/issues/1430). |
| **Lens / OpenLens / FreeLens** | Electron "Kubernetes IDE." Rich single-cluster GUI. | Mandatory **account/login**, telemetry-by-default, and (in the OSS build) removal of logs/shell drove an exodus; Electron weight; still per-cluster-centric. | The trust break is the textbook cautionary tale (prior research §Lens): community forked to OpenLens then MIT-licensed **FreeLens** and did not return to the commercial product. Sith's "no account, no telemetry, permissive license" is a direct answer. |
| **Headlamp** ([kubernetes-sigs](https://headlamp.dev/)) | CNCF/SIG-UI web + desktop app, plugin system, dual-mode (desktop + in-cluster). Named the Kubernetes-Dashboard successor. | Per-cluster-centric UX; multi-cluster registration exists but the center of gravity is one cluster; no cross-cluster correlation brain; plugin-authoring tax (React). | CNCF-blessed and improving monthly — the strongest *incumbent* in the exact "dual-mode client" niche. Sith must out-*fleet* it, not out-console it. |
| **kubevious** ([repo](https://github.com/kubevious/kubevious), [cli](https://github.com/kubevious/cli)) | App-centric **config validation & assurance**: CLI + Guard (cross-manifest policy enforcement) + Dashboard. Apache-2.0. | Validation/guardrails, not fleet ops; single-app/cluster framing; no timeline/telemetry lens; no cross-cluster RCA. | Closest tool to the *config-drift* rule (R4), but as a *linter/policy* engine, not a live four-lens investigator. Complementary, not competitive. |
| **Aptakube** (closed) | The one client that already aggregates **multiple clusters simultaneously** "as if one big cluster," kubeconfig-based, nothing on the cluster. | Closed-source, paid, GUI-only, no governance, no RCA. | Proves the exact aggregated-multi-cluster demand — and that the **OSS slot is empty** (prior research §form-factor). |

### Band B — Cluster platforms / management planes

| Tool | What it does | Gaps vs Sith | Real-user signal |
|---|---|---|---|
| **Rancher (SUSE)** | Cluster provisioning + import (outbound `cattle-cluster-agent`), RBAC, Fleet GitOps; 2026 "Liz" agentic crew + MCP. | Heavyweight platform; provisioning-centric; agentic assist is subscription-gated, not a neutral primitive; a repricing pushed alternatives-shopping (prior research). | Validates outbound-agent + multi-cluster + AI-on-top, but as a vendor platform, not a local-first client. |
| **Portainer** ([site](https://www.portainer.io/)) | Docker+K8s management GUI, multi-environment. | **Docker-era abstractions flatten K8s** (CRDs/operators/RBAC "feel hidden"); multi-cluster is "operational, not architectural"; **no native drift detection/reconciliation**; requires a dedicated `portainer` namespace (breaks multi-tenancy); `hostPath` default loses config on reschedule; **UI performance degradation unresolved as of Feb 2026**. | Portainer's own alternatives content and 2026 write-ups catalogue these gaps ([Portainer blog](https://www.portainer.io/blog/kubernetes-dashboard); [Dokploy 2026](https://dokploy.com/blog/portainer-alternatives)). Its drift/GitOps gap is exactly Sith's DESIRED lens + R4. |
| **Devtron** ([site](https://devtron.ai/)) | OSS batteries-included K8s platform (CI/CD + GitOps + obs + security). | Breadth/platform strategy — the opposite of Sith's narrow local-first wedge; heavy install. | A "do everything" platform; validates demand for unified K8s ops but not the local-first form factor. |
| **ArgoCD UI** ([argo-cd](https://argo-cd.readthedocs.io/)) | The GitOps CD console; the DESIRED-lens exemplar and a Sith connector (not a competitor). | As a **fleet viewer** it is weak: "you completely lose access to the Argo UI as a single dashboard for your application estate" across clusters; push-model multi-cluster needs credentials + inbound network to every cluster (a honeypot); pull-model means operating *N* Argo instances; **adding clusters isn't supported in the UI (CLI only)**. | [Plural: "Where ArgoCD falls short"](https://www.plural.sh/blog/where-argocd-falls-short/). This is the cross-cluster-visibility gap Sith's read federation fills, and the credential-centralization anti-pattern Sith's kubeconfig-local / OCM-outbound design avoids. |

### Band C — SaaS ops & AI-SRE / auto-triage (the value band — and the sharpest contrast)

This is where the "brain" competition lives, and where Sith's *deterministic, transparent,
abstaining, local, cross-cluster* stance is most differentiated.

| Tool | What it does | Approach | Gaps vs Sith's brain |
|---|---|---|---|
| **k8sgpt** ([repo](https://github.com/k8sgpt-ai/k8sgpt)) | CNCF Sandbox, Apache-2.0. Deterministic K8s **analyzers** turn errors into human-readable insights; an **LLM only *explains*** findings. `anonymize` flag masks names before sending to the LLM. *(search-surfaced)* | Rule-scan + LLM-explain. | Single-cluster, alert/scan-scoped; the LLM step still ships data to an external model by default ("exposing private company data to OpenAI"); no cross-cluster correlation; no governed action. The **analyzer** half is philosophically close to Sith's rule brain — Sith extends it to four lenses, cross-cluster, and no-LLM-required. |
| **HolmesGPT** ([holmesgpt.dev](https://holmesgpt.dev/)) | CNCF Sandbox, Apache-2.0, co-maintained by **Robusta + Microsoft**. Agentic **ReAct loop over 30+ observability toolsets**, read-only by design; ~40% of investigations resolve on known patterns (OOMKilled, ImagePullBackOff). *(search-surfaced)* | LLM agent iterating over tools. | The strongest K8s investigation agent — but **LLM-based and non-deterministic**; "read-only by design" is a good stance Sith shares, yet the reasoning is a black box vs Sith's cited rules. Its own docs note the honest-limits problem the whole category faces. |
| **Robusta** ([home](https://home.robusta.dev/)) | CNCF Sandbox, ~2,500★. Intercepts Prometheus alerts, attaches logs/status/metrics, AI RCA, routes to Slack/Teams/PagerDuty. *(search-surfaced)* | Alert-enrichment + AI RCA (SaaS-leaning). | Alert-triggered and notification-centric; not a fleet client; not local-first. |
| **Botkube** ([botkube.io](https://botkube.io/)) | ChatOps: kubectl from Slack/Teams, AI troubleshooting, event alerts, automated workflows. *(search-surfaced)* | Chat-driven ops + AI. | Chat surface, not a fleet view; **kubectl-from-chat is exactly the exec-from-a-bot risk** Sith's typed-intent model rejects. |
| **Cleric** ([site](https://cleric.io/)) | **Autonomous** AI SRE agent: auto service-mapping, parallel hypothesis testing with confidence tracking, continuous learning; root-causes without runbooks. *(search-surfaced)* | Autonomy-first LLM agent (commercial). | The autonomy-first end of the market — the opposite of "proposes, never executes." A live argument for Sith's boundaries-not-instructions stance. |
| **Komodor** | SaaS K8s ops + multi-agent AI-SRE ("Klaudia"), agent per cluster, MCP-extensible, sandboxed remediation. | Closed SaaS (China/air-gap excluded), diagnosis-first, per-cluster agent; no neutral OSS governed-action primitive (prior research §landscape). | The closest *product* to "operate the fleet with AI," but proprietary and not local-first. |
| **Cast AI** | GPU/cost automation that *mutates* clusters (rightsizing/spot); quote-priced; EKS/GKE/AKS/OpenShift only, no China clouds / air-gap (prior research). | Cost-optimization automation. | Different category (mutation/cost); relevant only to Sith's read-only cost overlay (E13) as a boundary. |

**The category-defining tension in Band C:** the "**hallucination gap**" — an LLM doing root-cause
without live telemetry, topology, and recent state transitions "bridges the gap using probability,
which is why answers often sound confident even when they are wrong"
([Sherlocks.ai](https://www.sherlocks.ai/blog/the-hallucination-gap-why-general-llms-fail);
*search-surfaced*). Independent academic work is now building *falsification* methodology for
agentic Kubernetes operations precisely because reported "accuracy" is observational
([arXiv 2605.23058](https://arxiv.org/pdf/2605.23058); *search-surfaced*). Sith's brain is the
structural answer: it reasons over the **live four-lens graph**, cites the exact signals per
hypothesis, and **abstains when a required lens is missing** — it cannot hallucinate a cause it
has no evidence for, and it never sends cluster data to a third-party model.

### Band D — IDPs / catalogs (adjacent, not competitors)

Backstage / Port / Cortex / OpsLevel are developer portals — the category that lost the "single
pane of glass" bet and that the predecessor `devops-portal` died on (prior research §review-10).
Self-hosted Backstage runs to multiple FTEs and 6–12 months. Sith is **not** a portal; a portal
can *consume* Sith's API/MCP. Included only to hold the category line.

### Comparison matrix

Legend: ✅ yes · ◑ partial/limited · ❌ no · — n/a. "Local-first" = runs from your kubeconfig with
no server/account. "X-cluster" = merged cross-cluster views by default. "4-lens" = correlates
live+desired+timeline+telemetry. "RCA" = root-cause reasoning. "Transparent" = non-LLM /
explainable-by-construction. "Gov. action" = governed typed-intent writes. "Offline/no-acct" = no
telemetry, no login required.

| Tool | Local-first | X-cluster | 4-lens | RCA | Transparent | Gov. action | OSS | Offline/no-acct |
|---|---|---|---|---|---|---|---|---|
| **k9s** | ✅ | ❌ (asked ×4) | ❌ (live only) | ❌ | ✅ | ❌ | ✅ | ✅ |
| **Lens/FreeLens** | ✅ (FreeLens) | ◑ (per-cluster) | ❌ | ❌ | ✅ | ❌ | ◑ | ◑ (Lens acct) |
| **Headlamp** | ✅ | ◑ | ❌ | ❌ | ✅ | ❌ | ✅ | ✅ |
| **kubevious** | ◑ | ❌ | ◑ (desired/validate) | ◑ (config) | ✅ | ❌ | ✅ | ◑ |
| **Aptakube** | ✅ | ✅ | ❌ | ❌ | ✅ | ❌ | ❌ | ✅ |
| **Rancher** | ❌ | ✅ | ◑ | ◑ (Liz, LLM) | ❌ | ◑ (platform) | ◑ | ❌ |
| **Portainer** | ❌ | ◑ | ❌ (no drift) | ❌ | ✅ | ❌ | ◑ | ❌ |
| **ArgoCD UI** | ❌ | ❌ ("lose the dashboard") | ◑ (desired) | ❌ | ✅ | ◑ (sync) | ✅ | ❌ |
| **k8sgpt** | ◑ (CLI) | ❌ | ◑ (live+scan) | ◑ (analyzer+LLM) | ◑ (rules + LLM) | ❌ | ✅ | ◑ (LLM egress) |
| **HolmesGPT** | ◑ | ◑ | ◑ (telemetry-heavy) | ✅ (LLM) | ❌ (LLM) | ❌ (read-only) | ✅ | ❌ (LLM) |
| **Robusta** | ❌ | ◑ | ◑ | ✅ (LLM) | ❌ | ◑ | ◑ | ❌ |
| **Cleric** | ❌ | ◑ | ◑ | ✅ (autonomous) | ❌ | ◑ (autonomy) | ❌ | ❌ |
| **Komodor** | ❌ | ✅ | ◑ | ✅ (LLM) | ❌ | ◑ (SaaS) | ❌ | ❌ |
| **Sith (proposed)** | ✅ | ✅ | ✅ | ✅ (rule brain) | ✅ (cited rules) | ✅ (typed-intent, hub) | ✅ | ✅ |

The bottom row is the thesis: **no incumbent occupies the full row.** k9s/Headlamp/Aptakube own the
local client but stop at LIVE-on-one-cluster; the AI-SRE band owns RCA but is LLM-based, SaaS-y, and
alert-scoped; Komodor does the whole thing but proprietary. Sith's defensibility is the
*combination*, in one local-first OSS binary.

---

## 2. Category framing — is "local-first, cross-cluster-by-default, no-account" a real wedge?

**Yes, with an honest caveat.** The evidence:

- **The demand is articulated inside the incumbent's own tracker, repeatedly and unbuilt.** k9s has
  *four* distinct open multi-cluster requests (#1006, #2730, #3374, #1430). That is a durable,
  multi-year signal that the single-context ceiling is felt — and that the maintainer won't cross
  it. *Honest caveat (from prior research):* the per-issue reaction counts are modest (single/low
  double digits), so this is a **convenience/efficiency gap, not a screaming emergency** — the read
  wedge is a beachhead, not by itself a moat.
- **The aggregated-multi-cluster experience already sells — but only closed.** Aptakube ships exactly
  "many clusters as one," kubeconfig-based, and charges for it; the **OSS slot is empty**. ArgoCD
  users hit the same wall from the GitOps side ("you lose the single dashboard").
- **Local-first + no-account + permissive-license is a proven trust wedge.** The Lens→FreeLens
  migration shows the audience punishes account walls and telemetry and rewards permissive OSS; k9s
  shows install-and-run beats install-a-platform. The CNCF adoption barriers are complexity and
  abandonment-fear, not missing features (prior research §CNCF-2024).
- **The privacy/offline angle is newly sharp in 2026 because of the AI-SRE band.** k8sgpt's default
  data egress to OpenAI and the "hallucination gap" mean a **local, no-egress, deterministic**
  investigator is not just a nicety — it is the enterprise-safe alternative to the whole LLM-SRE
  category. "Cross-cluster-by-default" + "your data never leaves your laptop" + "it tells you *why*
  and shows its work" is a positioning none of Band C can claim.

**Verdict:** the wedge is real but must be sequenced correctly — lead with the *read* client
("k9s for your whole fleet"), and let the *brain* ("…that also tells you why payments is down,
without sending your logs to anyone") be the reason it becomes a default rather than a curiosity.
The brain is the differentiator; the local multi-cluster client is the on-ramp.

---

## 3. Standards & guidelines to align with

Aligning to these is both correctness and credibility — it is how a solo/small OSS project earns
trust and avoids reinventing security-sensitive wheels.

| Standard / guideline | Why it matters to Sith | Authoritative source | 2026 status note |
|---|---|---|---|
| **OpenTelemetry Kubernetes semantic conventions** | Sith's `EntityRef` correlation keys (`k8s.cluster.name`, `k8s.namespace.name`, stable `container.image.repo_digests`, `service.name`, …) are the join across the four lenses. Standardizing on semconv means connectors interoperate and the graph joins without heuristics. | [semconv/resource/k8s](https://opentelemetry.io/docs/specs/semconv/resource/k8s/); [k8s attribute registry](https://opentelemetry.io/docs/specs/semconv/registry/attributes/k8s/) | ⚠️ **Flag:** the k8s attributes were **promoted to release-candidate in 2026, not yet stable** ([OTel blog](https://opentelemetry.io/blog/2026/k8s-semconv-rc/)); migration is behind Collector feature gates. Sith should pin to the RC schema and track the stable promotion. |
| **Kubernetes API conventions** | The normalized fleet model and verb semantics should mirror K8s object/status/condition conventions so Sith feels native and stays forward-compatible. | [sig-architecture/api-conventions](https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md) | Stable, canonical. |
| **client-go exec-credential-plugin auth** | Local mode must honor exec plugins (`aws eks get-token`, `kubelogin`, `gke-gcloud-auth-plugin`) exactly as kubectl does; a cloud kubeconfig is *not* a self-contained credential. | [K8s auth reference](https://kubernetes.io/docs/reference/access-authn-authz/authentication/) | Stable (verified in prior research §identity). |
| **Open Cluster Management (OCM)** | The hub's outbound-only transport (`cluster-proxy` + `managed-serviceaccount`) — adopt, don't build (ADR-0001, M0 PASSED). | [open-cluster-management.io](https://open-cluster-management.io/); [cluster-proxy](https://github.com/open-cluster-management-io/cluster-proxy) | Stable; CNCF sandbox. |
| **Model Context Protocol (MCP)** | The governed agent surface (`sith serve --mcp`); annotations are hints, enforcement is server-side. Now vendor-neutral. | [modelcontextprotocol.io](https://modelcontextprotocol.io/); [LF donation, 2025-12-09](https://blog.modelcontextprotocol.io/posts/2025-12-09-mcp-joins-agentic-ai-foundation/) | MCP donated to the Linux Foundation's Agentic AI Foundation — vendor-neutral (verified in prior research §integrations). |
| **SLSA + Sigstore/cosign + SBOM** | Release integrity: cosign-sign, SLSA L2 provenance, SPDX/CycloneDX SBOM from the first tag. Scorecards weigh these before humans read code. | [slsa.dev](https://slsa.dev/); [docs.sigstore.dev](https://docs.sigstore.dev/) | SLSA L2 is now "an afternoon" via GitHub attestation + `slsa-github-generator`; Sigstore keyless (Fulcio OIDC + Rekor) removes long-lived keys ([2026 supply-chain guides](https://aquilax.ai/blog/supply-chain-artifact-signing-slsa); *search-surfaced*). |
| **CNCF project norms** | Permissive license (Apache-2.0 ✅), OpenSSF Scorecard, third-party audit expectations for security-adjacent tooling. | [cncf.io](https://www.cncf.io/); [OpenSSF Scorecard](https://github.com/ossf/scorecard) | Signing + CI hygiene outrank SBOM in Scorecard weighting (prior research §identity §6). |

---

## 4. Differentiation & positioning

### What makes Sith a *default* tool

1. **Cross-cluster-by-default in a local client.** The one thing k9s/Headlamp won't do and only a
   closed tool (Aptakube) ships. This is the install-me on-ramp.
2. **A brain that reasons over live evidence and shows its work — no LLM required, no data egress.**
   The structural answer to the hallucination gap and to k8sgpt's OpenAI-by-default egress. "It
   tells you *why* `payments` is down across the fleet, cites the exact signals, and abstains when
   it can't confirm" is a claim no Band-C tool can make honestly.
3. **One engine, two modes; one brain, two modes.** The local advisory brain and the hub
   governed-plan brain are the same rules — adoption and governance share code, so nothing is
   thrown away when a team upgrades from laptop to hub.
4. **Boundaries, not instructions.** Typed-intent closed vocabulary, `plan`-never-`execute` at the
   brain, no shell — the direct lesson from the Replit deletion and from Botkube-style
   kubectl-from-chat.

### The strongest wedge (and the sequence)

**Lead:** "**k9s for your whole fleet**" — the local, cross-cluster, no-account read client
(Phase L / E11). **Land the differentiator right behind it:** the **local advisory Investigation
Brain** over the reachable lenses ("…that also tells you why, offline"). **Expand:** the hub turns
the same brain's plan into a governed typed intent. Positioning line: *the local-first fleet client
that tells you why — and, when you're ready, safely acts.*

### Risks & objections (and the answer)

- **"k9s/Headlamp already do this / will add multi-cluster."** They've had years; the maintainer
  closed the k9s requests as not-planned; the differentiator is the *brain + governance*, not the
  merged table alone. Answer: ship the brain, which they are not positioned to build.
- **"Rule-based can't compete with LLM breadth."** True for open-ended Q&A; false for the *six
  failures that cause most incidents*. Answer: deterministic + cited + abstaining beats
  confident-and-sometimes-wrong for operational trust; the arxiv falsification work backs this.
- **"Another dashboard."** Answer: cross-cluster + four-lens + RCA + governed action in one OSS
  binary is not another dashboard; it is the row no one occupies (matrix §1).
- **"Solo-project trust discount."** Answer: permissive license (already Apache-2.0), no account,
  no telemetry, cosign/SLSA/SBOM day one, and a CNCF-aligned standards posture (§3).
- **Telemetry-lake temptation.** The biggest *self-inflicted* risk. Answer: TELEMETRY is
  **query-through** (derived answers only, no series retained) — the single decision that adds the
  telemetry lens without becoming the store SCOPE forbids.

### What NOT to build (anti-drift, confirmed by the landscape)

- **Not another single-cluster console** — Headlamp/k9s own that; build the *fleet* view.
- **Not an LLM-autonomy SRE** — Cleric/Komodor own autonomy-first; Sith is deterministic + proposes.
- **Not a telemetry lake / metrics store** — read Prom/Loki/ES query-through; never retain series.
- **Not a GitOps reconciler / a portal / a scheduler / a cost-optimization mutator** — Argo, Flux,
  Backstage, Karmada, Cast AI own those; Sith reads them and (for cost) overlays read-only.
- **Not kubectl-from-chat / free-form exec** — the Botkube/Replit anti-pattern; typed intents only.
- **Fluentd/Fluent-bit are not data sources** — read the sinks (ES/OpenSearch/Splunk/Loki), treat
  shippers as health-only workloads.
- **Grafana is deep-link only** — brokered read-through, never re-skinned (no iframe-Grafana trap).

---

## 5. Recommended epic themes → candidate epics/features

Mapping findings to the existing backlog (epics #18–#39 from the implementation roadmap #39). Most
findings **reinforce** existing epics; two argue for **new** first-class epics.

| Theme (from findings) | Evidence | Maps to | Recommended action |
|---|---|---|---|
| **Cross-cluster-by-default fleet client** is the on-ramp | k9s #1006/#2730/#3374/#1430; Aptakube-only; ArgoCD "lose the dashboard" | **E11 #29** (local fleet client) + **E2 #20** (F2.6 four-lens graph, F2.7 EntityRef) | Keep E11 the lead; ensure F2.6/F2.7 land the merged cross-cluster view early. |
| **Investigation Brain** is *the* differentiator vs Band C | hallucination gap; k8sgpt LLM-egress; HolmesGPT/Cleric black-box; deterministic+abstaining is the trust answer | **NEW epic E14 — Investigation Brain** (proposed in the E2 spec §3.7) | **Open E14** (label `epic`), depends on E2 (#20), E12 (#30), E4/E5 (#22/#23). Ship a **local advisory subset in Phase L** (rules R1–R6 over reachable lenses); governed-plan mode with the write path. This is the highest-leverage new epic. |
| **Four-lens graph + OTel-semconv keys** is the substrate the brain needs | OTel k8s semconv (RC); ArgoCD/Portainer drift gaps | **E2 #20** → add **F2.6** (four-lens operational graph) + **F2.7** (correlation keys / `EntityRef`) | Add these two features to E2; pin to the OTel RC schema and track stable promotion (flag §3). |
| **7-verb connector contract + integration waves** | Portainer no-drift; connector breadth (Argo/Prom/ES/logs) is what powers the rules | **E12 #30** (connector framework) | Encode the wave matrix (W1 = K8s+GitHub+Argo+Prom+ES+AWS) as E12 features; W1 is exactly the coverage R1–R6 need. Keep three connector kinds (RA/BR/TA); Grafana = brokered deep-link only. |
| **Local, no-egress, deterministic** as the privacy/trust wedge | k8sgpt OpenAI egress; Lens telemetry backlash | **E11 #29 / F11.6 #36** (no-account/no-telemetry/keychain) | Reinforce F11.6 as a *positioning* pillar, not just a feature; make "no data leaves the machine" a tested guarantee. |
| **Governed action, boundaries-not-instructions** | Replit deletion; Botkube kubectl-from-chat; Cleric autonomy | **E4 #22 / E5 #23 / E7 #25** | Unchanged — the landscape *validates* the closed-vocabulary + PEP + MCP-server design. Brain's `plan` hands off to E4/E5; MCP write tools stay elicitation-gated. |
| **Supply-chain integrity as trust signal** | SLSA L2 "an afternoon"; Scorecard weighting; solo-project trust discount | **E9 #27** (packaging) | Confirm cosign-sign + SLSA L2 provenance + SBOM from the first tag; multi-arch + registry-relocatable (China/India). |
| **Cross-cloud enum/cred (incl. China)** | Cast AI's US-only gap; Komodor SaaS excludes air-gap | **E1 #19 / E9 #27 / E12 #30** | Thin per-cloud enum + short-lived token minting (EKS/AKS/GKE + ACK/CCE/TKE); no long-lived keys; air-gap posture. |
| **Cost as read-overlay only** | Cast AI mutates (boundary) | **E13 #31** | Unchanged — OpenCost rollup + GPU columns, read-only; never a metering/optimization engine. |

**The one new epic to open:** **E14 — Investigation Brain** (the E2 spec already proposes it and
scopes it). It is what turns Sith from "the best OSS multi-cluster viewer" into "the tool that
tells you why, without a black box" — the single most defensible thing in the matrix.

---

## 6. Sources & references

Primary sources and standards (canonical/stable unless noted). Items marked *(search-surfaced)*
were summarized from a 2026 web search rather than fetched page-by-page — verify before they become
load-bearing in external material.

**Competitors — clients & platforms**
- k9s repo & multi-cluster issues: <https://github.com/derailed/k9s> · [#1006](https://github.com/derailed/k9s/issues/1006) · [#2730](https://github.com/derailed/k9s/issues/2730) · [#3374](https://github.com/derailed/k9s/issues/3374) · [#1430](https://github.com/derailed/k9s/issues/1430)
- Headlamp: <https://headlamp.dev/>
- kubevious: <https://github.com/kubevious/kubevious> · <https://github.com/kubevious/cli> · <https://kubevious.io/>
- Aptakube (multi-cluster): <https://aptakube.com/multi-cluster>
- Portainer (gaps): <https://www.portainer.io/blog/kubernetes-dashboard> · <https://dokploy.com/blog/portainer-alternatives> *(search-surfaced)*
- Devtron: <https://devtron.ai/>
- ArgoCD UI multi-cluster limits: <https://www.plural.sh/blog/where-argocd-falls-short/> *(search-surfaced)* · <https://argo-cd.readthedocs.io/>

**Competitors — AI-SRE / auto-triage** *(all search-surfaced unless a repo)*
- k8sgpt: <https://github.com/k8sgpt-ai/k8sgpt>
- HolmesGPT: <https://holmesgpt.dev/> · CNCF auto-diagnosing post: <https://www.cncf.io/blog/2026/04/21/auto-diagnosing-kubernetes-alerts-with-holmesgpt-and-cncf-tools/>
- Robusta: <https://home.robusta.dev/>
- Botkube: <https://botkube.io/>
- Cleric: <https://cleric.io/>
- The "hallucination gap": <https://www.sherlocks.ai/blog/the-hallucination-gap-why-general-llms-fail>
- Falsification methodology for agentic K8s ops (arXiv): <https://arxiv.org/pdf/2605.23058>
- Open-source AI-SRE comparison (2026): <https://www.aurorasre.ai/blog/open-source-ai-sre-aurora-vs-holmesgpt-vs-k8sgpt>

**Standards & guidelines**
- OpenTelemetry K8s semconv: <https://opentelemetry.io/docs/specs/semconv/resource/k8s/> · registry <https://opentelemetry.io/docs/specs/semconv/registry/attributes/k8s/> · RC blog <https://opentelemetry.io/blog/2026/k8s-semconv-rc/>
- Kubernetes API conventions: <https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md>
- client-go exec-plugin auth: <https://kubernetes.io/docs/reference/access-authn-authz/authentication/>
- Open Cluster Management: <https://open-cluster-management.io/> · cluster-proxy <https://github.com/open-cluster-management-io/cluster-proxy>
- Model Context Protocol: <https://modelcontextprotocol.io/> · LF donation <https://blog.modelcontextprotocol.io/posts/2025-12-09-mcp-joins-agentic-ai-foundation/>
- SLSA: <https://slsa.dev/> · Sigstore/cosign: <https://docs.sigstore.dev/> · supply-chain 2026 overview <https://aquilax.ai/blog/supply-chain-artifact-signing-slsa> *(search-surfaced)*
- OpenSSF Scorecard: <https://github.com/ossf/scorecard> · CNCF: <https://www.cncf.io/>

**Internal (prior research & specs, this repo)**
- [`USE-CASE-AND-SHAPE.md`](USE-CASE-AND-SHAPE.md) · [`market-and-form-factor.md`](market-and-form-factor.md) · [`integrations-and-ai-governance.md`](integrations-and-ai-governance.md) · [`identity-connections-security.md`](identity-connections-security.md)
- Specs (PRs #40–#43): Slice-0 foundation · F2.1 7-verb source-adapter contract · E2 four-lens read-federation + Investigation Brain · F11 local fleet UX
- Implementation backlog: master roadmap **#39**; epics **#18–#31**; Phase-L children **#32–#38**
