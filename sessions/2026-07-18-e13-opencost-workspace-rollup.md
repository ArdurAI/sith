# Session — 2026-07-18 — E13 OpenCost coverage-aware workspace rollup

**Builder:** Gnani Rahul · **Branch:** `gnanirahulnutakki/feat/e13-opencost-workspace-rollup`
**Slice:** [#284](https://github.com/ArdurAI/sith/issues/284), E13
[#31](https://github.com/ArdurAI/sith/issues/31) · **Status:** complete local proof

## [G] Goal

Preserve successful-empty per-cluster OpenCost coverage and compute one deterministic workspace USD
total for an explicit window without guessing the blocked live transport or team-attribution
contracts.

## [S] Scope

- One value-only snapshot envelope around each successful F13.1a projection.
- One explicit expected cluster set and zero or one successful snapshot per reporting cluster.
- Exact workspace, scope, UTC window, USD, fact taxonomy, entity, provenance, payload, and native
  identity revalidation.
- Exact component and total aggregation plus sorted expected/reported/empty/missing coverage.
- No client, endpoint, discovery, credentials, port-forward, Kubernetes Service proxy, OCM
  transport, persistence, runtime wiring, team grouping, UI/API, stale objective, conversion,
  billing, optimization, GPU efficiency, mutation, or execution.

## [A] Decision and implementation

- Reconciled every open issue and current `dev`; all higher-priority residuals remain
  landed-to-current-contract or explicitly human/upstream blocked, and no competing PR is open.
- Verified current access guidance in the official OpenCost API and installation documentation.
  OpenCost recommends operator port forwarding and permits deployment-specific Service/Ingress
  exposure; Sith has no accepted owner for discovery, authentication, or TLS.
- Recorded the live-transport options and recommended local-versus-brokered split on parent issue
  31. F13.1 transport remains held pending GR confirmation; no URL, credential, RBAC grant, or
  network path was guessed.
- Opened issue 284 and recorded the accepted contract in Notion and the EXTENDED Obsidian Sith
  project before source work.
- Added a successful projection snapshot that preserves an empty fact set as reported coverage.
- Added a deterministic workspace rollup with exact decimal aggregation, explicit coverage, nil
  observation time when nothing reported, whole-input atomic failure, privacy-minimized output, and
  fixed scope/fact/byte/magnitude bounds.
- Extended the AST boundary so the package remains value-only and cannot acquire network,
  credential, persistence, process, planning, mutation, or execution capability unnoticed.

## [T] Focused proof

- Package tests pass with the race detector and 91.9% statement coverage.
- Positive coverage proves sorted partial coverage, successful-empty versus missing distinction,
  exact component totals, deterministic order independence, absent observation time when no scope
  reports, and non-retention of namespace/source metadata beyond authorized cluster coverage.
- Adversarial coverage revalidates workspace, scope, UTC window, currency, taxonomy, entity,
  provenance, native identity, canonical JSON, namespace identity, amount precision, component
  totals, duplication, count, and size bounds with zero partial rollup on every error.
- Native Go fuzzing completed exactly 50,000 generated rollup executions with four workers after
  correcting one fuzz-harness-only invalid-RawMessage identity helper; the production path did not
  panic or emit a partial/invalid rollup.
- The projection and rollup fuzz boundaries each pass exactly 50,000 generated executions with four
  workers. Full repository CI passes formatting, vet, lint with zero issues, `govulncheck` with no
  reachable vulnerabilities, race coverage, shell policy tests, Prometheus rules, performance,
  end-to-end tests, and the production build.
- PostgreSQL forced-RLS isolation and two 50,000-execution cross-workspace fuzzers pass. The
  reproducible four-platform release, SPDX SBOM, checksum, Homebrew, and multi-platform OCI proof
  passes. The pinned Kubernetes 1.36.1 kind suite passes in 238.257 seconds.
- CodeRabbit's first complete review found one minor ADR result-shape omission. The ADR now names
  the retained coverage categories, `complete`, and optional `observed_at`; source behavior was
  unchanged. The second complete review covers all changed files and reports zero findings.
- Manual red-team confirms that `complete` is relative to the caller-authoritative expected set and
  that successful-empty presence is a trusted caller claim; the transport-free core cannot prove
  either claim independently and does not pretend to. Late invalid input remains atomic, authorized
  cluster coverage is the only retained identity, total and component fields are summed
  independently, limits fail closed, errors omit raw inputs, race proof is green, and the AST wall
  admits no I/O or authority seam.
- The changed-file credential sweep has zero high-signal matches. Live GitHub queues are 0 open
  Dependabot, 0 code-scanning, and 0 secret-scanning alerts; no competing PR is open; exact
  `origin/dev` remains `3d45529624a275652d4c6793271859dfe5add152`.

## [S] Security, reliability, and cost

The rollup revalidates normalized facts and returns no prefix on an invalid later scope or fact.
Only aggregate amounts plus explicit authorized cluster coverage survive; namespace names and raw
provider/workload/source metadata do not. Work is bounded in memory and CPU and creates no network,
cloud API, storage, egress, logging-volume, credential, privilege, or recurring-service cost.

## [P] Primary sources

- [OpenCost allocation API](https://opencost.io/docs/integrations/api/)
- [OpenCost installation and access](https://opencost.io/docs/installation/install/)
- [OpenCost v1.120.2](https://github.com/opencost/opencost/releases/tag/v1.120.2)
- [ADR 0011](../docs/adr/0011-opencost-namespace-cost-facts.md)
- [ADR 0012](../docs/adr/0012-opencost-coverage-aware-workspace-rollup.md)

## [N] Next

Create one signed DCO/GSTACK commit, publish a PR to `dev`, require exact-head CI/CodeQL/hosted
CodeRabbit, merge while preserving the signed head, and require exact post-merge `dev` evidence
before closing issue 284.

## [C] Checkpoint #1

Issue, decision, code, focused race coverage, adversarial tests, exact 50,000-execution fuzz proof,
README, mirrored roadmap, ADR, and GSTACK journal are present in the EXTENDED worktree.

## [C] Checkpoint #2

The complete local CI, isolation, reproducible release, and real-cluster gates pass from the
EXTENDED worktree. Both OpenCost fuzz boundaries pass 50,000 executions. The first complete
CodeRabbit review's sole documentation finding is corrected and the second review reports zero
findings. Manual red-team, credential scan, security queues, exact base, and no-competing-PR checks
are clean; signed publication remains the fail-closed gate.
