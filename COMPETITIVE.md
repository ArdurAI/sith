# Sith — Competitive Landscape

**Status:** planning · **Date:** 2026-07-08 · **All facts web-verified July 2026** (sources at end)

This is an honest map, including where the space is **crowded**. The conclusion is *not*
"nobody does this" — several strong efforts converge on parts of it. The conclusion is that
**vendor-neutral, OSS, governed *action* federation as an adoptable primitive** — with the
AI as a client of the governance rather than the product — is a narrow but real gap, and
that Sith is defensible only if it stays narrow and gets the governance right.

---

## 1. The substrate we adopt (not compete with)

| Project | What it is | Verified status / version | Role for Sith |
|---|---|---|---|
| **Open Cluster Management (OCM)** | CNCF framework for multi-cluster management | **CNCF Sandbox** (accepted 2021-11-09); core `ocm` **v1.3.1** (2026-05-19) | The substrate — Sith builds on it |
| **OCM `cluster-proxy`** | Reverse tunnels managed→hub; hub reaches cluster-local services across isolated VPCs; automates konnectivity | **v0.10.0** (2026-02-02); "Service Proxy" added v0.9.0 | Adopted transport |
| **OCM `managed-serviceaccount`** | Syncs SAs to spokes, projects scoped tokens back to hub | **v0.10.0** (2026-02-02) | Adopted scoped identity |
| **Kubernetes SIG `apiserver-network-proxy` (Konnectivity)** | The underlying reverse-tunnel technique | Kubernetes SIG project | The pattern cluster-proxy automates |
| **Rancher `remotedialer`** | Outbound reverse tunnel (K3s/Rancher scale) | **v0.6.1** | Proves the pattern is commodity |

**Takeaway:** the "outbound-only agent + hub reaches cluster-local services" mechanism is
**commodity, hardened, CNCF/vendor-maintained plumbing** — building it bespoke would be
re-implementing security-sensitive infrastructure worse than three existing efforts. So it
is **out of scope** ([ADR-0001](docs/adr/0001-adopt-ocm-vs-bespoke-tunnel.md)).

## 2. Adjacent but different (clear category lines)

| Project | Category | Verified | Why it is *not* Sith |
|---|---|---|---|
| **Karmada** | Multi-cluster **scheduling / workload placement** | CNCF; **v1.18.1** (2026-06-30) | Places workloads; Sith governs *operations*, not placement |
| **Clusterpedia** | Multi-cluster **resource inventory / search** | CNCF Sandbox; **v0.9.1** (2026-04-16) | Read/search only; no governed action federation |
| **Headlamp** | Single-/multi-cluster **UI** | Kubernetes-sigs; **v0.43.0** (2026-06-16) | A console, not a governed action control plane |
| **Devtron** | OSS K8s **platform** (CI/CD + GitOps + obs + security) | **v2.1.1** (2026-03-24) | Batteries-included platform, not a narrow federation-governance layer |
| **argocd-agent** | Agent-based **Argo CD** multi-cluster | argoproj-labs; **v0.9.0** (2026-06-04) | Federates Argo CD's *own* surface; Sith is tool-agnostic + governed |
| **kagent** | CNCF K8s-native **agent framework** | **v0.10.0-beta4** (2026-07-06) | A framework to *build* agents; a potential **MCP client** of Sith, not a competitor |

## 3. The genuinely competitive zone (be honest — it is crowded)

The product space *above* the plumbing is occupied and converging on "federate existing
tools + govern + put AI on top":

- **Komodor** — Autonomous AI SRE. Shipped an **extensible multi-agent architecture**
  (2026-03-18) letting orgs bring their own tools/services/agents via **MCP or OpenAPI**,
  orchestrated alongside **50+ specialized agents**, with unified fleet visibility. This is
  the closest thing to "federate existing cluster-local tools + AI", **shipping and
  funded**.
- **SUSE Rancher Prime** — At **KubeCon EU 2026**, positioned as an **agentic AI
  ecosystem**: the "Liz" assistant became a **crew** of agents (Security/Observability/
  Platform/SLE/App), with **external MCP server** plug-in so third-party tools become
  "crew members", plus K3k virtual-cluster GPU multi-tenancy. Hyperscaler-scale, vendor
  platform.
- **Devtron** — OSS, multi-cluster, AI-native platform at scale.

**Honest read:** the *visibility + AI-assistant* layer is well-served and competitive. If
Sith tried to be "an AI SRE that sees your fleet", it would be entering Komodor's and
Rancher's strongest 2026 investment with none of their funding, data, or vendor weight.

## 4. Where the wedge is defensible

