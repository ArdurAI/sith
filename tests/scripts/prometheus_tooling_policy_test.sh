#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0

set -Eeuo pipefail

REPO_ROOT="$(git rev-parse --show-toplevel)"
readonly REPO_ROOT
readonly EXPECTED_VERSION="v3.13.1"
readonly EXPECTED_LINUX_AMD64_SHA256="962b812371aff838d152b6ff2d56fdb7a6396f5542f48ebf73421b9721f0d103"

ci_version="$(awk -F '"' '/^  PROMETHEUS_VERSION: / { print $2 }' "${REPO_ROOT}/.github/workflows/ci.yml")"
ci_checksum="$(awk -F '"' '/^  PROMETHEUS_LINUX_AMD64_SHA256: / { print $2 }' "${REPO_ROOT}/.github/workflows/ci.yml")"

[[ "${ci_version}" == "${EXPECTED_VERSION}" ]] || {
  printf '[prometheus-policy] FAIL: CI version = %q, want %q\n' "${ci_version}" "${EXPECTED_VERSION}" >&2
  exit 1
}
[[ "${ci_checksum}" == "${EXPECTED_LINUX_AMD64_SHA256}" ]] || {
  printf '[prometheus-policy] FAIL: CI checksum = %q, want official archive checksum %q\n' \
    "${ci_checksum}" "${EXPECTED_LINUX_AMD64_SHA256}" >&2
  exit 1
}

rules="${REPO_ROOT}/monitoring/sith-hub.rules.yml"
[[ "$(grep -c '^[[:space:]]*- alert:' "${rules}")" == 3 ]]
! grep -Fq '{{' "${rules}"
! grep -Eq 'kind:[[:space:]]*(ServiceMonitor|PrometheusRule)|apiVersion:[[:space:]]*monitoring\.coreos\.com/' "${rules}"
go test -count=1 -run '^TestPortableAlertRulesStayBoundedAndStatic$' ./internal/observability

printf '[prometheus-policy] pinned promtool and static portable-rule boundaries verified\n'
