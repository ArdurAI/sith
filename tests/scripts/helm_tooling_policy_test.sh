#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# shellcheck disable=SC2016

set -Eeuo pipefail

REPO_ROOT="$(git rev-parse --show-toplevel)"
readonly REPO_ROOT
readonly EXPECTED_VERSION="v4.2.3"
readonly EXPECTED_LINUX_AMD64_SHA256="e9b88b4ee95b18c706839c28d3a0220e5bc470e9cd9262410c90793c45ff8b7c"

PASS_COUNT=0

pass() {
  PASS_COUNT=$((PASS_COUNT + 1))
  printf '[helm-policy] PASS: %s\n' "$1"
}

assert_equal() {
  local actual=$1
  local expected=$2
  local description=$3

  if [[ "${actual}" != "${expected}" ]]; then
    printf '[helm-policy] FAIL: %s = %q, want %q\n' \
      "${description}" "${actual}" "${expected}" >&2
    return 1
  fi
  pass "${description}"
}

ci_version="$(awk -F '"' '/^  HELM_VERSION: / { print $2 }' "${REPO_ROOT}/.github/workflows/ci.yml")"
ci_checksum="$(awk -F '"' '/^  HELM_LINUX_AMD64_SHA256: / { print $2 }' "${REPO_ROOT}/.github/workflows/ci.yml")"
runner_version="$(awk -F '"' '/^readonly HELM_VERSION=/ { print $2 }' "${REPO_ROOT}/hack/experiments/m0-ocm-falsification.sh")"
contract_version="$(awk -F '"' '/^[[:space:]]*helmContractVersion = / { print $2 }' "${REPO_ROOT}/tests/e2e/helm_chart_test.go")"

assert_equal "${ci_version}" "${EXPECTED_VERSION}" "CI Helm pin is current"
assert_equal "${runner_version}" "${EXPECTED_VERSION}" "M0 runner Helm pin matches CI"
assert_equal "${contract_version}" "${EXPECTED_VERSION}" "hub chart contract Helm pin matches CI"
assert_equal "${ci_checksum}" "${EXPECTED_LINUX_AMD64_SHA256}" "CI Linux amd64 checksum matches the official archive"

grep -Fqx '| Helm | `v4.2.3` |' "${REPO_ROOT}/docs/experiments/M0-ocm-falsification.md"
pass "M0 experiment documentation matches the executable pin"

grep -Fq 'helm_version_is_pinned_release "${helm_version}"' \
  "${REPO_ROOT}/hack/experiments/m0-ocm-falsification.sh"
pass "M0 tool validation uses the tested exact-version policy"

if grep -Fq 'strings.HasPrefix(strings.TrimSpace(output), helmContractVersion)' \
  "${REPO_ROOT}/tests/e2e/helm_chart_test.go"; then
  printf '[helm-policy] FAIL: hub chart contract still accepts Helm version prefixes\n' >&2
  exit 1
fi
pass "hub chart contract has no prefix-version fallback"

printf '[helm-policy] %d assertions passed\n' "${PASS_COUNT}"
