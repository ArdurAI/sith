#!/usr/bin/env bash

# shellcheck disable=SC2016

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
release_job_step() {
  local name="$1"
  awk -v name="$name" '
    $0 == "      - name: " name { inside = 1 }
    inside && $0 ~ /^      - name: / && $0 != "      - name: " name { exit }
    inside { print }
  ' <<<"$release_job"
}

assert_contains "$workflow_contents" 'HUB_IMAGE: ghcr.io/ardurai/sith-hub' 'uses one fixed GHCR hub image name'
assert_text_contains "$release_job" 'packages: write' 'grants package publication permission to the release job'
for action in setup-qemu setup-buildx login build-push; do
  action_lines="$(grep -E "^[[:space:]]+uses: docker/${action}-action@" <<<"$release_job" || true)"
  if [[ "$(wc -l <<<"$action_lines" | tr -d ' ')" != 1 ||
    ! "$action_lines" =~ docker/${action}-action@[0-9a-f]{40}[[:space:]]+#[[:space:]]+v[0-9] ]]; then
    printf '[release-hub-image] FAIL: %s action must use one full commit pin with a version comment\n' "$action" >&2
    exit 1
  fi
  printf '[release-hub-image] PASS: %s action uses one full commit pin\n' "$action"
done

for assertion in \
  'tar -xzf "dist/sith_${VERSION}_linux_amd64.tar.gz"|stages the released linux amd64 binary' \
  'tar -xzf "dist/sith_${VERSION}_linux_arm64.tar.gz"|stages the released linux arm64 binary' \
  'dist/sith_${VERSION}_hub.image|attaches the digest address to the release' \
  'dist/sith_${VERSION}_hub.provenance.sigstore.json|attaches image provenance evidence to the release' \
  'dist/sith_${VERSION}_hub.sbom.sigstore.json|attaches image SBOM evidence to the release'; do
  needle="${assertion%%|*}"
  description="${assertion#*|}"
  assert_contains "$release_job" "$needle" "$description"
done

missing_dist="${repo_root}/tests/scripts/.missing-release-dist"
set +e
missing_dist_output="$(DOCKER_BIN=true "$verifier" --dist "$missing_dist" 2>&1)"
missing_dist_status="$?"
set -e
if [[ "$missing_dist_status" == 0 || "$missing_dist_output" == *'expected exactly one Linux archive'* ]]; then
  printf '[release-hub-image] FAIL: invalid dist path did not stop at canonicalization\n' >&2
  exit 1
fi
printf '[release-hub-image] PASS: invalid dist path fails before archive or Docker work\n'

publish_step="$(release_job_step 'Publish immutable multi-platform hub image')"
platforms="$(awk '/^[[:space:]]+platforms:/{ print $2 }' <<<"$publish_step")"
if [[ "$platforms" != 'linux/amd64,linux/arm64' ]]; then
  printf '[release-hub-image] FAIL: publish step platforms = %q, want linux/amd64,linux/arm64\n' "$platforms" >&2
  exit 1
fi
printf '[release-hub-image] PASS: publishes exactly the two supported Linux platforms\n'
assert_contains "$publish_step" 'push: true' 'publishes the release image before release publication'
assert_contains "$publish_step" 'tags: ${{ env.HUB_IMAGE }}:${{ github.ref_name }}' 'uses the exact release tag without a latest tag'
assert_contains "$publish_step" 'org.opencontainers.image.source=${{ github.server_url }}/${{ github.repository }}' 'links the image package to its source repository'
assert_contains "$publish_step" 'provenance: false' 'uses the explicit GitHub provenance attestation path'
assert_contains "$publish_step" 'sbom: false' 'uses the explicit SPDX SBOM attestation path'

signing_step="$(release_job_step 'Sign and verify published hub image')"
assert_text_contains "$signing_step" 'HUB_DIGEST: ${{ steps.hub_image.outputs.digest }}' 'derives the signing digest from the pushed manifest'
assert_text_contains "$signing_step" 'image="${HUB_IMAGE}@${HUB_DIGEST}"' 'constructs the signed image from the pushed manifest digest'
assert_text_contains "$signing_step" 'cosign sign --yes "$image"' 'keylessly signs that manifest digest'

for attestation in 'Attest hub image build provenance' 'Attest hub image SBOM'; do
  attestation_step="$(release_job_step "$attestation")"
  assert_text_contains "$attestation_step" 'subject-name: ${{ env.HUB_IMAGE }}' "$attestation uses the tag-free image name"
  assert_text_contains "$attestation_step" 'subject-digest: ${{ steps.hub_image.outputs.digest }}' "$attestation uses the pushed manifest digest"
done

distribution_step="$(release_job_step 'Verify public hub image distribution')"
assert_text_contains "$distribution_step" 'HUB_DIGEST: ${{ steps.hub_image.outputs.digest }}' 'checks public access to the pushed manifest digest'
assert_text_contains "$distribution_step" 'set -euo pipefail' 'fails closed when the anonymous distribution check errors'
assert_text_contains "$distribution_step" 'docker logout ghcr.io' 'removes registry credentials before public access verification'
assert_text_contains "$distribution_step" 'docker manifest inspect "${HUB_IMAGE}@${HUB_DIGEST}"' 'fails closed unless the immutable hub digest is anonymously pullable'
logout_line="$(grep -n -F 'docker logout ghcr.io' <<<"$distribution_step" | head -n 1 | cut -d: -f1)"
inspect_line="$(grep -n -F 'docker manifest inspect "${HUB_IMAGE}@${HUB_DIGEST}"' <<<"$distribution_step" | head -n 1 | cut -d: -f1)"
if [[ -z "$logout_line" || -z "$inspect_line" || "$logout_line" -ge "$inspect_line" ]]; then
  printf '[release-hub-image] FAIL: must log out of ghcr.io before the anonymous manifest inspection\n' >&2
  exit 1
fi
printf '[release-hub-image] PASS: logs out of ghcr.io before the anonymous manifest inspection\n'
hub_provenance_line="$(grep -n -F '      - name: Attest hub image build provenance' <<<"$release_job" | head -n 1 | cut -d: -f1)"
hub_sbom_line="$(grep -n -F '      - name: Attest hub image SBOM' <<<"$release_job" | head -n 1 | cut -d: -f1)"
distribution_line="$(grep -n -F '      - name: Verify public hub image distribution' <<<"$release_job" | head -n 1 | cut -d: -f1)"
attach_line="$(grep -n -F '      - name: Attach attestations and Homebrew formula' <<<"$release_job" | head -n 1 | cut -d: -f1)"
if [[ -z "$hub_provenance_line" || -z "$hub_sbom_line" || -z "$distribution_line" || -z "$attach_line" || "$hub_provenance_line" -ge "$distribution_line" || "$hub_sbom_line" -ge "$distribution_line" || "$distribution_line" -ge "$attach_line" ]]; then
  printf '[release-hub-image] FAIL: public digest verification must follow both image attestations and precede release attachment\n' >&2
  exit 1
fi
printf '[release-hub-image] PASS: public digest verification follows image attestations before release attachment\n'

guard_step="$(release_job_step 'Guard hub image tag against overwrite')"
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
