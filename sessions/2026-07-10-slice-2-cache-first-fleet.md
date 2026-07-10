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
[C] Checkpoint #2: ef8f828 — connector-isolated background hydration; next: shared renderer and CLI.
[A] Action: Added the shared Tier-1 table/coverage renderer and cache-backed `get`, `search`, and
`correlate` commands. Scripted get requires an explicit context or `--all-clusters`; partial
coverage warns with exit 0 and total failure is non-zero after coverage output.
[T] Test: Renderer golden tests cover every Tier-1 lens, wide/name modes, truncation, and mandatory
coverage. CLI tests prove pre-I/O validation, JSON schema, partial/total exit semantics, image
search, and deployment-health correlation across two contexts. Focused lint and race tests pass.
[C] Checkpoint #3: 3227387 — shared cache renderer and scriptable fleet reads; next: Bubble Tea TUI.
[A] Action: Added the Bubble Tea v2.0.8 cache-first TUI. Bare terminal launches enter the fleet
view while redirected invocations remain help-only; `sith tui` is the explicit entrypoint. The
view supports Tier-1 lens commands, live filter, whole-fleet structured/fuzzy search, numeric
cluster scopes, pause/resume, async refresh, navigation, and coverage detail.
[T] Test: Model tests prove cold first paint under 250 ms, no syncer calls on interactions,
incremental/background message handling, pause/coverage/scope/search behavior, CLI/TUI table
parity, Unicode/bounds safety, and cancellation. TUI coverage is 87.7%; a dedicated non-race CI
gate measures 3,000 cached pods at p95 <100 ms while race tests validate concurrency separately.
[C] Checkpoint #4: 25c14e2 — interactive cache-first fleet view; next: real two-cluster parity and staleness proof.
[A] Action: Expanded the digest-pinned kind gate to seed deterministic Pods and Deployments in two
clusters, exercise built-binary cache commands, and then delete one previously live cluster during
the same in-memory session.
[T] Test: Race-enabled real APIs prove get/search/correlate answers over 2/3 contexts, partial
warning/JSON coverage, image and unhealthy-deployment correctness, CLI/cache parity, and immediate
last-known stale retention after the second cluster disappears. The gate passes in 68 seconds.
[R] Review: The regular GitHub security audit reports zero open Dependabot alerts. Code scanning
has no analysis configured and secret scanning is disabled; schedule a narrow post-slice security
lane for CodeQL and repository secret-scanning/push-protection enablement. Local govulncheck is clean.
[C] Checkpoint #5: this commit — real cache/search/staleness proof; next: generic resource lens and final review.

---

**Session close:** in progress · **Open questions touched:** Q12 keeps the roadmap TUI/CLI-first default
