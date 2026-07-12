# Sith release and verification guide

Sith releases are immutable, tag-driven builds from `main`. The release job creates a draft,
builds four archives with GoReleaser, emits an SPDX 2.3 SBOM for each archive with Syft, signs the
archives, SBOMs, and checksum manifest with keyless Cosign, and creates GitHub SLSA provenance plus
one SBOM attestation per platform. The draft becomes public only after every step succeeds.

The workflow follows the primary guidance for [GitHub artifact attestations](https://docs.github.com/en/actions/how-tos/secure-your-work/use-artifact-attestations/use-artifact-attestations),
[GoReleaser reproducible Go builds](https://goreleaser.com/customization/builds/builders/go/#reproducible-builds),
[GoReleaser SBOM generation](https://goreleaser.com/customization/sbom/), and
[Cosign blob bundles](https://docs.sigstore.dev/cosign/signing/signing_with_blobs/).

## Install with Homebrew

```bash
brew tap ArdurAI/tap
brew trust --formula ArdurAI/tap/sith
brew install sith
sith version --output json
```

Homebrew 6 requires explicit trust for third-party taps. Trust only the Sith formula as shown; do
not disable tap trust or broaden it to the whole tap. Older Homebrew releases without tap trust can
omit the `brew trust` command.

The tap formula is generated from the release checksum manifest; it does not carry hand-entered
URLs or hashes. The release workflow signs the formula itself. The tap's own repository automation
verifies that signature and the signed checksum manifest before importing the formula, so the Sith
release token never needs cross-repository write access.

## Verify a release

Set the release and platform, then download its assets:

```bash
tag=v0.1.0
version=${tag#v}
platform=darwin_arm64
gh release download "$tag" --repo ArdurAI/sith --dir "sith-$version"
cd "sith-$version"
shasum -a 256 -c checksums.txt
```

Verify the archive's keyless signature. The certificate identity binds the signature to the exact
Sith release workflow and tag; the issuer check binds it to GitHub Actions OIDC:

```bash
archive="sith_${version}_${platform}.tar.gz"
cosign verify-blob \
  --bundle "${archive}.sigstore.json" \
  --certificate-identity "https://github.com/ArdurAI/sith/.github/workflows/release.yml@refs/tags/${tag}" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
  "$archive"
```

Verify SLSA provenance online against GitHub's attestation store:

```bash
gh attestation verify "$archive" \
  --repo ArdurAI/sith \
  --signer-workflow ArdurAI/sith/.github/workflows/release.yml
```

Or verify with the release-attached provenance bundle:

```bash
gh attestation verify "$archive" \
  --repo ArdurAI/sith \
  --signer-workflow ArdurAI/sith/.github/workflows/release.yml \
  --bundle "sith_${version}_provenance.sigstore.json"
```

Verify that the platform SBOM is cryptographically tied to the same archive:

```bash
gh attestation verify "$archive" \
  --repo ArdurAI/sith \
  --signer-workflow ArdurAI/sith/.github/workflows/release.yml \
  --predicate-type https://spdx.dev/Document/v2.3 \
  --bundle "sith_${version}_${platform}.sbom.sigstore.json"
```

These checks establish producer identity, artifact integrity, build provenance, and the SBOM
binding. They do not prove that every dependency is vulnerability-free; consumers must still
evaluate the attached SBOM against their own policy and current advisory data.

## Maintainer release procedure

1. Merge the feature PR into `dev`, ensure the full CI and release-snapshot jobs are green, then
   merge a reviewed `dev` to `main` release PR. `dev` is the durable integration source: never use
   `--delete-branch` for this release PR. Automatic branch deletion is reserved for merged feature
   branches.
2. From an up-to-date `main`, run `make ci` and `make release-check`. The latter compares archive
   SHA-256 digests across two complete builds; SBOM creation timestamps and Sigstore signatures are
   intentionally not expected to be byte-for-byte reproducible.
3. Create an annotated, signed stable-semver tag on the release commit and push only that tag.
4. Watch the `release` workflow. A failure leaves a draft, not a partially trusted public release.
   A rerun replaces the incomplete draft and its assets.
5. Verify one archive with the commands above, dispatch the `ArdurAI/homebrew-tap` sync workflow,
   and prove a clean `brew install sith && sith version` before announcing the release.
6. Check Dependabot, code-scanning, and secret-scanning alerts after publication.
7. Confirm `dev` still exists at the intended integration tip before starting the next feature
   branch.

Published versions are immutable. A bad public release is corrected with a new patch version; do
not replace its tag or silently rewrite assets. The release job uses only short-lived GitHub OIDC
credentials for Fulcio/Rekor and GitHub attestations. No long-lived signing key or cross-repository
Homebrew token is stored in Sith.

## Cost and operational notes

The incremental cost is GitHub-hosted runner time for four cross-builds, two snapshot builds on
each PR, Syft scans, and five attestations on a tag. Fulcio and Rekor use Sigstore's public-good
service for this public repository. Releases create no runtime cloud service, NAT egress path, or
persistent signing infrastructure.
