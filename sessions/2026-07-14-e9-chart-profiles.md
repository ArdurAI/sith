# E9 F9.3a fail-closed hub resource profiles

Issue: [#135](https://github.com/ArdurAI/sith/issues/135)

Branch: `gnanirahulnutakki/feat/e9-chart-profiles`

Base: `origin/dev` at `741a5e5c26a5fe9374d26d66f82ef904fdfdb76a`

## [G] Goal

Add the first independently shippable F9.3 sub-slice: two bounded hub-chart resource envelopes
that preserve every existing admission, credential, and workload-security invariant. This is not a
claim that the still-unpublished chart already supplies an in-chart database, high availability, or
cloud-KMS topology.

## [S] Scope

- Version the chart `0.2.0` and add exactly `light` and `heavy` profile choices.
- Apply fixed CPU/memory requests and limits to both the long-running hub and its short-lived
  migration hook. Light requests 100m CPU/128Mi and caps at 500m/512Mi; heavy requests 500m/512Mi
  and caps at 2 CPU/2Gi.
- Reject unknown profiles and the removed arbitrary `resources` escape hatch through both the
  value schema and template logic, including when Helm schema validation is bypassed.
- Assert real Helm renders for both profiles. After removing only container resource blocks from
  deep-copied rendered objects, require every manifest to be structurally identical.
- Document the resource and cost envelope truthfully. Do not render an in-chart database, KMS
  materializer, HA replica topology, public image, broad egress policy, or a spoke-agent addon.

## [A] Analysis and red-team checks

- The parent F9.3 eventual design describes light/minimal-Postgres and heavy/HA/external-Postgres/
  cloud-KMS topology. Those dependencies do not exist yet: KMS custody belongs to E3 and a safe HA
  claim needs its own operational evidence. Issue #135 was therefore explicitly scoped and renamed
  F9.3a, preventing the profile labels from overstating current behavior.
- The profile is a closed choice, not a free-form resource object. Resource overrides create
  unreviewed scheduling and cost behavior, so a top-level `resources` value fails even under
  `--skip-schema-validation` rather than being silently ignored.
- Heavy reserves five times the requested CPU and four times the requested memory. This is a
  bounded scheduling/cost envelope, not a capacity recommendation; measured deployment sizing is
  deferred.
- The first peer review correctly flagged the communication risk that the docs could be read as
  implementing the parent F9.3 topology. The implementation was not broadened to make unproven
  claims. Instead, the issue and three operator-facing documents now label this as F9.3a and state
  the deferred topology/custody work. The final independent review found zero issues.

## [T] Tests and evidence

- Test-first check: the new real-Helm contract initially failed against the prior chart because
  `profile` was an unknown value. It passed after the selector and render assertions were added.
- Final focused command: `make e2e-helm HELM=/Volumes/EXTENDED/MacData/tools/bin/helm-v4.2.2`
  PASS (2.130s) after the documentation clarification. It lints/renders both profiles, asserts
  exact resources and non-resource equality, and rejects mutable images, malformed/missing image
  digests, blank runtime Secret names, unknown profiles, image-pull-secret input, and resource
  overrides; mutable images, unknown profiles, and resource overrides also fail with
  `--skip-schema-validation`.
- `make ci`: PASS (format, vet, golangci-lint, govulncheck with no vulnerabilities, race/coverage
  tests, source-boundary and operator-script safety checks, binary E2E, latency check, and
  reproducible build).
- `make e2e-isolation`: PASS, including digest-pinned PostgreSQL RLS/destructive isolation and the
  fixed 50,000-execution selector fuzz campaign. Final coverage: hubauth 85.2%, hubserver 90.0%,
  hubdb destructive suite 73.5%.
- `make release-check`: PASS; GoReleaser reproduced Darwin/Linux amd64/arm64 snapshot archives,
  generated SPDX SBOMs and checksums, and rendered the Homebrew formula twice.
- `make e2e-kind KIND=/Volumes/EXTENDED/MacData/tools/bin/kind`: PASS (161.993s) against real
  temporary two-cluster fan-out; test cleanup removed both clusters.
- Final CodeRabbit uncommitted-diff review against `741a5e5`: PASS, zero findings. The first
  review's one documentation-scope finding was resolved by the explicit F9.3a boundary above.
- `git diff --check`: PASS. Final GitHub queues before commit: Dependabot 0, code scanning 0,
  secret scanning 0. `kind get clusters` reports none. Docker prune reclaimed 1.364 GB of only
  disposable Kind/build artifacts while preserving active `elated_antonelli` and
  `ardur-191-baseline` containers.

## [C] Checkpoint #1

- Final source is ready for signed/DCO commit
  `2026-07-14/e9-chart-profiles#1`; PR, merge, and exact post-merge `dev` evidence remain pending.
