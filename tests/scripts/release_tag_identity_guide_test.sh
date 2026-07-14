#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
guide="${repo_root}/docs/RELEASE.md"

assert_contains() {
  local needle="$1"
  local description="$2"

  if ! grep -Fq -- "$needle" "$guide"; then
    printf '[release-guide] FAIL: %s\n' "$description" >&2
    exit 1
  fi

  printf '[release-guide] PASS: %s\n' "$description"
}

assert_contains 'gh api user/emails --paginate' \
  'requires a GitHub-recognized tagger identity before tagging'
assert_contains 'ssh_signing_keys' \
  'requires the configured SSH signing key to match GitHub registration'
assert_contains 'git tag -v "$tag"' \
  'requires local annotated-tag signature verification'
assert_contains '.verification.verified' \
  'requires GitHub tag-object verification after push'
assert_contains 'do not delete, force-push, or retag the published name' \
  'forbids rewriting a rejected release tag'
assert_contains 'cut a new patch version only after the identity issue is fixed' \
  'requires immutable patch-version recovery'

printf '[release-guide] 6 assertions passed\n'
