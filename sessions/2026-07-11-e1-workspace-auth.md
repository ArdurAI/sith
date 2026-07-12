# Session — 2026-07-11 — e1-workspace-auth

**Builder:** Gnani Rahul · **Model/effort:** GPT-5, max · **Branch:** gnanirahulnutakki/feat/e1-workspace-auth
**Slice(s):** Phase 1 / E1 / issue #7 · **Status:** in-progress

---

[G] Goal: Implement the first Phase-1 isolation layer: the Workspace tenancy anchor, signed-token-only identity and membership claims, exact least-privilege roles, and a hard-fail application scope with no request-header trust.
[S] Scope: Immutable tenancy/authentication contracts and HTTP middleware for issue #7. PostgreSQL persistence and FORCE RLS (#8), the destructive negative-control isolation suite (#12), OIDC/JWKS discovery, and the OCM read adapter remain separate follow-on slices.
[A] Action: Started from post-M0-closeout `dev` commit `30aadc7` after PR #74 and its full post-merge CI passed. Reconciled issue #7, E1, ADR-0003, the existing workspace-stamped fleet/cache seams, and the local keychain MCP token boundary. Selected a static Ed25519 JWT session profile behind a verifier interface so future OIDC/JWKS providers cannot change downstream authorization semantics.
[T] Test: Current primary sources verified golang-jwt v5.3.1 and the RFC 8725 requirements to pin algorithms, validate issuer/subject/audience, use explicit token typing, and keep validation rules mutually exclusive.
[A] Action: Added validated `Workspace`, `Membership`, `Principal`, and `Scope` contracts with defensive membership copies, an exact reader/operator/approver/admin action matrix, and a generic hard-fail guard for foreign workspace rows. Admin intentionally receives read plus workspace-management authority, not implicit proposal or approval authority, preserving the separation-of-duties roles defined by E1.
[T] Test: Table-driven tenancy tests prove every role's exact allow/deny matrix, membership-map immutability, foreign-workspace scope denial, hard-fail row validation, and rejection of padded, blank, control-character, and unknown-role identities. Focused tests pass under the race detector.
[C] Checkpoint #1: workspace tenancy and fail-safe app-scope contract — next: strict signed-token verification and header-agnostic HTTP authentication.
[A] Action: Added a static-key Ed25519 JWT verifier using pinned golang-jwt v5.3.1. The verifier accepts only the explicit `sith-session+jwt` profile, requires a known `kid`, exact EdDSA method identity, configured issuer and audience, expiry, issued-at, token ID, subject, and at least one valid membership; it rejects remote key URLs and critical extensions instead of introducing implicit network or algorithm agility.
[A] Action: Added HTTP authentication middleware that requires exactly one unambiguous Bearer credential, clones the request, removes inbound identity/role/tenant/workspace/membership headers, verifies the token, and exposes an immutable principal plus signed-membership scope through private context keys. Authentication failures return one generic non-cacheable response and never echo the credential or claim failure.
[T] Test: Race-enabled auth tests cover valid signed membership, expired/not-yet-valid/missing claims, issuer and audience substitution, unknown roles and keys, missing/wrong type, remote key/certificate URLs, unsupported critical headers, forged signatures, HS256 algorithm substitution, canceled/oversized inputs, key-copy immutability, ambiguous Authorization headers, and injected admin/foreign-tenant headers. Focused vet and golangci-lint are green with zero findings.
[C] Checkpoint #2: signed-token identity and no-header-trust middleware — next: full repository gates and adversarial review.
[A] Action: Added the hub-facing `fleetcache.QueryScoped` seam so handlers pass a verified `tenancy.Scope`, not a raw header-derived workspace string. The wrapper queries only the signed workspace and revalidates every returned record. Existing local-mode callers keep their zero-overhead string path.
[T] Test: Race tests populate two workspaces and prove a workspace-A principal receives only workspace-A records. Guessed foreign and nonexistent scope names both return the same synthetic, unreachable, zero-observation echo, proving the cache does not create a cross-workspace existence oracle while retaining honest requested-scope coverage.
[C] Checkpoint #3: signed scope wired to the shared fleet cache — next: full repository gates and adversarial review.

---

**Session close:** in progress · **Open questions touched:** none
