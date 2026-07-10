# Sith — Market, tool landscape, and form factor (workstreams A · B · C · F · G)

**Status:** research · **Date:** 2026-07-09 · **Provenance:** salvaged and consolidated from a deep-research fan-out (100+ verified sub-agent results per workstream, run 2026-07-08) plus a small amount of hand gap-filling (2026-07-09). Every load-bearing claim carries a primary-source link; vendor-commissioned figures are labelled as such. This document consolidates three workstream drafts — practitioner pains (A), form factor & developer experience (C), and the tool / cost / multi-cloud landscape (B · F · G). Companions: [identity-connections-security.md](identity-connections-security.md) (run D), [integrations-and-ai-governance.md](integrations-and-ai-governance.md) (run E), and the synthesis [USE-CASE-AND-SHAPE.md](USE-CASE-AND-SHAPE.md).

**Contents**
- [Part 1 — Practitioner pains: global, China, India](#part-1)
- [Part 2 — Form factor and DX: the "Lens IDE" question](#part-2)
- [Part 3 — Tool landscape, cost, and multi-cloud gap map](#part-3)

---

<a id="part-1"></a>

## Part 1 — Practitioner pains: global, China, India

**Date:** 2026-07-08 · **Method:** web research with fetched primary sources; verbatim quotes pulled from each source. Load-bearing claims were re-checked by hand where flagged. Survey figures are cited with the exact page/figure where the source provides one. Vendor-commissioned research is labelled as such.

This file answers: which pains are frequent and severe enough that a fleet-operations tool must kill them, and what China and India add.

---

## 1. The quantitative baseline: fleets are real, and they hurt

| Fact | Figure | Source (fetched) |
|---|---|---|
| Fleet size is mainstream, not exotic | "The average Kubernetes adopter now operates more than 20 clusters"; 56% have >10 | [Spectro Cloud, State of Production Kubernetes 2024](https://www.spectrocloud.com/blog/ten-essential-insights-into-the-state-of-kubernetes-in-the-enterprise-in-2024) (n=416, vendor survey) |
| Same figure from a second, independent vendor | "A typical enterprise now runs more than 20 clusters, with nearly half operating across more than four environments"; 37% manage >100 clusters, 12% >1,000 | [Komodor 2025 Enterprise Kubernetes Report](https://komodor.com/blog/komodor-2025-enterprise-kubernetes-report-finds-nearly-80-of-production-outages/) (customer telemetry, not a survey) |
| Fleets span environments | Half of businesses run clusters in 4+ environments (clouds, DCs, edge) | Spectro Cloud 2024, above |
| Hybrid is the norm | 86% deploy across both public and private cloud | [Portworx, Voice of Kubernetes Experts 2024](https://portworx.com/wp-content/uploads/2024/06/The-Voice-of-Kubernetes-Experts-Report-2024.pdf) (n=527, 500+ employee orgs, Dimensional Research) |
| Estates skew self-managed and multi-cloud | 59% self-managed on-prem and 59% self-managed public cloud; 37% use 2 cloud providers, 26% use 3 | [CNCF Annual Survey 2024 PDF](https://www.cncf.io/wp-content/uploads/2025/04/cncf_annual_survey24_031225a.pdf) (n=689–750 depending on question) |
| Complexity is the headline pain | "Three quarters of businesses that use Kubernetes today say their adoption of K8s has actually been inhibited by the complexity" | Spectro Cloud 2024, above |
| Change is the outage engine | "79% of production issues originate from a recent system change" | Komodor 2025, above |
| Toil is quantified | >60% of ops time spent troubleshooting; 64+ workdays/year lost; median MTTD ~40 min, MTTR >50 min for high-impact outages; 38% see high-impact outages weekly | Komodor 2025, above (telemetry sample caveat) |
| Cost pressure is rising | Nearly two-thirds report K8s TCO grew and face more cost pressure than a year ago, with poor visibility into future cost | Spectro Cloud 2024, above |
| Waste is endemic | ">82% of Kubernetes workloads are overprovisioned (65% use less than half of the CPU and memory they request)" | Komodor 2025, above |
| Lock-in anxiety is measurable | 55% "already feel locked in"; more than half worry about vendors shutting down | Spectro Cloud 2024, above |
| Demand for a central plane exists | 71% say a unified/centralized platform would greatly benefit them; 43% specifically want streamlined hybrid/multi-cloud management across Kubernetes environments | Portworx 2024, above (framed around data services — Portworx's domain; treat as directional) |
| The buyer function exists almost everywhere | 96% of surveyed enterprises have a platform-engineering function; drivers include cost (49%) and security mandates (43%) | Portworx 2024, above |

Two honest caveats on this table. First, three of the five sources are vendor-commissioned; the figures converge (20+ clusters, 4+ environments, complexity/cost as top pains), which is why they are usable, but no single number should be treated as precise. Second, the CNCF survey's own challenge ranking (Figure 8: cultural change 46%, CI/CD 40%, training 38%, security 37%, monitoring 36%, complexity 35%) shows people/skills pains now rank alongside technical ones — a tool that requires deep OCM or fleet-theory expertise to install will fail the very population that needs it.

### What the CNCF survey says about *adopting a new OSS tool* (directly actionable for Sith)

The top 2024 barriers to running open-source projects in production: fear the project becomes inactive (46%, +9 pts YoY), too complex to understand or run (46%, +13 pts), lack of documentation (45%, +5 pts) ([CNCF 2024 PDF](https://www.cncf.io/wp-content/uploads/2025/04/cncf_annual_survey24_031225a.pdf)). Security-vulnerability concern *fell* to 29%. The adoption killers for a new tool are perceived abandonment risk, complexity, and thin docs — not security scanners. Sith's counter must be structural: trivial install, excellent docs, visible release cadence.

## 2. Ranked pains a fleet tool must kill

Ranked by convergence of survey figures and community evidence. (The community-thread search lane of this research was partially rate-limited; where thread-level citations are thin the ranking leans on the survey base above and the tool-exodus evidence in [Part 2](#part-2).)

1. **"What is happening across my clusters right now?" — fleet-wide visibility and correlation.** 20+ clusters across 4+ environments with per-cluster consoles means N logins to answer one question. The Portworx 71%/43% centralization demand and the Komodor MTTD figures quantify it. Single-cluster tools structurally cannot answer "which clusters run image X" or "where is `payments` degraded".
2. **"What changed, where?" — change attribution across the fleet.** 79% of production issues stem from a recent change (Komodor). A fleet tool that can answer "what changed in the last hour, across which clusters, by whom" attacks the single largest outage cause.
3. **Doing the same action on N clusters safely.** The action side of pain 1. Today it is a shell loop over kubeconfigs (no gates, no audit, no rollback) or a heavyweight platform (Rancher/ACM). Severity evidence is indirect but strong: change-driven outages (79%), weekly high-impact outages (38%), and the absence of any OSS primitive for gated fan-out (see [Part 3](#part-3) §OCM).
4. **Credential and access sprawl.** Kubeconfig-per-cluster with exec plugins, shared admin credentials, no per-user attribution. Survey proxy: security named by 37% (CNCF). The predecessor's own failure (central god-kubeconfig) and the entire brokered-access market (Teleport et al., see [identity-connections-security.md](identity-connections-security.md)) exist because of this pain.
5. **Cost visibility across clusters and clouds — including GPU.** Two-thirds face rising TCO with poor visibility (Spectro Cloud); >82% overprovisioned (Komodor); GPU is the extreme case — see §3.
6. **Complexity/skills mismatch.** 75% say complexity inhibited adoption (Spectro Cloud); training gaps at 38% (CNCF). A fleet tool must *reduce* the expertise required, not add a new discipline.
7. **Vendor lock-in anxiety.** 55% feel locked in (Spectro Cloud); the 2025 SUSE/Rancher repricing (competitor-reported 4–9x increases — [Portainer's account](https://www.portainer.io/blog/suse-rancher-price-hike-why-enterprises-are-searching-for-alternatives-in-2025), cross-check against SUSE's [shop page](https://www.suse.com/shop/suse-rancher-prime/)) and the Kubecost/IBM and Lens/Mirantis consolidations feed it. Vendor-neutral OSS is a distribution advantage here, not a nicety.

## 3. MLOps / GPU fleet pains

- **GPU waste is extreme and measured.** CAST AI's 2026 analysis of tens of thousands of production clusters: **average GPU utilization 5%** ("95% of GPU capacity is doing nothing"), vs 8% CPU and 20% memory ([CAST AI 2026 State of Kubernetes Optimization](https://cast.ai/press-release/2026-state-of-kubernetes-optimization-report/); vendor telemetry). The ClearML/AIIA survey (~1,000 AI leaders): only 7% achieve >85% peak GPU utilization; 74% dissatisfied or only moderately satisfied with job-scheduling tools; 93% say easy self-serve compute would substantially raise productivity ([ClearML State of AI Infrastructure at Scale 2024](https://clear.ml/blog/the-state-of-ai-infrastructure-at-scale-2024)).
- **Per-team GPU cost attribution is formalized as a discipline but only single-cluster.** The FinOps Foundation working group codifies namespace-as-cost-centre labeling enforced at admission, showback-then-chargeback, and MIG for right-sizing ("sub-5 GB models hogging an entire A100") — and its paper **covers only single-cluster scenarios; cross-cluster allocation is unaddressed** ([FinOps WG: Scaling Kubernetes for AI/ML with FinOps](https://www.finops.org/wg/scaling-kubernetes-for-ai-ml-workloads-with-finops/)). That gap is exactly the fleet layer.
- **In-cluster GPU scheduling is being commoditized; the fleet layer is not.** NVIDIA open-sourced its Run:ai-derived KAI-Scheduler (CNCF Sandbox) for fairness across teams at thousand-node scale ([KAI-Scheduler](https://github.com/kai-scheduler/KAI-Scheduler)). The defensible pains for a fleet tool sit *above* the scheduler: federated per-team usage/cost across N clusters, and governed actions on training/inference fleets.
- **ML platform teams re-invent governed access.** ZenML's platform guidance: data scientists should never hold raw cluster credentials; access should be centrally brokered ([ZenML on 8xH100 multi-tenancy](https://www.zenml.io/blog/managing-mlops-at-scale-on-kubernetes-when-your-8xh100-server-needs-to-serve-everyone)). MLOps is thus a *user segment* of the same governed-access/action wedge — not a separate product.
- 54% of enterprise K8s orgs already run AI/ML on Kubernetes (Portworx 2024, above), so GPU columns in the fleet model serve a mainstream slice, not a niche.

## 4. China

The CNCF survey cannot support China conclusions (~3% of respondents China-HQ'd — the survey says so itself, [2024 PDF](https://www.cncf.io/wp-content/uploads/2025/04/cncf_annual_survey24_031225a.pdf) demographics). The evidence below is from Chinese-language primary sources (vendor docs, practitioner tutorials) and is labelled accordingly. Direct practitioner-sentiment evidence at scale is thin in public sources; that gap is flagged rather than papered over.

**4.1 Air-gap/offline is a first-class, routine deployment mode — not an edge case.**
- KubeSphere documents air-gapped installation as a standard path (KubeKey artifact + private Harbor) ([KubeSphere docs, 离线安装](https://kubesphere.io/zh/docs/v3.3/installing-on-linux/introduction/air-gapped-installation/)).
- A representative practitioner walkthrough (运维有术 series on Tencent Cloud's developer community) builds the offline bundle on a connected node and carries it in; the full artifact is ~13 GB and populates 124 image repos in self-hosted Harbor; the topology is described as a 1:1 replica of a small production environment ([cloud.tencent.com/developer/article/2419243](https://cloud.tencent.com/developer/article/2419243)).
- The assumed baseline is that public registries are unreachable: the tutorial *verifies* isolation by showing `docker.io` image pulls failing on Chinese public DNS. A separate xinchuang field report states plainly that hub.docker.com "is currently not accessible from within China — figure out your own way" ([CSDN, 信创适配实战](https://blog.csdn.net/yztezhl/article/details/139698545), June 2024).

**4.2 Compliance shapes tooling: MLPS 2.0 (等保 2.0) is operationalized, not theoretical.**
- Alibaba ACK ships MLPS 2.0 Level 3 hardening as a cluster-creation option implementing GB/T 22239-2019 ([ACK 等保加固说明](https://help.aliyun.com/zh/ack/ack-managed-and-ack-dedicated/security-and-compliance/ack-reinforcement-based-on-classified-protection)). Level-3 hardening mandates: **role separation** (ACK creates distinct `ack_admin`, `ack_audit`, `ack_security` users), **tamper-resistant, backed-up audit records** ("应对审计记录进行保护，定期备份，避免受到未预期的删除、修改或覆盖"), and **no root SSH** — i.e., hardened Chinese estates structurally favor governed, audited, agent-mediated access over interactive credentials.
- The MLPS model explicitly allows substituting self-attested equivalent controls ("如果有其他方式，可自行举证并忽略此项") — a self-hosted OSS tool can legitimately slot into an MLPS-graded estate.
- **Xinchuang (信创) localization** adds: domestic OSes (openEuler/OpenAnolis, or Kylin/UOS when customers demand), domestic ARM CPUs (Kunpeng/Phytium), hence **linux/arm64 multi-arch images are a hard requirement**, and tolerance for older pinned K8s versions (the field report ran v1.24 in mid-2024) ([CSDN field report](https://blog.csdn.net/yztezhl/article/details/139698545)).

**4.3 The cloud substrate is conformant Kubernetes.** Alibaba ACK, Huawei CCE and Tencent TKE all hold current CNCF Certified Kubernetes conformance (hand-verified in [cncf/k8s-conformance](https://github.com/cncf/k8s-conformance): `v1.32/alicloud` = "Alibaba Cloud Container Service for Kubernetes v1.32.0"; `huawei-cce` submissions continuously v1.29–v1.34; `v1.34/tencentcloud` = "Tencent Kubernetes Engine v1.34.1"). All three issue standard kubeconfigs (RAM/IAM/CAM-integrated) with private-VPC endpoints ([ACK kubeconfig](https://www.alibabacloud.com/help/en/ack/ack-managed-and-ack-dedicated/user-guide/obtain-the-kubeconfig-file-of-a-cluster-and-use-kubectl-to-connect-to-the-cluster), [CCE permissions](https://support.huaweicloud.com/intl/en-us/usermanual-cce/cce_10_0187.html), [TKE connecting](https://www.tencentcloud.com/document/product/457/30639)). Rancher already imports ACK/CCE/TKE via its outbound agent ([SUSE announcement](https://www.suse.com/c/rancher_blog/announcing-added-support-for-leading-kubernetes-services-in-china/)) — feasibility proof for the same pattern.
- Chinese-language practitioner comparisons of multi-cluster tools (Rancher vs KubeSphere vs Karmada) turn on cluster import, network reachability and unified permissions ([Zhihu 多集群管理工具对比](https://zhuanlan.zhihu.com/p/539203985), [Kubernetes多集群管理之路](https://zhuanlan.zhihu.com/p/584378217) — the latter names heterogeneous cluster types, 10k-node scale spread, per-region compliance differences, version drift, and "scattered permissions with uncontrolled security risk" as the operating reality).

**4.4 What this means for Sith in China.** A US-hosted SaaS control plane (Komodor, Datadog, Lens-with-account) is structurally disadvantaged: GFW/egress policy, data-residency posture, and procurement all push toward self-hosted. Sith fits **if and only if** it ships: (1) fully offline installation (single bundle, Zarf-style, no phone-home — see [Part 3](#part-3) on Zarf); (2) linux/arm64 + x86 multi-arch images and support for openEuler/Kylin-class hosts; (3) registry-relocatable images (no hardcoded docker.io/gcr.io pulls); (4) tamper-evident audit logs and admin/auditor role separation (maps directly to MLPS L3); (5) hub-and-spoke that works entirely inside one network boundary (the hub is self-hosted in-country; outbound-only spokes work within/behind the boundary). These are the same properties the federated design already targets — China raises their priority from "nice" to "mandatory for the market".

## 5. India

- **The buyer base is large and institutional.** 1,700+ GCCs (2,975+ units) generating $64.6B and employing ~1.9M as of FY2024, projected to 2,100–2,200 centers and $99–105B by 2030 ([Nasscom–Zinnov India GCC Landscape 2026 PDF](https://media.zinnov.com/wp-content/uploads/2026/05/zinnov-nasscom-india-gcc-landscape-2026-report.pdf)). GCCs increasingly own engineering/platform functions for global parents — i.e., they operate exactly the multi-cluster, multi-environment estates Sith targets. IT-services majors position Kubernetes as the multi-cloud abstraction across client estates ([Wipro on K8s-native multi-cloud](https://www.wipro.com/blogs/sreekanth-nyamars/kubernetes-native-design-thinking-realizing-true-multi-cloud-adoption/) — vendor voice; treat as estate-shape evidence, not pain ranking). The service-integrator profile adds a specific requirement: **hard isolation between client estates in one operator's tooling** — Sith's workspace model must treat "many clients, one operator" as a first-class shape.
- **Regulatory pull is procurement-level, not absolute.** DPDP Act 2023 (+ DPDP Rules 2025) uses a blacklist approach to cross-border transfer rather than blanket localization, but the government can pin specified data of "significant data fiduciaries" in-country; phased compliance runs to ~May 2027 with penalties to ₹250 crore ([EY analysis](https://www.ey.com/en_in/insights/cybersecurity/decoding-the-digital-personal-data-protection-act-2023)). The hard residency floor is sectoral: RBI's 2018 directive requires payment-system data stored only in India ([Google Cloud RBI compliance page](https://cloud.google.com/security/compliance/rbi-india)). BFSI is a top GCC vertical, so in-country self-hosting is often a de-facto requirement; SaaS buyers increasingly ask for India hosting or on-prem options ([Wattlecorp DPDP-for-SaaS guide](https://www.wattlecorp.com/saas-providers-guide-to-dpdp-act-india/) — mid-tier source). Self-hostable OSS derisks all of this by construction.
- **Cost sensitivity + skills profile.** GCC hiring data indicates a 40%+ skills gap in tech roles and a cited 55–60% deficit in cloud-native expertise, with salary premiums for K8s/CI-CD engineers ([Savannah HR aggregation of NASSCOM/Deloitte figures](https://savannahr.com/blog/top-8-gcc-skills-india-2026/) — recruiting-blog source, verify against the underlying reports before quoting numbers). Implication: free OSS core with radically simple install and opinionated safe defaults beats "powerful but expert-only". Direct India practitioner sentiment on fleet tooling is thin in public sources — flagged as a gap; the estate-shape and regulatory evidence above is the reliable part.

## 6. Conclusions carried into the synthesis

1. The pains Sith must kill, in order: fleet-wide visibility/correlation; change attribution; safe fan-out actions; access/credential sprawl; fleet cost overlay (GPU included). These map 1:1 onto read federation, action federation, and a cost read-integration — the charter's wedge survives contact with the demand evidence.
2. OSS-adoption barriers (abandonment fear, complexity, docs) mean the *form* of the product decides adoption as much as the wedge. A 10-minute, single-binary first-run is not polish; it is the counter to the #1 and #2 adoption barriers.
3. China: air-gap bundle, multi-arch, registry relocation, tamper-evident audit, role separation — mandatory, and cheap if designed in early.
4. India: multi-client workspace isolation and self-hostability are the fit; price the OSS core at zero and keep the install trivial.
5. MLOps is a segment, not a separate product: GPU util/cost columns in the fleet model + the same governed actions.


---

<a id="part-2"></a>

## Part 2 — Form factor and DX: the "Lens IDE" question

**Date:** 2026-07-08 · **Method:** fetched primary sources with verbatim quotes; GitHub figures read from the live pages/API on 2026-07-08; the Kubernetes-Dashboard-to-Headlamp claim, k9s stats, and China-cloud conformance were re-verified by hand.

This file answers: what happened to Lens and where users went; what a local Kubernetes tool must have; what makes tools feel effortless; what technology to build on; and whether local + federated should be one product or two.

---

## 1. The Lens story, verified in both directions

The "Lens went paywall/telemetry" narrative is **real but more precise than the folklore**:

| Date | Event | Source |
|---|---|---|
| 2020-08-13 | Mirantis acquires Lens ("world's most popular Kubernetes IDE", MIT-licensed, ~35k users); founder quoted promising it "would remain vendor neutral and open source" | [Mirantis press release](https://www.mirantis.com/company/press-center/company-news/mirantis-acquires-lens-the-worlds-most-popular-kubernetes-ide/) |
| ~2022-05/06 (Lens 5.5.x) | Mandatory, non-skippable **Lens ID login** lands; team concedes it "could have done better at communicating the change" | [lensapp/lens#5444](https://github.com/lensapp/lens/issues/5444) |
| 2022-07 | **Lens 6 subscription model**: Lens Pro $19.90/user/mo; free Personal tier restricted to individuals and orgs under $10M revenue/funding | [Mirantis announcement](https://www.mirantis.com/blog/lens-pro-vision-for-the-future-new-subscription-model-new-features-available/); backlash: [HN "Lens goes subscription only"](https://news.ycombinator.com/item?id=32269258), [HN on the acquisition/login](https://news.ycombinator.com/item?id=32408122) |
| 2022-12/2023-01 (6.3.0) | Pod **logs/shell menus removed from the open-source build** (moved to an extension); the single most-cited exodus trigger | [lensapp/lens#6823](https://github.com/lensapp/lens/issues/6823); [OpenLens README](https://github.com/MuhammedKalkan/OpenLens) ("type `@alebcay/openlens-node-pod-menu` into the Extensions page") |
| 2024-03 | **Lens closes its source**; OpenLens build repo freezes (last release v6.5.2, 2023-06-30; repo README: "Lens Closed its source code. So please do not expect any more updates.") | [OpenLens repo](https://github.com/MuhammedKalkan/OpenLens); [HN thread](https://news.ycombinator.com/item?id=39811772); alternatives catalogued in [lensapp/lens#8008](https://github.com/lensapp/lens/issues/8008) |
| Today (verified 2026-07-08) | Lens pricing: Personal **free** (under $10M revenue/funding) and still includes multi-cluster management, metrics, logs, terminal, Helm, port-forwarding, resource editing; Plus $25/user/mo (AI copilot, EKS/AKS auto-discovery, Security Center); Enterprise custom. Telemetry: Lens "may automatically communicate with Mirantis servers" for updates/usage tracking, **opt-out available**, vendor asserts no kubeconfigs/secrets uploaded | [lenshq.io/pricing](https://lenshq.io/pricing) (k8slens.dev/pricing redirects here); [Lens licensing/telemetry FAQ](https://docs.k8slens.dev/faq/subscription-and-licensing/) |

So the adversarial check lands here: **core features are mostly still free; what users actually revolted against was the account wall, the trust break (closed source after a vendor-neutrality promise), telemetry-by-default, and the removal of logs/shell from the OSS build.** Those four things — not any missing feature — created the exodus. A new tool wins that audience by structural promises: no account, no telemetry, open source, logs/exec in core forever.

### Where the exodus went (traction figures as of 2026-07-08)

| Tool | What it is | Traction | Gap it leaves |
|---|---|---|---|
| [Freelens](https://github.com/freelensapp/freelens) | MIT fork of OpenLens (Electron, TypeScript), no account/telemetry | **5.3k stars in ~2 years** (repo created 2024-06-19), v1.10.3 released 2026-07-07, ~monthly cadence, 29 releases | Inherits Lens's one-cluster-at-a-time UX and Electron weight; a continuation, not a rethink |
| [OpenLens builds](https://github.com/MuhammedKalkan/OpenLens) | Login-free Lens binary | 4.4k stars, **dead since 2023-06** | Demand signal for "no login" — 4.4k stars for a *build repo* |
| [k9s](https://github.com/derailed/k9s) | Terminal UI, single Go binary | **34.1k stars**, v0.51.0 (2026-06-06), Apache-2.0, `brew install derailed/k9s/k9s` | **One context at a time** (`:ctx` to switch); no aggregated fleet view; TUI ceiling for sharing/visualization |
| [Headlamp](https://github.com/kubernetes-sigs/headlamp) | Kubernetes SIG-UI web UI + desktop app, plugin system | 6.8k stars; v0.43.0 (2026-06-16); monthly releases; AI assistant via MCP; Artifact Hub plugin catalog ([2025 highlights, kubernetes.io](https://kubernetes.io/blog/2026/01/22/headlamp-in-2025-project-highlights/)) | Per-cluster-centric UX (multi-cluster registration exists; ClusterProfile inventory is alpha); no fleet correlation; no governed actions |
| [Aptakube](https://aptakube.com/) | Closed-source Tauri desktop client | Vendor claims "thousands" of users; **$9/mo personal / $7/seat teams, 15-day trial, no free tier**; installers 15–28 MB | Proves the paying gap: its headline claim is being "the **only** Kubernetes UI that can connect to multiple clusters simultaneously" and aggregate resources in one view, plus "no extra configuration… data never leaves your machine" ([aptakube.com](https://aptakube.com/), [lens-alternative page](https://aptakube.com/lens-alternative)). Closed and paid — the OSS slot for exactly this is **empty** |

**The pivotal ecosystem event (hand-verified):** the official **Kubernetes Dashboard is archived**, and the kubernetes.io blog names Headlamp the way forward *explicitly because of* "multi-cluster visibility … and flexible deployment options that work both in-cluster and on the desktop" ([From Kubernetes Dashboard to Headlamp, 2026-06-01](https://kubernetes.io/blog/2026/06/01/dashboard-to-headlamp/), authored by Will Case/Headlamp). Read as market evidence: the single-mode, single-cluster web console lost; the dual-mode, multi-cluster tool became the community default. Any Sith local mode is therefore **not** competing with a vacuum — Headlamp is CNCF-blessed and improving monthly. Sith's local mode must not be "another general console"; it must be the **fleet** view Headlamp doesn't center on (aggregation, correlation, staleness, governed actions) — see §5.

### Table-stakes for a Lens-class local tool (ranked by how often the evidence cites them)

1. Multi-cluster from existing kubeconfig, zero config ("if you're already using kubectl, it just works" — the Aptakube pitch).
2. Pod logs + exec/shell in core (their removal *created* the OpenLens exodus — [#6823](https://github.com/lensapp/lens/issues/6823)).
3. No account/login wall (4.4k stars on a build repo whose only feature was deleting the login).
4. No telemetry, or opt-in only; local-only data ("never leave your machine").
5. Fast on big clusters; low memory (Electron complaints are constant in Lens-alternative threads).
6. Resource browse + YAML edit, port-forward.
7. Open source under a permissive license, active cadence (CNCF-survey adoption barriers: abandonment fear, docs).
8. Aggregated multi-cluster single view — the one thing users can otherwise only buy (Aptakube).

## 2. Why the effortless tools feel effortless (transferable mechanics)

- **Latency budget:** 0.1 s = direct manipulation; 1 s = flow intact; 10 s = attention lost ([Nielsen/NN-g response-time limits](https://www.nngroup.com/articles/response-times-3-important-limits/)). Palette open, fuzzy search, and view switches must land under ~100 ms — only achievable when rendering from a **local cache/store**, not a per-keystroke round-trip to N API servers.
- **Local-first with background sync (the Linear mechanic):** the server is a sync target, not the UI's source of truth; the client hydrates a local store, every query hits it first, pages render in <50 ms with no spinners; deltas reconcile asynchronously ([How is Linear so fast](https://performance.dev/how-is-linear-so-fast-a-technical-breakdown); practitioner corroboration: [local-first rabbit hole](https://bytemash.net/posts/i-went-down-the-linear-rabbit-hole/)). Kubernetes has the perfect substrate for this: **watch streams into a local informer cache**. This is the single most important DX decision for Sith's local mode: cache-first render + staleness stamps, never spinner-first.
- **Keyboard-first + command palette:** the cmd-K bar bridges GUI discoverability and CLI speed and is the canonical pattern for serving both kubectl power users and GUI users in one product ([Maggie Appleton, Command K Bars](https://maggieappleton.com/command-bar)). k9s is the in-domain proof that keyboard-first wins operators (34.1k stars).
- **Install friction:** k9s (`brew install`, one binary, no server, no account) and Freelens (brew cask + winget/scoop/flatpak/deb/rpm, arm64+amd64) define the funnel. Tailscale's "install, sign in, connected — value on first run" is the canonical 10-minute-wow articulation ([why-tailscale](https://tailscale.com/why-tailscale)). Contrast: "helm install a platform, configure SSO, then see value" is the devops-portal/Backstage adoption cliff. **Sith's local mode must be a single artifact that shows a populated fleet view within minutes of `brew install`.**

## 3. Desktop technology: Electron vs Tauri vs neither

Benchmarks and production reports (all fetched):
- Same-app comparisons: Tauri installer ~2.5 MB vs Electron ~85 MB; idle RAM ~80 MB vs ~120 MB; cold start ~2 s vs ~4 s ([Authme dev, levminer.com](https://www.levminer.com/blog/tauri-vs-electron)). Minimal-app: 8.6 MiB vs 244 MiB bundle; ~172 MB vs ~409 MB with 6 windows; **startup difference negligible** in that test ([Hopp benchmark, 2025](https://www.gethopp.app/blog/tauri-vs-electron)).
- Counterweights: Tauri renders via OS webviews (WebView2/WKWebView/WebKitGTK) → **cross-platform rendering inconsistency** to manage, vs Electron's identical-everywhere Chromium; DoltHub stayed on Electron for packaging gaps (no .appx/.msix, no macOS universal binaries) while still concluding Tauri "eliminates much of the classic Electron bloat" ([DoltHub, 2025-11](https://www.dolthub.com/blog/2025-11-13-electron-vs-tauri/)); post-migration retrospective: [Fluxzy five months after](https://www.fluxzy.io/resources/blogs/electron-to-tauri-migration-fluxzy-desktop).
- Category proof: **Aptakube ships on Tauri** in exactly this product class, marketing the small/fast footprint against Electron-based Lens; other K8s clients on Tauri exist (JET Pilot, Kunobi — [awesome-tauri](https://github.com/tauri-apps/awesome-tauri)); Tauri's sidecar mechanism cleanly wraps an existing backend binary (DoltHub, Hopp).

**Recommendation:** don't make the webview choice the first decision. The proven architecture in this exact category is **Headlamp's**: one Go backend + one web frontend, identical in both modes, with the desktop app being a thin shell over the same code ([Headlamp architecture docs](https://headlamp.dev/docs/latest/development/architecture/)). Sith should ship a **single Go binary** whose `sith ui` serves the local web UI from the embedded frontend (k9s-grade install friction, no app-store/codesigning tax on day one), plus a first-class CLI. A **Tauri** shell (not Electron — memory/footprint evidence above, and Lens-refugee sensitivity to Electron bloat) is a fast-follow for dock presence/deep-OS integration, wrapping the same binary as a sidecar. This sequences the risk: the web UI must exist in both form factors anyway; the wrapper is additive.

## 4. One product or two? The dual-mode precedents

| Precedent | Shape | Verdict |
|---|---|---|
| **Headlamp** | Same Go backend + React frontend runs as single-user desktop app (local kubeconfigs) and as in-cluster multi-user web deployment; docs state the modes are "not mutually exclusive" — individuals use desktop while the org runs in-cluster ([installation docs](https://headlamp.dev/docs/latest/installation/), [architecture](https://headlamp.dev/docs/latest/development/architecture/)) | **Strongest validation.** The dual mode is *why* it won the Dashboard succession ([kubernetes.io, 2026-06-01](https://kubernetes.io/blog/2026/06/01/dashboard-to-headlamp/)) |
| **Portainer** | One server binary; solo homelab user runs it alone; the same server accepts agents for centralized multi-environment management; Business layers RBAC on the same core ([architecture docs](https://docs.portainer.io/start/architecture)) | Validates "same product, agents arrive later, governance is a layer" |
| **Grafana** | Same OSS core run locally/self-hosted; Cloud adds hosting + governance features ([oss-vs-cloud](https://grafana.com/oss-vs-cloud/)) | Validates monetizing/governing a tier above one core |
| **Teleport** | Desktop client (Teleport Connect, Electron) lives in the same monorepo as the server/Web UI, sharing infrastructure ([Teleport Connect docs](https://goteleport.com/docs/connect-your-client/teleport-clients/teleport-connect/)) | Weaker (client-of-server, not standalone local mode), but same-repo/shared-UI economics hold |

**Conclusion: one product, one binary, two modes.** Every relevant precedent shares code between local and served modes; the community's own Dashboard→Headlamp migration explicitly rewarded the dual mode. Two separate products would double surface area (the devops-portal failure mode) and break the land-and-expand path (§5).

## 5. The recommended shape for Sith

- **One Go binary, three run modes:** `sith` (CLI verbs), `sith ui` (local web UI on localhost, kubeconfig-direct, single user, zero config, no account, no telemetry), `sith hub` (the federated control plane serving the *same* UI multi-user with workspaces/governance). One frontend; the UI renders identically from a local cache (direct mode) or the hub's fleet model (federated mode).
- **The local mode's center of gravity is the fleet, not the pod.** Aggregated all-clusters resource views, fleet search/correlation ("which clusters run image X", "where is payments degraded"), staleness stamps, and the same typed-verb actions with dry-run/diff — locally self-approved, but the same intent model that later gains real governance. Per-pod table stakes (logs, exec, port-forward, YAML edit) must exist because their absence created the Lens exodus — but they are commodity K8s API calls, not integrations, and Sith should not chase Headlamp/k9s feature-for-feature beyond them.
- **The wow:** `brew install sith && sith ui` → all kubeconfig contexts detected → one aggregated fleet view with cmd-K fuzzy search across every cluster, under 10 minutes, offline-capable, nothing leaves the machine.
- **The expand:** when the team needs shared visibility and real approvals, the same binary becomes the hub; clusters upgrade from "direct (my kubeconfig)" to "minion (outbound OCM agent)" without the user relearning anything. Local mode is the top of the funnel for the governed wedge — not a second product.


---

<a id="part-3"></a>

## Part 3 — Tool landscape, cost, and multi-cloud gap map

**Date:** 2026-07-08 · **Method:** fetched primary sources with quotes; facts marked *(repo-verified)* were web-verified in this repository's [`COMPETITIVE.md`](../../COMPETITIVE.md) during the July 2026 planning pass and re-used here; hand-verified items are marked. Where pricing is quote-based or was not directly fetched, the row says so rather than inventing numbers.

The question this file answers: what does each incumbent actually provide and charge, and what is missing across *all* of them — the white space Sith can own.

---

## 1. Local / single-operator clients

Covered in depth in [Part 2](#part-2). Summary of the gap: **k9s** (34.1k stars, one context at a time — hand-verified), **Lens** (account wall, telemetry-with-opt-out, Plus $25/user/mo; Personal free under $10M), **OpenLens** (dead 2023), **Freelens** (healthy MIT fork, Electron, Lens-lineage single-cluster UX), **Headlamp** (SIG-UI, dual-mode, per-cluster-centric, plugin system), **Aptakube** (closed, $9/mo, the only aggregated multi-cluster single view), **kubectl** (the substrate everyone shells out to; no fleet semantics beyond contexts). **White space: an OSS, no-account, no-telemetry, aggregated multi-cluster client with fleet-level search/correlation.** Nobody occupies it; the closest occupant is closed and paid (Aptakube), and the community default (Headlamp) centers on per-cluster views.

## 2. GitOps / deploy layer (things Sith integrates with, never replaces)

| Tool | What it is | Relevant boundary facts |
|---|---|---|
| **Argo CD** | CNCF-graduated GitOps CD; REST/gRPC API + `Application` CRDs; its own multi-cluster story is being agent-ified by [argocd-agent](https://github.com/argoproj-labs/argocd-agent) (v0.9.0, 2026-06-04 *(repo-verified)*) | Argo federates *its own* surface only; no cross-tool governed actions. Sith's `argocd.sync|rollback` verbs ride its API |
| **Flux** | CNCF-graduated GitOps controllers; **CRD-only surface** (no API server) — integration = read/patch CRs | Same boundary: reconciler, not an ops control plane |
| **Helm / Kustomize** | Package/overlay standards; Helm state lives in in-cluster release Secrets | Read adapters for inventory ("what release/version is where"), not action targets in v1 |
| **Kargo** ([akuity/kargo](https://github.com/akuity/kargo)) | GitOps *promotion* orchestration; Apache-2.0, v1.10.8 (2026-06-25), ~3.4k stars; open-core (Kargo Enterprise by Akuity, inquiry pricing) | **Closest OSS analog to "governed promotion"** — stages with gates and Git as audit trail — but it promotes *artifacts through environments* via GitOps; it is Argo-ecosystem-coupled and does not execute typed live operations (restart/scale/drain) across heterogeneous fleets. The repo page itself doesn't substantiate multi-approver gates (verify against docs.kargo.io before citing specifics). Boundary, and validation that "promotion with gates" resonates |

The lesson the predecessor already paid for: re-skinning these tools is negative value. Sith reads their state into the fleet model and dispatches a **closed set of typed verbs** to their APIs — nothing else.

## 3. Fleet / platform incumbents

| Tool | Provides | Pricing/positioning | What it does NOT do |
|---|---|---|---|
| **Rancher / Rancher Prime (SUSE)** | Cluster provisioning + import (outbound `cattle-cluster-agent`), RBAC, catalog; Fleet = GitOps at scale; 2026: "Liz" agentic AI expanding to a crew (Linux/Observability/Security/Provisioning/Fleet) + MCP server integration ([SUSE KubeCon EU 2026 announcement](https://www.suse.com/c/kubecon-eu-2026-first-agentic-ecosystem-platform/)) | Prime = paid subscription ([SUSE shop](https://www.suse.com/shop/suse-rancher-prime/), per-node/per-vCPU tiers); 2025 repricing to a vCPU metric with **competitor-reported 4–9x increases driving alternatives-shopping** ([Portainer's account](https://www.portainer.io/blog/suse-rancher-price-hike-why-enterprises-are-searching-for-alternatives-in-2025) — rival-authored, treat accordingly) | No typed-intent action vocabulary, no multi-approver fan-out gates, no abstention semantics; agentic AI is subscription-gated assistance, not a neutral governance primitive. Imports ACK/CCE/TKE ([SUSE](https://www.suse.com/c/rancher_blog/announcing-added-support-for-leading-kubernetes-services-in-china/)) — feasibility proof for agent-based China coverage |
| **OpenShift + ACM (Red Hat)** | The enterprise platform; ACM is the multi-cluster manager **built on OCM upstream** *(repo-verified: OCM underpins ACM)* | Subscription platform; OpenShift-first | ACM governs via Policy CRDs + ManifestWork fan-out; OpenShift-centric, heavyweight, not a neutral primitive for arbitrary conformant clusters; no closed-verb typed actions with per-wave human gates |
| **Open Cluster Management (OCM)** | CNCF Sandbox; hub/spoke registration, Placement, ManifestWork, addons; `cluster-proxy` + `managed-serviceaccount` (both v0.10.0, 2026-02-02) give outbound-only reach + scoped identity *(repo-verified; M0 reproduced hands-on in [`docs/experiments/M0-ocm-falsification.md`](../experiments/M0-ocm-falsification.md))*; OCM v1.3.1 (2026-05-19) | OSS substrate, Red Hat-sponsored | **The critical finding (fetched, [ManifestWorkReplicaSet docs](https://open-cluster-management.io/docs/concepts/work-distribution/manifestworkreplicaset/)):** OCM already ships alpha **Progressive / ProgressivePerGroup rollout strategies** (minSuccessTime, progressDeadline, maxFailures) — so raw canary *sequencing* is NOT white space. What OCM has **no concept of**: approval gates, multi-approver workflows, typed/closed action vocabulary (ManifestWork = arbitrary YAML), operation-level audit ledger, abstention. Governance above OCM is the actual gap, and OCM's addon framework exists precisely so others build it ([CNCF comparison post](https://www.cncf.io/blog/2022/09/26/karmada-and-open-cluster-management-two-new-approaches-to-the-multicluster-fleet-management-challenge/)) |
| **Karmada** | CNCF; multi-cluster *scheduling/propagation*; push and pull modes (so "outbound agent" alone is weak differentiation) | OSS, Huawei-sponsored | Placement engine, not governed ops; same absence of approvals/typed verbs/audit |
| **Clusterpedia** | CNCF Sandbox; multi-cluster resource **search/inventory** (v0.9.1 *(repo-verified)*) | OSS | Read-only; no actions at all; validates fleet-search demand |
| **KubeSphere** | China-origin OSS platform (4.x "LuBan" architecture), first-class air-gap install ([docs](https://kubesphere.io/zh/docs/v3.3/installing-on-linux/introduction/air-gapped-installation/)) | Open core (QingCloud) | Platform breadth, China strength; not a neutral governed-action primitive; validates air-gap-first distribution in China |
| **Spectro Cloud Palette** | Full-stack cluster lifecycle via Cluster Profiles; VerteX/air-gap editions | Usage-based kilo-Core-hours, edge from ~$250/device/yr ([palette editions](https://www.spectrocloud.com/palette-editions)) | Proprietary, provisioning-centric; not an ops-action governance layer over existing clusters |
| **Devtron** | OSS K8s platform (CI/CD+GitOps+obs+security), v2.1.1 (2026-03-24) *(repo-verified)* | Open core | Batteries-included platform (the breadth strategy), not a narrow federation primitive |
| **Komodor** | SaaS K8s ops/AI-SRE; agent per cluster; Klaudia multi-agent (50+ specialized agents), MCP/OpenAPI extensibility, sandboxed + audited remediation ([press release, 2026-03-18](https://www.globenewswire.com/news-release/2026/03/18/3258257/0/en/komodor-introduces-extensible-autonomous-multi-agent-architecture-for-ai-driven-site-reliability-engineering.html)) | Node-based annual pricing ([pricing page](https://komodor.com/platform/pricing-and-plans/)); closed SaaS | The closest *product* to "operate the fleet with AI on top" — but closed, SaaS (China/air-gap excluded), diagnosis-first; no closed-vocabulary typed intents, no multi-approver canary waves as a neutral primitive |
| **Loft / vCluster** | Virtual clusters for hard multi-tenancy | Open core | Tenancy primitive, not fleet ops; complementary (and a GPU multi-tenancy voice: [vCluster on GPU sharing](https://www.vcluster.com/blog/ai-infrastructure-gpu-utilization-kubernetes-multitenancy)) |
| **kagent** | CNCF agent framework for K8s (v0.10.0-beta4 *(repo-verified)*) | OSS (Solo.io-originated) | Framework to *build* agents — a natural **MCP client of Sith**, not a governance layer |
| **Port / Cortex / Harness** | IDPs / delivery platforms (catalog, scorecards, self-service; Harness = CI/CD+FinOps suite) | Commercial | Different category (the predecessor's failed race). Boundary only: Sith is not a portal; portals can *consume* Sith's API/MCP |

## 4. Observability / logging (integrate-only; the telemetry-lake trap)

Prometheus/Grafana/Loki/Mimir, Elastic/OpenSearch/Kibana, Fluentd/Fluent Bit, Datadog, Splunk: each is a query/config surface for Sith to **read through** (per-tool mechanics and auth in [integrations-and-ai-governance.md](integrations-and-ai-governance.md)). The predecessor proved embedding/proxying them is negative value. The fleet-relevant gap none of them fills: they aggregate *telemetry*, not *operational state + actions* — none can answer "which clusters are running image X with a failing rollout and what changed there", and none dispatches governed actions. Datadog/Splunk cost pain is a recurring driver pushing teams toward self-hosted stacks (treated qualitatively here; no pricing figures were fetched in this pass — keep out of load-bearing claims).

## 5. Cost (workstream F): the fleet rollup is the gap, not the metering

Fetched evidence chain:

- **OpenCost** is the CNCF-incubating standard (promoted from Sandbox **2024-10-31**, [CNCF announcement](https://www.cncf.io/blog/2024/10/31/opencost-advances-to-the-cncf-incubator/)) for **per-cluster** allocation: workload-granularity costs from Prometheus + cloud-billing integrations, exported as metrics; the announcement describes no fleet-level aggregation; plugins pull external SaaS costs (Datadog, OpenAI, MongoDB Atlas).
- **The DIY rollup pain is documented first-person by Grafana Labs**: OpenCost "is designed to expect its storage having the same scope as its deployment" — so they run OpenCost per cluster, ship metrics to central Mimir, and had to insert `prom-label-proxy` so each instance sees only its own data; they list native multi-cluster support and multi-cluster query docs as missing ([Grafana Labs on OpenCost](https://grafana.com/blog/2023/02/02/how-grafana-labs-uses-and-contributes-to-opencost-the-open-source-project-for-real-time-cost-monitoring-in-kubernetes/)). A user request for multi-cluster/multi-cloud aggregation was triaged P3 and closed unresolved ([opencost#2638](https://github.com/opencost/opencost/issues/2638)).
- **Kubecost** (the commercial layer; **acquired by IBM, announced 2024-09-17**, [IBM newsroom](https://newsroom.ibm.com/blog-ibm-acquires-kubecost-to-broaden-hybrid-cloud-cost-management-capabilities), folding into the Apptio/Cloudability/Turbonomic FinOps suite): free tier is capped (250 cores, 15-day retention; 3.0's licensing gate ≈ $100k spend/trailing-30-days — [Kubecost 3.0 announcement](https://www.apptio.com/blog/ibm-kubecost-3-0-faster-smarter-and-built-for-scale/)); **"Unified, multi-cluster view" is an Enterprise-tier feature** ([nOps pricing teardown](https://www.nops.io/blog/kubecost-pricing/) — competitor-authored; its $70k–100k/3-cluster anecdote is unverified, treat as directional). 3.0 also moves to a unified IBM agent "eliminating the dependency on Prometheus" — i.e., the leading commercial tool is consolidating into a proprietary suite, not staying a neutral primitive.
- **CAST AI** is automation-first (rightsizing/spot/GPU optimization that *mutates* clusters), quote-based pricing only, and its own intake form lists just EKS/GKE/AKS/OpenShift-on-AWS — **no China clouds, no generic on-prem/air-gap** ([cast.ai/pricing](https://cast.ai/pricing/), fetched 2026-07-08; $108M Series C at ~$900M valuation, [TechCrunch 2025-04-30](https://techcrunch.com/2025/04/30/cast-ai-raises-108m-to-get-the-max-out-of-ai-kubernetes-and-other-workloads/)).
- **GPU:** Kubecost 2.4 does DCGM-based GPU efficiency/idle-cost per container ([Apptio GPU monitoring](https://www.apptio.com/blog/gpu-monitoring/)); MIG/fractional attribution remains beyond it (DCGM reports at physical-GPU level — [vCluster analysis](https://www.vcluster.com/blog/ai-infrastructure-gpu-utilization-kubernetes-multitenancy)); OpenCost GPU pricing has had correctness bugs ([opencost#2029](https://github.com/opencost/opencost/issues/2029)); the FinOps WG GPU paper is single-cluster-only ([FinOps WG](https://www.finops.org/wg/scaling-kubernetes-for-ai-ml-workloads-with-finops/)); Kubecost 3.0's GPU story is one line ("recommendations are GPU-aware").

**Verdict for Sith:** cost is a **read-overlay integration, not a wedge and not a build**. The move: deploy/read OpenCost (or its metrics) per cluster via the existing read federation, aggregate at the hub into per-workspace/per-team fleet rollups (GPU columns included where DCGM exists), stamp freshness like every other fleet fact. That lands precisely in the documented OSS gap (per-cluster standard exists; the free fleet view doesn't — it's paywalled at Kubecost Enterprise and unsolved in OpenCost). Building a metering/billing engine or automation-optimizer would re-fight OpenCost/Kubecost/CAST on their ground and violate the telemetry-lake non-goal.

## 6. Multi-cloud + China clouds (workstream G)

- **Conformance (hand-verified in [cncf/k8s-conformance](https://github.com/cncf/k8s-conformance)):** Alibaba ACK (`v1.32/alicloud`, "Alibaba Cloud Container Service for Kubernetes v1.32.0"), Huawei CCE (submissions every version v1.29–v1.34), Tencent TKE (`v1.34/tencentcloud`, v1.34.1). The Kubernetes API surface Sith depends on is uniform across US and China clouds.
- **Auth/enumeration is where clouds differ.** EKS: `aws eks list-clusters` + access entries + `aws eks get-token` exec plugin. AKS: `az aks list` + Entra ID + kubelogin. GKE: `gcloud container clusters list` + IAM + `gke-gcloud-auth-plugin` (+ Connect gateway for fleet reach). ACK: kubeconfigs per RAM user/role via `DescribeClusterUserKubeconfig`, configurable expiry/revocation, VPC-internal endpoints ([ACK docs](https://www.alibabacloud.com/help/en/ack/ack-managed-and-ack-dedicated/user-guide/obtain-the-kubeconfig-file-of-a-cluster-and-use-kubectl-to-connect-to-the-cluster)). CCE: IAM for cloud perms + standard kubeconfig for cluster RBAC, intranet/public endpoints ([CCE docs](https://support.huaweicloud.com/intl/en-us/usermanual-cce/cce_10_0187.html)). TKE: separate public/private kubeconfigs, CAM-integrated ([TKE docs](https://www.tencentcloud.com/document/product/457/30639)).
- **OpenShift:** conformant API + `oc`, Routes, SecurityContextConstraints; ACM occupies the fleet layer natively. Treat OpenShift as: conformant-API coverage guaranteed; deep OpenShift-isms (Routes/SCC-aware views) explicitly later; never compete for ACM-committed estates.
- **Recommended abstraction:** exactly two layers. (1) **Pure Kubernetes API** for everything inside a cluster — guaranteed by conformance, including China. (2) **Thin per-cloud adapters** for *enumeration and credential minting only* (list clusters; mint short-lived tokens via each cloud's mechanism; exec-plugin passthrough in local mode). Anything deeper per-cloud (node pools, cloud LBs, billing) is out of scope — that's the cloud console's job. The federated mode needs no cloud adapter at all: the minion dials out, so a TKE cluster behind restricted egress registers exactly like an EKS one (Rancher's ACK/CCE/TKE support proves the pattern; OCM's pull architecture is the same shape).

## 7. Air-gap distribution prior art

**Zarf** ([zarf-dev/zarf](https://github.com/zarf-dev/zarf), Apache-2.0, v0.80.0 2026-06-25, ~2k stars, Naval Postgraduate School lineage) owns air-gap *packaging*: single-file bundles of images/charts/manifests, embedded registry/Gitea, image-path rewriting. It is a delivery vehicle, not a control plane — **complement, not competitor**: Sith should ship an official Zarf package (or Zarf-style single artifact) as its China/regulated-market distribution, rather than building bespoke offline tooling.

## 8. The white space, stated precisely

Across every tool above, four candidate positions were tested against the evidence:

1. **Governed action federation as a neutral OSS primitive — EMPTY.** OCM has rollout mechanics but no approvals/typed verbs/audit; ACM/Rancher have platform-coupled policy; Kargo gates artifact promotion only; Komodor audits its own AI's remediation inside a closed SaaS. Nobody ships "typed intent + multi-approver + per-wave gates + abstention + signed dispatch + decision ledger" vendor-neutrally. This is Sith's wedge, confirmed.
2. **Fleet-wide live correlation/search — PARTIALLY OCCUPIED.** Clusterpedia (search) and every platform's inventory exist; correlation joined with actions and staleness semantics does not. Differentiating as part of the wedge, not alone.
3. **Fleet cost read-overlay — EMPTY IN OSS, PAYWALLED COMMERCIALLY.** (§5.) High-value fast-follow on top of read federation.
4. **OSS aggregated multi-cluster local client — EMPTY.** (§1, form-factor file.) The adoption on-ramp.

And one distribution property that cuts across all four: **air-gap-first, no-phone-home, multi-arch, registry-relocatable** — mandatory for China/regulated (see [Part 1](#part-1)), cheap if designed in from day one, and none of the SaaS incumbents can follow there.


---
