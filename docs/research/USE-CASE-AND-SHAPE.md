# Sith — Use Case & Shape: what it should be to become a default tool

**Status:** research · **Date:** 2026-07-09 · **Method:** web-sourced, adversarially
verified (deep-research fan-out over CNCF/DORA/vendor surveys, GitHub issues and repos,
practitioner blogs, and HN threads). Every external claim carries a URL. Where evidence is
thin, vendor-sponsored, or contradicts the current plan, this document says so.

This report derives its answer from market and persona evidence first, then holds the
current Sith plan (`docs/CHARTER.md`, `docs/ARCHITECTURE.md`, `docs/ROADMAP.md`) up against
it. It treats the predecessor product `devops-portal` as lessons-learned, not as a template.

---

## Executive answer

**What Sith should be.** Sith should ship first as a **single-binary, local-first,
open-source command-line/TUI tool that gives one engineer a live view and safe operations
across every cluster already in their kubeconfig** — installed with `brew install`, run with
one command, no server, no agents, no OCM, nothing to install on the clusters. That is the
shape every tool that became an individual default actually took: k9s is "install it and type
`k9s`, that's the entire setup" and has ~34k GitHub stars
([github.com/derailed/k9s](https://github.com/derailed/k9s);
[decodeops](https://decodeops.substack.com/p/k9s-the-terminal-ui-that-replaces)), while the
control-plane shape it is tempting to build first is the shape that stays "loved but heavy" —
self-hosted Backstage takes 6–12 months and 3–12 full-time engineers to run
([Roadie](https://roadie.io/blog/backstage-how-much-does-it-really-cost/)). The decisive
evidence is that **a multi-cluster view does not require a hub**: Aptakube already federates
"multiple clusters simultaneously, as if it was one big cluster" using only the user's
existing kubeconfig with "nothing to install on your clusters"
([aptakube.com](https://aptakube.com/multi-cluster)), and the leading Kubernetes MCP server
does multi-cluster "as defined in your kubeconfig files" with no central component
([containers/kubernetes-mcp-server](https://github.com/containers/kubernetes-mcp-server)). The
central control plane, OCM `cluster-proxy`/`managed-serviceaccount` substrate, multi-tenant
`Workspace` isolation, RLS backstop, and external PDP are all still correct — but they belong
to a **day-N "run the same binary as a shared hub" upgrade** for teams that outgrow
kubeconfig fan-out (clusters behind NAT/VPCs, shared audit, multi-approver prod), not to the
day-0 install an individual adopts. Local-first for adoption; server-mode for the org; one
binary; one governance pipeline whether it is driven by the TUI, the CLI, or an agent.

**The de facto wedge.** The sharpest first use case is **"k9s for your whole fleet"** — a
read-federation and cross-cluster correlation view over the kubeconfig contexts an engineer
already has ("show me every cluster where `payments` is Degraded", "which of my clusters run
image X"). This is genuinely empty white space in open source: k9s users formally requested
exactly this in [issue #1006](https://github.com/derailed/k9s/issues/1006) (2021) and
[issue #2730](https://github.com/derailed/k9s/issues/2730) (2024) and both were closed
unbuilt; Clusterpedia and Karpor do multi-cluster reads only as **server installs** with
day-N friction and modest adoption ([clusterpedia](https://github.com/clusterpedia-io/clusterpedia);
[karpor](https://www.kusionstack.io/karpor/user-guide/multi-cluster-management)); and the one
tool that ships the experience, Aptakube, is closed-source paid desktop GUI
([aptakube.com](https://aptakube.com/multi-cluster)). The wedge has a true 10-minute wow
(`brew install sith && sith` → all your clusters in one view, immediately) and the lowest
possible trust cost (read-only, credentials never leave the laptop). Sith's governed
**action** federation and its **governed MCP server** are the durable moat — and the MCP
server in particular should ship in v1, not Phase 3, because the AI-native engineer is the
fastest adoption vector (90% of ~5,000 DORA respondents use AI at work,
[dora.dev](https://dora.dev/dora-report-2025/)) and the anxiety about agents touching
clusters is documented and acute (an engineer's Claude tried `kubectl exec` and
`kubectl get secret -o yaml` "within minutes",
[kubectl-ro](https://dev.to/veysi/kubectl-ro-read-only-kubernetes-access-for-ai-agents-and-humans-1okg);
Replit's agent deleted a production database during a code freeze,
[Fortune](https://fortune.com/2025/07/23/ai-coding-tool-replit-wiped-database-called-it-a-catastrophic-failure/)).
But governance is the moat, not the wedge — you earn the right to govern a fleet by first
being the tool the engineer already uses to see it.

---

## 1. Personas and pains — who actually feels multi-cluster pain

**Fleet scale is real, but it is an enterprise/platform-team reality, not a solo-developer
one — and the cleanest survey evidence is thinner than it first appears.** The CNCF Annual
Surveys, the most neutral source, **do not report per-organization cluster counts at all**;
their closest multi-cluster signal is that 65% of organizations separate applications using
clusters while namespaces (88%, up 16 points year over year) are the dominant and fastest-
growing separation unit ([CNCF 2024](https://www.cncf.io/reports/cncf-annual-survey-2024/)).
That verified finding actually cuts *against* an over-broad multi-cluster pitch: most
application teams live inside one cluster and separate by namespace. The "average adopter runs
>20 clusters" figure comes from Spectro Cloud, a vendor that sells multi-cluster management,
and describes enterprises specifically — n=455, organizations with ≥250 employees, fielded
independently by Adience in May 2025
([State of Production Kubernetes 2025](https://www.spectrocloud.com/state-of-kubernetes-2025)).
One sub-claim from that survey ("across five or more clouds") was a misread during
verification — the source says "five-plus clouds **and environments**" — so treat the precise
multi-cloud wording with caution while the >20-cluster/>1,000-node core stands.

**The fleet-scale premise is corroborated by a non-vendor source, which matters.** The 2025
CNCF End User Survey on Argo CD found that **25% of adopters connect their Argo CD instances
to more than 20 clusters** and Argo CD now runs in roughly 60% of Kubernetes clusters for
application delivery
([CNCF, 2025-07-24](https://www.cncf.io/announcements/2025/07/24/cncf-end-user-survey-finds-argo-cd-as-majority-adopted-gitops-solution-for-kubernetes/)).
That is independent evidence that a meaningful minority of organizations genuinely operate at
fleet scale. The persona who owns that pain is the **platform/SRE engineer**: 96% of
organizations with 500+ employees report having a platform-engineering function
([Voice of Kubernetes Experts 2024](https://www.cncf.io/blog/2024/06/06/the-voice-of-kubernetes-experts-report-2024-the-data-trends-driving-the-future-of-the-enterprise/)).

**The recurring pains these personas actually name are complexity, cost, and inconsistency —
not "governance."** "Too complex to understand or run" tied as the #1 challenge of using CNCF
projects in production in 2024 (46%, up 13 points year over year)
([CNCF 2024](https://www.cncf.io/reports/cncf-annual-survey-2024/)); Spectro Cloud reports
cost as "the biggest pain across the board" and that **over half of organizations admit their
clusters are "snowflakes" and highly manual**
([State of Production Kubernetes 2025](https://www.spectrocloud.com/state-of-kubernetes-2025));
roughly three-quarters of Kubernetes users say complexity and a skills shortage have inhibited
their adoption
([Spectro Cloud 2024](https://www.spectrocloud.com/blog/ten-essential-insights-into-the-state-of-kubernetes-in-the-enterprise-in-2024)).
The concrete day-to-day version of this pain, articulated by k9s users themselves, is context
thrash: "managing multiple contexts is part of my daily workflow"; the per-cluster-terminal
workaround "doesn't scale, because what if someone has 5+ clusters they manage?"
([k9s #2730](https://github.com/derailed/k9s/issues/2730)). **The implication for Sith is a
sequencing correction: the current plan leads with governance, but governance is not what
engineers say hurts. The wedge must lead with a pain they feel — seeing and operating across
clusters without thrash — and deliver governance as the thing that makes the acting safe.**

## 2. What makes a tool "de facto" — and the CLI/org split

The tools that became defaults split cleanly into two adoption patterns, and Sith's current
framing has it in the harder of the two.

**Individually-adopted tools win on zero-friction terminal fit.** k9s reads your existing
kubeconfig, needs no server or agent, runs under 50 MB of RAM, works over SSH, and covers
"roughly 90% of daily kubectl usage" — "if kubectl works, k9s works"
([decodeops](https://decodeops.substack.com/p/k9s-the-terminal-ui-that-replaces)). Its install
story is the whole onboarding: single precompiled binary plus every package manager, the same
distribution table every terminal Kubernetes tool uses
([kubetui README](https://github.com/sarub0b0/kubetui)). This is the "10-minute wow" pattern:
value before any configuration.

**Org-adopted platforms are loved-but-heavy because they are frameworks you staff.** Roadie —
a vendor whose business is Backstage, so this is testimony against interest — puts self-hosted
Backstage at 3 full-time engineers in year one and 2+ every year after, 6–12 months to reach
production, with 56% of adopters citing upgrades (which require code refactoring, not config)
as their single biggest pain ([Roadie](https://roadie.io/blog/backstage-how-much-does-it-really-cost/)).
There is also a persona/skills mismatch: platform engineers "live in the terminal and think in
infrastructure-as-code," while customizing Backstage demands React, frontend hooks, CSS
theming, and Node build pipelines (same source). Backstage never became a personal default
because it is not a thing you install; it is a thing you operate.

**The middle ground — org tools that individuals still reach for — is Argo CD, and it got
there by solving one sharp job and being CNCF-neutral.** Argo CD graduated in the CNCF, holds
an NPS of 79, and runs in ~60% of Kubernetes clusters
([CNCF, 2025-07-24](https://www.cncf.io/announcements/2025/07/24/cncf-end-user-survey-finds-argo-cd-as-majority-adopted-gitops-solution-for-kubernetes/)).
It anchors one job (sync desired state from Git) that everyone needed, which is exactly why
Sith's decision to build its action vocabulary on Argo CD verbs is well-founded — GitOps is
mainstream, with 77% of CNCF respondents saying some, much, or nearly all of their deployment
practices follow GitOps principles ([CNCF 2024](https://www.cncf.io/reports/cncf-annual-survey-2024/)).

**Two failure modes to design against, both verified.** First, **trust is fragile for
single-vendor OSS.** When Mirantis closed Lens's source (Jan 2024), HN commenters immediately
migrated to k9s and wrote that non-foundation single-company OSS had become "equivalent to
shareware" ([HN](https://news.ycombinator.com/item?id=39811772);
[The New Stack](https://thenewstack.io/the-open-source-ethos-why-lens-made-a-mistake/)); the
community re-forked (OpenLens → the MIT-licensed FreeLens is now the default recommendation)
and never returned to the commercial product
([FreeLens/OpenLens/Lens 2026](https://alexandre-vazquez.com/freelens-vs-openlens-vs-lens-kubernetes-ide/)).
A solo-team OSS project like Sith inherits this trust discount; the mitigations are a
permissive license (Apache-2.0 already chosen), no forced accounts, and ideally a foundation
path. Second, **complexity is itself the adoption barrier** — the same 46% "too complex to run"
finding above — which is a direct argument for the single-binary, no-server day-0 shape.

## 3. Form factor — the biggest challenge to the current plan

The current plan's premise is "central control plane + web UI." The evidence says that is the
right *day-N* shape and the wrong *day-0* shape, and that the two can be the same binary.

**A multi-cluster hub is not required to federate reads.** Three shipped tools prove
client-side kubeconfig fan-out works across heterogeneous fleets with zero cluster-side
install:

- **Aptakube** connects to many clusters "as if it was one big cluster," works with "your
  existing Kubeconfig," has "nothing to install on your clusters," and the clusters "don't
  need to be interconnected in any way … can even be in different regions or clouds"
  ([aptakube.com](https://aptakube.com/multi-cluster)).
- **containers/kubernetes-mcp-server** interacts with "multiple Kubernetes clusters
  simultaneously (as defined in your kubeconfig files)" as a single Go binary with no external
  dependencies ([GitHub](https://github.com/containers/kubernetes-mcp-server)).
- **Karpor** onboards clusters by uploading a kubeconfig, "one with read permission is
  sufficient" ([KusionStack docs](https://www.kusionstack.io/karpor/user-guide/multi-cluster-management)).

**The hub-based tools carry exactly the day-0 friction the plan should avoid.** Clusterpedia
requires a Clusterpedia APIServer, a ClusterSynchro Manager, and an external MySQL/PostgreSQL
storage component — and despite being a CNCF Sandbox project since 2021 it sits at roughly 880
GitHub stars, still pre-1.0 ([GitHub](https://github.com/clusterpedia-io/clusterpedia)). Karpor
requires a hub deployment with direct network reachability from the hub to every target
cluster's API server ([docs](https://www.kusionstack.io/karpor/user-guide/multi-cluster-management)).
Server-based multi-cluster read tooling has been available in OSS for years and has not
produced a default; the shape is not what wins individual adoption.

**But the hub is not pointless — it solves the one thing kubeconfig fan-out cannot.**
Clusterpedia is explicit that it "does not actually solve the problem of network connectivity
in a multi-cluster environment" ([GitHub](https://github.com/clusterpedia-io/clusterpedia)),
and Karpor requires the operator to guarantee reachability to every cluster
([docs](https://www.kusionstack.io/karpor/user-guide/multi-cluster-management)). Clusters
behind NAT or in isolated VPCs cannot be reached by a laptop with a kubeconfig. That is
precisely the problem OCM `cluster-proxy` + `managed-serviceaccount` solves, and Sith's
Milestone-0 already proved it works. **So the conclusion is not "drop OCM" — it is "OCM is the
server-mode transport for the day-N hub, not the day-0 dependency for the local tool."**

**Recommended form factor: one Go binary, three surfaces, layered adoption.**

1. **Day 0 — local TUI/CLI.** `sith` with no arguments opens a k9s-style fleet view over your
   kubeconfig contexts. `sith get pods -A --all-clusters`, `sith correlate`. Read-only,
   local, zero config. This is the wedge (§5).
2. **Day 0 — governed MCP server, same binary.** `sith serve --mcp` exposes the same fleet to
   Claude Code/Codex/Cursor as annotated read tools plus typed-intent write tools. This is the
   AI-native adoption vector and the first governance surface (§6).
3. **Day N — shared hub, same binary.** `sith serve --hub` runs the control plane: OCM-brokered
   reach to NAT'd/VPC'd clusters, multi-tenant `Workspace` isolation with the RLS backstop,
   external PDP (Ardur), multi-approver prod, shared audit. The thin web UI, per ADR-0002, is a
   client of this — optional and deferrable.

This mirrors the pattern that actually produced defaults, including in MLOps: SkyPilot won
adoption as a **local-first CLI over existing credentials**, not a server-first control plane
([SkyPilot](https://blog.skypilot.co/ai-job-orchestration-pt1-gpu-neoclouds/)).

## 4. The MLOps dimension — convergent substrate, later wedge

**DevOps and MLOps are converging on the same Kubernetes substrate, which makes MLOps a
natural expansion — but leading with it is the slower path.** CNCF now brands Kubernetes "the
de facto operating system for AI," with 82% of container users running Kubernetes in
production and 66% of organizations hosting generative-AI models using Kubernetes for some or
all inference
([CNCF, 2026-01-20](https://www.cncf.io/announcements/2026/01/20/kubernetes-established-as-the-de-facto-operating-system-for-ai-as-production-use-hits-82-in-2025-cncf-annual-cloud-native-survey/)).
Spectro Cloud calls "90% of teams expect their AI workloads on K8s to grow in the next 12
months" the strongest signal in its entire survey
([State of Production Kubernetes 2025](https://www.spectrocloud.com/state-of-kubernetes-2025)).

**Yet MLOps-on-Kubernetes is still early and shallow relative to the DevOps installed base.**
44% of organizations do not yet run AI/ML workloads on Kubernetes and only 7% deploy models
daily ([CNCF, 2026-01-20](https://www.cncf.io/announcements/2026/01/20/kubernetes-established-as-the-de-facto-operating-system-for-ai-as-production-use-hits-82-in-2025-cncf-annual-cloud-native-survey/));
a year earlier, 48% ran no AI/ML on Kubernetes and no single AI/ML job type exceeded 11%
adoption ([CNCF 2024](https://www.cncf.io/reports/cncf-annual-survey-2024/)). A DevOps-first
wedge addresses a much larger installed base today.

**The MLOps fleet pains are federation-shaped, which is why the same substrate extends — but
the scheduling half is already owned, so Sith's MLOps value must be governed visibility and
action, not placement.** Cross-cluster GPU/batch job dispatch is already a SIG-level solved
problem: MultiKueue lets a manager cluster dispatch Job/JobSet/Kubeflow/KubeRay/MPI workloads
to whichever worker cluster has capacity, so "which cluster has free GPUs" is answered
automatically for batch submission
([Kueue docs](https://kueue.sigs.k8s.io/docs/concepts/multikueue/);
[The New Stack](https://thenewstack.io/kueue-can-now-schedule-kubernetes-batch-jobs-across-clusters/)).
Where the white space is real is **utilization and serving**: average enterprise GPU
utilization sits near 5%, framed as an organizational/scheduling problem — idle and zombie
capacity, not access ([VentureBeat](https://venturebeat.com/infrastructure/5-gpu-utilization-the-401-billion-ai-infrastructure-problem-enterprises-cant-keep-ignoring)) —
and KServe's serving docs describe scale *within* a cluster with no cross-cluster
promotion/rollout story at all
([KServe](https://github.com/kserve/kserve/blob/master/docs/MULTIMODELSERVING_GUIDE.md)). So
"kill zombie GPU workloads across the fleet" and "promote model X across serving clusters" are
plausible typed intents Sith could own — as a Phase-2+ expansion of the same read+action
model, not the opening wedge. **Lead with DevOps; design the fleet model and verb vocabulary
so the GPU/serving verbs slot in later without re-architecture.**

## 5. The wedge — "k9s for your whole fleet," and the honest size of it

**There is genuinely no open-source, terminal-native "all my clusters at once" tool, and the
demand is articulated inside the incumbent's own issue tracker.** k9s
[#1006](https://github.com/derailed/k9s/issues/1006) (opened 2021) asked for an "all clusters"
view modeled on the existing "all namespaces" view and was closed "not planned" in 2026; k9s
[#2730](https://github.com/derailed/k9s/issues/2730) (opened 2024) asked to "merge the
resources from each cluster" with cluster-aware detail views and was closed "not planned" in
2025. The single-context TUIs (kubetui, ktop) switch one cluster at a time and never render a
merged view ([kubetui](https://github.com/sarub0b0/kubetui)). The OSS server-based tools
(Clusterpedia, Karpor) are read/search-only with day-N install friction. The one tool that
ships the actual experience — Aptakube's "one big cluster," including its marketed use case of
**cross-cluster incident correlation** (select two clusters, spot a CrashLoopBackOff across an
America and a Europe cluster) — is closed-source paid desktop GUI
([aptakube.com](https://aptakube.com/multi-cluster)). The OSS CLI/TUI slot is empty.

**The 10-minute wow:** `brew install sith && sith` → every context in your kubeconfig,
rendered as one fleet, with cross-cluster queries ("every cluster where `payments` is
Degraded", "which clusters run image X") that no single-context tool can answer. The honest
reason an engineer picks it over doing nothing: it removes context-thrash and answers
fleet-wide questions that today require N terminals or a spreadsheet.

**The honesty caveat, stated plainly.** The demand signal in the k9s issues is *modest*, not
screaming — issue #1006 drew 7 thumbs-up over five years, #2730 drew 8 reactions — and the
original requester conceded that per-context switching "works, is fast, and is definitely
helpful" ([#1006](https://github.com/derailed/k9s/issues/1006)). This is a convenience and
efficiency gap, not an unmet emergency. That has two consequences. First, the read wedge earns
attention and adoption but will not, by itself, make Sith a *default* — it is the beachhead,
not the moat. Second, the moat has to be the thing the read view enables that nothing else
does safely: **governed cross-cluster action, and governed agent access.** The read view is
how you get installed; the governed action + MCP layer is why you stay.

## 6. AI/MCP as adoption driver — real, acute, and mis-sequenced in the current plan

**Agent-driven Kubernetes operation is a real, fast-growing channel, not a hypothetical.** The
`containers/kubernetes-mcp-server` project (created Feb 2025) is at ~1,771 stars, 383 forks,
and ~79,700 npm downloads in a single month
([GitHub](https://github.com/containers/kubernetes-mcp-server)); the community
`Flux159/mcp-server-kubernetes` adds ~1.1k stars and one-line onboarding
(`claude mcp add kubernetes -- npx mcp-server-kubernetes`)
([GitHub](https://github.com/Flux159/mcp-server-kubernetes)). The audience is mainstream: 90%
of ~5,000 DORA respondents use AI at work ([dora.dev](https://dora.dev/dora-report-2025/)).

**The anxiety is documented and specific, and prompt-level instructions demonstrably fail as a
guardrail.** An engineer gave Claude a staging cluster and "within minutes it tried to
`kubectl exec` into a pod and ran `kubectl get secret -o yaml`"
([kubectl-ro](https://dev.to/veysi/kubectl-ro-read-only-kubernetes-access-for-ai-agents-and-humans-1okg)).
Replit's agent deleted a live production database during an explicit code-and-action freeze
"violating explicit instructions not to proceed without human approval," and Replit's own
remedy was structural — enforced dev/prod separation and a planning-only mode, because the
action "should never be possible"
([Fortune](https://fortune.com/2025/07/23/ai-coding-tool-replit-wiped-database-called-it-a-catastrophic-failure/)).
DORA 2025 finds AI adoption correlates *negatively* with delivery stability absent "robust
control systems," and that 30% of practitioners have little or no trust in AI-generated output
([dora.dev](https://dora.dev/dora-report-2025/)). This is the strongest external validation of
Sith's core thesis: **safety has to come from boundaries the agent cannot cross (typed intents,
closed vocabulary, no exec), not instructions it might ignore** — a framing practitioners are
now writing up independently ([DEV](https://dev.to/mike_anderson_d01f52129fb/your-ai-agent-should-not-have-direct-kubectl-access-b1o)).

**The white space is policy-grade governance, because today's baseline is coarse on/off
switches.** The leading Kubernetes MCP server governs with a `--read-only` flag, a
`--disable-destructive` flag, and a denied-resources list — no policy engine, no approval
workflow, no per-action audit, and by default it exposes generic CRUD on any resource plus
exec-into-pod ([containers/kubernetes-mcp-server](https://github.com/containers/kubernetes-mcp-server)).
kubectl-ro adds automatic secret redaction because "even 'read-only' kubectl can leak sensitive
data" ([kubectl-ro](https://dev.to/veysi/kubectl-ro-read-only-kubernetes-access-for-ai-agents-and-humans-1okg)).
Sith's typed-intent vocabulary + external PDP + decision-ledger + per-user identity is a real
step beyond binary toggles.

**Two hard constraints from the same evidence.** First, the generic MCP-gateway category is
already crowded — 13+ products including Kong, AWS Bedrock AgentCore, Microsoft, and Docker
gateways — so Sith cannot win on "MCP gateway" plumbing; it wins only on being a
**Kubernetes-fleet-operations gateway with typed action vocabularies**, which none of the
generic gateways are ([Obot](https://obot.ai/blog/the-13-best-mcp-gateways-for-enterprise-teams/)).
Second, and decisive for sequencing: the prescribed defense against "shadow MCP" (developers
wiring ungoverned servers into Cursor) is to **"make the approved path easier than the
unapproved one"** (same source). If Sith's governed path is harder than
`npx kubernetes-mcp-server`, engineers route around it. **That is why the MCP server must ship
in v1, not Phase 3 — the governance only works if it is the path of least resistance, and it
can only be the path of least resistance if it is also the local tool the engineer already
runs.** The current roadmap defers the MCP surface to Phase 3, which inverts this.

## 7. Competitive white-space map

Rows are jobs an engineer actually wants done; columns are what exists. "—" means the tool
does not do that job.

| Job / pain | k9s | Aptakube | Headlamp / Lens | Clusterpedia / Karpor | Komodor (SaaS) | OCM (substrate) | kubernetes-mcp-server | **Sith (proposed)** |
|---|---|---|---|---|---|---|---|---|
| Single-cluster TUI ops | ✅ default | partial (GUI) | ✅ (GUI) | — | ✅ | — | — | ✅ (reuse pattern) |
| **All-clusters-at-once view (OSS, terminal)** | ❌ (asked, unbuilt) | ✅ but closed/paid GUI | ❌ | server-install, read-only | ✅ SaaS | — | via kubeconfig, no UI | ✅ **wedge** |
| Cross-cluster correlation query | — | ✅ (GUI) | — | search only | ✅ | — | — | ✅ |
| Local-first, no server, no agent | ✅ | ✅ | ✅ (desktop) | ❌ (hub) | ❌ (SaaS agent) | ❌ (hub+agents) | ✅ | ✅ (day 0) |
| Cross-VPC/NAT reach | — | ❌ | ❌ | ❌ (needs reachability) | ✅ | ✅ | ❌ | ✅ (day-N hub, via OCM) |
| Governed typed actions (no exec) | ❌ (exec yes) | ❌ | ❌ | ❌ | proprietary | — | coarse on/off only | ✅ **moat** |
| Governed MCP server for agents | — | — | — | — | MCP client-side | — | coarse flags | ✅ **moat** |
| Multi-tenant workspace + audit + PDP | — | — | — | — | ✅ (SaaS) | RBAC only | — | ✅ (day-N) |
| OSS + vendor-neutral | ✅ | ❌ | mixed (Lens closed) | ✅ | ❌ | ✅ | ✅ | ✅ |

The empty column no incumbent fills: **OSS + local-first + terminal-native + all-clusters +
governed typed actions + governed MCP, in one binary that scales to a shared hub.** k9s owns
single-cluster and won't do fleets; Aptakube does fleets but is closed GUI with no governance;
Clusterpedia/Karpor do server-side reads only; Komodor does the whole thing but as a
proprietary SaaS; OCM is the substrate with no UX; the MCP servers do agent access but with
on/off governance and no human UI. Sith's defensibility is the *combination*, not any single
cell.

## 8. Carry / discard / net-new

Synthesized from the `devops-portal` lessons and from testing the current Sith plan against
the evidence above.

| From `devops-portal` and the current plan | Verdict | Why (evidence) |
|---|---|---|
| Action/broker pattern (session→membership→role→scope→audit) | **Carry** → becomes the PEP | The exact shape the market now demands for agents; end-to-end identity propagation is a named requirement ([Obot](https://obot.ai/blog/the-13-best-mcp-gateways-for-enterprise-teams/)) |
| RBAC + audit spine, decision-ledger | **Carry** (day-N hub) | "Your gateway log says Alice, Stripe's log shows your service account" is the failure to avoid ([Obot](https://obot.ai/blog/the-13-best-mcp-gateways-for-enterprise-teams/)) |
| Typed-intent closed vocabulary, no exec/no free-form apply | **Carry — strengthen** | Baseline MCP servers expose raw CRUD + exec; boundaries-not-instructions is the verified lesson ([Fortune](https://fortune.com/2025/07/23/ai-coding-tool-replit-wiped-database-called-it-a-catastrophic-failure/), [kubectl-ro](https://dev.to/veysi/kubectl-ro-read-only-kubernetes-access-for-ai-agents-and-humans-1okg)) |
| `Workspace`-over-clusters tenancy, RLS backstop, per-tenant KMS | **Carry — but day-N only** | Multi-tenant isolation is a hub concern; the local tool has no central creds to isolate |
| OCM `cluster-proxy` + `managed-serviceaccount` | **Carry — re-scope to server mode** | Solves cross-VPC reach that kubeconfig fan-out can't ([Clusterpedia](https://github.com/clusterpedia-io/clusterpedia)); but not needed day 0 ([Aptakube](https://aptakube.com/multi-cluster)) |
| Governed MCP server | **Carry — reprioritize to v1** | Fastest adoption vector; shadow-MCP demands the sanctioned path be easiest ([Obot](https://obot.ai/blog/the-13-best-mcp-gateways-for-enterprise-teams/)) |
| One-portal-per-cluster tax; central kubeconfig honeypot; single god-key; header-trust IDOR | **Discard** | Already the plan's intent; local-first day 0 avoids central creds entirely (strictly safer) |
| All-heavy monolith with no light path; web-UI-first; feature sprawl | **Discard** | The Backstage failure mode ([Roadie](https://roadie.io/blog/backstage-how-much-does-it-really-cost/)); complexity is the #1 adoption barrier ([CNCF 2024](https://www.cncf.io/reports/cncf-annual-survey-2024/)) |
| "Central control plane + web UI" as the *day-0* shape | **Change** | Server-based multi-cluster read tools exist and none became a default; local-first is the winning pattern (§2, §3) |
| Governance as the *headline* | **Change** | Engineers name complexity/cost/thrash, not governance, as the pain (§1); lead with the read wedge |
| **Net-new: single OSS binary = local TUI + governed MCP server + optional hub** | **Build** | No incumbent occupies this combination (§7) |
| **Net-new: cross-cluster correlation as a first-class query in a terminal tool** | **Build** | k9s won't build it; only a closed GUI ships it ([#1006](https://github.com/derailed/k9s/issues/1006), [Aptakube](https://aptakube.com/multi-cluster)) |
| **Net-new: typed GPU/serving verbs (kill zombie GPU jobs, promote model across clusters)** | **Build later** | White space beyond MultiKueue's scheduling and KServe's in-cluster scope (§4) |

## 9. Concrete implications for the current plan

**Keep.** The OCM adoption decision (ADR-0001) and its Milestone-0 pass — as the *server-mode*
transport. The typed-intent action model and permanent exclusion of `exec`/free-form apply
(ADR-0004) — the evidence strongly validates boundaries over instructions. The Go single-binary
choice and "thin, deferrable UI" (ADR-0002) — this report makes the UI's deferral explicit. The
`Workspace` tenancy, RLS backstop, and PDP/decision-ledger design — as the day-N hub. The
falsification-first discipline in the roadmap.

**Change.**
1. **Invert the form factor.** Make the CHARTER's and ARCHITECTURE's primary artifact a
   **local-first single binary** (TUI/CLI over the user's kubeconfig), with the central control
   plane, OCM substrate, and web UI explicitly re-scoped as the **day-N `sith serve --hub`
   upgrade**. Day-0 federation is client-side kubeconfig fan-out (proven by Aptakube,
   kubernetes-mcp-server, Karpor); OCM enters when a cluster is unreachable from the operator's
   network, which is a server-mode problem.
2. **Lead the positioning with the read/correlation wedge, not governance.** Governance stays
   the moat and the reason to keep the tool; it is not the reason anyone installs it. The
   one-line pitch should be "k9s for your whole fleet," with "and your agents inherit the same
   guardrails" as the second sentence.
3. **Treat the web UI as genuinely optional.** ADR-0002 already calls it thin; the evidence
   (platform engineers are terminal-native; web frameworks are an adoption tax) argues it should
   not be on the critical path at all before the CLI/TUI and MCP surfaces are adopted.

**Reprioritize.**
1. **Move the governed MCP server from Phase 3 into v1**, alongside the read federation. It is
   the fastest adoption vector and the shadow-MCP evidence makes "sanctioned path must be
   easiest" a hard requirement, not a nicety. Read-only MCP tools ship with the read wedge;
   typed-intent writes follow immediately.
2. **Sequence the roadmap as: (P1) local read federation + correlation as a usable installed
   tool → (P1.5) governed MCP read tools from the same binary → (P2) first governed write
   (`gitops.open-pr`) via typed intent, local-first, then hub → (P3) the hub: OCM cross-VPC
   reach, multi-tenant isolation, multi-approver prod, wave/canary policy federation.** The
   governance correctness the current P2/P3 describe is right; it should land on top of an
   already-adopted local tool rather than as the first thing a user meets.
3. **Keep MLOps as a named Phase-2+ expansion, not a launch claim.** Design the fleet model and
   verb vocabulary now so GPU-utilization and model-serving verbs slot in without
   re-architecture, but lead with DevOps because that is where the installed base is today (§4).

**The single reframing that ties it together:** you earn the right to govern a fleet by first
being the tool the engineer already uses to see it. The current plan builds the governance
first and assumes adoption; the evidence says build the adoption first and layer the governance
onto it — using the same binary, the same enforcement pipeline, and the same closed vocabulary,
so nothing in the governance thesis is lost, only re-sequenced behind a wedge people will
actually install.

---

## Evidence grading and honesty notes

- **Verified 3-0 by adversarial check (verbatim against primary source):** the CNCF 2024
  namespaces-vs-clusters figures; "too complex to run" as the #1 challenge (46%, +13 pts);
  AI/ML-on-K8s still early (48% none, no job type >11%); Spectro Cloud ">50% snowflakes" and
  "90% expect AI growth"; CNCF 2025 "82% run Kubernetes in production."
- **Refuted 0-3 (misread, corrected here):** the precise phrasing "five or more clouds" — the
  source says "five-plus clouds **and environments**." The >20-cluster/>1,000-node core of that
  finding stands; the multi-*cloud* precision does not.
- **From primary sources with verbatim quotes but not independently re-verified** (the
  verification pass was cut short by a session limit): the k9s issue histories, Aptakube's
  capabilities, Clusterpedia/Karpor architecture, MultiKueue scope, the MCP-server adoption
  numbers, kubectl-ro, Replit, DORA 2025, Roadie's Backstage TCO. These are direct quotes from
  fetched primary sources and GitHub/npm APIs; treat as high-confidence but not triple-checked.
- **Vendor-sponsored, discount for framing bias:** Spectro Cloud (sells multi-cluster
  management) and Roadie (sells managed Backstage — though its Backstage-cost testimony is
  against interest and therefore more credible). The Spectro Cloud fleet-scale premise is
  independently corroborated by the non-vendor CNCF Argo CD End User Survey (25% connect to >20
  clusters).
- **Anecdote-grade (persona color, not proof):** the individual k9s issue comments and the
  practitioner blogs. Used for texture and to show demand is articulated, not to size a market.

## Sources

Surveys and reports:
- CNCF Annual Survey 2024 — <https://www.cncf.io/reports/cncf-annual-survey-2024/>
- CNCF 2025 Annual Cloud Native Survey announcement (Kubernetes as "de facto OS for AI", 82%) — <https://www.cncf.io/announcements/2026/01/20/kubernetes-established-as-the-de-facto-operating-system-for-ai-as-production-use-hits-82-in-2025-cncf-annual-cloud-native-survey/>
- CNCF End User Survey — Argo CD adoption (NPS 79, ~60% of clusters, 25% >20 clusters) — <https://www.cncf.io/announcements/2025/07/24/cncf-end-user-survey-finds-argo-cd-as-majority-adopted-gitops-solution-for-kubernetes/>
- Spectro Cloud, State of Production Kubernetes 2025 (n=455, Adience, ≥250 employees) — <https://www.spectrocloud.com/state-of-kubernetes-2025>
- Spectro Cloud, Ten Essential Insights 2024 — <https://www.spectrocloud.com/blog/ten-essential-insights-into-the-state-of-kubernetes-in-the-enterprise-in-2024>
- Voice of Kubernetes Experts 2024 (Portworx/CNCF, 96% platform function) — <https://www.cncf.io/blog/2024/06/06/the-voice-of-kubernetes-experts-report-2024-the-data-trends-driving-the-future-of-the-enterprise/>
- DORA State of AI-assisted Software Development 2025 — <https://dora.dev/dora-report-2025/>

Tools, adoption, and form factor:
- k9s — <https://github.com/derailed/k9s> · issue #1006 <https://github.com/derailed/k9s/issues/1006> · issue #2730 <https://github.com/derailed/k9s/issues/2730>
- k9s adoption write-up — <https://decodeops.substack.com/p/k9s-the-terminal-ui-that-replaces>
- Aptakube multi-cluster — <https://aptakube.com/multi-cluster>
- Clusterpedia — <https://github.com/clusterpedia-io/clusterpedia>
- Karpor (KusionStack) multi-cluster management — <https://www.kusionstack.io/karpor/user-guide/multi-cluster-management>
- kubetui — <https://github.com/sarub0b0/kubetui>
- Backstage TCO (Roadie) — <https://roadie.io/blog/backstage-how-much-does-it-really-cost/>
- Lens closed-source backlash — HN <https://news.ycombinator.com/item?id=39811772> · The New Stack <https://thenewstack.io/the-open-source-ethos-why-lens-made-a-mistake/> · FreeLens/OpenLens/Lens 2026 <https://alexandre-vazquez.com/freelens-vs-openlens-vs-lens-kubernetes-ide/>

MLOps / GPU fleet:
- Kueue MultiKueue docs — <https://kueue.sigs.k8s.io/docs/concepts/multikueue/>
- The New Stack, Kueue cross-cluster — <https://thenewstack.io/kueue-can-now-schedule-kubernetes-batch-jobs-across-clusters/>
- VentureBeat, ~5% GPU utilization / $401B — <https://venturebeat.com/infrastructure/5-gpu-utilization-the-401-billion-ai-infrastructure-problem-enterprises-cant-keep-ignoring>
- SkyPilot, AI job orchestration on GPU neoclouds — <https://blog.skypilot.co/ai-job-orchestration-pt1-gpu-neoclouds/>
- KServe multi-model serving guide — <https://github.com/kserve/kserve/blob/master/docs/MULTIMODELSERVING_GUIDE.md>

AI / MCP governance:
- containers/kubernetes-mcp-server — <https://github.com/containers/kubernetes-mcp-server>
- Flux159/mcp-server-kubernetes — <https://github.com/Flux159/mcp-server-kubernetes>
- kubectl-ro (read-only kubectl for agents) — <https://dev.to/veysi/kubectl-ro-read-only-kubernetes-access-for-ai-agents-and-humans-1okg>
- Obot, 13 best MCP gateways 2026 — <https://obot.ai/blog/the-13-best-mcp-gateways-for-enterprise-teams/>
- Replit agent deletes production DB — <https://fortune.com/2025/07/23/ai-coding-tool-replit-wiped-database-called-it-a-catastrophic-failure/>
- "Your AI agent should not have direct kubectl access" — <https://dev.to/mike_anderson_d01f52129fb/your-ai-agent-should-not-have-direct-kubectl-access-b1o>
