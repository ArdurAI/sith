# ADR 0009: Reproducible and identity-bound release supply chain

**Status:** Accepted
**Date:** 2026-07-11
**Decision owners:** E9 / Slice P (#27)

## Context

Phase L promises `brew install sith` while the product itself is local-first and credential
sensitive. A release archive is therefore part of Sith's trust boundary: an attacker who replaces
the binary, its checksum, or its Homebrew hash bypasses every runtime guardrail before Sith starts.
E9 requires multi-platform builds, SBOMs, keyless signing, in-toto attestations, and SLSA Build
Level 2 from the first tag.

Release jobs also fail in the middle. Publishing archives before their signatures and provenance
exist creates a public interval with incomplete trust material. Giving the Sith repository a broad
personal token to update another repository would solve Homebrew automation by introducing a
long-lived cross-repository credential.

## Decision

1. GoReleaser v2.17.0 produces `darwin/amd64`, `darwin/arm64`, `linux/amd64`, and `linux/arm64`
   archives with Go 1.26.5. Builds disable CGO and VCS stamping, trim paths, consume a verified
   module cache with `GOPROXY=off` and `-mod=readonly`, use the commit time for embedded build
   metadata and file modification times, and normalize archive modes and ownership.
2. CI performs two complete snapshot builds and compares archive SHA-256 digests. It separately
   verifies checksum coverage, exact archive shape, native `sith version` metadata, and SPDX 2.3
   documents. SBOM timestamps and transparency-log signatures are not called reproducible.
3. Syft v1.46.0 creates one SPDX SBOM per archive. The checksum manifest covers both archives and
   SBOMs. Cosign v3.0.6 signs every archive, SBOM, and the checksum manifest with GitHub's short-lived
   OIDC identity and emits self-contained Sigstore bundles.
4. `actions/attest` v4 creates one SLSA provenance statement over the checksum manifest's subjects
   and one SPDX predicate binding for each archive/SBOM pair. Action dependencies are pinned to
   immutable commit SHAs.
5. GoReleaser publishes to a replaceable draft. Only after formula generation and all attestations
   are attached does the workflow make the release public. Stable tags must be annotated and point
   to a commit reachable from `main`.
6. The Homebrew formula is rendered by repository-owned, unit-tested Go code from the same checksum
   manifest and receives its own keyless signature. The tap verifies the formula and checksum
   identities, then pulls it using its own scoped automation; Sith stores no personal or
   cross-repository token.

## Consequences

- A consumer can verify checksums, the release-workflow identity, Rekor inclusion, SLSA provenance,
  and the archive-specific SBOM binding online or from attached bundles.
- A compromised ordinary feature workflow cannot mint the expected release identity because
  verification binds the certificate to `.github/workflows/release.yml` at a stable tag.
- GitHub-hosted runners and the public Sigstore/GitHub attestation services remain trusted build
  dependencies. SLSA L2 provides hosted, authenticated provenance; it is not a hermetic or
  independently reproduced build.
- Pull requests pay for two four-target builds and Syft scans. Tag releases pay for keyless signing
  and five attestations. There is no persistent signing service or runtime cloud cost.
- A failed tag run leaves a draft that can be replaced. A published bad release requires a new
  patch version; existing tags and assets are immutable.
- GoReleaser, Syft, Cosign, and action pins require explicit update PRs. Dependabot and regular
  security review must cover those pins as part of the release boundary.

## Alternatives considered

- **Long-lived Cosign key:** rejected because key storage, rotation, and compromise recovery add a
  high-value secret where GitHub OIDC and Fulcio provide an ephemeral identity.
- **Sign only `checksums.txt`:** rejected because direct archive and SBOM bundles make offline
  verification simpler and reduce trust-chain ambiguity.
- **Publish first, attest later:** rejected because a mid-run failure would expose a public release
  without its promised trust material.
- **Store a personal token for the Homebrew tap:** rejected because its blast radius crosses
  repositories and it cannot be least-privilege relative to a tap-owned updater.
- **Use GoReleaser's deprecated formula publisher or require a cask:** rejected. Sith keeps the
  conventional `brew install sith` formula UX while owning the small deterministic renderer.
- **Claim reproducible SBOMs and signatures:** rejected. Syft creation metadata, OIDC certificates,
  and transparency-log timestamps are intentionally time-varying; only the archives are compared
  byte for byte.
