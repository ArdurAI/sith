# Session — 2026-07-10 — slice-2-cache-first-fleet

**Builder:** Gnani Rahul · **Model/effort:** engineering, max · **Branch:** feat/cache-first-fleet
**Slice(s):** Slice 2 / #33 + local portion of #10 · **Status:** ready-for-PR

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
[C] Checkpoint #5: 399b3bd — real cache/search/staleness proof; next: generic resource lens and final review.
[A] Action: Added on-demand generic resource discovery through each context's Kubernetes discovery
API, cached GVR resolution, generic-kind cache aliases, and `:<kind>` TUI hydration without adding
network calls to ordinary interaction paths.
[T] Test: Unit and race tests cover plural-to-advertised-Kind aliases, cached custom-resource
resolution, and generic TUI hydration. The real two-cluster gate seeds ConfigMaps and proves the
built CLI discovers and renders them across 2/3 contexts with honest partial coverage in 61 seconds.
[R] Review: CodeRabbit CLI was unavailable, so the required diff review used the documented local
fallback after a changed-file secret scan. The review found and fixed the generic alias mismatch;
it also found that bounded background polling does not satisfy #33's explicit watch-stream contract.
[C] Checkpoint #6: this commit — generic discovery and lens proof; next: watch-backed delta hydration.
[A] Action: Added the source-abstract optional live-reader extension without changing the locked
seven-verb registry. The kubeconfig adapter now runs independent list-watch loops per reachable
context/lens with bounded reconnects, a five-minute server watch timeout, and a two-minute safety
rediscovery; non-watch readers retain a slow fallback. Generic lenses join the active watch set.
[A] Action: Added atomic per-scope snapshot/upsert/delete/error application to the store. Disconnects
retain and immediately stale last-known rows; pause drops incoming mutations and resume forces a
full reconciliation. TUI startup now owns the long-running hydrator rather than a 15-second poll.
[T] Test: Race tests cover adapter snapshot/upsert/delete streams, cache delta lifecycle, source-
scope rejection, disconnect staleness, dynamic generic-kind watch restart, and shutdown. The real
two-cluster gate creates and deletes a Pod and observes both cache deltas without manual resync; the
complete gate passes in 72 seconds.
[R] Review: Manual red-team review found and fixed cross-scope fact injection at the live-reader
boundary. Continuous cost is explicit: one watch per active lens per reachable context plus bounded
relist/recovery traffic; no credential, object, or telemetry leaves the machine.
[C] Checkpoint #7: 998d26e — watch-backed fleet deltas; next: generic server-print columns and final review.
[A] Action: Added source-abstract display fields backed by Kubernetes `meta.k8s.io/v1` Table
responses. Generic list/watch evidence now carries the API server's column names, priorities, and
cells into the normalized store; the shared renderer adds cluster/namespace identity and honors
priority columns in wide mode. Tier-1 bespoke lenses are unchanged.
[T] Test: HTTP contract tests verify the Table Accept header, URL, selector, identity, priorities,
and cells. Renderer tests prove normal/wide server columns. The real two-cluster ConfigMap path
proves `Name/Data/Age` reach JSON and shared text rendering; the complete gate passes in 70 seconds.
[R] Review: Red-team review treats cluster-provided print cells as untrusted terminal input. Shared
rendering now removes escape/control characters and folds line breaks before CLI/TUI output.
[C] Checkpoint #8: ec2f089 — server-print generic renderer; next: final CI/security/PR review.
[A] Action: Added stable YAML output beside text/JSON/wide/name for version, clusters, and all
cache-backed read commands, closing the final scripting-format mismatch in the locked UX contract.
[T] Test: YAML round-trip tests cover build metadata, allocated empty fleet results, and cache
snapshots. Final `make ci` passes format, vet, lint, reachable-vulnerability scan, race/coverage,
warm p95, binary e2e, and build gates; the digest-pinned two-cluster gate passed separately.
[R] Review: Final GitHub security audit found zero open Dependabot alerts. Enabled Dependabot
security updates plus secret scanning, push protection, validity checks, and non-provider patterns.
Enabled CodeQL default setup; Actions and Go analysis run 29126732288 completed successfully.
Changed-file secret/SPDX checks are clean and all nine commits are signed, DCO-compliant, and carry
the exact GSTACK checkpoint trailer.
[C] Checkpoint #9: this commit — contract and security closure; next: publish and review PR.

---

**Session close:** ready for PR · **Open questions touched:** Q12 keeps the roadmap TUI/CLI-first default
