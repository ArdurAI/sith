# x/text malformed-input security update — 2026-07-21

## [S] Scope

This slice starts from exact `dev` merge `ff435833816ddc3a523686304ef52cd8a4444f24` and changes
only the vulnerable Go module resolution. No production API, configuration, schema, IAM, network,
or release contract changes.

## [D] Decision

Upgrade `golang.org/x/text` from v0.38.0 to v0.39.0, the first release containing the fix for
[GO-2026-5970](https://pkg.go.dev/vuln/GO-2026-5970) / CVE-2026-56852. The affected normalization
package is reachable through `internal/hubdb -> pgx/pgconn -> x/text/secure/precis ->
x/text/unicode/norm`; malformed external identity or credential text therefore must not retain the
known infinite-loop implementation.

The minimum fixed version is intentional. v0.40.0 is available, but adopting a later pre-v1 module
than required would widen compatibility risk without improving this remediation.

## [V] Verification

- `go mod verify` passed.
- `govulncheck ./...` reported no reachable vulnerabilities.
- `go test -race -count=1 ./...` passed.
- Current Trivy and Grype scans no longer report CVE-2026-56852.
- Full `make ci` passed formatting, vet, zero-issue lint, vulnerability analysis, every race test,
  operator policy, alert rules, performance, binary integration, and production build.
- `make e2e-isolation` passed PostgreSQL 18.4 forced-RLS coverage and both 50,000-execution
  workspace-isolation fuzzers.
- `make release-check` passed dual four-platform reproducibility, SPDX SBOMs, checksums, Homebrew,
  and release-derived amd64/arm64 OCI verification.
- The Kubernetes 1.36.1 kind gate passed fleet fan-out, OCI image, and Argo Application projection
  under the race detector in 242.332 seconds; teardown left no kind cluster or Sith Buildx builder.

Both broad scanners still list GO-2026-5932 against `x/crypto/openpgp`, for which the Go advisory
publishes no fixed version. Sith does not import the affected OpenPGP cleartext-signature packages,
and call-graph-aware `govulncheck` reports no reachable vulnerability; this is a scanner-level,
not-affected disposition rather than an ignored reachable finding.

## [C] Cost and blast radius

The module checksum and selected library implementation change at build time only. There is no new
service, storage, egress, credential, permission, or recurring cloud cost.

`README.md` was reviewed and remains accurate because no user-visible or operator-facing contract
changes.
