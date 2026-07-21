#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# shellcheck disable=SC2016

set -Eeuo pipefail

REPO_ROOT="$(git rev-parse --show-toplevel)"
readonly REPO_ROOT
readonly EXPECTED_GORELEASER_VERSION="v2.17.0"
readonly EXPECTED_SYFT_VERSION="v1.49.0"
readonly EXPECTED_COSIGN_VERSION="v3.1.2"

PASS_COUNT=0

pass() {
  PASS_COUNT=$((PASS_COUNT + 1))
  printf '[release-tooling] PASS: %s\n' "$1"
}

assert_equal() {
  local actual=$1
  local expected=$2
  local description=$3

  if [[ "${actual}" != "${expected}" ]]; then
    printf '[release-tooling] FAIL: %s = %q, want %q\n' \
      "${description}" "${actual}" "${expected}" >&2
    return 1
  fi
  pass "${description}"
}

workflow_value() {
  local workflow=$1
  local name=$2
  awk -F '"' -v key="${name}:" '$1 ~ "^[[:space:]]*" key "[[:space:]]*$" { print $2 }' "${workflow}"
}

ci_workflow="${REPO_ROOT}/.github/workflows/ci.yml"
release_workflow="${REPO_ROOT}/.github/workflows/release.yml"

ci_goreleaser="$(workflow_value "${ci_workflow}" GORELEASER_VERSION)"
ci_syft="$(workflow_value "${ci_workflow}" SYFT_VERSION)"
release_goreleaser="$(workflow_value "${release_workflow}" GORELEASER_VERSION)"
release_syft="$(workflow_value "${release_workflow}" SYFT_VERSION)"
release_cosign="$(workflow_value "${release_workflow}" COSIGN_VERSION)"

assert_equal "${ci_goreleaser}" "${EXPECTED_GORELEASER_VERSION}" "CI GoReleaser pin is current"
assert_equal "${release_goreleaser}" "${ci_goreleaser}" "release GoReleaser pin matches CI"
assert_equal "${ci_syft}" "${EXPECTED_SYFT_VERSION}" "CI Syft pin is current"
assert_equal "${release_syft}" "${ci_syft}" "release Syft pin matches CI"
assert_equal "${release_cosign}" "${EXPECTED_COSIGN_VERSION}" "release Cosign pin is current"

grep -Fq "GoReleaser ${EXPECTED_GORELEASER_VERSION} and Syft ${EXPECTED_SYFT_VERSION}" \
  "${REPO_ROOT}/README.md"
pass "README release prerequisites match executable pins"

grep -Fq "Syft ${EXPECTED_SYFT_VERSION}" "${REPO_ROOT}/docs/adr/0009-release-supply-chain.md"
grep -Fq "Cosign ${EXPECTED_COSIGN_VERSION}" "${REPO_ROOT}/docs/adr/0009-release-supply-chain.md"
pass "release-supply-chain decision records current pins"

grep -Fq 'syft-version: ${{ env.SYFT_VERSION }}' "${ci_workflow}"
grep -Fq 'syft-version: ${{ env.SYFT_VERSION }}' "${release_workflow}"
grep -Fq 'cosign-release: ${{ env.COSIGN_VERSION }}' "${release_workflow}"
pass "installer actions consume the synchronized pins"

if grep -Eq -- '--(payload|output-attestation)([=[:space:]]|$)' "${release_workflow}"; then
  printf '[release-tooling] FAIL: release workflow uses a Cosign v3.1-deprecated flag\n' >&2
  exit 1
fi
pass "release workflow avoids newly deprecated Cosign flags"

printf '[release-tooling] %d assertions passed\n' "${PASS_COUNT}"
