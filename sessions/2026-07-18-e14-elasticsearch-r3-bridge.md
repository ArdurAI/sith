# Session — 2026-07-18 — E14 Elasticsearch R3 graph bridge

**Builder:** Gnani Rahul · **Branch:** `gnanirahulnutakki/feat/e14-elasticsearch-r3-bridge`
**Slice:** [#280](https://github.com/ArdurAI/sith/issues/280), E14
[#46](https://github.com/ArdurAI/sith/issues/46) · **Status:** complete local proof; ready for signed commit

## [G] Goal

Connect the reviewed, bounded Elasticsearch `search/ecs-v1` log-cause facts to the existing R3
CrashLoop rule without widening the source contract, retaining raw logs, inferring coverage,
correlating Pods, or adding any read, credential, storage, mutation, or execution capability.

## [S] Scope

- Route only exact Elasticsearch `LogSignal` TELEMETRY `FactDerived` values through a fail-closed
  graph bridge.
- Revalidate source/provenance, exact Pod attachment, native/resource identity, closed payload,
  count, event interval, clock skew, and optional container text.
- Normalize only the exact `logs.cause` value and last classified event time. Preserve source and
  staleness; copy caller-declared coverage without inference.
- Exercise the actual projector-to-graph-to-R3 path, deterministic replay, and text/JSON rendering.
- Keep live Elasticsearch querying, TLS/auth, endpoint/index configuration, mappings, pagination,
  retention, negative evidence, dependency identity, new rules, alerts, typed intents, mutation,
  dispatch, and execution out of scope.

## [A] Design and implementation

- `FromGraphFacts` recognizes the exact `elasticsearch` / `search/ecs-v1` protocol before generic
  derived-fact filtering, so malformed exact claims cannot bypass validation.
- Accepted facts must be derived TELEMETRY, have exact source and provenance, carry no attributes
  or display fields, identify one Pod in the same scope and namespace, and use a lowercase
  `sha256:` native ID whose prefix exactly names the `LogSignal` resource. The projector now binds
  that digest only to retained sanitized workspace, Pod, aggregate, and collection fields, so the
  bridge can recompute it and fail closed on scope, namespace, Pod, or payload retargeting.
- The JSON object is duplicate-safe, exact-case, single-valued, size-bounded, and closed to `key`,
  `value`, `count`, `first_event_at`, `last_event_at`, and optional non-null `container`.
- Only `logs.cause` values `panic`, `missing-config`, and `dependency-failure` survive. Count,
  container, source payload, and all raw source material are discarded after validation.
- The emitted observation uses the attached Pod identity, the last event time, evidence source,
  and fact stale flag. Different Pods remain different evaluator entities; no fleet correlation is
  introduced.

## [T] Focused proof

- The brain and CLI packages pass after formatting.
- All three cause classes pass through the real Elasticsearch projector and become one exact
  Pod-scoped R3 observation and citation.
- Missing, unavailable, stale, omitted, and observation-stale TELEMETRY coverage remain honest;
  fact presence never creates coverage.
- Adversarial cases cover workspace/source/scope/namespace/entity mismatch, ambiguous entity
  dimensions, wrong kind/lens/protocol, malformed native/resource identity, unexpected metadata,
  missing/duplicate/mixed-case/unknown/trailing/malformed JSON, invalid count/time/window, and
  invalid optional container values.
- A cross-Pod regression proves Elasticsearch evidence for one Pod cannot strengthen another.
- The deterministic replay corpus now includes a sanitized Elasticsearch R3 case.
- Text and JSON renderer tests start with a raw-message-bearing Search response, pass through the
  source projector and graph bridge, and prove the raw marker plus count/container/time metadata
  never appears in output.
- Native Go fuzzing completed 653,689 post-hardening executions in fifteen seconds without a panic
  or closed contract escape.
- Focused race coverage is green: Elasticsearch 95.7%, brain 88.6%, and CLI 62.7%.
- CodeRabbit's first complete uncommitted review found one valid defense-in-depth improvement: make
  the non-empty source scope requirement explicit at the Elasticsearch bridge even though graph
  validation already enforces it. The explicit check and regression are present, focused race tests
  remain green, and the repeated full review reports zero findings across all changed files.
- `make ci` passes formatting, vet, lint with zero findings, `govulncheck` with no reachable
  vulnerabilities, the complete race suite, policy and alert-rule checks, performance, subprocess
  E2E, and production build. Brain coverage is 88.6% and Elasticsearch remains 95.7%.
- `make e2e-isolation` passes PostgreSQL 18.4 forced-RLS tests and both 50,000-execution
  cross-workspace fuzzers.
- `make release-check` verifies modules, two byte-identical four-platform release builds, archive
  contents, SPDX SBOMs, Homebrew output, and the release-derived amd64/arm64 distroless Hub OCI
  layout.
- `make e2e-kind` passes the pinned Kubernetes 1.36.1 two-cluster fan-out, OCI, and Argo suite in
  234.972 seconds. Teardown leaves no Kind cluster or matching Kind/Sith container.
- `README.md` was reviewed in full and updated because this slice adds a public graph-fact behavior
  even though the cache-backed CLI still performs no Elasticsearch fetch. A final high-signal
  credential scan finds only the deliberately fake `.invalid` raw-message marker used by the CLI
  non-retention test; no real credential or private-key material is present.

## [S] Security, reliability, and cost

Raw logs are classified and discarded by the existing source projector before the bridge executes.
The bridge accepts only the closed sanitized fact and fails closed on ambiguous provenance,
identity, or payload. It creates one bounded in-memory observation per accepted fact and performs no
I/O. There is no new cloud resource, API request, egress, retention, credential, privilege, write
path, or recurring cost. A future live reader remains responsible for TLS, least-privilege index
`read`, an allowlisted target, finite request/query budgets, and complete-shard responses.

## [P] Primary sources

- [Elasticsearch Search API](https://www.elastic.co/docs/api/doc/elasticsearch/operation/operation-search)
- [Elasticsearch selected fields](https://www.elastic.co/docs/reference/elasticsearch/rest-apis/retrieve-selected-fields)
- [Elastic Common Schema orchestrator fields](https://www.elastic.co/docs/reference/ecs/ecs-orchestrator)
- [Filebeat Kubernetes processor fields](https://www.elastic.co/docs/reference/beats/filebeat/exported-fields-kubernetes-processor)

## [N] Next

Create one signed DCO/GSTACK commit, publish and verify the exact PR head, require hosted CI,
CodeQL, and CodeRabbit, merge without rewriting the signed head, prove exact post-merge `dev`, close
#280, update #46 without claiming E14 complete, recheck all GitHub security queues, and synchronize
Notion and Obsidian.

## [C] Checkpoint #1

Pending signed implementation commit — issue, design, production bridge, identity hardening,
adversarial tests, replay, CLI privacy proof, documentation, full local gate matrix, repeated
zero-finding review, and clean Kind teardown are frozen in the EXTENDED worktree.
