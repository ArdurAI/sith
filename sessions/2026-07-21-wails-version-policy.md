# Wails desktop version policy — 2026-07-21

## [S] Scope

The desktop module was already upgraded to Wails v2.13.0, but `make desktop-build` still required
v2.12.0. Its substring check also accepted lookalike version strings. This slice aligns the tool
gate with `go.mod` and makes exact-version enforcement independently testable on every CI runner.

## [D] Decision

Treat the `github.com/wailsapp/wails/v2` requirement in `go.mod` as the authoritative compatibility
version. A fail-closed verifier resolves the configured executable, requires a strict `vX.Y.Z`
expected value, executes the version command successfully, and compares its first output line
exactly. Additional informational lines remain compatible; prerelease, vendor, whitespace, empty,
and failed-command variants are rejected.

## [V] Verification

- Rebased without conflict onto the exact post-merge `dev` commit
  `9e135de08cab047386b0a948311e7443e2741404`, whose CI and CodeQL runs completed successfully.
- The policy test derives both pins and passed 12 assertions covering valid, lookalike,
  missing-command, command-failure, and malformed-expectation cases; both scripts pass ShellCheck.
- The checksummed Wails v2.13.0 CLI passes the verifier. The corresponding upstream release is
  stable and was published on 2026-07-06:
  <https://github.com/wailsapp/wails/releases/tag/v2.13.0>.
- `go mod verify`, `govulncheck ./...`, and `make ci` pass, including the race detector and all
  operator-facing policy tests.
- `make e2e-isolation` passes against PostgreSQL plus 100,000 tenant-boundary fuzz executions.
- `make release-check` passes two independently rebuilt archive/SBOM distributions, Homebrew
  formula validation, and the dual-architecture OCI layout contract.
- `make desktop-build` produces a strictly code-sign-valid `com.ardurai.sith` bundle containing a
  Mach-O arm64 executable.
- `make e2e-kind KIND=/Volumes/EXTENDED/MacData/tools/bin/kind` passes the fleet fan-out, immutable
  OCI image, and Argo application projection contracts in 241.937 seconds.

## [C] Security, operations, and cost

Exact matching prevents an unintended prerelease or vendor binary from satisfying the local build
gate. The change adds no runtime dependency, service, permission, network path, storage, or recurring
cloud cost.
