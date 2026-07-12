# Session — 2026-07-12 — P1 policy-hook seam

**Builder:** Gnani Rahul · **Branch:** gnanirahulnutakki/feat/p1-policy-hook
**Slice:** [#11](https://github.com/ArdurAI/sith/issues/11), Phase 1 [#9](https://github.com/ArdurAI/sith/issues/9) · **Status:** ready for review

---

## [G] Goal

Establish the mandatory, audited policy-enforcement seam for hub reads so Ardur can later replace
only the policy hook with a real PDP rather than re-architecting each fleet reader for Phase-2 writes.

## [D] Design

- `internal/pep.Enforcer` owns the ordered post-authentication read gate: signed tenancy scope,
  role, closed verb, validated canonical-argument SHA-256 digest, policy hook, then audit. It models
  `allow`, `deny`, and `require-approval` and fails closed on a hook, decision-validation, or audit
  failure.
- Hub snapshot collection, fleet sourcing, and cross-cluster correlation require the concrete
  enforcer at construction and invoke it before any downstream store, transport, or query call.
  This forbids a substitute allow-stub from bypassing auditing.
- Audit events retain only workspace, actor, role, fixed action/verb, verdict, and a bounded ASCII
  reason code. They exclude credentials, selectors, targets, facts, snapshots, and result data.
- Local kubeconfig operations remain outside the hub PEP by design; their source boundary test
  continues to prohibit an `internal/pep` import.

## [T] Evidence

- Focused race tests cover allow, deny, approval-required, hook failure, invalid decisions, audit
  failure, canonical argument-digest binding, strict reason-code validation, mandatory configuration,
  audit-schema privacy, forbidden PEP imports, and refusal before hub I/O.
- `make ci` plus its separately verified e2e/build tail passed: formatting, vet, golangci-lint,
  `govulncheck`, full race suite (PEP **85.5%**, hubfleet **73.0%**), M0 safety assertions, warm-cache
  performance, binary smoke, and standard e2e.
- `make e2e-kind` passed with the concrete PEP enabled for real two-spoke snapshot collection and
  correlation. `make e2e-isolation` passed forced PostgreSQL RLS/destructive suites (hubauth
  **85.2%**, hubserver **89.5%**, fleetcache **87.0%**, hubdb **72.4%**) plus the fixed **50,000x**
  cross-workspace fuzz campaign.
- `make release-check` completed two reproducible four-platform snapshots, release-distribution
  verification, SPDX SBOM checks, digest comparison, and Homebrew formula generation. An independent
  final `releasecheck verify` and formula generation also passed.
- Manual red-team review found and fixed a potential injectable-authorizer bypass by requiring the
  concrete `*pep.Enforcer` at all hub-read constructors. It then confirmed fail-closed policy/audit,
  no direct network/connector/dispatch imports, opaque argument binding, and audit exclusion of raw
  selector data. CodeRabbit CLI is unavailable in this environment; no external diff was submitted.
- Post-gate cleanup confirmed zero kind clusters; Docker prune reclaimed **1.318 GB**. GitHub
  Dependabot, code-scanning, and secret-scanning queues were each **0** open alerts.

## [S] Scope and safety

No write verb, action dispatch, Ardur network client, credential, endpoint, telemetry export, raw
audit payload, or local-mode behavior is introduced. The blocked ClusterGateway transport #103/#104
remains independent and untouched.

## [N] Next

Check README applicability, create the signed/DCO/GSTACK checkpoint, publish one narrow PR into
`dev`, and merge only after hosted CI and exact post-merge CI are green.
