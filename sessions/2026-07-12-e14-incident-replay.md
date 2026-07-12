# Session — 2026-07-12 — E14 deterministic incident replay

**Builder:** Gnani Rahul · **Branch:** gnanirahulnutakki/feat/e14-incident-replay
**Slice:** [#111](https://github.com/ArdurAI/sith/issues/111), E14 [#46](https://github.com/ArdurAI/sith/issues/46) · **Status:** ready for review

---

## [G] Goal

Add a local, deterministic, sanitized replay corpus for the advisory Investigation Brain so rule
catalog changes cannot silently regress ranking, evidence citation, coverage abstention, cause
chaining, advisory rendering, or exact immutable-image fleet correlation.

## [D] Design

- The replay harness is test-only under `internal/brain`; no binary command, connector, endpoint,
  cache persistence, account, credential, list/watch, or execute path is introduced.
- JSON decoding is bounded and rejects unknown/trailing data. Every fixture must declare its version,
  sanitized status, canonical top rule, full advisory shape, exact cited lens/predicate/value evidence,
  coverage gaps, and fleet expectation.
- The corpus includes a safe negative case for bare runtime image digests. Fleet-wide output is
  allowed only by the existing exact `repository@sha256:<64>` boundary.

## [T] Evidence

- Focused replay tests, the full brain race suite, and `golangci-lint` pass. The fleet-correlation
  replay initially exposed nondeterministic representative/citation selection from map iteration;
  the brain now sorts each correlation group and every citation before returning the result.
- `go test -race -count=50 -run '^TestIncidentReplayFixturesAreDeterministic/r3-fleet-image-correlation$'
  ./internal/brain` passed after the ordering fix.
- `make ci` passed: formatting, vet, lint, `govulncheck`, full race suite (brain **83.9%**), M0
  safety assertions, performance gate, e2e smoke, and binary build.
- `make e2e-kind` passed against two fresh local kind clusters. `make e2e-isolation` passed forced
  PostgreSQL RLS/destructive suites (hubauth **85.2%**, hubserver **89.5%**, fleetcache **87.0%**,
  hubdb **72.4%**) plus the fixed **50,000x** cross-workspace fuzz campaign.
- `make release-check` passed two reproducible four-platform snapshots, SPDX SBOM verification, and
  generated Homebrew formula.
- Manual red-team review confirms strict fixture decoding, synthetic-only corpus guidance, exact
  immutable-image correlation, explicit non-fleet rejection for a bare runtime digest, no new
  connector/action dependency, and deterministic fleet output. CodeRabbit CLI is unavailable in this
  environment; no external diff was submitted. No P0/P1 concern remains.
- Post-gate cleanup confirmed zero kind clusters. Docker prune reclaimed **2.285 GB**. GitHub
  Dependabot, code-scanning, and secret-scanning queues were each **0** open alerts.

## [S] Scope and safety

Fixtures use only synthetic identifiers and timestamps. The brain remains pure/read-only and continues
to return an advisory for a human; it does not import or invoke an action, connector, secret, or
remote service.

## [N] Next

Check README applicability, create the signed/DCO/GSTACK checkpoint, publish the narrow PR into
`dev`, and merge only after its CI and exact post-merge CI are green.
