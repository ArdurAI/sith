# E9 fail-closed Helm hub chart contract

Issue: [#133](https://github.com/ArdurAI/sith/issues/133)

Branch: `gnanirahulnutakki/feat/e9-helm-contract`

Base: `origin/dev` at `401431bcbeb571f8440dcc6f0fff556a021df773`

## [G] Goal

Provide a reviewable, install-time admission boundary for the unpublished Sith hub: an operator
must provide an immutable hub image and references to already-provisioned runtime and migration
Secrets before Helm can render the release.

## [S] Scope

- Add a versioned Helm v2 chart that accepts only an explicit `repository@sha256:<64 lowercase
  hex>` hub image reference; reject tags and digest-less references even if Helm schema validation
  is bypassed.
- Render only references to existing runtime and migration Secrets. The chart never creates a
  Secret or renders `data`/`stringData` material.
- Run `sith hub migrate` as a short-lived pre-install/pre-upgrade hook with the owner database URL
  and no Kubernetes API token; run the long-lived hub with the non-owner runtime configuration.
- Deploy the hub with a non-root identity, a read-only root filesystem, dropped Linux capabilities,
  `privileged: false`, no privilege escalation, and `RuntimeDefault` seccomp.
- Grant the hub only `get` on core `secrets` named `sith-reader` through a ClusterRole. No list,
  watch, wildcard resource, or migration ServiceAccount access is rendered.
- Pin Helm v4.2.2 by verified upstream SHA-256 in CI and inspect a real Helm render under the race
  detector. This slice does not publish an image, provision a KMS/external-secret materializer, or
  impose a broad egress NetworkPolicy; cluster operators retain network-policy ownership.

## [A] Analysis and red-team checks

- `values.schema.json` rejects unknown inputs and invalid required values, while the image template
  independently calls `required`, `regexMatch`, and `fail`. A caller using
  `--skip-schema-validation` therefore cannot bypass the immutable-image admission rule.
- The Deployment service selector is tested against the Pod template labels, and the
  ClusterRoleBinding subject is tested against the Deployment service-account name, preventing
  future name/selector drift from silently disconnecting the hub or its constrained permission.
- The runtime Secret volume has only the required public/session and TLS key files. The database
  URL is read directly from the existing runtime Secret and no secret value crosses the rendered
  manifest or test output.
- Helm hooks are deliberately bounded (`backoffLimit: 0`, 300-second deadline, one-hour TTL) and
  include the hook deletion policy. A failed migration blocks install/upgrade before the non-owner
  hub deployment is accepted.
- Initial peer review identified four hardening gaps: an unverified binding subject, unchecked
  Service selector/ports, implicit `privileged`, and permissive float-to-int test conversion.
  All were corrected. The final independent review against `401431b` returned zero findings.

## [T] Tests and evidence

- Official Helm v4.2.2 was downloaded over HTTPS and verified against the upstream darwin/arm64
  SHA-256 before local contract testing.
- `make e2e-helm HELM=/Volumes/EXTENDED/MacData/tools/bin/helm-v4.2.2`: PASS (2.057s) after the
  final review corrections. It runs `helm lint`, renders valid values, rejects mutable and
  digest-less images, blank Secret references, and unknown keys, and proves the mutable image is
  still rejected with `--skip-schema-validation`.
- `make ci`: PASS (format, vet, golangci-lint, govulncheck with no vulnerabilities, race/coverage
  tests, source-boundary and operator-script checks, binary E2E, latency check, and reproducible
  build).
- `make e2e-isolation`: PASS, including PostgreSQL RLS/destructive isolation and the fixed
  50,000-execution selector fuzz campaign. Final coverage: hubauth 85.2%, hubserver 90.0%, hubdb
  destructive suite 73.5%.
- `make release-check`: PASS, including reproducible Darwin/Linux amd64/arm64 snapshot archives,
  SPDX SBOM generation, checksums, and repeated Homebrew formula rendering.
- `make e2e-kind KIND=/Volumes/EXTENDED/MacData/tools/bin/kind`: PASS (159.994s) against the real
  two-cluster contract; cleanup removed both temporary clusters.
- Final CodeRabbit uncommitted-diff review against `401431b`: PASS, zero findings across all chart,
  CI, documentation, and Helm-test files.
- `git diff --check`: PASS. Final GitHub queues before commit: Dependabot 0, code scanning 0,
  secret scanning 0. `kind get clusters` reports none; Docker prune reclaimed 1.364 GB of only
  unused kind/build artifacts while preserving the active `elated_antonelli` and
  `ardur-191-baseline` containers.

## [C] Checkpoint #1

- Final source is ready for signed/DCO commit
  `2026-07-14/e9-helm-contract#1`; PR, merge, and exact post-merge `dev` evidence remain pending.
