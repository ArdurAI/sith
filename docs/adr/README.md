# Architecture Decision Records

ADRs capture *significant, hard-to-reverse* decisions with their context and consequences.
Adding a verb to the action vocabulary, taking on an OCM addon dependency, or relaxing a
safe default is an ADR-level change.

Format: Status · Context · Decision · Consequences · Alternatives considered. Where a
decision rests on an external fact, that fact is web-verified and cited (see also
[`../../COMPETITIVE.md`](../../COMPETITIVE.md)).

| ADR | Title | Status |
|---|---|---|
| [0001](0001-adopt-ocm-vs-bespoke-tunnel.md) | Adopt OCM as the substrate (vs. a bespoke tunnel/agent) | Proposed |
| [0002](0002-stack-and-language.md) | Stack & language (cluster-side Go; control-plane/API/UI) | Proposed |
| [0003](0003-tenancy-isolation.md) | Tenancy model & multi-tenant isolation | Proposed |
| [0004](0004-typed-intent-action-model.md) | Typed-intent action model (closed vocabulary, no shell) | Proposed |
| [0005](0005-ai-mcp-ardur-pdp.md) | AI / MCP server surface & Ardur as the PDP | Proposed |
| [0006](0006-credential-key-custody.md) | Credential & key custody | Proposed |

All ADRs are **Proposed** — this is the planning phase; the owner reviews before any
implementation. Milestone-0's falsification result will move 0001 to Accepted/Rejected.
