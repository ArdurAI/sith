# Sith — what it should be, and the shape it should take

**Status:** research synthesis (authoritative — reconciles the two research lanes) · **Date:** 2026-07-09

This is the single authoritative answer to: *what should Sith be so DevOps / Platform / SRE /
MLOps engineers reach for it by default, and what form should it take?* It reconciles the two
prior research lanes into one document:

- the **evidence set** (PR #16, `docs/research`): the four-file base — identity/security,
  connectors/AI-governance, pains/China/India, and the tool/cost/multi-cloud landscape;
- the **sharper day-0 framing** (PR #15, `docs/research-usecase`): local-first single-binary
  CLI/TUI as the day-0 wedge, MCP in v1, and honest sizing of the read wedge.

Every load-bearing claim carries a primary-source URL here or in a companion file
([market-and-form-factor.md](market-and-form-factor.md),
[identity-connections-security.md](identity-connections-security.md),
[integrations-and-ai-governance.md](integrations-and-ai-governance.md)). `devops-portal` is
treated as lessons-learned, not a template (reviews at
`/Volumes/EXTENDED/checkpoints/devops-portal/review-01..10.md`).

**Reconcile decision:** this document supersedes both prior `USE-CASE-AND-SHAPE.md` versions.
**Recommend closing PR #15** — its synthesis is fully absorbed here and its sharpest points
(day-0 local-first CLI/TUI, MCP-in-v1, honest wedge-sizing) are adopted. PR #16's other three
evidence files (market, identity, integrations) are unchanged and carried alongside this
reconciled synthesis on the reshape branch.

---

## Executive answer

**What Sith should be.** Sith should be the tool an engineer reaches for the moment they operate
more than one Kubernetes cluster — and it should earn that reach the way k9s did: `brew install`,
one command, no server, no account, no telemetry, nothing to install on the clusters, value
before any configuration. Day 0, `sith` opens a fast **local fleet view over the kubeconfig
contexts the engineer already has** — a terminal UI and CLI (the k9s-grade wow), with an optional
local web "fleet IDE" (`sith ui`) that is Lens-but-better for people who want a visual surface.
Both read the kubeconfigs on the machine directly; credentials never leave the laptop; there is
no hub, no OCM, no agents. That is the shape every individually-adopted tool actually took, while
the central-control-plane shape it is tempting to build first is the one that stays loved-but-heavy
(self-hosted Backstage runs to 3–12 engineers and 6–12 months,
[Roadie](https://roadie.io/blog/backstage-how-much-does-it-really-cost/)). The multi-tenant hub —
OCM `cluster-proxy`/`managed-serviceaccount`, `Workspace` isolation, the RLS backstop, the
external policy decision point — is all still correct, but it belongs to a **day-N `sith hub`
upgrade** that the same binary becomes when a team outgrows kubeconfig fan-out (clusters behind
NAT/VPCs, shared audit, multi-approver prod), not to the day-0 install an individual adopts.

**The de facto wedge, and the moat behind it.** The wedge is **"k9s for your whole fleet"** — a
cross-cluster read and correlation view no OSS tool ships ("every cluster where `payments` is
Degraded", "which clusters run image X"). k9s users asked for exactly this twice
([#1006](https://github.com/derailed/k9s/issues/1006), 2021;
[#2730](https://github.com/derailed/k9s/issues/2730), 2024) and both were closed *not planned*;
the server-based OSS options (Clusterpedia, Karpor) are read-only with day-N install friction and
modest adoption; the one tool that ships the experience, Aptakube, is closed-source paid GUI
([aptakube.com/multi-cluster](https://aptakube.com/multi-cluster)). But the wedge is a beachhead,
not the moat — the demand signal is a convenience gap, not an emergency (§5). The **moat is
governed action across the fleet and governed agent access**: typed, signed, approval-gated
cluster operations applied identically to a human and to an AI agent — the position the evidence
finds empty across every incumbent. The single reframing that ties it together: *you earn the
right to govern a fleet by first being the tool the engineer already uses to see it.* Lead with
the read wedge; keep governance as the reason to stay; ship both on one binary, one enforcement
pipeline, one closed vocabulary, whether the driver is the TUI, the CLI, or an agent.

---

## 1. The two wedges (the reframe the plan needs)

The current charter names one wedge — governed action federation — and puts a local console
*out of scope* ("A single-cluster console / IDE … owned by Headlamp, k9s, Lens",
[SCOPE.md](../SCOPE.md)). That is half right and half a missed on-ramp, and the split matters:

- **The adoption wedge — the local aggregated fleet client.** How you get ten thousand
  individual engineers to install the thing and like it. Won by form and trust, not governance
  features. The top barriers to adopting a new OSS tool in 2024 were *too complex to run (46%,
  +13 pts)*, *fear of abandonment (46%)*, and *thin docs (45%)* — security scanners ranked far
  lower ([CNCF 2024](https://www.cncf.io/reports/cncf-annual-survey-2024/)). The Lens revolt was
  about an account wall, a trust break (closed source after a vendor-neutrality promise),
  telemetry-by-default, and logs/shell removed from the OSS build — not a missing feature. So the
  adoption wedge is a single binary, ten-minute wow, no-account, no-telemetry, permissively
  licensed, that answers a fleet-wide question on first run.

- **The durable wedge — governed action federation with AI as a client.** What makes Sith
  defensible and, eventually, what an organization pays to self-host and standardize on. The
  research confirms this position is *empty*: OCM ships rollout mechanics but no
  approvals/typed-verbs/audit; ACM and Rancher have platform-coupled policy; Kargo gates artifact
  promotion only; the AI-SRE incumbents (Komodor, Rancher "Liz", HolmesGPT) stop at
  advise/diagnose or go autonomy-first with no approval primitives; the MCP gateways (Kong, Solo
  agentgateway, MintMCP, Permit.io) enforce auth and tool-allowlists but have no fleet-aware,
  blast-radius-conscious, approval-gated action
  ([integrations-and-ai-governance.md](integrations-and-ai-governance.md) §4). The baseline
  Kubernetes MCP servers govern with a `--read-only` flag and a denied-resources list and by
  default expose generic CRUD plus exec-into-pod
  ([containers/kubernetes-mcp-server](https://github.com/containers/kubernetes-mcp-server)).

The predecessor `devops-portal` had **neither** wedge: no adoption on-ramp (helm-install a
platform, then SSO, then value; it 500'd on a fresh install — review-01, review-05) and no
durable moat (it reverse-proxied and iframed tools that were already better, which review-01
called *negative value*). Sith wins by holding both: lead with the adoption wedge, defend on the
durable one, and build them on **one shared engine** so the local client and the hub are the same
fleet model rendered several ways.

## 2. Who feels the pain (personas, with honest caveats)

Fleet scale is real but is an enterprise/platform-team reality, and the cleanest survey evidence
is thinner than the folklore. The CNCF Annual Surveys — the most neutral source — **do not report
per-organization cluster counts**; their closest signal is that 65% of orgs separate applications
by cluster while namespaces (88%) are the dominant, fastest-growing separation unit
([CNCF 2024](https://www.cncf.io/reports/cncf-annual-survey-2024/)) — which actually cuts against
an over-broad multi-cluster pitch. The ">20 clusters" figure comes from Spectro Cloud, a vendor
that sells multi-cluster management (n=455, ≥250 employees,
[State of Production Kubernetes 2025](https://www.spectrocloud.com/state-of-kubernetes-2025)). It
is corroborated by a **non-vendor** source that matters: the 2025 CNCF Argo CD End User Survey
found **25% of adopters connect Argo CD to more than 20 clusters**
([CNCF, 2025-07-24](https://www.cncf.io/announcements/2025/07/24/cncf-end-user-survey-finds-argo-cd-as-majority-adopted-gitops-solution-for-kubernetes/)).
The persona who owns the pain is the **platform/SRE engineer** — 96% of 500+-employee orgs report
a platform function ([Voice of Kubernetes Experts 2024](https://www.cncf.io/blog/2024/06/06/the-voice-of-kubernetes-experts-report-2024-the-data-trends-driving-the-future-of-the-enterprise/)).

Crucially, **the pains these people name are complexity, cost, and inconsistency — not
"governance."** "Too complex to run" tied for #1 challenge (46%); over half of orgs admit their
clusters are "snowflakes" and highly manual (Spectro Cloud 2025); the concrete daily version,
articulated by k9s users, is context thrash: the per-cluster-terminal workaround "doesn't scale …
what if someone has 5+ clusters" ([k9s #2730](https://github.com/derailed/k9s/issues/2730)). The
sequencing implication is direct: **the wedge must lead with a pain they feel — seeing and
operating across clusters without thrash — and deliver governance as the thing that makes acting
safe.** The deeper pain table (fleet size, change-driven outages, GPU waste, China/India
constraints) is in [market-and-form-factor.md § Part 1](market-and-form-factor.md#part-1).

## 3. What makes a tool "de facto" — the CLI/org split

Defaults split into two adoption patterns, and Sith's current framing is in the harder one.
**Individually-adopted tools win on zero-friction terminal fit:** k9s reads your existing
kubeconfig, needs no server or agent, runs under 50 MB, works over SSH, and covers "~90% of daily
kubectl usage" — its install story *is* the onboarding
([decodeops](https://decodeops.substack.com/p/k9s-the-terminal-ui-that-replaces)).
**Org-adopted platforms are loved-but-heavy because they are frameworks you staff:** self-hosted
Backstage is 3 FTEs in year one, 6–12 months to production, upgrades that require refactoring, and
a persona mismatch (terminal-native engineers vs React/CSS customization)
([Roadie](https://roadie.io/blog/backstage-how-much-does-it-really-cost/)). The middle ground that
individuals still reach for is Argo CD — it won by anchoring one sharp job and being CNCF-neutral
(NPS 79, ~60% of clusters), which is exactly why building Sith's action verbs on Argo CD is
well-founded.

Two failure modes to design against, both verified. **Trust is fragile for single-vendor OSS:**
when Mirantis closed Lens's source, the community migrated to k9s and re-forked to the
MIT-licensed FreeLens and never returned ([HN](https://news.ycombinator.com/item?id=39811772)). A
solo-team project inherits that discount; the mitigations are a permissive license (Apache-2.0,
chosen), no forced accounts, and ideally a foundation path. **Complexity is itself the adoption
barrier** (the 46% "too complex" finding) — a direct argument for the single-binary, no-server
day-0 shape.

## 4. Recommended form factor — one binary, three modes, layered adoption

**One Go binary, one embedded web frontend, three run modes.** This is the shape the evidence
points to and what let Headlamp win the Kubernetes-Dashboard succession
([kubernetes.io, 2026-06-01](https://kubernetes.io/blog/2026/06/01/dashboard-to-headlamp/)):

| Mode | Command | What it is | Who runs it | Phase |
|---|---|---|---|---|
| **CLI + TUI** | `sith` | k9s-style local fleet view over kubeconfig contexts; `sith get pods -A --all-clusters`, `sith correlate`. Read-only, local, zero config | The individual engineer — the fastest day-0 wow | Day 0 (wedge) |
| **Local web "fleet IDE"** | `sith ui` | The same fleet model as a local web UI on `localhost` — Lens-but-better, kubeconfig-direct, **no account, no telemetry** | The engineer who wants a visual surface | Day 0 (wedge) |
| **Governed MCP server** | `sith serve --mcp` | The same fleet exposed to Claude Code / Codex / Cursor as annotated read tools + typed-intent writes | The AI-native engineer — fastest adoption vector | Day 0 / v1 (§6) |
| **Hub (federated)** | `sith hub` | The same UI served multi-user: OCM minions reach NAT'd/VPC'd clusters, `Workspace` isolation + RLS, external PDP (Ardur), multi-approver prod, shared audit | The platform/SRE team | Day N (moat) |

Why this shape, with the evidence:

- **A hub is not required to federate reads.** Aptakube connects to many clusters "as if it was
  one big cluster" from the existing kubeconfig with "nothing to install on your clusters"
  ([aptakube.com/multi-cluster](https://aptakube.com/multi-cluster)); `kubernetes-mcp-server`
  federates "as defined in your kubeconfig files" as a single Go binary; Karpor onboards with a
  read-only kubeconfig. Day-0 federation is **client-side kubeconfig fan-out**.
- **But the hub solves the one thing fan-out cannot.** Clusterpedia states it "does not actually
  solve … network connectivity in a multi-cluster environment"; Karpor requires the hub to reach
  every cluster's API server. Clusters behind NAT / isolated VPCs cannot be reached by a laptop —
  which is exactly what OCM `cluster-proxy` + `managed-serviceaccount` solves and Milestone-0
  already proved. So the conclusion is not "drop OCM" — it is **"OCM is the day-N server-mode
  transport, not the day-0 dependency."**
- **Single artifact, cache-first render.** k9s (`brew install`, one binary) defines the funnel;
  "helm-install a platform then SSO then value" is the cliff `devops-portal` fell off. Render from
  a local informer/watch cache in tens of milliseconds, never spinner-first (the Linear local-first
  mechanic; the [0.1s/1s/10s limits](https://www.nngroup.com/articles/response-times-3-important-limits/)).
- **Center of gravity is the fleet, not the pod.** The local mode is *not* another single-cluster
  console (that slot is taken and correctly out of scope). It is the aggregated, cross-cluster
  view Headlamp/k9s/Lens don't center on — with per-pod table stakes (logs, exec, port-forward,
  YAML) present because their absence drove the Lens exodus, but not a place to out-feature
  Headlamp. Details and the Tauri-not-Electron desktop-shell call are in
  [market-and-form-factor.md § Part 2](market-and-form-factor.md#part-2).

This mirrors what produced defaults, including in MLOps: SkyPilot won as a local-first CLI over
existing credentials, not a server-first control plane
([SkyPilot](https://blog.skypilot.co/ai-job-orchestration-pt1-gpu-neoclouds/)).

## 5. The wedge — "k9s for your whole fleet," honestly sized

There is genuinely no OSS terminal-native "all my clusters at once" tool, and the demand is
articulated inside the incumbent's own tracker (k9s #1006, #2730 — both closed *not planned*). The
one tool that ships it, including marketed **cross-cluster incident correlation**, is Aptakube's
closed paid GUI. The **10-minute wow:** `brew install sith && sith` → every context rendered as
one fleet, with cross-cluster queries no single-context tool can answer.

**The honesty caveat, stated plainly.** The k9s demand signal is *modest* — #1006 drew 7
reactions over five years, #2730 drew 8, and the requester conceded per-context switching "works,
is fast, and is definitely helpful." This is a convenience and efficiency gap, not an unmet
emergency. Two consequences: (1) the read wedge earns adoption but will not by itself make Sith a
*default* — it is the beachhead; (2) the moat must be what the read view *enables* that nothing
else does safely — governed cross-cluster action and governed agent access. The read view is how
you get installed; the governed action + MCP layer is why you stay.

## 6. AI/MCP as an adoption driver — ship the MCP server in v1, not Phase 3

This is the sharpest reprioritization the reconciliation adopts. Agent-driven Kubernetes operation
is a real, fast-growing channel: `containers/kubernetes-mcp-server` (~1.8k stars, ~80k npm
downloads/month) and `Flux159/mcp-server-kubernetes` (~1.1k stars, `claude mcp add kubernetes …`)
have real traction, and 90% of ~5,000 DORA respondents use AI at work
([dora.dev](https://dora.dev/dora-report-2025/)). The anxiety is documented and specific: an
engineer gave Claude a staging cluster and "within minutes it tried to `kubectl exec` … and ran
`kubectl get secret -o yaml`"
([kubectl-ro](https://dev.to/veysi/kubectl-ro-read-only-kubernetes-access-for-ai-agents-and-humans-1okg));
Replit's agent deleted a production database during an explicit freeze, and the vendor's own
remedy was *structural* — the action "should never be possible"
([Fortune](https://fortune.com/2025/07/23/ai-coding-tool-replit-wiped-database-called-it-a-catastrophic-failure/)).
DORA 2025 finds AI adoption correlates *negatively* with delivery stability absent "robust control
systems." This is the strongest external validation of Sith's thesis: **safety must come from
boundaries the agent cannot cross (typed intents, closed vocabulary, no exec), not instructions it
might ignore.**

Two constraints from the same evidence. The generic MCP-gateway category is crowded (13+ products
including Kong, Bedrock AgentCore, Docker), so Sith wins only as a **Kubernetes-fleet-operations
gateway with typed action vocabularies**, which none of the generic gateways are
([Obot](https://obot.ai/blog/the-13-best-mcp-gateways-for-enterprise-teams/)). And the prescribed
defense against "shadow MCP" (developers wiring ungoverned servers into Cursor) is to **make the
approved path easier than the unapproved one**. If Sith's governed path is harder than
`npx kubernetes-mcp-server`, engineers route around it. **That is why the MCP server must ship in
v1** — read-only MCP tools ship with the read wedge from the same binary the engineer already
runs; typed-intent writes follow immediately behind the first governed write. MCP is now
vendor-neutral Linux Foundation infrastructure
([donated 2025-12-09](https://blog.modelcontextprotocol.io/posts/2025-12-09-mcp-joins-agentic-ai-foundation/)):
the protocol is settled, the governance on top of it is not.

## 7. The MLOps dimension — convergent substrate, later verbs

DevOps and MLOps are converging on Kubernetes ("the de facto operating system for AI"; 66% of orgs
hosting gen-AI use K8s for some inference,
[CNCF 2026-01-20](https://www.cncf.io/announcements/2026/01/20/kubernetes-established-as-the-de-facto-operating-system-for-ai-as-production-use-hits-82-in-2025-cncf-annual-cloud-native-survey/)),
which makes MLOps a natural expansion — but leading with it is the slower path (44% of orgs run no
AI/ML on K8s; only 7% deploy models daily). The scheduling half is already owned: MultiKueue
dispatches batch jobs across clusters, so "which cluster has free GPUs" is answered for submission
([Kueue docs](https://kueue.sigs.k8s.io/docs/concepts/multikueue/)). The real white space is
**utilization and serving** — average GPU utilization near 5%, an organizational/scheduling
problem, and no cross-cluster model-promotion story in KServe. So "kill zombie GPU workloads
across the fleet" and "promote model X across serving clusters" are plausible **later** typed
intents — design the fleet model and verb vocabulary now so they slot in without re-architecture;
lead with DevOps, where the installed base is. GPU cost columns in the fleet model are in scope
early (§8); GPU *action* verbs are a Phase-2+ expansion.

## 8. Multi-cloud, China/India, cost, connectors (the evidence base)

The deep material lives in the companion files; the load-bearing conclusions for shape and
priority:

- **Multi-cloud is two thin layers, not per-cloud products.** The Kubernetes API is uniform across
  US and China clouds (Alibaba ACK, Huawei CCE, Tencent TKE are all conformance-certified), so
  cluster-*inside* views work day one with no cloud code. Only **enumeration and short-lived
  credential minting** differ per cloud (EKS `get-token`, AKS Entra+kubelogin, GKE plugin,
  ACK/CCE/TKE) — a thin adapter that stores no long-lived keys
  ([market § Part 3 §6](market-and-form-factor.md#part-3)).
- **China/India raise the priority of properties Sith should have anyway.** Air-gap install,
  `linux/arm64`+`amd64` multi-arch images, registry-relocatable images, tamper-evident audit +
  admin/auditor role separation (maps to MLPS 2.0 Level 3), and a hub that works entirely inside
  one network boundary — all mandatory for the China/regulated market and cheap if designed in
  early. DPDP/RBI (India) want in-country self-hosting, satisfied by the self-hosted hub + no
  phone-home ([market § Part 1 §4–5](market-and-form-factor.md#part-1)).
- **Cost is a read-overlay, not a build.** The fleet rollup is the documented gap (OpenCost is
  per-cluster by design; its multi-cluster ask was triaged P3 and closed; Kubecost's unified
  multi-cluster view is Enterprise-tier). Deploy/read OpenCost per cluster, aggregate at the hub
  into per-workspace/team rollups with GPU columns where DCGM exists. Building a metering engine
  re-fights OpenCost/Kubecost/CAST AI ([market § Part 3 §5](market-and-form-factor.md#part-3)).
- **Connectors are three fixed kinds, not an open ecosystem.** Grafana (out-of-process gRPC,
  SDK-first) and Terraform (versioned, minor-additive protocol) scaled; Backstage (in-process,
  unversioned, 250+ plugins, breaking changes in minor releases) drowned. Sith's framework is
  out-of-process gRPC, SDK-first, **one canonical connector per tool**, three kinds only: read
  adapter / brokered read-through (deep-link, never re-skin) / typed-action adapter. The day-1 six
  that feed the model cheaply or host the first write are **Argo CD, Flux, Helm, Prometheus, Loki,
  GitHub** ([integrations-and-ai-governance.md](integrations-and-ai-governance.md) §1–2).

## 9. Competitive white-space map

Rows are jobs an engineer wants done; "—" means the tool does not do that job.

| Job / pain | k9s | Aptakube | Headlamp / Lens | Clusterpedia / Karpor | Komodor (SaaS) | OCM (substrate) | kubernetes-mcp-server | **Sith (proposed)** |
|---|---|---|---|---|---|---|---|---|
| Single-cluster ops UI/TUI | ✅ default | GUI | ✅ GUI | — | ✅ | — | — | ✅ (reuse pattern) |
| **All-clusters-at-once view (OSS, local)** | ❌ (asked, unbuilt) | ✅ closed/paid GUI | ❌ | server-install, read-only | ✅ SaaS | — | via kubeconfig, no UI | ✅ **wedge** |
| Cross-cluster correlation query | — | ✅ GUI | — | search only | ✅ | — | — | ✅ |
| Local-first, no server/agent | ✅ | ✅ | ✅ desktop | ❌ hub | ❌ SaaS agent | ❌ hub+agents | ✅ | ✅ (day 0) |
| Cross-VPC/NAT reach | — | ❌ | ❌ | ❌ | ✅ | ✅ | ❌ | ✅ (day-N hub, via OCM) |
| Governed typed actions (no exec) | ❌ (exec yes) | ❌ | ❌ | ❌ | proprietary | — | coarse on/off | ✅ **moat** |
| Governed MCP server for agents | — | — | — | — | MCP client-side | — | coarse flags | ✅ **moat** |
| Multi-tenant workspace + audit + PDP | — | — | — | — | ✅ SaaS | RBAC only | — | ✅ (day-N) |
| OSS + vendor-neutral | ✅ | ❌ | mixed | ✅ | ❌ | ✅ | ✅ | ✅ |

The empty column no incumbent fills: **OSS + local-first + all-clusters + governed typed actions +
governed MCP, in one binary that scales to a shared hub.** Sith's defensibility is the
combination, not any single cell.

## 10. The ruthlessly prioritized roadmap

Feature sprawl killed `devops-portal` (12 providers, 92 routes, ~7/10 pillars pure pass-through —
review-01). Every capability the owner named is placed in exactly one bucket, and "not now" is as
important as the wedge. A capability earns a higher bucket only if it serves a wedge.

**Wedge (build first — reason to exist + on-ramp):**
- Local fleet client — `sith` CLI/TUI (k9s-style, the fastest wow) **and** `sith ui` (local web
  fleet IDE, "Lens but better"), kubeconfig-direct, no account/telemetry, cache-first, cmd-K/`:`
  fleet search, logs/exec/port-forward/YAML in core.
- **Source-agnostic** read federation + normalized fleet model + cross-cluster correlation — the
  shared engine behind every mode; source is local kubeconfig (day-0) *or* OCM spoke (day-N).
- **Governed MCP read server** from the same binary (`sith serve --mcp`) — annotated read tools in
  v1 (the shadow-MCP argument makes this a hard requirement, §6).
- Minions (outbound OCM agents) + multi-auth (kubeconfig for local; API key / JWT / OIDC and
  short-lived cloud IAM for the hub).
- Governance spine, day one: `Workspace` tenancy, signed-token authn (never header trust),
  least-privilege RBAC, forced Postgres RLS backstop, audit-log + decision-ledger.
- First governed typed write — `gitops.open-pr` through the Ardur PDP; MCP write tool for it lands
  right behind it.
- No-god-key custody: no central admin kubeconfig; KMS-envelope per-tenant DEKs (hub), OS keychain
  (local); cosign-signed releases + SLSA L2 provenance + SBOM from the first tag.

**Fast-follow (right after the wedge proves out):**
- Policy federation — waves/canary, environment gates, multi-approver for prod, partial-failure /
  auto-rollback, abstention; the live-mutation verbs (`argocd.sync`, `rollout.promote|abort`,
  `deployment.scale|restart`) behind all of it.
- Connector framework — out-of-process gRPC, SDK-first, three fixed kinds — generalizing the day-1
  six.
- Cost read-overlay — OpenCost rollup + GPU columns; not a metering engine.
- Multi-cloud enumeration + short-lived token minting incl. ACK/CCE/TKE; OpenShift conformant-API
  coverage.
- Air-gap / multi-arch / registry-relocatable packaging (Zarf-style, no phone-home); Tauri desktop
  shell.

**Later (real, but not until the above lands):**
- Long-tail read connectors (Datadog, Splunk, Elastic/OpenSearch/Kibana, Terraform/OpenTofu
  state-drift); ITSM typed actions (Jira/Zendesk/ServiceNow change tickets).
- Sith as an MCP *client* (calling kagent, Grafana MCP, GitHub MCP); governing LangChain/LangGraph
  agents (they connect as MCP clients of Sith, get a scoped identity, ride the same PEP — Sith
  governs, it does not orchestrate).
- MLOps typed verbs (kill zombie GPU jobs, promote model across serving clusters); OpenShift-specific
  views (Routes/SCC).

**Explicitly not now (say no, loudly — the anti-sprawl contract):**
- Re-skinning / proxying tool UIs (the `devops-portal` iframe-Grafana trap) · a telemetry lake /
  metrics store · a metering / cost-optimization engine · an agent-orchestration framework (that is
  LangGraph/kagent) · a developer portal / IDP / catalog / scorecards / DORA · a GitOps reconciler ·
  a multi-cluster scheduler · Fluentd/Fluent-bit as data sources (read the sinks) · Kustomize/Helm
  as action targets in v1 · `exec` / free-form apply / Secret / RBAC mutation (permanently
  inexpressible) · running SPIRE (support SPIFFE IDs/mTLS, don't force the platform).

## 11. Carry / discard / net-new vs `devops-portal`

| Carry (the good bones) | Discard (the failure modes) | Net-new (what the research says Sith needs) |
|---|---|---|
| Action/exec broker service-layer (clean 1:1 tool→service map, review-08) — **redesigned as the PEP** with a closed vocabulary, no shell | Shared central admin kubeconfig / inbound-god-kubeconfig — replaced by outbound OCM minions + scoped MSA tokens + no central admin cred | Local aggregated fleet client (CLI/TUI + web) as the adoption wedge (empty OSS slot) |
| Per-org encrypted credential vault (AES-256-GCM key-ring, review-08) — **re-architected** as KMS-envelope per-tenant DEKs | Single god key (`TOKEN_ENCRYPTION_KEY` decrypts every tenant) | Cross-cluster correlation as a first-class query in a local tool (k9s won't build it; only a closed GUI ships it) |
| RBAC + audit spine (review-08) — kept, **hardened** with signed-token authz + a separate decision-ledger | Dead/inert RLS + `x-user-role` header-trust IDOR — replaced by FORCE RLS (non-owner role) + signed-token-only authz | Typed-intent action model + signed dispatch + per-minion local allowlist re-validation (two independent blast-radius bounds) |
| Governed AI/MCP ambition — review-10's one salvageable idea — becomes the **core, done right** (real MCP server in v1, elicitation gates, AI-as-client) | All-heavy monolith (~48k LOC, 92 routes; iframes better tools) — replaced by one narrow Go binary + local surfaces, no re-skinning | Policy federation — waves / multi-approver / abstention (novel, empty) |
| `Workspace`-over-clusters tenancy — kept as the isolation anchor with a **real** RLS backstop (day-N hub) | Feature sprawl (12 providers, ~7/10 pass-through) — replaced by the closed vocabulary + three connector kinds + a hard scope gate | Governed MCP server as a Kubernetes-fleet-ops gateway (generic gateways aren't fleet-aware) |
| OCM `cluster-proxy` + `managed-serviceaccount` — **re-scope to day-N server mode** (solves cross-VPC reach fan-out can't) | Broken onboarding (platform-install cliff; 500s on fresh install) — replaced by `brew install && sith`, ten-minute wow | Air-gap / multi-arch / registry-relocatable distribution for China/regulated |
| Typed-intent closed vocabulary, no exec — **carry, strengthen** (boundaries-not-instructions is the verified lesson) | Features that never ran (dead write path, mock MCP page, qwen2.5:3b) — replaced by falsification-first, ship-what's-verified | Cost read-overlay with fleet rollup + GPU columns (empty in OSS, paywalled commercially) |
| | "Central control plane + web UI" as the *day-0* shape — **change** to local-first day-0, hub day-N | MLOps typed verbs (kill zombie GPU jobs, promote model across clusters) — build later |

## 12. Concrete changes to the plan

The governance thesis is *validated* by the evidence (annotations are hints, enforcement must be
server-side, no incumbent occupies governed fleet action). The change is to add the adoption wedge,
invert the day-0/day-N shape, pull the MCP read server into v1, and hang the named capabilities off
the wedge without diluting the anti-drift contract.

- **CHARTER** — reframe from one wedge to **two** (adoption = local client; durable = governed
  federation); add the **individual operator** as the top-of-funnel user; add an adoption success
  criterion (`brew install` → populated cross-cluster answer in <10 min, offline, nothing leaves
  the machine); state the two share one engine.
- **SCOPE** (the key edit) — refine "single-cluster console — out of scope" to "*another*
  single-cluster console" and add **in-scope** "aggregated multi-cluster local fleet client (the
  adoption on-ramp), distinguished by fleet aggregation/correlation, not per-pod parity." Make the
  §10 "not now" list contractual.
- **ARCHITECTURE** — add the three run-modes on one Go binary + embedded frontend; make read
  federation **source-abstract** (local kubeconfig *or* OCM spoke); add the four-mode identity
  model, the connector framework (three kinds), the cost overlay; note cosign/SLSA/SBOM and
  SPIFFE-without-SPIRE.
- **ROADMAP** — invert to local-first day-0; sequence **P1 local read + correlation → P1 governed
  MCP read tools (same binary) → P2 first governed write (`gitops.open-pr`) + its MCP write tool →
  P3 the hub (OCM cross-VPC reach, multi-tenant isolation, multi-approver, wave/canary)**. Keep the
  falsification gate; require multi-arch images from the first release.
- **EPICS** — amend E2 to treat local kubeconfig contexts as a first-class read source; pull the
  MCP *read* tools (E7) forward into P1; add **E11 (local fleet client)**, **E12 (connector
  framework)**, **E13 (cost overlay)**; amend E9 (multi-arch/air-gap/signing) and E8 (the console
  is the one frontend served by `sith ui` and `sith hub`).

## 13. Evidence grading and honesty notes

- **Verified 3-0 (verbatim vs primary source):** the identity/security claims (exec-plugin auth,
  no cert revocation, short-lived-token guidance), the connector-framework claims (Grafana
  out-of-process, Terraform protocol versioning, Argo/Flux/Helm/Prom/Loki/Grafana surfaces), the
  MCP annotation claims; plus CNCF 2024 namespace-vs-cluster figures and "too complex to run" (46%).
  Full votes in the companion files.
- **Refuted / corrected:** the "five or more clouds" phrasing was a misread — the source says
  "five-plus clouds **and environments**"; the >20-cluster core stands and is corroborated by the
  non-vendor CNCF Argo CD survey (25% >20 clusters).
- **Primary-source quotes not independently re-verified (a verification pass was cut short by a
  session limit):** the k9s issue histories, Aptakube capabilities, Clusterpedia/Karpor
  architecture, MCP-server adoption numbers, kubectl-ro, Replit, DORA 2025, Backstage TCO, the
  China/India source set. High-confidence, direct quotes, not triple-checked.
- **Vendor-sponsored (discount for framing bias):** Spectro Cloud (sells multi-cluster mgmt), CAST
  AI, Komodor, Roadie (Backstage cost testimony is against interest, so more credible). Fleet-scale
  premise independently corroborated by the CNCF Argo CD survey.

---

**The one-line test for every future feature request:** does it serve the adoption wedge (get an
individual to install and love the local fleet client) or the durable wedge (governed action
across the fleet, humans and agents alike)? If neither, it belongs in §10's "not now" — no matter
how useful it sounds. That question, applied ruthlessly, is the difference between Sith and the
predecessor.
