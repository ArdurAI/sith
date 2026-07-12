# Session — 2026-07-12 — e2-cross-cluster-correlation

**Builder:** Gnani Rahul · **Branch:** gnanirahulnutakki/feat/e2-cross-cluster-correlation
**Slice(s):** E2 cross-cluster correlation query ([#10](https://github.com/ArdurAI/sith/issues/10), parent [#9](https://github.com/ArdurAI/sith/issues/9)) · **Status:** validated, publication pending

---

## [G] Goal

Answer a tenant-scoped question such as “every cluster where deployment `X` is unhealthy” through
the hub fleet model, with exact resource identity and explicit stale coverage.

## [S] Scope

- Add a typed read-only correlation service over the RLS-scoped normalized fleet query path.
- Require exact resource name plus a closed health condition; surface coverage/staleness unchanged.
- Reuse existing local `sith correlate`, local UI, and local MCP read presentation rather than
  introducing an incomplete hub listener.
- Out: OCM transport wiring, arbitrary query language, image/CVE correlation, writes, and hub UI.

## [A] Design

- The persisted hub query currently has exact kind/namespace and a status equality filter but only
  a name prefix. Correlation cannot safely approximate `payments` as `payments-*`; it needs an
  exact name predicate and a typed health-negation condition.
- The correlation service will depend only on a narrow RLS query interface, so hubdb remains the
  persistence owner and no raw database pool reaches presentation code.

## [T] Evidence so far

- #100 / PR #101 established registered-spoke collection, normalized persistence, stale-on-failure,
  and `fleet.Source` adaptation. Exact post-merge CI 29187858172 is green.

## [C] Checkpoint #1

- Added exact `Selector.Name` and closed `Selector.HealthNot`, forbidding ambiguous simultaneous
  health equality/negation. The database requires health-only predicates, exact JSON resource name,
  and an explicit `status` field for negation.
- Added `hubfleet.Correlator`, which accepts only a signed read scope plus exact kind/name/namespace
  and one closed excluded health status; it delegates through a narrow tenant-scoped query interface.
- Snapshot validation now rejects duplicate normalized facts, preventing duplicate source records
  from inflating a correlation answer.

## [T] Final validation

- `make ci`: PASS — format, vet, golangci-lint (0 findings), govulncheck (0 vulnerabilities), full
  race suite, M0 safety harness, local e2e, and build. Hubfleet unit coverage: 70.3%.
- `make e2e-isolation`: PASS — PostgreSQL hubdb coverage: 72.4%; selector isolation fuzz: 50,000
  executions. The PostgreSQL proof includes two workspace spokes, exact `payments` versus
  `payments-canary`, healthy/non-healthy selection, stale coverage, and a foreign-workspace denial.
- `make e2e-kind`: PASS in 91.769s against two temporary Kubernetes 1.36.1 clusters. The fixture
  correlated the exact real-source-backed `sith-worker-sample` across two spokes, then retained it
  as stale after its spoke became unreachable.
- `make release-check`: PASS — two reproducible Darwin/Linux amd64/arm64 snapshots, SPDX SBOMs,
  checksums, and Homebrew formula rendering.
- Strict self-review/red-team: PASS. Checked exact-name versus prefix behavior, health-only closed
  negation, missing-status exclusion, duplicate fact rejection, scope authorization, RLS routing,
  stale coverage when the stale spoke is not a match, and no new endpoint/token/credential input.
  CodeRabbit CLI is unavailable in this environment, so no external CodeRabbit approval is claimed.

## [C] Checkpoint #1

- Validated implementation; next: security queues, Docker cleanup, README recheck, signed/DCO/GSTACK
  commit, PR into `dev`, green CI and exact post-merge verification, then issue/roadmap updates.
