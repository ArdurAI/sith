# Session — 2026-07-18 — E13 bounded DCGM GPU utilization

**Builder:** Gnani Rahul · **Branch:** `gnanirahulnutakki/feat/e13-dcgm-gpu-utilization`
**Slice:** [#286](https://github.com/ArdurAI/sith/issues/286), E13
[#31](https://github.com/ArdurAI/sith/issues/31) · **Status:** complete local proof

## [G] Goal

Normalize already-authorized DCGM GPU utilization evidence into bounded fleet facts without
guessing the blocked live transport, freshness, coverage, cost-join, or presentation contracts and
without overstating per-workload precision.

## [S] Scope

- One exact Prometheus instant-vector expression: `DCGM_FI_DEV_GPU_UTIL`.
- Whole-GPU, paired MIG, and explicitly best-effort workload attribution.
- Deterministic derived TELEMETRY facts, selected-identity SHA-256, atomic failure, and fixed input/
  output bounds.
- No client, endpoint, credentials, network, RBAC, arbitrary PromQL, range aggregation,
  persistence, runtime wiring, stale objective, coverage rollup, GPU cost/idle-cost join, team
  mapping, UI/API, billing, optimization, mutation, or execution.

## [A] Decision and implementation

- Reconciled the live backlog and confirmed that higher-priority residuals remain landed to their
  current contracts or explicitly blocked on owner, custody, runtime, package-admin, or upstream
  release decisions; no competing PR is open.
- Verified the current Prometheus instant-vector contract and NVIDIA dcgm-exporter 4.6.0-4.8.3
  metric, renderer, MIG, and Kubernetes attribution contracts from primary sources.
- Opened issue 286, linked parent issue 31, and recorded matching Notion and EXTENDED Obsidian
  decisions before source work.
- Added a pure projector that revalidates the exact query, source identity, timestamps, values,
  disabled API series limit/lookback override, label groups, bounds, and whole response before
  returning deterministic facts.
- Discards raw GPU UUID, host, PCI bus, scrape target, and arbitrary labels. Complete workload
  evidence is labelled `workload_best_effort`; physical or MIG device scope remains explicit.
- Added an AST structure/import/declaration wall so network, credential, process, persistence,
  planning, mutation, or execution capability cannot enter unnoticed.

## [T] Focused proof

- Package race tests pass with 92.8% statement coverage on the final query contract.
- Positive fixtures cover physical GPU, MIG, workload-best-effort physical GPU, workload-best-effort
  MIG, exact decimal canonicalization, graph attachment, privacy, ordering, and successful-empty
  abstention.
- Adversarial fixtures reject malformed/duplicate/deep/oversized JSON, warnings, infos, non-vector
  data, label and series overflow, partial or invalid MIG/workload identity, raw metric mismatch,
  timestamp mismatch, non-string/non-finite/out-of-range values, projected duplicates, and a late
  invalid series without returning partial facts.
- Native Go fuzzing completed exactly 50,000 generated executions with four workers; the projector
  did not panic, return partial facts on error, violate bounds, or emit an invalid graph fact.
- The first full CodeRabbit review reported zero findings. A manual second-angle review found that
  Prometheus's optional API `limit` can truncate a successful vector. The public contract now
  requires `limit=0` and no per-query lookback override, preventing an API-limited prefix from being
  asserted as complete while preserving the explicit F13.4 freshness gap. Focused race tests and
  another exact 50,000-execution fuzz run passed after the change; the second full CodeRabbit review
  also reported zero findings.
- The final unchanged source passes full repository CI with zero lint findings and no reachable
  vulnerabilities, forced-RLS PostgreSQL isolation and both at-least-50,000-execution cross-workspace
  fuzzers, reproducible four-platform archives plus SPDX SBOM/checksum/Homebrew/two-platform OCI
  proof, and the pinned Kubernetes 1.36.1 two-cluster suite in 236.293 seconds. Teardown left no
  Kind cluster or matching container.
- Manual red-team confirms atomic late failure, exact query-time binding, complete MIG/workload
  groups, stable selected identity, duplicate protection for discarded labels, no raw hardware or
  scrape identity retention, and no I/O/authority seam. It also records two nonclaims: Prometheus
  evaluation time does not prove scrape age, and the value-only core cannot prove that central
  Prometheus data belongs only to the caller-asserted scope.
- The changed-file high-signal credential scan is clean. Live GitHub queues are 0 open Dependabot,
  0 code-scanning, and 0 secret-scanning alerts; no competing PR is open; exact `origin/dev` remains
  `f3e00f492e69235fd4df2de9b802332eab6d9793`.

## [S] Security, reliability, and cost

The parser treats source JSON and labels as untrusted, bounds memory/cardinality, rejects ambiguous
or partial identity, and preserves only fields needed for the reviewed claim. It introduces no new
authority or recurring cloud cost. A future live query path will add Prometheus compute/cardinality
cost and must be scoped, rate-limited, and observed separately.

## [P] Primary sources

- [Prometheus HTTP API](https://prometheus.io/docs/prometheus/latest/querying/api/)
- [NVIDIA dcgm-exporter 4.6.0-4.8.3](https://github.com/NVIDIA/dcgm-exporter/releases/tag/4.6.0-4.8.3)
- [NVIDIA default counters](https://github.com/NVIDIA/dcgm-exporter/blob/4.6.0-4.8.3/etc/default-counters.csv)
- [NVIDIA DCGM exporter Kubernetes and MIG documentation](https://docs.nvidia.com/datacenter/dcgm/latest/gpu-telemetry/dcgm-exporter.html)
- [ADR 0013](../docs/adr/0013-dcgm-gpu-utilization-facts.md)

## [N] Next

Run the full local matrix, complete CodeRabbit and manual red-team review, fix every finding, then
create one signed DCO/GSTACK commit and require exact-head plus exact post-merge `dev` proof.

## [C] Checkpoint #1

Issue, primary-source decision, implementation, adversarial tests, AST boundary, exact
50,000-execution fuzz proof, README, mirrored E13 roadmap, ADR, and GSTACK journal are present in the
EXTENDED worktree. Full local and hosted proof remain fail-closed gates.

## [C] Checkpoint #2

The tightened query-completeness contract and complete final local matrix are green from the
EXTENDED worktree. Two complete CodeRabbit reviews end at zero findings; manual red-team,
credential scan, exact base, no-competing-PR check, and GitHub 0/0/0 security queues are clean.
Signed publication and hosted exact-head/post-merge proof remain the fail-closed gates.
