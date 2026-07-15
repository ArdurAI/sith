# Sith release and verification guide

Sith releases are immutable, tag-driven builds from `main`. Stable releases use `vMAJOR.MINOR.PATCH`;
the beta channel uses `vMAJOR.MINOR.PATCH-beta.N` and never replaces the latest stable release. The release job creates a draft,
builds four archives with GoReleaser, emits an SPDX 2.3 SBOM for each archive with Syft, signs the
archives, SBOMs, and checksum manifest with keyless Cosign, and creates GitHub SLSA provenance plus
one SBOM attestation per platform. It also publishes the tag's multi-architecture hub image by its
manifest digest, signs it with keyless Cosign, and creates separate provenance and SPDX SBOM
attestations for that digest. The workflow then removes its registry credentials and proves that the
exact digest is anonymously pullable; the release draft becomes public only after every step
succeeds.

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

## Verify a hub OCI image

Hub images are published only by a signed Sith release tag and must be consumed by immutable
manifest digest. A release includes a signed `sith_<version>_hub.image` file whose only line is the
digest address; do not substitute the convenient version tag or add `latest` to a Helm value.

```bash
tag=vX.Y.Z                 # use a tag released after hub-image publication is enabled
version=${tag#v}
gh release download "$tag" --repo ArdurAI/sith \
  --pattern "sith_${version}_hub.image" \
  --dir "sith-$version"
image=$(cat "sith-$version/sith_${version}_hub.image")
case "$image" in ghcr.io/ardurai/sith-hub@sha256:*) ;; *) exit 1 ;; esac
```

Verify the keyless image signature against the exact tag workflow identity, then verify GitHub
provenance and the SPDX SBOM attestation. Before the first Hub-image release, an organization
package admin must make the `sith-hub` Container package public in its GitHub Package settings;
GitHub makes that choice irreversible. A completed Hub-image release is then anonymously pullable
by its release-bound digest only after these trust records are created and the workflow's anonymous
pull check passes. The later air-gap workflow consumes mirrored, pre-verified material rather than
weakening this verification boundary.

```bash
identity="https://github.com/ArdurAI/sith/.github/workflows/release.yml@refs/tags/${tag}"
cosign verify \
  --certificate-identity "$identity" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
  "$image"
gh attestation verify "oci://$image" \
  --repo ArdurAI/sith \
  --signer-workflow ArdurAI/sith/.github/workflows/release.yml
gh attestation verify "oci://$image" \
  --repo ArdurAI/sith \
  --signer-workflow ArdurAI/sith/.github/workflows/release.yml \
  --predicate-type https://spdx.dev/Document/v2.3
```

The existing Helm chart remains fail-closed: it accepts this digest and names of pre-materialized
runtime and migration Secrets only. It neither creates secret data nor supplies a KMS provider,
database, ingress, or mutable image reference.

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

## OCI image contract

Sith's deployment image recipe is validated locally before any registry publication. It assembles
the existing static Linux binary into a digest-pinned distroless runtime, uses non-root UID/GID
`65532`, and exposes only the `sith` entrypoint. The test builds and inspects both `linux/amd64`
and `linux/arm64` variants without pushing, then runs the native image with a read-only filesystem,
no network, no capabilities, and `no-new-privileges`; the same image must complete a hardened Job
on two real Kind clusters.

No network is an isolated image-check constraint, not the operational hub policy. A deployment
must allow only narrowly scoped egress to configured runtime dependencies, including the database
and, where enabled, the pinned OIDC discovery and JWKS endpoints.

Hub OCI images are published only as a release-boundary artifact, never by a pull request or local
build. Consumers must not infer a mutable image tag from a release archive. The
[`charts/sith-hub`](../charts/sith-hub) chart accepts only an explicit immutable
`repository@sha256:...` reference, and its defaults intentionally fail until an operator provides
that reference and existing Secret names. Older releases can lack the hub-image assets; use the
digest address attached to a release that includes them. Its fixed `light` and `heavy` profiles
alter only the reviewed resource envelope; both preserve the same digest, Secret-reference,
migration, RBAC, and workload-hardening contract. This first F9.3a slice is not a claim of the
parent feature's future in-chart database, HA, or cloud-KMS topology.

## Maintainer release procedure

1. Merge the feature PR into `dev`, ensure the full CI and release-snapshot jobs are green, then
   open a `dev` to `main` release PR. The same full CI and release-snapshot jobs must be green on
   that release PR before merging it, and the exact `main` push run must pass afterward. `dev` is
   the durable integration source: never use `--delete-branch` for this release PR. Automatic
   branch deletion is reserved for merged feature branches.
