# Elasticsearch bounded log-cause facts

- Builder: gnanirahulnutakki
- Effort: deep
- Branch: `gnanirahulnutakki/feat/elasticsearch-log-causes`
- Issue: `#214`
- Status: locally verified; awaiting signed PR and exact post-merge proof

## Goal

Define Sith's first Elasticsearch normalization contract by projecting one already-authorized,
already-fetched Search API response into bounded TELEMETRY cause facts for R3 without adding an
HTTP client, endpoint/index configuration, credential custody, persistence, or mutation.

## Scope

- Accept only the current ECS/Filebeat field profile: `@timestamp`, `message`,
  `orchestrator.cluster.name`, `kubernetes.namespace`, `kubernetes.pod.name`, and optional
  `kubernetes.container.name`.
- Take workspace, cluster scope, namespace, Pod, optional container, query window, and collection
  time only from trusted caller input.
- Require every hit to match that identity and fall inside a maximum fifteen-minute window.
- Classify a closed cause taxonomy: `panic`, `missing-config`, or `dependency-failure`.
- Emit at most one aggregate fact per cause with count and first/last event time.
- Discard raw messages, index/document IDs, source documents, unknown fields, labels, URLs, query
  text, and user data before graph construction.

## Decision

Prove the pure evidence boundary before any live Elasticsearch adapter. The response must be
complete, untimed-out, not early-terminated, and have zero failed shards. `_source`, ignored raw
values, highlights, inner hits, unknown `fields` members, ambiguous arrays, absent cluster identity,
and identity mismatches fail closed. Successful empty or unclassified results return zero facts and
never claim that logs or the wider fleet are clean.

The ECS `orchestrator.cluster.name` field is mandatory even though Elastic documents that some
Kubernetes deployments do not populate it. In a multi-cluster hub, namespace and Pod names alone
are not a safe join key. Operators must populate the cluster field or Sith abstains; it never guesses.

## Security, operability, and cost

- Raw logs can contain credentials, personal data, and internal addresses. Classification happens
  in bounded memory, errors never echo field contents, and only the closed derived answer survives.
- The source-level boundary test exact-allowlists every production import and declaration and
  rejects injected interfaces plus network, filesystem, process, database, credential, persistence,
  gRPC, client-go, and mutation seams.
- This pure projector creates no infrastructure and no runtime cloud cost. A future live reader must
  use TLS, an allowlisted index/data-stream target, finite timeout/window/size budgets,
  `allow_partial_search_results=false`, and index `read` only. Wide wildcards and deep pagination
  would add data-exposure and Elasticsearch CPU/memory cost and remain out of scope.

## Progress and verification

[G] Normalize one bounded Elasticsearch log search into R3 TELEMETRY facts for #214.
[S] Pure ECS response projection only; live querying, auth, index discovery, pagination/PIT/scroll,
raw-log retention, negative evidence, and out-of-process framework work remain out of scope.
[A] Verified the E12/R3 contract against repository specs and current official Elastic Search API,
selected-fields, ECS, Kubernetes-field, and privilege documentation; no duplicate issue or code
existed. Opened #214 and based an isolated worktree on exact `origin/dev` merge `4c1e194`.
[A] Implemented bounded input/response parsing, exact cluster/namespace/Pod/container matching,
closed conservative classification, deterministic aggregation, and sanitized entity-attached facts.
[T] Focused race tests pass with 95.7% statement coverage. Adversarial cases cover secret
non-retention, classifier specificity and false positives, partial/failed shards, `_source`, ignored
and expanded content, unknown fields, malformed types, duplicate/trailing/deep JSON, attacker-sized
numbers, size/count/time budgets, identity confusion, determinism, abstention, and AST boundaries.
[T] Native fuzzing completed 1,329,594 executions with no panic, invalid fact, non-atomic error,
capability escape, or excess fact count.
[T] `make ci` passes formatting, vet, lint with zero findings, vulnerability scanning with no
findings, the full race suite, policy tests, performance, subprocess E2E, and build.
[T] `make e2e-isolation` passes real PostgreSQL RLS plus both 50,000-execution workspace-isolation
fuzzers. `make e2e-kind` passes pinned two-cluster fan-out, OCI, and Argo tests in 236.347 seconds.
[T] `make release-check` passes module verification, two reproducible release builds, archive/SPDX
SBOM validation, Homebrew formula generation, and the multi-platform distroless OCI layout.
[T] CodeRabbit's committed-diff review found two valid fail-closed gaps: present JSON `null` values
could decode as absent values, and the declaration boundary keyed methods only by their bare name.
The projector now rejects every present `null` response field, while the boundary allowlist keys
methods by receiver type and has a regression proving identically named methods remain distinct.
[T] Post-review focused race tests still pass at 95.7% statement coverage, and native fuzzing
completed 4,185,770 executions without a failure. The complete post-review matrix is green:
`make ci`, `make e2e-isolation`, `make e2e-kind` (236.131 seconds), and `make release-check`.
[T] `README.md` was reviewed in full. No update is warranted because this slice adds no user-facing
command, configuration, authentication flow, endpoint, runtime connector, or supported behavior;
the roadmap and this checkpoint are the correct documentation surfaces.

Primary compatibility references:

- <https://www.elastic.co/docs/api/doc/elasticsearch/operation/operation-search>
- <https://www.elastic.co/docs/reference/elasticsearch/rest-apis/retrieve-selected-fields>
- <https://www.elastic.co/docs/reference/elasticsearch/security-privileges>
- <https://www.elastic.co/docs/reference/ecs/ecs-base>
- <https://www.elastic.co/docs/reference/ecs/ecs-orchestrator>
- <https://www.elastic.co/docs/reference/beats/filebeat/exported-fields-kubernetes-processor>

## Checkpoint

- `2026-07-17/elasticsearch-log-causes#1`
- `2026-07-17/elasticsearch-log-causes#2`

## Open questions

- Live endpoint/index configuration, authorization, mapping discovery, and query execution remain a
  later connector child. That slice must reuse this projector and enforce the documented request
  contract rather than introduce another normalization path.
