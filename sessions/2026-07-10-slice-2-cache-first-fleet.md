# Session — 2026-07-10 — slice-2-cache-first-fleet

**Builder:** Gnani Rahul · **Model/effort:** engineering, max · **Branch:** feat/cache-first-fleet
**Slice(s):** Slice 2 / #33 + local portion of #10 · **Status:** in-progress

---

[G] Goal: Implement a cache-first Tier-1 fleet view, shared CLI/TUI render path, and
coverage-honest cross-cluster search over at least two contexts.
[S] Scope: in-memory fleet store, background reconciliation over the Slice-1 Reader seam,
Tier-1 normalization, scriptable get/search/correlate commands, Bubble Tea TUI, latency/parity/
offline tests, and real two-cluster proof. Per-pod operations, local web, MCP, disk persistence,
keychain custody, OCM, and governed writes are out of scope.
[A] Action: Started from post-merge `dev` commit `7767bf4` after PR #52 and both its PR and
post-merge CI runs passed the real two-kind-cluster gate; closed completed Slice-1 issues #32/#38.
[A] Action: Selected an in-memory immutable-snapshot store as the only interaction/render source,
with connector access isolated in a background hydrator. Raw object persistence is deferred until
Slice 5 defines encryption/custody so pod specifications do not become a new plaintext local cache.
[A] Action: Verified current upstream Bubble Tea v2.0.8; use only its core runtime, with local
table/search rendering to avoid unnecessary component/style dependencies.
[A] Action: Added a concurrency-safe fleet store with immutable snapshots, per-lens coverage,
last-known preservation, dynamic freshness, pause/offline state, and change notification. Tier-1
objects normalize once on ingest into render/search fields.
[T] Test: Race tests cover atomic reconciliation, failed-scope last-known retention, structured and
fuzzy search, Tier-1 normalization, pending/offline/paused states, immutable concurrent snapshots,
and change cancellation. Focused lint passes at 86.6% statement coverage.
[C] Checkpoint #1: 09d5470 — cache model and normalized query engine; next: background hydrator.
[A] Action: Added the background hydrator as the sole connector caller. It discovers once per
cycle, fans Tier-1 lens queries out with bounded concurrency, and publishes successful lenses
incrementally while retaining them if a peer lens fails.
[T] Test: Race tests prove frequency-ordered lens selection, concurrency bounds, partial-success
retention, duplicate-sync exclusion, pause behavior, and constructor fail-safety. Focused lint
passes at 87.2% statement coverage.
[C] Checkpoint #2: this commit — connector-isolated background hydration; next: shared renderer and CLI.

---

**Session close:** in progress · **Open questions touched:** Q12 keeps the roadmap TUI/CLI-first default
