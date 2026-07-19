# Session — 2026-07-18 — E13 OpenCost namespace cost facts

**Builder:** Gnani Rahul · **Branch:** `gnanirahulnutakki/feat/e13-opencost-namespace-costs`
**Slice:** [#282](https://github.com/ArdurAI/sith/issues/282), E13
[#31](https://github.com/ArdurAI/sith/issues/31) · **Status:** complete local proof; ready for signed commit

## [G] Goal

Define the first F13.1 normalization boundary: project one already-authorized exact OpenCost
namespace-allocation response into deterministic Sith cost facts without adding transport,
credentials, persistence, billing, optimization, or mutation.

## [S] Scope

- One explicit UTC window, one allocation set, `aggregate=namespace`, step equal to the window, and
  disabled filter, accumulation, idle, sharing, proportional-asset, and metadata options.
- Trusted USD-only source assertion because allocation JSON has no currency field.
- Exact cluster and Kubernetes namespace identity, allowlisted component/adjustment/total amounts,
  source window, and closed provenance.
- Response, row, payload, decimal, identity, time, count, and depth bounds with atomic failure.
- Keep live endpoint discovery, HTTP/TLS/auth, OpenCost availability coverage, fleet/team rollup,
  currency conversion, freshness objectives, GPU utilization/DCGM/MIG, billing, optimization,
  recommendations, persistence, and every write path out of scope.

## [A] Decision and implementation

- Verified the current contract against the official OpenCost API, v1.120.2 release, allocation
  response and total-cost source, HTTP envelope, and Swagger schema.
- Opened #282 after a live duplicate check, linked it from #31, and based the isolated EXTENDED
  worktree on exact `origin/dev` `df28654b87ecfa1e491901ba8a3d718ba0825a49`.
- Recorded the design, blockers, primary sources, security boundary, costs, alternatives, and
  nonclaims in Notion and the EXTENDED Obsidian vault before source work.
- Added `internal/connector/opencost`: case-exact and duplicate-safe envelope decoding, exact query
  metadata, UTC window validation, Kubernetes namespace correlation, rational five-decimal cost
  validation, total recomputation, deterministic ordering and identity, privacy-minimized graph
  facts, and no-partial-result behavior.
- Added a bidirectional AST boundary that pins the value-only public API, imports, declarations,
  production structure, and the sole permitted `io.EOF` use.
- Added ADR 0011 plus README and mirrored E13 roadmap updates. The documentation states that no live
  CLI or Hub reader exists and does not mark F13.1 or E13 complete.

## [T] Focused proof

- Focused race tests pass with 93.2% statement coverage.
- Positive coverage proves sorted namespace facts, exact graph attachment, canonical USD amounts,
  true window-end observation time, graph validation, and raw provider/label/annotation/controller/
  endpoint/collection-time non-retention.
- Adversarial coverage proves exact query flags, window/step/clock-skew bounds, success-envelope and
  one-set requirements, namespace and cluster matching, synthetic-row refusal, exact-case and
  duplicate JSON handling, decimal/magnitude/total checks, depth/size/count limits, atomic errors,
  deterministic identity, and complete-empty abstention.
- Native Go fuzzing completed exactly 50,000 generated executions with four workers and no panic,
  invalid fact, duplicate identity, taxonomy escape, or partial error result.
- The first complete CodeRabbit pass found two valid mirrored-roadmap ambiguities around empty
  results. The second found two valid README contract gaps around exact fact attachment and
  whole-response failure. All four documentation findings were corrected; the third complete
  review reports zero findings across all nine changed files.
- `make ci` passes formatting, vet, lint with zero findings, `govulncheck` with no reachable
  vulnerabilities, the full race suite, operator policy and alert-rule tests, warm-view
  performance, subprocess E2E, and the production build. OpenCost coverage remains 93.2%.
- `make e2e-isolation` passes PostgreSQL 18.4 forced-RLS tests and both 50,000-execution
  cross-workspace fuzzers.
- `make release-check` verifies modules, two byte-identical four-platform release snapshots,
  archive contents, SPDX SBOMs, Homebrew output, and the release-derived amd64/arm64 distroless Hub
  OCI layout.
- `make e2e-kind` passes the pinned Kubernetes 1.36.1 two-cluster fan-out, OCI, and Argo suite in
  236.706 seconds. Teardown leaves no Kind cluster or matching Kind/Sith container.
- Manual red-team review rechecked whole-response atomicity, case aliases, duplicate/trailing/deep
  JSON, source/query/window/currency/namespace confusion, decimal bounds and rounding tolerance,
  historical freshness, secret-bearing unknown fields, synthetic namespace rows, identity
  determinism, concurrency, and memory caps; no unresolved path remains. The complete changed-file
  high-signal credential scan reports zero candidates.
- Pre-publication GitHub queues are `0` open Dependabot alerts, `0` code-scanning alerts, and `0`
  secret-scanning alerts. A final fetch confirms exact base `origin/dev` remains `df28654b`.
- `README.md` was reviewed and updated because the new public library contract needs explicit
  exact attachment, decimal, whole-response failure, and no-live-reader boundaries. The roadmap,
  ADR, and README all preserve the incomplete F13.1/E13 status.
- Hosted PR and exact post-merge evidence remain pending.

## [S] Security, reliability, and cost

Raw allocation data remains in bounded memory and only an allowlisted namespace monetary payload
survives. Unknown and privacy-sensitive fields are discarded; ambiguity fails before any fact is
returned. Runtime cost is bounded local CPU and memory only, with no network, cloud API, egress,
storage, telemetry-volume, credential, privilege, or recurring-service cost. A future live reader
must use least-privilege read-only OpenCost access and preserve the exact request contract.

## [P] Primary sources

- [OpenCost allocation API](https://opencost.io/docs/integrations/api/)
- [OpenCost v1.120.2](https://github.com/opencost/opencost/releases/tag/v1.120.2)
- [Allocation response implementation](https://github.com/opencost/opencost/blob/v1.120.2/core/pkg/opencost/allocation_json.go)
- [Total-cost implementation](https://github.com/opencost/opencost/blob/v1.120.2/core/pkg/opencost/allocation.go)
- [HTTP response envelope](https://github.com/opencost/opencost/blob/v1.120.2/core/pkg/protocol/http.go)
- [OpenCost API schema](https://github.com/opencost/opencost/blob/v1.120.2/docs/swagger.json)

## [N] Next

Complete the final high-signal credential and GitHub security-queue checks, synchronize Notion and
Obsidian proof, create one signed DCO/GSTACK commit, and require exact-head plus exact post-merge
`dev` evidence before closing #282.

## [C] Checkpoint #1

Pending signed implementation commit. The reviewed issue, decision, projector, adversarial tests,
boundary, README, roadmap, ADR, session record, 50,000-execution projector fuzz proof, full local CI,
isolation, reproducible release/SPDX/Homebrew/multi-platform OCI proof, zero-finding repeated review,
and clean two-cluster teardown are frozen in the EXTENDED worktree.
