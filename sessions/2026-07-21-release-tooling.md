# Release supply-chain tooling refresh — 2026-07-21

## [S] Scope

Refresh the explicitly pinned SBOM and signing executables used by pull-request reproducibility and
tag release. The release identity, permissions, action commit pins, artifact graph, publish order,
and consumer verification contract remain unchanged.

## [D] Decision

- Syft advances from v1.46.0 to v1.49.0. The upstream release fixes Go `replace`-directive
  interpretation and adds root OCI-layout index support, both relevant to Sith's Go SBOM and
  multi-architecture image boundary.
- Cosign advances from v3.0.6 to v3.1.2. The upstream release fixes malformed-input panics and
  bundle signing/verification defects. Sith already emits bundles and uses none of the newly
  deprecated `--payload` or `--output-attestation` flags.
- A repository policy test now keeps CI, tag release, README prerequisites, and the supply-chain ADR
  synchronized instead of relying on manual version searches.

## [V] Verification

- Rebased onto exact `dev` merge `ae2d28de2d7fc6e6661098d9b1bb07e1b9381cad`, after its CI and
  CodeQL workflows completed successfully. The one shared Makefile insertion retained both the
  Wails and release-tooling policy gates.
- Official release assets are checksum-verified before local use. Both selected versions are stable
  upstream releases: [Syft v1.49.0](https://github.com/anchore/syft/releases/tag/v1.49.0), published
  2026-07-21, and [Cosign v3.1.2](https://github.com/sigstore/cosign/releases/tag/v3.1.2), published
  2026-07-17.
- Actionlint, ShellCheck, and the focused policy test pass. Its 9 assertions validate every
  synchronized pin, installer binding, documentation reference, and deprecated-flag exclusion.
- The real installed tools report Syft 1.49.0, Cosign 3.1.2, and GoReleaser 2.17.0.
- `go mod verify`, `govulncheck ./...`, and `make ci` pass, including race and all operator-facing
  policy tests.
- `make e2e-isolation` passes against PostgreSQL plus 100,000 tenant-boundary fuzz executions.
- `make release-check` passes two independently rebuilt archive/SBOM distributions, Homebrew
  formula validation, and the dual-architecture OCI layout contract using Syft 1.49.0.
- `make e2e-kind KIND=/Volumes/EXTENDED/MacData/tools/bin/kind` passes the fleet fan-out, immutable
  OCI image, and Argo application projection contracts in 241.080 seconds.

## [C] Security, operations, and cost

The update repairs producer-side parsing and verification behavior without adding credentials,
permissions, services, storage, egress, or recurring cloud cost. Pull-request and tag CPU duration
may vary slightly with the newer scanners, but the number of builds, SBOMs, signatures, and
attestations is unchanged.
