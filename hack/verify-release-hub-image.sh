#!/usr/bin/env bash

# SPDX-License-Identifier: Apache-2.0

set -euo pipefail

usage() {
  printf 'usage: %s --dist <release-distribution-directory>\n' "${0##*/}" >&2
  exit 2
}

if [[ "$#" != 2 || "$1" != "--dist" ]]; then
  usage
fi

REPOSITORY_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
readonly REPOSITORY_ROOT
DIST_DIRECTORY="$(cd "$2" && pwd -P)"
readonly DIST_DIRECTORY
readonly DOCKER_BIN="${DOCKER_BIN:-docker}"

command -v "$DOCKER_BIN" >/dev/null
command -v python3 >/dev/null

shopt -s nullglob
amd64_archives=("${DIST_DIRECTORY}"/sith_*_linux_amd64.tar.gz)
arm64_archives=("${DIST_DIRECTORY}"/sith_*_linux_arm64.tar.gz)
if [[ "${#amd64_archives[@]}" != 1 || "${#arm64_archives[@]}" != 1 ]]; then
  printf 'expected exactly one Linux archive per supported architecture in %s\n' "$DIST_DIRECTORY" >&2
  exit 1
fi

context_directory="$(mktemp -d)"
builder="sith-release-hub-${RANDOM}-$$"
cleanup() {
  "$DOCKER_BIN" buildx rm --force "$builder" >/dev/null 2>&1 || true
  rm -rf "$context_directory"
}
trap cleanup EXIT
install -d -m 0755 "$context_directory/bin/linux/amd64" "$context_directory/bin/linux/arm64"
tar -xzf "${amd64_archives[0]}" -C "$context_directory/bin/linux/amd64" sith
tar -xzf "${arm64_archives[0]}" -C "$context_directory/bin/linux/arm64" sith
install -m 0644 "$REPOSITORY_ROOT/Containerfile" "$context_directory/Containerfile"
test -x "$context_directory/bin/linux/amd64/sith"
test -x "$context_directory/bin/linux/arm64/sith"

oci_layout="$context_directory/sith-hub.oci"
"$DOCKER_BIN" buildx create --driver docker-container --name "$builder" >/dev/null
"$DOCKER_BIN" buildx inspect --builder "$builder" --bootstrap >/dev/null
"$DOCKER_BIN" buildx build \
	--builder "$builder" \
  --platform linux/amd64,linux/arm64 \
	--provenance=false \
	--sbom=false \
  --file "$context_directory/Containerfile" \
  --output "type=oci,dest=${oci_layout}" \
  "$context_directory"

oci_directory="$context_directory/oci-layout"
install -d -m 0755 "$oci_directory"
tar -xf "$oci_layout" -C "$oci_directory"
python3 - "$oci_directory" <<'PY'
import json
import sys
from pathlib import Path

root = Path(sys.argv[1])
index_media_types = {
    "application/vnd.oci.image.index.v1+json",
    "application/vnd.docker.distribution.manifest.list.v2+json",
}

def indexed_platforms(index):
    platforms = set()
    for manifest in index.get("manifests", []):
        if manifest.get("mediaType") in index_media_types:
            algorithm, digest = manifest["digest"].split(":", 1)
            with (root / "blobs" / algorithm / digest).open(encoding="utf-8") as nested_file:
                platforms.update(indexed_platforms(json.load(nested_file)))
            continue
        platform = manifest.get("platform", {})
        os_name = platform.get("os")
        architecture = platform.get("architecture")
        if os_name is not None and architecture is not None:
            platforms.add((os_name, architecture))
    return platforms

with (root / "index.json").open(encoding="utf-8") as index_file:
    platforms = indexed_platforms(json.load(index_file))

expected = {("linux", "amd64"), ("linux", "arm64")}
if platforms != expected:
    raise SystemExit(f"OCI layout platforms = {sorted(platforms)!r}, want {sorted(expected)!r}")
PY

printf 'release hub OCI layout verified from release archives\n'
