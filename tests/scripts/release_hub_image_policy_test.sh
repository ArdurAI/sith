#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
workflow="${repo_root}/.github/workflows/release.yml"
guide="${repo_root}/docs/RELEASE.md"
makefile="${repo_root}/Makefile"
verifier="${repo_root}/hack/verify-release-hub-image.sh"
workflow_contents="$(<"$workflow")"

assert_contains() {
  local text="$1"
  local needle="$2"
  local description="$3"

  if [[ "$text" != *"$needle"* ]]; then
    printf '[release-hub-image] FAIL: %s\n' "$description" >&2
    exit 1
  fi
  printf '[release-hub-image] PASS: %s\n' "$description"
}

workflow_step() {
  local name="$1"
  awk -v name="$name" '
    $0 == "      - name: " name { inside = 1 }
    inside && $0 ~ /^      - name: / && $0 != "      - name: " name { exit }
    inside { print }
  ' "$workflow"
}

workflow_job() {
  local name="$1"
  awk -v name="$name" '
    $0 == "  " name ":" { inside = 1 }
    inside && $0 ~ /^  [[:alnum:]_-]+:$/ && $0 != "  " name ":" { exit }
    inside { print }
  ' "$workflow"
}

make_target() {
  local name="$1"
  awk -v name="$name" '
    $0 ~ "^" name ":" { inside = 1 }
    inside && $0 ~ /^[[:alnum:]_-]+:/ && $0 !~ "^" name ":" { exit }
    inside { print }
  ' "$makefile"
}

assert_text_contains() {
  local text="$1"
  local needle="$2"
  local description="$3"

  if [[ "$text" != *"$needle"* ]]; then
    printf '[release-hub-image] FAIL: %s\n' "$description" >&2
    exit 1
  fi
  printf '[release-hub-image] PASS: %s\n' "$description"
}

release_job="$(workflow_job 'release')"
assert_contains "$workflow_contents" 'HUB_IMAGE: ghcr.io/ardurai/sith-hub' 'uses one fixed GHCR hub image name'
assert_text_contains "$release_job" 'packages: write' 'grants package publication permission to the release job'
for assertion in \
  'docker/setup-qemu-action@c7c53464625b32c7a7e944ae62b3e17d2b600130|pins QEMU setup action' \
  'docker/setup-buildx-action@8d2750c68a42422c14e847fe6c8ac0403b4cbd6f|pins Buildx setup action' \
  'docker/login-action@c94ce9fb468520275223c153574b00df6fe4bcc9|pins registry login action' \
  'docker/build-push-action@10e90e3645eae34f1e60eeb005ba3a3d33f178e8|pins image build action' \
  'tar -xzf "dist/sith_${VERSION}_linux_amd64.tar.gz"|stages the released linux amd64 binary' \
  'tar -xzf "dist/sith_${VERSION}_linux_arm64.tar.gz"|stages the released linux arm64 binary' \
  'dist/sith_${VERSION}_hub.image|attaches the digest address to the release' \
  'dist/sith_${VERSION}_hub.provenance.sigstore.json|attaches image provenance evidence to the release' \
  'dist/sith_${VERSION}_hub.sbom.sigstore.json|attaches image SBOM evidence to the release'; do
  needle="${assertion%%|*}"
  description="${assertion#*|}"
  assert_contains "$release_job" "$needle" "$description"
done

publish_step="$(workflow_step 'Publish immutable multi-platform hub image')"
platforms="$(awk '/^[[:space:]]+platforms:/{ print $2 }' <<<"$publish_step")"
if [[ "$platforms" != 'linux/amd64,linux/arm64' ]]; then
  printf '[release-hub-image] FAIL: publish step platforms = %q, want linux/amd64,linux/arm64\n' "$platforms" >&2
  exit 1
fi
printf '[release-hub-image] PASS: publishes exactly the two supported Linux platforms\n'
assert_contains "$publish_step" 'push: true' 'publishes the release image before release publication'
assert_contains "$publish_step" 'tags: ${{ env.HUB_IMAGE }}:${{ github.ref_name }}' 'uses the exact release tag without a latest tag'
assert_contains "$publish_step" 'provenance: false' 'uses the explicit GitHub provenance attestation path'
assert_contains "$publish_step" 'sbom: false' 'uses the explicit SPDX SBOM attestation path'

