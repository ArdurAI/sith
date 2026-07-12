# Session — 2026-07-12 — e2-spoke-snapshot-collector

**Builder:** Gnani Rahul · **Branch:** gnanirahulnutakki/feat/e2-read-federation
**Slice(s):** E2 bounded tenant-scoped spoke snapshot collector ([#100](https://github.com/ArdurAI/sith/issues/100), parent [#9](https://github.com/ArdurAI/sith/issues/9)) · **Status:** delivered

---

## [G] Goal

Collect bounded inventory and health snapshots from registered OCM-brokered spokes, persist them
inside the existing forced-RLS workspace boundary, and return coverage that makes a failed spoke
visible without dropping its last-known facts.

## [S] Scope

- Define a transport-independent registered-spoke snapshot contract; it carries no endpoint,
  kubeconfig, projected token, or arbitrary network destination.
- Bound one collection per spoke with a timeout and isolate failures so healthy spokes still answer.
- Persist only normalized inventory/health evidence and current cluster lifecycle metadata through
  tenant-scoped database transactions.
- Out: OCM ClusterGateway/managed-serviceaccount wire-up, spoke agent packaging, hub list/watch,
  arbitrary connector querying, writes, and persistent raw credentials.

## [A] Research and design

- E0 proves OCM `cluster-proxy` plus `managed-serviceaccount` reach and scoped-token RBAC denial.
  OCM documents that combination for hub reads, while cautioning against hub-side list/watch; the
  collector therefore uses bounded snapshots and stale-on-failure retention.
- The existing `sith.clusters` and `sith.fleet_facts` tables are already RLS-protected. The design
  will add only lifecycle metadata required to distinguish last success from a failed attempt.

## [T] Evidence so far

- #94 and parent E1 #85 are delivered; Phase-L plus E1 was promoted to `main` in PR #99 after
  green CI and CodeQL. Dependabot, code scanning, and secret scanning remain zero.
- Child #100 was filed under #9 to keep the collector seam reviewable; the concrete OCM transport
  remains an explicit later M0-lab proof rather than an implicit credential expansion.

## [C] Checkpoint #1

- Added `internal/hubfleet`: an injected registered-spoke transport and bounded collector with a
  fixed `ocm-spoke` source stamp, 1–30-second per-spoke deadline, sorted honest coverage, and
  stale-on-failure retention. Transport failures persist only one closed failure kind.
- The snapshot profile allows only source-bound `inventory`/`health` facts and a small normalized
  payload vocabulary. It rejects raw object fields, duplicate JSON keys, credentials/endpoints,
  secrets, attributes, display hints, deep links, and opaque native metadata before persistence.
- Added the `hubfleet.Source` adapter so a signed workspace scope reads the same `fleet.Source`
  model as local mode. Added PostgreSQL lifecycle migration `0005`: attempt/error state plus
  structured resource/provenance columns while keeping existing forced RLS policies intact.
- Implemented RLS-scoped registered-spoke lookup, atomic per-spoke replacement, generic-failure
  staleness, bounded normalized querying, and a no-leak foreign-workspace query shape.

## [T] Final validation

- `make ci`: PASS — format, vet, golangci-lint (0 findings), govulncheck (0 vulnerabilities),
  full race suite, M0 safety harness, local e2e, and build. Hubfleet unit coverage: 68.6%.
- `make e2e-isolation`: PASS — PostgreSQL RLS/hubdb destructive coverage: 72.7%; selector
  isolation fuzz: 50,000 executions.
- `make e2e-kind`: PASS in 83.872s against two temporary Kubernetes 1.36.1 clusters. The test
  collected real pod-derived normalized inventory/health from both clusters, then made one source
  unavailable and verified retained stale coverage.
- `make release-check`: PASS — two reproducible Darwin/Linux amd64/arm64 snapshots, SPDX SBOMs,
  checksums, and Homebrew formula rendering.
- Strict self-review/red-team: PASS. Confirmed no endpoint/token/kubeconfig channel in the transport
  contract; exact source/workspace binding; future/old snapshot rejection; duplicate JSON and raw
  payload rejection; failure-string non-persistence; atomic replacement; RLS-scoped reads/writes;
  tenant metadata non-disclosure; bounded SQL query limit; and stale semantics. CodeRabbit CLI is
  unavailable in this environment, so no external CodeRabbit approval is claimed.
- Final GitHub queues: Dependabot 0, code scanning 0, secret scanning 0. Docker prune reclaimed
  1.659 GB, left unrelated active containers running, and confirmed zero kind clusters.

## [C] Checkpoint #1

- Validated implementation; next: README recheck, signed/DCO/GSTACK commit, PR into `dev`, green
  CI and exact post-merge verification, then issue/roadmap updates.

## [C] Delivery

- Signed/DCO/GSTACK commit: `5e5f426`
  (`2026-07-12/e2-spoke-snapshot-collector#1`).
- Delivery PR [#101](https://github.com/ArdurAI/sith/pull/101) merged cleanly into `dev` as
  `6b4fd0ba959613279bc52b30283c696f935b80d5` on 2026-07-12 after PR CI passed core in 6m46s and
  reproducible archives/SPDX SBOM/Homebrew formula in 1m00s.
- Exact post-merge `dev` CI [29187858172](https://github.com/ArdurAI/sith/actions/runs/29187858172)
  passed core/race/RLS/binary/two-kind fan-out in 6m47s and release verification in 1m03s.
- #100 is closed; #9, #20, and #39 record delivery evidence and point to #10. GitHub queues remain
  Dependabot 0, code scanning 0, secret scanning 0.
