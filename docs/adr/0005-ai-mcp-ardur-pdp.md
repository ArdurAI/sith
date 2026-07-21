# ADR-0005 — AI / MCP server surface & Ardur as the PDP

**Status:** Proposed · **Date:** 2026-07-08

## Context

Sith should let AI agents operate the fleet — but the 2026 danger is real: agents on
ungoverned control planes are the largest "prompt → fleet action" risk. The market
(Komodor, SUSE Rancher Prime — verified in [`../../COMPETITIVE.md`](../../COMPETITIVE.md))
puts agents *on top* of the fleet. Sith inverts this: **the AI is a *client* of the
governance, not the product.**

Verified MCP facts (July 2026):
- Tool annotations (`readOnlyHint`/`destructiveHint`/`idempotentHint`/`openWorldHint`)
  shipped in the **2025-03-26** spec and are **hints, not guarantees — enforce server-side**.
- **Elicitation** (a server requesting structured user input mid-flow via `elicitation/create`)
  shipped in the **2025-06-18** spec. The latest published
  [**2025-11-25** contract](https://modelcontextprotocol.io/specification/2025-11-25/client/elicitation)
  adds URL mode;
  Sith uses form mode with a constrained schema for non-secret human approval. MCP does not define
  the lifetime of the resulting server-side grant, so Sith enforces that boundary independently.

Ardur (ArdurAI's runtime-governance runtime) is purpose-built to be a policy decision point,
identity broker, and decision-ledger for agent actions.

## Decision

### Expose Sith as a governed MCP server
- **Read tools** (`fleet.inventory`, `fleet.health`, `fleet.correlate`, `fleet.cve-search`,
  …) carry `readOnlyHint: true` and hit the fleet model; scoped to the caller's workspace.
- **Write tools** map **1:1 to the closed verb vocabulary** ([ADR-0004](0004-typed-intent-action-model.md)),
  carry `destructiveHint: true` (+ correct `idempotentHint`), and require **Elicitation-based
  approval bound to a hash of the resolved args** (the agent cannot approve-then-swap).
  `intent.gitops-open-pr` ships first.
- The durable approval is single-use and valid for one immutable absolute 10-minute window.
  PostgreSQL statement time mints and consumes it; the same atomic update requires
  `approved_at <= consumed_at < expires_at`, and the lifecycle evidence digest binds the expiry.
- **Annotations are hints ⇒ enforcement is server-side.** The MCP layer is a **thin adapter
  over the same PEP** the UI uses. There is no privileged agent path: an external agent
  (Claude Code, Codex, kagent) gets **exactly** the governance a human does.

### Ardur is the policy decision point (and identity broker, and decision-ledger)
At the `executeIntent` boundary, the PEP asks Ardur for every intent:
> *May `{actor}` issue `{verb}` on `{resolved targets}` in `{workspace}` right now?*
→ **allow / deny / require-approval(s)** (fan-out aware: env gates, multi-approver, caps).

- **PDP:** replaces any hardcoded "which tools need approval" with versioned, per-tenant,
  fan-out-aware policy.
- **Identity broker:** Ardur mints the **short-lived, per-action, scoped execution identity**
  so the AI/agent **never holds a cluster credential** and its ceiling is **strictly below**
  the human's. (Complements OCM `managed-serviceaccount` on the spoke side.)
- **Decision-ledger:** Ardur records **why** each action was allowed, complementing Sith's
  **audit-log** (what happened). Together = a complete agent-action record.

### AI safety rules (baked in)
- **Ground-or-abstain:** any statement about live state must be backed by a tool result or
  flagged as general knowledge; a write may be *proposed* only from an evidence-citing chain.
  Low confidence → "here's what I'd check", never a write.
- **Explicit "I won't act" is a first-class, logged outcome**, not an error (ties to
  federation abstention in [ADR-0004](0004-typed-intent-action-model.md) / architecture §4.3).
- **Per-tenant/per-actor token + action budgets** in the harness; write proposals rate-limited
  separately from reads.
- **The MCP write surface is the most-hardened surface in the system** (see
  [`../THREAT-MODEL.md`](../THREAT-MODEL.md) §3 S7).

### Build the seam early
A **policy hook at the `executeIntent` boundary exists from Phase 1** (returns "allow" for
reads), so Ardur drops in for Phase 2 writes without re-architecture.

## Consequences

**Positive**
- "**A governed MCP gateway to your whole fleet**" — a platform position, not a chatbot. Any
  agent inherits the org's guardrails.
- Human and agent actions are governed by one PDP, one vocabulary, one audit + ledger.
- Structural safety: the AI never holds a credential, never gets a shell, cannot bypass the PEP.

**Negative / risks**
- Dependency on Ardur's readiness/interfaces. Mitigation: the policy-hook seam abstracts the
  PDP; a minimal built-in policy can stand in until Ardur is wired, then be replaced.
- MCP is young and evolving (Elicitation is explicitly early). Mitigation: server-side
  enforcement is SDK-independent; annotation/elicitation are UX on top of hard gates.
- An MCP server is an attack surface. Mitigation: it is the most-hardened surface; same PEP;
  tighter write rate limits; `gitops.open-pr`-first.

## Alternatives considered
- **AI as a first-class actor with its own privileged path.** Rejected: creates a governance
  bypass; the whole thesis is that the AI is a *client* of the governance.
- **Portal-embedded assistant only (MCP client, not server).** Rejected: lower leverage;
  meeting developers in their own agent (via an MCP server) + inheriting governance is the
  differentiator.
- **Trust MCP annotations as enforcement.** Rejected outright: annotations are hints; the
  spec says enforce server-side. Sith enforces at the PEP.
