#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# shellcheck disable=SC2016

set -Eeuo pipefail

REPO_ROOT="$(git rev-parse --show-toplevel)"
readonly REPO_ROOT
readonly EXPECTED_VERSION="v2.13.0"
readonly VERIFIER="${REPO_ROOT}/hack/verify-wails-version.sh"

PASS_COUNT=0

pass() {
  PASS_COUNT=$((PASS_COUNT + 1))
  printf '[wails-policy] PASS: %s\n' "$1"
}

assert_equal() {
  local actual=$1
  local expected=$2
  local description=$3

  if [[ "${actual}" != "${expected}" ]]; then
    printf '[wails-policy] FAIL: %s = %q, want %q\n' \
      "${description}" "${actual}" "${expected}" >&2
    return 1
  fi
  pass "${description}"
}

expect_failure() {
  local description=$1
  shift
  if "$@" >/dev/null 2>&1; then
    printf '[wails-policy] FAIL: %s\n' "${description}" >&2
    exit 1
  fi
  pass "${description}"
}

makefile_version="$(awk '$1 == "WAILS_VERSION" && $2 == "?=" { print $3 }' "${REPO_ROOT}/Makefile")"
module_version="$(awk '$1 == "github.com/wailsapp/wails/v2" { print $2 }' "${REPO_ROOT}/go.mod")"

assert_equal "${module_version}" "${EXPECTED_VERSION}" "Wails module is current"
assert_equal "${makefile_version}" "${module_version}" "desktop tool pin matches go.mod"

scratch="$(mktemp -d)"
readonly scratch
trap 'rm -rf "${scratch}"' EXIT

fake_wails="${scratch}/wails"
readonly fake_wails
printf '%s\n' \
  '#!/usr/bin/env bash' \
  'if [[ "${WAILS_FAKE_EXIT:-0}" != "0" ]]; then exit "${WAILS_FAKE_EXIT}"; fi' \
  'printf "%b" "${WAILS_FAKE_OUTPUT:-}"' >"${fake_wails}"
chmod 0700 "${fake_wails}"

WAILS_FAKE_OUTPUT=$'v2.13.0\nadditional upstream text\n' \
  "${VERIFIER}" "${fake_wails}" "${EXPECTED_VERSION}"
pass "exact version accepts additional lines after the version"

for lookalike in 'v2.13.0-rc.1' 'v2.13.00' 'v2.13.0+vendor' ' v2.13.0' ''; do
  expect_failure "rejects lookalike version ${lookalike:-<empty>}" \
    env WAILS_FAKE_OUTPUT="${lookalike}" "${VERIFIER}" "${fake_wails}" "${EXPECTED_VERSION}"
done

expect_failure "rejects a failed version command" \
  env WAILS_FAKE_EXIT=1 "${VERIFIER}" "${fake_wails}" "${EXPECTED_VERSION}"
expect_failure "rejects a missing Wails command" \
  "${VERIFIER}" "${scratch}/missing-wails" "${EXPECTED_VERSION}"
expect_failure "rejects a malformed expected version" \
  "${VERIFIER}" "${fake_wails}" 'v2.13'

grep -Fq 'hack/verify-wails-version.sh "$(WAILS)" "$(WAILS_VERSION)"' "${REPO_ROOT}/Makefile"
pass "desktop build invokes the tested exact-version verifier"

printf '[wails-policy] %d assertions passed\n' "${PASS_COUNT}"
