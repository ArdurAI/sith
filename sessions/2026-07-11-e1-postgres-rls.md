# Session — 2026-07-11 — e1-postgres-rls

**Builder:** Gnani Rahul · **Model/effort:** GPT-5, max · **Branch:** gnanirahulnutakki/feat/e1-postgres-rls
**Slice(s):** Phase 1 / E1 / issue #8 · **Status:** in-progress

---

[G] Goal: Implement the independent database-level tenancy backstop from ADR-0003: non-owner application connections, forced PostgreSQL RLS on every current workspace table, transaction-local workspace scope, and a CI guard that fails on isolation drift.
[S] Scope: Initial PostgreSQL tenancy schema, migration/grant boundary, scoped application DAL, catalog audit, and real PostgreSQL acceptance test. The broader destructive isolation suite and removed-policy negative control remain issue #12.
[A] Action: Started from accepted `dev` commit `dd063c7` after E1 signed workspace authentication (#7) and its closeout journal passed post-merge CI. Reconciled #8, ADR-0003, F1.5, the conceptual hub data model, the production privacy boundary, and current CI topology.
[T] Test: Current primary sources verified PostgreSQL 18.4 semantics: superusers and `BYPASSRLS` roles always bypass policies; table owners bypass unless `FORCE ROW LEVEL SECURITY` is set; `set_config(..., true)` is transaction-local; `USING` filters existing rows while `WITH CHECK` rejects foreign inserts/updates. Verified pgx v5.10.0 as the current stable pool/transaction API and pinned the multi-architecture PostgreSQL 18.4 Alpine image by OCI digest.
[A] Action: Selected a hidden-pool `AppDB` API so production callers cannot issue unscoped pool queries. Every operation must enter through a validated `tenancy.Scope`, an explicit read-committed transaction, and parameterized transaction-local `sith.workspace_id`; PostgreSQL then re-enforces the boundary even when callback SQL deliberately omits a workspace predicate.
[C] Checkpoint #1: RLS schema, least-privilege pool, scoped transaction, and real-container test drafted — next: compile, run the real database falsification cases, and harden catalog drift detection.
[T] Test: The first real PostgreSQL 18.4 test passed under the race detector in 6.397s. It proved all four current tables return only workspace-A rows even when SQL omits a scope predicate; an explicit workspace-B read returns zero; a foreign insert fails with SQLSTATE 42501; the table owner sees zero without scope because RLS is forced; direct unscoped pooled reads remain zero after a scoped transaction; and the app role cannot truncate.
[A] Action: Database self-review added SHA-256 checksums to the migration ledger so an already-applied migration cannot be silently edited. Tightened the catalog audit to require the exact transaction-setting policy shape for both `USING` and `WITH CHECK`, PUBLIC policy application, text/non-null scope columns, no unsafe table/schema/meta privileges, no role memberships, and `NOINHERIT` in addition to non-owner/NOBYPASSRLS status.
[C] Checkpoint #2: real RLS behavior proved and migration/catalog guard hardened — next: rerun the real database gate, focused lint/race tests, then checkpoint the schema boundary.
[T] Test: The hardened real-database test passed again in 2.831s and the tagged suite covers 67.8% of the new package; focused race tests and golangci-lint pass with zero findings. A govulncheck invocation without the mandated toolchain correctly exposed fixed Go 1.26.0 standard-library advisories; rerunning with the repository-pinned Go 1.26.5 toolchain reports no vulnerabilities, with no suppression or exception added.
[C] Checkpoint #3: production PostgreSQL tenancy boundary committed with SSH signature, DCO, and immutable migration evidence — next: commit the real-container/CI harness, then run all repository and real-environment gates.

---

**Session close:** in progress · **Open questions touched:** none
