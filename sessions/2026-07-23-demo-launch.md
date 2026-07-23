# Session — 2026-07-23 — demo-launch

**Builder:** Gnani Rahul Nutakki · **Model/effort:** Codex, high · **Branch:** gnanirahulnutakki/feat/demo-launch
**Slice(s):** Phase-L demo readiness · #315 · **Status:** done

---

[G] Goal: Make the completed local fleet client discoverable through one safe graphical launch command for issue #315.
[S] Scope: `internal/cli`, README first-run guidance, command and launch smoke tests, and this journal. Out of scope: demo fixture deployment, Hub release policy, GHCR visibility, governed writes, and cluster mutation.
[A] Action: Reconciled live `dev`, releases, roadmap/build-sequence demo criteria, existing command surfaces, duplicate issues, and release blockers; selected orchestration over the existing desktop and loopback UI surfaces.
[A] Action: Added `sith launch` with closed `auto|desktop|ui` selection, explicit UI-only flag validation, shared bounded directory import, root registration, and first-run documentation. Refactored the existing UI command to accept an explicit directory without requiring a default kubeconfig backend.
[T] Test: Focused CLI race tests and twenty repeated package runs passed. The built binary launched the loopback UI from the provided self-managed test kubeconfig directory, served HTTP 200, and shut down cleanly without creating a cluster workload.
[T] Test: `make ci`, `make e2e-isolation`, both 50,000-case tenancy fuzzers, Helm and multi-architecture OCI contracts, the real two-cluster Kind suite, `go mod tidy -diff`, `go mod verify`, and the double-build `make release-check` all passed; Kind cleanup left no clusters.
[C] Checkpoint #1: this commit — one-command local demo launcher and complete local verification; next: publish PR into `dev`, require green PR and exact post-merge checks, then resume the broader demo-readiness gap audit.

---

**Session close:** implementation and local verification complete · **Open questions touched:** Q12 — preserve the existing graphical surfaces and add an auto-selecting entry point rather than creating a new UI
