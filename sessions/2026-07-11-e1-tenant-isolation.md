# Session — 2026-07-11 — e1-tenant-isolation

**Builder:** Gnani Rahul · **Model/effort:** GPT-5, max · **Branch:** gnanirahulnutakki/test/e1-tenant-isolation
**Slice(s):** Phase 1 / E1 / issue #12 · **Status:** in-progress

---

[G] Goal: Build the unified E1 isolation suite that proves signed identity, application scope, and PostgreSQL RLS prevent cross-workspace access—and proves the suite detects deliberately broken policies.
[S] Scope: Multi-layer test orchestration, all-table database read/write invariants, removed/permissive-policy negative controls, and deterministic foreign-selector fuzz seeds. New production authorization or persistence behavior is out of scope.
[A] Action: Started from `dev` commit `71b744f` after the #8 RLS implementation and journal closeout merged. Reconciled #12, ADR-0003 F1.6, the existing strict JWT/header tests, signed-scope cache tests, real PostgreSQL harness, and CI topology.
[A] Action: Applied the testing-mastery falsification rule: the database invariant must fail both when its policy is removed (PostgreSQL default-deny loses the expected own row) and when its policy is weakened to `USING (true)` (foreign rows become visible). The test then restores the exact policy and requires recovery, avoiding order-dependent or permanently mutated fixtures.
[C] Checkpoint #1: unified isolation target, destructive database controls, and deterministic selector fuzz drafted — next: format/compile, run the real suite, and correct any false assumptions exposed by PostgreSQL.
[T] Test: The first unified real suite passed under the race detector: hubauth 96.3%, hubserver 90.2%, fleetcache 86.6%, and hubdb 71.1% tagged coverage. All four foreign insert classes returned SQLSTATE 42501, foreign update/delete affected zero rows, both destructive policy mutations were detected, and exact restoration recovered both data and catalog invariants.
[A] Action: Added a separately bounded five-second Go fuzz campaign for the in-memory signed-scope query target. CI therefore exercises generated selector patterns rather than only replaying the seed corpus, while a fixed clock, isolated store per input, input-size bound, and no external I/O keep failures deterministic and reproducible.
[C] Checkpoint #2: real multi-layer suite and bounded fuzz campaign implemented — next: run the fuzz target, lint, and full repository gates.
[T] Test: The bounded fuzzer completed 102,327 generated executions in its first standalone campaign, then the exact `make e2e-isolation` target passed all four packages and completed another 106,146 executions. Workflow YAML parses, focused race tests pass, and golangci-lint reports zero findings.
[C] Checkpoint #3: destructive invariants and CI orchestration proved locally — next: signed checkpoint commit, full repository CI, and real kind regression.
[T] Test: Final local `make ci` is green with zero lint findings and no vulnerabilities on Go 1.26.5; the pinned real kind two-cluster regression passed in 91.359s. Cleanup confirmed zero kind clusters and no `sith-rls` containers, then reclaimed 1.21 GB. GitHub Dependabot, code-scanning, and secret-scanning queues were each zero immediately before publication.
[C] Checkpoint #4: full repository, mutation, fuzz, PostgreSQL, and Kubernetes gates green — next: signed evidence commit, remote CI, review, and merge.

---

**Session close:** in progress · **Open questions touched:** none
