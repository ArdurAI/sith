# ADR-0002 — Stack & language

**Status:** Proposed · **Date:** 2026-07-08

## Context

Sith has three distinct surfaces with different constraints:
1. **Cluster-side** (the Sith spoke agent + anything that speaks to OCM/Kubernetes APIs).
2. **Control-plane / API** (the hub: read/action/policy federation, PEP, MCP server).
3. **UI** (an operator console — modest; not the product's center of gravity).

Constraints and facts:
- The **OCM and Kubernetes ecosystem is Go** — controller-runtime, client-go, the OCM
  addon-framework, `cluster-proxy`/`managed-serviceaccount`, kubebuilder. Anything that is a
  Kubernetes controller/addon or links OCM libraries is overwhelmingly least-friction in Go.
- Ardur (the PDP/runtime governance) is a **Go** project.
- The MCP ecosystem has mature SDKs in multiple languages; MCP servers are commonly written
  in TypeScript or Go/Python. Enforcement must be server-side regardless of SDK.
- The team is small; **fewer languages = less cognitive load**, but the *right* language per
  surface matters more than uniformity where the ecosystems pull hard (cluster-side → Go).

## Decision

- **Cluster-side (spoke agent, OCM/K8s integration): Go.** Non-negotiable — it lives in the
  Kubernetes ecosystem, links OCM/controller-runtime, and matches Ardur. This is where the
  ecosystem gravity is strongest and where correctness/security matter most.
- **Control-plane / API / MCP server: Go.** Chosen for (a) one language shared with the
  cluster-side and Ardur, (b) strong concurrency for fan-out dispatch, (c) a single binary
  and simple deployment, (d) a viable MCP server story. This keeps the security-critical
  core in **one** language and one review surface.
- **UI: a thin TypeScript/React console**, treated as a **client** of the governed API with
  **no privileged path** — it has exactly the governance the MCP surface does. Deliberately
  minimal; the product's value is the governed API, not the UI.
- **Datastore: PostgreSQL** for the control-plane state (fleet model cache, workspaces,
  intents, decisions, audit), chosen specifically because it supports the **row-level
  security (RLS)** backstop that [ADR-0003](0003-tenancy-isolation.md) requires. A cache
  (e.g. Redis) may back the fleet model / rate limits later.

**Guiding principle:** keep the **security-critical core (PEP, action federation, spoke
agent) in Go, in one place, small and heavily reviewed**; keep the UI thin and unprivileged.

## Consequences

**Positive**
- One primary language (Go) across the security-critical surfaces and shared with OCM +
  Ardur → less context-switching, shared libraries, one review discipline.
- Postgres RLS gives a **real DB-level tenant backstop** (a direct fix for a predecessor
  anti-pattern; see ADR-0003).
- Single-binary control plane simplifies deployment and supply-chain hardening.

**Negative / risks**
- Go's MCP server ecosystem is less mature than TypeScript's in some respects. Mitigation:
  keep the MCP layer a thin adapter over the same PEP; the SDK choice is reversible because
  enforcement is server-side and language-independent.
- A TS/React UI adds a second toolchain. Mitigation: keep it minimal and unprivileged;
  it can even be deferred behind the API + MCP surface in early phases.

## Alternatives considered

- **TypeScript/Node for the control plane** (to match a rich MCP/agent ecosystem).
  Rejected as the *core* language: it would split the security-critical code from the
  cluster-side Go and from Ardur, doubling the review surface for the most sensitive code.
  TS remains the choice for the thin UI.
- **Python for the control plane** (agent/AI ecosystem). Rejected for the core for the same
  split-surface reason and weaker single-binary/deploy story; may appear only in
  offline/analysis tooling, never on the enforcement path.
- **Rust** for the core. Attractive for safety, but ecosystem friction with OCM/K8s
  (Go-centric) and team velocity outweigh the benefit at this stage. Revisit only if a
  specific component demands it.

*This ADR governs the eventual implementation; no code is written in the planning phase.*
