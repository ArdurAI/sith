#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0

set -Eeuo pipefail

if [[ "$#" -ne 2 ]]; then
  printf 'usage: %s <wails-command> <expected-version>\n' "$0" >&2
  exit 2
fi

readonly WAILS_COMMAND="$1"
readonly EXPECTED_VERSION="$2"

if [[ ! "${EXPECTED_VERSION}" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  printf 'invalid expected Wails version: %s\n' "${EXPECTED_VERSION}" >&2
  exit 2
fi

if ! wails_path="$(command -v "${WAILS_COMMAND}")"; then
  printf 'Wails %s is required\n' "${EXPECTED_VERSION}" >&2
  exit 1
fi
readonly wails_path

if ! version_output="$("${wails_path}" version)"; then
  printf 'failed to execute Wails version check\n' >&2
  exit 1
fi
readonly version_output

actual_version="${version_output%%$'\n'*}"
readonly actual_version
if [[ "${actual_version}" != "${EXPECTED_VERSION}" ]]; then
  printf 'Wails %s is required; got: %s\n' "${EXPECTED_VERSION}" "${actual_version:-<empty>}" >&2
  exit 1
fi
