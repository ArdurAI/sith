# Session — 2026-07-11 — slice-7-local-advisory-brain

**Builder:** Gnani Rahul · **Model/effort:** Codex, high · **Branch:** gnanirahulnutakki/feat/local-advisory-brain
**Slice(s):** Phase-L F14.5 #48 · **Status:** ready-for-PR

---

[G] Goal: Ship the deterministic local advisory Investigation Brain for R1-R6 with cited evidence, abstention, fleet-first correlation, and no write path.
[S] Scope: `internal/brain`, the local cache-to-brain projection, a read-only CLI surface, tests, and operator docs. Hub governance, PEP dispatch, stored telemetry, and connector expansion are out of scope.
[A] Action: Re-read issue #48, epic #46, and the authoritative E2 brain spec; audited current cache facts and confirmed LIVE evidence exists while DESIRED/TIMELINE/TELEMETRY coverage must remain explicit and honest.
[T] Test: Design review against R1-R6 acceptance criteria; verified no overlapping open PR, remote branch, or active worktree.
[A] Action: Added the four evidence lenses and normalized pod failure reasons, runtime image digests, and active node conditions without retaining additional raw data or adding network access.
[T] Test: Record tests prove simultaneous current/previous failure reasons and content-addressed image identity are preserved.
[C] Checkpoint #1: fleet evidence normalization — next: deterministic rules.
[A] Action: Added pure R1-R6 evaluation with exact citations, entity-local coverage gates, confirmed/detected/unconfirmed states, R1-to-R3 cause composition, and same-digest fleet-first arbitration.
[T] Test: Table-driven corpus covers R1-R6; regressions cover stale required lenses, cross-entity coverage isolation, cause chaining, deterministic fleet ordering, and fail-safe input validation. Focused race tests and lint are green.
[C] Checkpoint #2: deterministic rule engine — next: local advisory surface.
[A] Action: Added `sith investigate [name] [--context]` over the existing tier-1 hydrator and cache. Text/JSON/YAML output names the rule, state, target/fleet scope, missing lenses, exact weighted citations, and inert advisory.
[T] Test: CLI tests prove cited output, stable JSON, clean telemetry abstention, and no write-capable package imports from the brain.
[C] Checkpoint #3: advisory CLI — next: process and multi-cluster proof.
[A] Action: Extended compiled E2E with `investigate` behind the existing HTTP/HTTPS proxy sentinel. Added a real nonzero-exit fixture to both kind clusters and waited for repeated failure from the Kubernetes API before invoking the compiled binary.
[T] Test: Final compiled E2E passed in 8.344s with zero sentinel egress. The final digest-pinned two-cluster kind gate passed under `-race` in 85.835s and proved a fleet-wide R3 verdict precedes both per-context findings while TELEMETRY is named missing. The gate also revalidated existing CLI, watch, local-ops, web, and MCP behavior. Docker cleanup reclaimed 913.1 MB after the final gate.
[C] Checkpoint #4: real process and fleet proof — next: docs, full gate, and red-team review.
[A] Action: Recorded ADR 0008 and updated the operator README with the command, evidence boundary, honest Phase-L limitations, security posture, and zero hosted-cost model.
[T] Test: Final `make ci` is green: formatting, vet, zero lint findings, govulncheck with no known vulnerabilities, all repository race tests, 86.5% brain coverage, performance, compiled E2E, and build. CGO-free darwin/amd64, linux/amd64, and windows/amd64 builds pass. GitHub Dependabot, code-scanning, and secret-scanning queues are each zero open before publication.
[C] Checkpoint #5: documentation and final validation — next: publish the verified signed stack and require green PR/merge/release gates.

---

**Session close:** final current-tree gates green; ready for PR · **Open questions touched:** local telemetry remains unavailable by default; detect and abstain rather than infer. Kubernetes repeated nonzero termination is normalized as R3 even when a durable waiting string is not exposed; exit-zero completion remains excluded.