signing_step="$(workflow_step 'Sign and verify published hub image')"
assert_text_contains "$signing_step" 'HUB_DIGEST: ${{ steps.hub_image.outputs.digest }}' 'derives the signing digest from the pushed manifest'
assert_text_contains "$signing_step" 'image="${HUB_IMAGE}@${HUB_DIGEST}"' 'constructs the signed image from the pushed manifest digest'
assert_text_contains "$signing_step" 'cosign sign --yes "$image"' 'keylessly signs that manifest digest'

for attestation in 'Attest hub image build provenance' 'Attest hub image SBOM'; do
  attestation_step="$(workflow_step "$attestation")"
  assert_text_contains "$attestation_step" 'subject-name: ${{ env.HUB_IMAGE }}' "$attestation uses the tag-free image name"
  assert_text_contains "$attestation_step" 'subject-digest: ${{ steps.hub_image.outputs.digest }}' "$attestation uses the pushed manifest digest"
done

guard_step="$(workflow_step 'Guard hub image tag against overwrite')"
assert_text_contains "$guard_step" 'docker manifest inspect "$HUB_TAG"' 'checks the exact release tag before publication'
assert_text_contains "$guard_step" 'could not establish whether the hub image tag exists' 'fails closed on registry inspection errors'
guard_line="$(grep -n -F '      - name: Guard hub image tag against overwrite' "$workflow" | head -n 1 | cut -d: -f1)"
publish_line="$(grep -n -F '      - name: Publish immutable multi-platform hub image' "$workflow" | head -n 1 | cut -d: -f1)"
if [[ -z "$guard_line" || -z "$publish_line" || "$guard_line" -ge "$publish_line" ]]; then
  printf '[release-hub-image] FAIL: immutable-tag guard must run before image publication\n' >&2
  exit 1
fi
printf '[release-hub-image] PASS: immutable-tag guard runs before image publication\n'

release_check="$(make_target 'release-check')"
if [[ "$release_check" != *'hack/verify-release-hub-image.sh --dist dist'* ]]; then
  printf '[release-hub-image] FAIL: release check does not build the OCI layout from release archives\n' >&2
  exit 1
fi
printf '[release-hub-image] PASS: release check builds the OCI layout from release archives\n'

buildx_command="$(awk '/"\$DOCKER_BIN" buildx build/{ inside = 1 } inside { print } inside && /"\$context_directory"$/{ exit }' "$verifier")"
if [[ "$buildx_command" != *'--builder "$builder"'* || "$buildx_command" != *'--provenance=false'* || "$buildx_command" != *'--sbom=false'* ]]; then
  printf '[release-hub-image] FAIL: release archive validation does not use an isolated OCI-capable builder\n' >&2
  exit 1
fi
printf '[release-hub-image] PASS: release archive validation uses an isolated OCI-capable builder\n'

if [[ "$buildx_command" != *'--provenance=false'* || "$publish_step" != *'provenance: false'* ]]; then
  printf '[release-hub-image] FAIL: release archive validation provenance setting does not match release publish step\n' >&2
  exit 1
fi
if [[ "$buildx_command" != *'--sbom=false'* || "$publish_step" != *'sbom: false'* ]]; then
  printf '[release-hub-image] FAIL: release archive validation SBOM setting does not match release publish step\n' >&2
  exit 1
fi
printf '[release-hub-image] PASS: release archive validation matches release OCI metadata settings\n'

verification_block="$(awk '/^identity=/{ inside = 1 } inside { print } inside && /^```$/{ exit }' "$guide")"
assert_text_contains "$verification_block" 'cosign verify' 'verifies the complete immutable image reference with Cosign'
assert_text_contains "$verification_block" 'gh attestation verify "oci://$image"' 'verifies attestations for the same immutable image reference'
assert_text_contains "$verification_block" '--signer-workflow ArdurAI/sith/.github/workflows/release.yml' 'pins the attestation signer workflow'

if grep -Eq 'ghcr\.io/ardurai/sith-hub:latest' "$workflow" "$guide"; then
  printf '[release-hub-image] FAIL: hub image workflow or guide permits a mutable latest tag\n' >&2
  exit 1
fi
printf '[release-hub-image] PASS: no mutable latest hub image tag appears in workflow or guide\n'

for needle in \
  'ghcr.io/ardurai/sith-hub@sha256:' \
  'cosign verify' \
  'gh attestation verify "oci://$image"' \
  'https://spdx.dev/Document/v2.3'; do
  if ! grep -Fq -- "$needle" "$guide"; then
    printf '[release-hub-image] FAIL: release guide missing %s\n' "$needle" >&2
    exit 1
  fi
done
printf '[release-hub-image] PASS: release guide documents digest, signature, provenance, and SBOM verification\n'