2. From an up-to-date `main`, run `make ci` and `make release-check`. The latter compares archive
   SHA-256 digests across two complete builds; SBOM creation timestamps and Sigstore signatures are
   intentionally not expected to be byte-for-byte reproducible.
3. Verify the configured tagger identity before creating a release tag. Local `git tag -v` proves
   that a signature is cryptographically valid on this machine; it does not prove that GitHub can
   associate the SSH signing key and tagger identity with an account. The release workflow requires
   GitHub verification, so this is a fail-closed preflight, not an optional cosmetic check. The
   signing key must be registered with the intended GitHub account and the configured tagger email
   must be one GitHub recognizes for that account. The account's verified no-reply address is an
   appropriate choice when its public email is unavailable.

   ```bash
   tagger_email="$(git config user.email)"
   test -n "$tagger_email"
   test "$(git config gpg.format)" = ssh
   signing_key_file="$(git config user.signingkey)"
   test -f "$signing_key_file"
   signing_key="$(awk '{print $1 " " $2}' "$signing_key_file")"
   verified_emails="$(gh api user/emails --paginate \
     --jq '.[] | select(.verified) | .email')"
   grep -Fxq -- "$tagger_email" <<<"$verified_emails"
   github_login="$(gh api user --jq '.login')"
   registered_signing_keys="$(gh api "users/${github_login}/ssh_signing_keys" \
     --paginate --jq '.[].key' | awk '{print $1 " " $2}')"
   grep -Fxq -- "$signing_key" <<<"$registered_signing_keys"
   ```

   The `user/emails` call intentionally reads only the authenticated maintainer's local account
   metadata, while the signing-key comparison uses only the account's public signing keys and
   ignores optional key comments. Neither command prints key or email material; do not paste their
   values into issues, logs, or journals. If either command cannot run or a comparison does not
   match, resolve the account identity before creating a tag. See
   GitHub's [signature-verification overview](https://docs.github.com/en/authentication/managing-commit-signature-verification/about-commit-signature-verification)
   and [tag-signing guide](https://docs.github.com/en/authentication/managing-commit-signature-verification/signing-tags).
4. Create an annotated, SSH-signed canonical release tag on the release commit and verify it
   locally, then push only that tag. Stable tags use `vMAJOR.MINOR.PATCH`; beta tags use exactly
   `vMAJOR.MINOR.PATCH-beta.N` with a numeric `N`. The beta workflow publishes a prerelease with
   the same archives, SBOMs, signatures, and attestations, but never replaces the latest stable
   release:

   ```bash
   tag=vX.Y.Z              # or vX.Y.Z-beta.N
   git tag -s -a "$tag" -m "release: $tag"
   git tag -v "$tag"
   git push origin "refs/tags/$tag"
   ```

   After the push, confirm GitHub's tag-object verdict while the release workflow is running. This
   distinguishes local signature validity from the verification that the release gate enforces:

   ```bash
   tag_object=$(gh api "repos/ArdurAI/sith/git/ref/tags/${tag}" --jq '.object.sha')
   test "$(gh api "repos/ArdurAI/sith/git/tags/${tag_object}" \
     --jq '.verification.verified')" = true
   ```

   If this check fails, do not delete, force-push, or retag the published name. Diagnose the
   reported verification reason and cut a new patch version only after the identity issue is fixed.
   GitHub exposes the status and reason for signed tags in its
   [verification-status guidance](https://docs.github.com/en/authentication/troubleshooting-commit-signature-verification/checking-your-commit-and-tag-signature-verification-status).
5. Watch the `release` workflow. A failure leaves a draft, not a partially trusted public release.
   A rerun replaces the incomplete draft and its assets.
6. Verify one archive with the commands above, dispatch the `ArdurAI/homebrew-tap` sync workflow,
   and prove a clean `brew install sith && sith version` before announcing the release.
7. Check Dependabot, code-scanning, and secret-scanning alerts after publication.
8. Confirm `dev` still exists at the intended integration tip before starting the next feature
   branch.

Published versions are immutable. A bad public release is corrected with a new patch version; do
not replace its tag or silently rewrite assets. The release job uses only short-lived GitHub OIDC
credentials for Fulcio/Rekor and GitHub attestations. No long-lived signing key or cross-repository
Homebrew token is stored in Sith.

## Cost and operational notes

The incremental cost is GitHub-hosted runner time for four cross-builds, two snapshot builds on
each PR, Syft scans, and seven attestations on a tag. Fulcio and Rekor use Sigstore's public-good
service for this public repository. Releases create no runtime cloud service, NAT egress path, or
persistent signing infrastructure.
