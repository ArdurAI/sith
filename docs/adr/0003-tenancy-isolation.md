# ADR-0003 — Tenancy model & multi-tenant isolation

**Status:** Proposed · **Date:** 2026-07-08

## Context

Sith is multi-tenant: many teams/tenants share one hub, each governing its own set of
clusters. Isolation *is* the product — a control plane that can see and act across many
tenants' fleets must never leak or act across the tenant boundary.

A prior control-plane prototype failed exactly here, in instructive ways (documented
vendor-neutrally in [`../THREAT-MODEL.md`](../THREAT-MODEL.md) §7):
- authorization derived from **spoofable request headers** with no server-side re-check →
  cross-tenant IDOR + privilege escalation;
- an advertised **RLS backstop that was inert** (dead code; app connected as the table
  owner) → no independent DB-level enforcement;
- app-layer scoping that covered **only some models**, with a forgotten filter one bug away
  from a silent cross-tenant leak.

These must be structurally impossible in Sith.

## Decision

### Tenancy model
- **`Workspace` is the single scoped tenancy object.** Everything (clusters, policies,
  intents, decisions, audit, memberships, fleet facts) belongs to exactly one workspace.
  Tenancy is **"a workspace over many clusters"**, never "one deployment per cluster".
- A **`Membership`** grants a subject a **role** within a workspace
  (`reader | operator | approver | admin`). Cluster membership in a workspace is explicit.

### Isolation — defense in depth (all three, from day one)
1. **Authn/authz from signed token claims only — never request headers.** Tenant and role
   come from the cryptographically-verified session/token (`memberships[workspace] → role`).
   Request headers are **never** trusted for identity/role/tenant; any inbound
   `x-*-role`/`x-*-tenant` are stripped/ignored.
2. **Application-layer scoping on every query**, via a tenant-aware data access layer that
   injects the workspace scope and **hard-fails on mismatch** — covering **all**
   workspace-scoped models, not a subset. A CI guard forbids un-scoped access to
   workspace-scoped tables.
3. **Database-level backstop: PostgreSQL Row-Level Security (RLS), actually enforced.**
   - The app connects as a **non-owner role** (owners bypass RLS).
   - `ENABLE` **and** `FORCE ROW LEVEL SECURITY` on every workspace-scoped table.
   - The current workspace is set **per request inside the transaction**
     (`set_config('sith.workspace_id', …, true)`), and policies check it.
   - This layer catches any application-layer mistake independently — it is the backstop the
     predecessor advertised but never turned on.

### Target resolution
- An intent's `targetSelector` is resolved **only against clusters in the actor's
  workspace.** Cross-workspace targets are impossible by construction, not by filter.

## Consequences

**Positive**
- Cross-tenant access requires defeating **three independent layers** (signed token +
  app scope + DB RLS). No single bug leaks tenants.
- The most dangerous predecessor bug (header-trust IDOR) is structurally gone.
- Isolation is testable as a first-class property (see below).

**Negative / cost**
- RLS adds operational discipline (two DB roles, per-request `set_config`, transaction
  hygiene) and a small performance cost. Accepted — it is the difference between a real and
  a theatrical backstop.
- Every new workspace-scoped table must be wired into all three layers; the CI guard makes
  omissions fail the build.

## Verification (isolation is a primary test target)
- A cross-workspace read/write attempt is **denied at the DB layer** even if the app layer
  is deliberately bypassed in the test.
- A forged/absent token is rejected; a header-injected role has **no effect**.
- Fuzz `targetSelector` with foreign cluster IDs → always resolves to empty within-workspace.

## Alternatives considered
- **App-layer scoping only** (no DB backstop). Rejected — this is precisely the predecessor
  failure; one forgotten filter = silent cross-tenant leak with nothing behind it.
- **Hard isolation via separate DB/schema per tenant.** Stronger but heavier operationally;
  revisit for an enterprise tier. RLS + app-scope is the right default for many tenants.
- **`vCluster`-style control-plane isolation per tenant.** Overkill for the control-plane
  data model; relevant (if at all) only to how spokes are isolated, which is the spoke's own
  concern.
