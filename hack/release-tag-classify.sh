#!/usr/bin/env bash

set -euo pipefail

if [[ "$#" -ne 1 ]]; then
  echo "usage: release-tag-classify.sh <tag>" >&2
  exit 2
fi

tag="$1"
numeric='(0|[1-9][0-9]*)'

if [[ "$tag" =~ ^v${numeric}\.${numeric}\.${numeric}$ ]]; then
  printf 'stable\n'
  exit 0
fi

if [[ "$tag" =~ ^v${numeric}\.${numeric}\.${numeric}-beta\.${numeric}$ ]]; then
  printf 'beta\n'
  exit 0
fi

echo "release tag must be canonical vMAJOR.MINOR.PATCH or vMAJOR.MINOR.PATCH-beta.N" >&2
exit 1
