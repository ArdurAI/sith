#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
classifier="${repo_root}/hack/release-tag-classify.sh"
workflow="${repo_root}/.github/workflows/release.yml"
guide="${repo_root}/docs/RELEASE.md"

expect_class() {
  local tag="$1"
  local expected="$2"
  local actual

  actual="$("${classifier}" "${tag}")"
  if [[ "${actual}" != "${expected}" ]]; then
    printf '[release-tag-policy] FAIL: %s classified as %q, want %q\n' "${tag}" "${actual}" "${expected}" >&2
    exit 1
  fi
  printf '[release-tag-policy] PASS: %s classified as %s\n' "${tag}" "${expected}"
}

expect_rejection() {
  local tag="$1"

  if "${classifier}" "${tag}" >/dev/null 2>&1; then
    printf '[release-tag-policy] FAIL: unsafe tag %s was accepted\n' "${tag}" >&2
    exit 1
  fi
  printf '[release-tag-policy] PASS: unsafe tag %s rejected\n' "${tag}"
}

expect_class 'v0.3.0' stable
expect_class 'v0.3.0-beta.1' beta
expect_class 'v12.34.56-beta.0' beta

for tag in \
  'v00.3.0' \
  'v0.03.0' \
  'v0.3.00' \
  'v0.3.0-beta.01' \
  'v0.3.0-beta' \
  'v0.3.0-rc.1' \
  'v0.3.0-beta.1+build.1' \
  'v0.3.0-beta.1.2' \
  'v0.3.0 ' \
  'release-v0.3.0'; do
  expect_rejection "${tag}"
done

grep -Fq 'hack/release-tag-classify.sh "$GITHUB_REF_NAME"' "${workflow}"
printf '[release-tag-policy] PASS: workflow invokes the tested classifier\n'
grep -Fq 'gh release view "$GITHUB_REF_NAME" --json databaseId' "${workflow}"
if grep -Fq 'releases/tags/${GITHUB_REF_NAME}' "${workflow}"; then
  printf '[release-tag-policy] FAIL: beta publication uses draft-invisible tag lookup\n' >&2
  exit 1
fi
printf '[release-tag-policy] PASS: beta publication resolves the draft release by database ID\n'
grep -Fq -- '-f make_latest=false' "${workflow}"
printf '[release-tag-policy] PASS: beta publication cannot replace latest stable\n'
grep -Fq 'vMAJOR.MINOR.PATCH-beta.N' "${guide}"
printf '[release-tag-policy] PASS: guide documents the beta channel\n'
