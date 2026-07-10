# ADR-0004 — Typed-intent action model (closed vocabulary, no shell)

**Status:** Proposed · **Date:** 2026-07-08

## Context

The write path is the most dangerous part of any cross-cluster control plane: it is the
shortest route from "a request" (human or AI) to "something changed on production clusters",
multiplied by fan-out to N clusters. A prior prototype's write path had a shell
(`sh -lc` string interpolation → command injection → in-cluster RCE) and a **fail-open
"denylist of one"** for destructive tools — a forgotten classification became silently
auto-executable.

The market's AI-ops direction makes this worse: agents want to act. An agent on an
ungoverned write path is a loaded gun. MCP's own guidance is explicit that tool annotations
(`readOnlyHint`/`destructiveHint`) are **hints, not guarantees** — enforcement must be
server-side.

## Decision

**The only writes Sith performs are *typed intents* from a *closed verb vocabulary*.** There
is **no shell, no free-form `apply`, no secret/RBAC mutation** — ever, at any phase.

### The intent
```
Intent = {
  id, workspace, actor, verb ∈ CLOSED_VOCAB,
  targetSelector,        # resolved ONLY within the actor's workspace (ADR-0003)
  args,                  # typed + JSON-schema-validated per verb
  justification, evidenceRefs,
  signature              # signed by the hub; verified independently by each spoke
}
```

### The closed vocabulary (initial)
```
argocd.sync | argocd.rollback
rollout.promote | rollout.abort
deployment.scale | deployment.restart
gitops.open-pr
```
**Permanently excluded:** `exec`/shell, arbitrary `kubectl apply`, Secret create/mutate/read,
RBAC object mutation. Adding *any* verb is an ADR-level change.

### Enforcement rules
1. **Fail-safe allowlist, not fail-open denylist.** A verb executes only if it is explicitly
   in the vocabulary with a registered, schema-validated handler. Unknown verb / invalid
   args → **refuse**. A **CI test asserts every handler that reaches a write path is
   classified** — a forgotten classification fails the build, not production.
2. **Structured execution, never string interpolation.** Verbs map to typed API calls
   (Argo CD API, Rollouts API, scale subresource, a Git PR). No argument is ever
   concatenated into a shell command — there is no shell.
3. **Dry-run first** for every verb that supports it; surface the plan/diff, then require a
   separate explicit step to execute.
4. **`gitops.open-pr` is the safest first write and ships first** — it is a *proposal a
   human merges*, requiring zero new standing trust; perfect for an AI and consistent with
   GitOps orthodoxy. Live mutations are enabled per-workspace only after the PR path is
   proven.
5. **Signed dispatch + independent spoke re-validation.** The hub signs the intent; each
   spoke agent **verifies the signature** and **re-validates against its own local
   allowlist**, then executes with its **own scoped local identity**. A spoke **never
   blindly runs what the hub sends** — two independent blast-radius bounds (hub vocabulary +
   spoke allowlist/RBAC).
6. **Fan-out is governed** (see [ADR-0005](0005-ai-mcp-ardur-pdp.md) and
   [`../ARCHITECTURE.md`](../ARCHITECTURE.md) §4.3): environment gates, wave ordering with a
   per-wave gate, partial-failure/auto-rollback, idempotency/dedupe, and abstention on stale
   views.
7. **Everything audited + decision-ledgered** (proposed → approved → dry-run → executed).

## Consequences

**Positive**
- The catastrophic classes (shell RCE, arbitrary apply, secret/RBAC tamper) **cannot occur**
  — they are not expressible in the model.
- Every write is typed, schema-checked, signed, re-validated, and reversible-by-design where
  possible (PR-first, dry-run-first).
- The same model governs human and AI actors identically.

**Negative / cost**
- Expressiveness is deliberately limited — some legitimate operations are simply "not a verb
  yet". Accepted: adding a verb is a considered, reviewed act. This friction is the feature.
- Per-verb handlers + schemas + spoke-side allowlist entries are more work than a generic
  `apply`. Accepted — genericity is exactly the danger.

## Alternatives considered
- **Generic `kubectl apply` / raw manifest writes.** Rejected: unbounded blast radius; the
  predecessor's YAML-apply route was a privilege-escalation vector even with a `Secret`
  refusal. Genericity defeats the whole safety model.
- **Denylist of dangerous tools.** Rejected: fail-open; one omission = auto-executable. The
  predecessor's "denylist of one" is the canonical failure.
- **Let the AI emit `kubectl`/shell and sandbox it.** Rejected: sandboxing a shell is a
  losing arms race; typed verbs remove the need entirely. `exec` is permanently out.
