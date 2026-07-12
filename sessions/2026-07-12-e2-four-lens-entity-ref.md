# Session — 2026-07-12 — E2 four-lens EntityRef contract

**Builder:** Gnani Rahul · **Branch:** gnanirahulnutakki/feat/e2-four-lens-entity-ref
**Slice:** [#105](https://github.com/ArdurAI/sith/issues/105), E2 [#20](https://github.com/ArdurAI/sith/issues/20) · **Status:** ready for review

---

## [G] Goal

Establish the additive four-lens graph correlation boundary: every graph fact has one closed lens
and either a validated `EntityRef` or is explicitly unattached. This slice does not collect new
data, persist telemetry, add a network client, or touch the write path.

## [D] Standards decision

- OTel semantic conventions 1.43.0 defines stable `k8s.cluster.name`, `k8s.namespace.name`,
  `k8s.pod.name`, `k8s.node.name`, and `container.image.repo_digests`.
- The spec's previous `container.image.digest` spelling is not a current OTel attribute. Sith now
  parses one exact immutable `sha256:` value from stable repo-digest input and rejects tags,
  runtime IDs, short digests, and uppercase/non-hex forms.
- Local nodes always join with cluster plus namespace scope. Only a standalone exact digest is
  global, and the graph exposes that as an explicit image correlation rather than coalescing
  workload names.

## [T] Evidence

- Unit/race contract tests cover cluster and namespace collision rejection, explicit unattached
  facts, workspace isolation, source-provenance completion, exact digest correlation, and rejection
  of mutable tagged repo-digest input. Fleet race coverage: **88.4%**.
- `make ci` passed: formatting, vet, golangci-lint, govulncheck, full race suite, M0 safety (15
  assertions), performance gate, and the built `sith version` smoke.
- The real two-kind integration projects two live same-name `sith-payments` Deployments through
  the local-kubeconfig adapter and proves they remain two graph nodes. `make e2e-kind` passed from
  the final source.
- `make e2e-isolation` passed: PostgreSQL forced-RLS/destructive suites (hubdb **72.4%**) and the
  fixed **50,000x** `FuzzQueryScopedNeverLeaksForeignWorkspace` campaign.
- `make release-check` passed: two reproducible four-platform GoReleaser snapshots, SPDX SBOM
  verification, and generated Homebrew formula.
- Manual red-team review closed two ambiguity paths before the final run: an input carrying a
  mutable tag alongside an immutable digest is rejected, and every populated local `EntityRef`
  dimension participates in its key. CodeRabbit CLI was unavailable locally, so no external diff
  was sent; the manual review found no remaining P0/P1 issue.
- Post-gate cleanup confirmed zero kind clusters; Docker prune reclaimed **1.21 GB**. GitHub
  Dependabot, code-scanning, and secret-scanning queues were each **0** open alerts.

## [S] Scope and safety

- This is an additive local data contract only: it opens no endpoint, stores no telemetry series,
  adds no connector verb, list/watch, arbitrary attribute, secret, or write path.
- The blocked ClusterGateway transport remains isolated in [#103](https://github.com/ArdurAI/sith/issues/103)
  and [#104](https://github.com/ArdurAI/sith/issues/104); this slice does not weaken that boundary.
- Follow-up [#106](https://github.com/ArdurAI/sith/issues/106) owns normalization of legacy brain
  runtime-image evidence before it can participate in fleet-wide correlation.

## [N] Next

Check README applicability, create the signed/DCO/GSTACK checkpoint, publish the narrow PR into
`dev`, and merge only after its CI and exact post-merge CI are green.
