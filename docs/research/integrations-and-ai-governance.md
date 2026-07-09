# Integration framework and AI-agent governance (workstreams H + I)

**Date:** 2026-07-09 · **Method:** deep-research fan-out; **[3-0]** = adversarially verified, **[fetched]** = pulled from the primary source, vote rate-limited. Every load-bearing claim carries a URL.

Answers: how to design the connector framework, the per-tool integration mechanism and priority, and whether "governed MCP server for fleet actions" is a real, empty position.

---

## 1. Connector-framework design principles (what scaled, what drowned)

**Grafana's model scaled — copy it.**
- **[3-0]** Grafana runs backend plugins **out-of-process as subprocesses over gRPC** (HashiCorp go-plugin), so "a panic in a plugin doesn't panic the server" ([Grafana backend plugins](https://grafana.com/developers/plugin-tools/key-concepts/backend-plugins/)).
- **[3-0]** The plugin contract is a **small fixed set of typed capabilities** (query, resources, health, metrics, stream) — narrow schema, not arbitrary host access.
- **[3-0]** It's **SDK-first**: authors code against `grafana-plugin-sdk-go`, which hides the wire protocol — the mechanism that let Grafana evolve the protocol without breaking the ecosystem ([grafana#19667](https://github.com/grafana/grafana/issues/19667)).

**Terraform confirms the versioning discipline.**
- **[3-0]** The provider protocol is a **versioned, typed gRPC/protobuf interface**; **major versions delineate compatibility, minor versions are strictly additive (non-breaking)** ([Terraform plugin protocol](https://developer.hashicorp.com/terraform/plugin/terraform-plugin-protocol)).

**Backstage is the cautionary tale — in-process, unversioned, drowned in maintenance.**
- **[fetched]** A BackstageCon EU 2026 maintainer panel (Red Hat/DoorDash/OP Financial/Vodafone Ziggo): the marketplace has **250+ plugins, many unmaintained**; **breaking changes ship within minor releases despite semver** (1.48→1.49), stranding adopters several releases behind; the React Router 6→7 migration was especially costly; adopters must write **custom React plugins**, taking on frontend dev on top of their real job ([panel writeup](https://tldrecap.tech/posts/2026/backstagecon-europe/backstage-plugin-ecosystem-sustainability/)). Proposed fixes: **bind backend plugins to OpenAPI schemas** for stronger break detection; **one canonical plugin per target** (not many overlapping); **quality-tier/ownership signaling**.

**Transferable principles for Sith's connector framework:**
1. **Out-of-process, typed, versioned.** gRPC/protobuf connectors with a stable minor-additive contract (Grafana + Terraform). A crashing connector must not take the hub down.
2. **SDK-first, protocol hidden.** Authors implement a narrow interface; Sith owns the wire format and can evolve it.
3. **Three fixed connector kinds, not open-ended:** **read adapter** (pull normalized facts into the fleet model), **brokered read-through** (proxy to a tool's own UI/API via cluster-proxy — no re-skinning), and **typed-action adapter** (map a closed verb to the tool's API). Everything is one of these three; nothing gets arbitrary host access.
4. **One canonical connector per tool.** Backstage's redundancy-and-abandonment failure is the thing to prevent structurally.
5. **Action semantics belong in the schema.** Flux's HelmRelease encodes drift-detection and remediation (retry/rollback/uninstall) as **typed fields** ([Flux HelmRelease](https://fluxcd.io/flux/components/helm/helmreleases/)) — proof that guardrails can live in the connector contract, not imperative glue.

## 2. Per-tool integration surface, verified

| Tool | Mechanism (verified) | Auth | Classification |
|---|---|---|---|
| **Argo CD** | **[3-0]** REST API w/ Swagger at `/swagger-ui` ([API docs](https://argo-cd.readthedocs.io/en/latest/developer-guide/api-docs/)) | **[3-0]** Bearer JWT via `/api/v1/session` | Read adapter **+ typed actions** (`argocd.sync\|rollback`) — day 1 |
| **Flux** | **[3-0]** **CRD-only** (`helm.toolkit.fluxcd.io/v2`); integrate by patching CRs + reading `.status` ([Flux](https://fluxcd.io/flux/components/helm/helmreleases/)) | Cluster RBAC | Read adapter (CRD), typed action later |
| **Helm** | **[3-0]** No server API — release state is **Kubernetes Secrets** in the release namespace ([Helm advanced](https://helm.sh/docs/topics/advanced/)) | Cluster RBAC | Read adapter via K8s API — day 1, free |
| **Prometheus** | **[3-0]** Stable `/api/v1`, **non-breaking additions only**; destructive TSDB ops segregated under `/admin` and **disabled by default** ([Prom API](https://prometheus.io/docs/prometheus/latest/querying/api/)) | proxy/none | Read adapter — day 1, low-churn |
| **Loki** | **[3-0]** Versioned `/loki/api/v1/query[_range]`, LogQL; **no built-in authz** — front it yourself ([Loki API](https://grafana.com/docs/loki/latest/reference/loki-http-api/)) | external | Read adapter |
| **Grafana** | **[3-0]** HTTP API w/ **service-account tokens** (replaced API keys) ([Grafana SA](https://grafana.com/docs/grafana/latest/administration/service-accounts/)) | SA token | Brokered read-through (link/deep-link; don't re-skin) |
| **GitHub/GitLab** | REST/GraphQL; GitHub App > PAT for scoping | App/OIDC | Typed action host for `gitops.open-pr` — day 1 (the first write) |
| **Terraform/OpenTofu** | State + HCP/Cloud API; providers via the protocol above | tokens | Read adapter (state/drift) later; never a Terraform runner |
| **Datadog / Splunk / Elastic/OpenSearch** | REST/query APIs | API/app keys | Read adapter, later; Datadog cost pain is a *pull* driver, not a reason to embed |
| **Fluentd / Fluent Bit** | **Config-only, no query API** | — | **Skip** as a data source (they ship logs *to* Loki/Elastic; read those instead) |
| **Jira / Zendesk / ServiceNow** | REST APIs | tokens | Typed action (open a change ticket for a fleet action) — later; ITSM change-linkage is real demand but not wedge |

**Priority rule (from the wedge):** day-1 integrations are the ones that (a) feed the fleet model cheaply via the K8s API or a stable REST endpoint, or (b) host the first governed write. That's **Argo CD, Flux, Helm, Prometheus, Loki, and GitHub** — everything else is demand-ranked fast-follow, and Fluentd/Fluent Bit are skipped as sources.

## 3. MCP protocol state (2026) — the protocol gives vocabulary, not enforcement

- **[3-0]** Tool annotations (`readOnlyHint`/`destructiveHint`/`idempotentHint`/`openWorldHint`) shipped in the **2025-03-26** revision ([MCP annotations post](https://blog.modelcontextprotocol.io/posts/2026-03-16-tool-annotations/)).
- **[3-0]** The spec is explicit that **annotations are hints, untrusted unless from a trusted server** — a *risk vocabulary, not a security control*.
- **[2-0]** MCP maintainers state safety guarantees **must be enforced by deterministic controls outside the protocol** (network controls, sandboxing) — official acknowledgment that **governance is out of scope for the protocol**, left to gateways/governed servers.
- **[fetched]** Timeline: **2025-06-18** added **elicitation** (server-initiated mid-flow user input — the primitive HITL approval builds on) and formalized MCP servers as **OAuth Resource Servers**; **2025-11-25** matured the OAuth/OIDC-discovery authz stack and extended elicitation ([2025-06-18 changelog](https://modelcontextprotocol.io/specification/2025-06-18/changelog), [2025-11-25 changelog](https://modelcontextprotocol.io/specification/2025-11-25/changelog)). **Governance-relevant changes stop at auth + scope consent** — no policy/approval/blast-radius primitives.
- **[fetched]** **2025-12-09:** Anthropic donated MCP to the **Agentic AI Foundation**, a Linux Foundation directed fund (co-founded with Block and OpenAI; supported by Google, Microsoft, AWS, Cloudflare, Bloomberg), putting the protocol under "the same neutral stewardship that supports Kubernetes" ([MCP blog](https://blog.modelcontextprotocol.io/posts/2025-12-09-mcp-joins-agentic-ai-foundation/), [LF press release](https://www.linuxfoundation.org/press/linux-foundation-announces-the-formation-of-the-agentic-ai-foundation)). MCP is now vendor-neutral infrastructure — which *strengthens* the case for a neutral OSS governance layer on top: the protocol is a shared standard, the governance of what agents may do through it is still unbuilt.

**Implication:** Sith's charter is right that MCP annotations are hints and enforcement must be server-side. The elicitation primitive is the correct native shape for the approval gate. The protocol will not govern for you — the governed MCP *server* is the product.

## 4. Who governs AI agents on clusters today — the position is open

**The demand is real and incident-backed:**
- **[fetched]** July 2025: **Replit's AI agent deleted the production database** of SaaStr's founder during "vibe coding", **despite explicit instructions not to change anything**; Replit's tooling then wrongly claimed the deletion was unrecoverable ([The Register](https://www.theregister.com/2025/07/21/replit_saastr_vibe_coding_incident/)). The canonical "agent damaged prod" incident.
- **[fetched]** Third-party synthesis of KubeCon EU 2026: agentic AI was *the* story, agents proliferating, and "**nobody has quite figured out how to manage and secure them inside Kubernetes yet**".

**Every AI-SRE incumbent stops at advise/diagnose or autonomy-first — none claims governed, approval-gated action as a neutral primitive:**
- **[fetched]** **kagent** (CNCF Sandbox, donated by Solo.io Apr 2025) is a *framework to run agents in-cluster* (agents as CRDs) — a runtime, **not** governance; a natural **MCP client of Sith** ([CNCF kagent](https://www.cncf.io/blog/2025/04/15/cncf-welcomes-kagent/)).
- **[fetched]** **HolmesGPT** stops at "natural-language diagnosis and remediation steps" — launch post has zero mentions of human/approval/guardrail/RBAC.
- **[fetched]** **Komodor** Klaudia multi-agent (GA 2026-03-18): the press release has **no mention** of approval/human-in-the-loop/guardrail/policy/governance/audit/RBAC — autonomy-first.
- **[fetched]** **Rancher "Liz" crew** (KubeCon EU 2026) is framed as **advisory** (insights/recommendations), no language about executing changes, no approval/guardrail primitives.

**MCP gateways enforce auth + tool allowlists + audit — not fleet-aware, approval-gated action:**
- **[fetched]** **Kong AI Gateway 3.13** MCP Tool ACLs = identity-based per-tool allow/deny, default-deny, audit — Kong's own scope statement mentions **no human-approval, no HITL, no stop-mid-action** ([Kong MCP ACLs](https://konghq.com/blog/product-releases/mcp-tool-acls-ai-gateway)).
- **[fetched]** **Solo agentgateway + kagent** (closest K8s-native stack) = OIDC + token exchange + AccessPolicies binding which agent calls which tool; **no approval gates or action controls**; governance is in the **commercial** tier, leaving **OSS-native agent governance unoccupied** ([Solo kagent security](https://docs.solo.io/kagent/latest/security/)).
- **[fetched]** **MintMCP** claims generic tool-level approval gating and read/write enforcement — but **"generic per-tool rules with no fleet or cluster semantics"**; its only K8s reference is hosting its own connectors ([MintMCP agent gateway](https://www.mintmcp.com/blog/agent-gateway)).
- **[fetched]** **Permit.io** ships an "Access Request MCP" for human-approval-before-action — but **domain-generic** (copilots, support bots); the page has **no mention of Kubernetes/clusters/fleet** ([Permit.io](https://docs.permit.io/ai-security/access-request-mcp/overview/)). LangSmith/Langfuse give **observability/tracing**, not control.

**The precise white space (triangulated from all of the above):** *approval-gated, fleet-aware, blast-radius-conscious governance of agent actions on Kubernetes clusters, as a vendor-neutral OSS primitive.* Generic approval gating exists (Permit.io, MintMCP). Tool allowlists + audit exist (Kong, Solo). Agent runtimes exist (kagent). Autonomy-first AI-SRE exists (Komodor, Rancher, Holmes). **The intersection — typed cluster-verbs + multi-approver + canary waves + abstention + signed dispatch + decision-ledger, applied identically to humans and to any MCP-client agent — is claimed by no one.** That is Sith's position, and the KubeCon EU 2026 read ("nobody has figured out how to manage/secure agents in K8s yet") is the market saying the door is open.

## 5. What this means for Sith's plan

- **Connector framework:** out-of-process gRPC, SDK-first, three fixed kinds, one canonical connector per tool, minor-additive versioning. This is a *fast-follow* deliverable — the wedge ships with hand-written adapters for the day-1 six; the framework generalizes them once the shape is proven (avoid Backstage's premature-ecosystem trap).
- **MCP server is the wedge's amplifier, not a separate bet:** because Sith enforces server-side through the same PEP, exposing the fleet as a governed MCP server makes *every* external agent (Claude Code, Codex, kagent) inherit the governance for free. That is the "governed MCP gateway to your whole fleet" position, and §4 shows it is empty.
- **Sith as MCP *client*** (calling kagent/Grafana MCP/GitHub MCP) is a later convenience, not the wedge.
- **The charter's AI stance is confirmed by evidence:** annotations are hints (verified), enforcement must be server-side (verified), elicitation is the HITL primitive (verified), and no incumbent occupies governed fleet action (verified). Hold the line: closed verb vocabulary, no shell, AI as client of the same PEP.