Sith does **not** win on visibility or on "most agents". It is defensible only on a narrow
axis the incumbents under-serve as an *adoptable, neutral primitive*:

1. **Governed *action* federation, not just visibility.** The incumbents excel at *see* and
   at *investigate*; safe, policy-gated, **typed-intent fan-out to N clusters** — with wave
   gates, multi-approver prod, partial-failure/rollback, and **federation-specific
   abstention** — is under-served as a clean primitive rather than one vendor's feature.
2. **AI as a *client of the governance*, not the product.** Komodor and Rancher put agents
   *on top*. Sith inverts it: it is a **governed MCP server** where the org's own agent and
   *any* external agent (Claude Code, Codex, kagent) inherit the **same** PDP, closed verb
   vocabulary, scoped identity, and audit. "A governed MCP gateway to your whole fleet" is a
   platform position, not a chatbot.
3. **Vendor-neutral, OSS, and deliberately narrow.** Not tied to one vendor's stack (SUSE)
   or one SaaS (Komodor). Built on a CNCF substrate (OCM), tool-agnostic, and scoped so
   tightly (no portal, no scheduler, no telemetry store, **no shell ever**) that it can be
   *correct* where a broad platform can only be *featureful*.
4. **Correctness of isolation + action safety as the product.** The hard, valuable,
   defensible thing is getting multi-tenant isolation, signed intents, per-spoke local
   enforcement, and least-privilege identity **right** — precisely where broad platforms
   carry more surface and more risk.

## 5. Risks to the thesis (stated plainly)

- **The gap is narrowing.** As OCM's addon ecosystem, argocd-agent, and vendor MCP surfaces
  mature, the space compresses. Milestone-0 + Phase-1 must validate demand fast.
- **Governance-of-action is contested at the edges** (AI gateways, vendor MCP governance).
  Sith's differentiation is the *fan-out policy* + *neutral OSS* + *action-federation*
  intersection — thinner than a whole product, which is exactly why it must stay narrow.
- **Execution risk is the isolation problem itself.** The single hardest part (correct
  multi-tenant isolation + safe action) is the part predecessors got wrong. This plan makes
  those day-one, structural requirements rather than later hardening.

## 6. One-line positioning

> **Komodor/Rancher put AI *on top* of the fleet. Sith puts *governance* under the fleet —
> and lets any AI be a governed client of it.**

---

## Sources (verified July 2026)

- OCM — CNCF status: <https://www.cncf.io/projects/open-cluster-management/> · project:
  <https://open-cluster-management.io/> · core repo: <https://github.com/open-cluster-management-io/ocm>
- OCM `cluster-proxy` (v0.10.0, reverse tunnels / cross-VPC / konnectivity):
  <https://github.com/open-cluster-management-io/cluster-proxy> · Service Proxy:
  <https://open-cluster-management.io/blog/2025/cluster-proxy-now-supports-service-proxy-an-easy-way-to-access-services-in-managed-clusters/>
- OCM `managed-serviceaccount` (v0.10.0):
  <https://github.com/open-cluster-management-io/managed-serviceaccount> ·
  <https://open-cluster-management.io/docs/getting-started/integration/managed-serviceaccount/>
- Konnectivity / `apiserver-network-proxy`: <https://github.com/kubernetes-sigs/apiserver-network-proxy>
- Rancher `remotedialer`: <https://github.com/rancher/remotedialer>
- Karmada (v1.18.1): <https://karmada.io/> · <https://github.com/karmada-io/karmada>
- Clusterpedia (v0.9.1): <https://github.com/clusterpedia-io/clusterpedia>
- Headlamp (v0.43.0): <https://github.com/kubernetes-sigs/headlamp>
- Devtron (v2.1.1): <https://github.com/devtron-labs/devtron>
- argocd-agent (v0.9.0): <https://github.com/argoproj-labs/argocd-agent>
- kagent (v0.10.0-beta4): <https://kagent.dev/> · <https://github.com/kagent-dev/kagent>
- Komodor extensible multi-agent + MCP/OpenAPI (2026-03-18):
  <https://komodor.com/blog/komodor-introduces-extensible-autonomous-multi-agent-architecture-for-ai-driven-site-reliability-engineering/>
- SUSE Rancher Prime agentic AI + MCP (KubeCon EU 2026):
  <https://www.suse.com/c/kubecon-eu-2026-prime-mcp-plug-and-play/> · <https://www.suse.com/c/kubecon-eu-2026-prime-ai-crews/>
- MCP tool annotations (hints, enforce server-side): <https://blog.modelcontextprotocol.io/posts/2026-03-16-tool-annotations/>
- MCP Elicitation (2025-06-18): <https://modelcontextprotocol.io/specification/2025-06-18/client/elicitation>
