#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
workflow="${repo_root}/.github/workflows/ci.yml"
guide="${repo_root}/docs/RELEASE.md"

if ! awk '
  /^  push:/ { section = "push"; next }
  /^  pull_request:/ { section = "pull_request"; next }
  /^  [A-Za-z_]/ { section = "" }
  section == "push" && /branches: \[dev, main\]/ { push_ok = 1 }
  section == "pull_request" && /branches: \[dev, main\]/ { pull_request_ok = 1 }
  END { exit !(push_ok && pull_request_ok) }
' "${workflow}"; then
  printf '[release-pr-gate] FAIL: ci must run on dev and main pushes and pull requests\n' >&2
  exit 1
fi
printf '[release-pr-gate] PASS: ci runs on dev and main pushes and pull requests\n'

if ! grep -Fq 'release PR before merging it' "${guide}"; then
  printf '[release-pr-gate] FAIL: release guide does not require release-PR CI\n' >&2
  exit 1
fi
printf '[release-pr-gate] PASS: release guide requires release-PR CI\n'
