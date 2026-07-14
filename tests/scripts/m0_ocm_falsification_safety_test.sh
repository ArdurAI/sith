#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# shellcheck disable=SC2016

set -Eeuo pipefail

REPO_ROOT="$(git rev-parse --show-toplevel)"
readonly REPO_ROOT
readonly SCRIPT="${REPO_ROOT}/hack/experiments/m0-ocm-falsification.sh"
raw_test_root="$(mktemp -d)"
TEST_ROOT="$(${PYTHON_BIN:-python3} -c 'import os, sys; print(os.path.realpath(sys.argv[1]))' "${raw_test_root}")"
readonly TEST_ROOT
unset raw_test_root
trap 'rm -rf "${TEST_ROOT}"' EXIT

PASS_COUNT=0

pass() {
  PASS_COUNT=$((PASS_COUNT + 1))
  printf '[m0-safety] PASS: %s\n' "$1"
}

expect_failure() {
  local name=$1
  shift
  if "$@" >/dev/null 2>&1; then
    printf '[m0-safety] FAIL: %s unexpectedly succeeded\n' "${name}" >&2
    return 1
  fi
  pass "${name}"
}

mkdir -p "${TEST_ROOT}/traversal/source" "${TEST_ROOT}/traversal/target"
expect_failure "canonical validation rejects parent traversal" \
  env SITH_M0_SCRATCH_ROOT="${TEST_ROOT}/traversal/source/../target" \
  SITH_M0_ALLOW_NON_EXTENDED=1 bash -c 'source "$1"; validate_scratch_root' _ "${SCRIPT}"

mkdir -p "${TEST_ROOT}/symlink-target"
ln -s "${TEST_ROOT}/symlink-target" "${TEST_ROOT}/symlink-root"
expect_failure "canonical validation rejects a symlink scratch root" \
  env SITH_M0_SCRATCH_ROOT="${TEST_ROOT}/symlink-root" \
  SITH_M0_ALLOW_NON_EXTENDED=1 bash -c 'source "$1"; validate_scratch_root' _ "${SCRIPT}"

owned_root="${TEST_ROOT}/owned-root"
env SITH_M0_SCRATCH_ROOT="${owned_root}" SITH_M0_ALLOW_NON_EXTENDED=1 \
  bash -c '
    source "$1"
    prepare_scratch
    validate_owned_scratch
    [[ "$(cat "${SCRATCH_MARKER}")" == "sith-m0-owned-v1" ]]
    "${PYTHON_BIN}" -c "import os, sys; raise SystemExit(0 if (os.stat(sys.argv[1]).st_mode & 0o777) == 0o600 else 1)" "${KUBECONFIG_PATH}"
    remove_scratch
    [[ ! -e "${SCRATCH_ROOT}" ]]
  ' _ "${SCRIPT}"
pass "owned scratch lifecycle preserves the valid control"

default_tmpdir="${TEST_ROOT}/default-tmp"
mkdir -m 0700 "${default_tmpdir}"
env TMPDIR="${default_tmpdir}" bash -c '
    source "$1"
    [[ "${SCRATCH_ROOT}" == "${DEFAULT_SCRATCH_ROOT}" ]]
    prepare_scratch
    validate_owned_scratch
    remove_scratch
    [[ ! -e "${SCRATCH_ROOT}" ]]
  ' _ "${SCRIPT}"
pass "portable default scratch lifecycle remains private and removable"

default_tmpdir_alias="${TEST_ROOT}/default-tmp-alias"
ln -s "${default_tmpdir}" "${default_tmpdir_alias}"
env TMPDIR="${default_tmpdir_alias}" bash -c '
    source "$1"
    [[ "${DEFAULT_TMPDIR}" == "${2}" ]]
    [[ "${SCRATCH_ROOT}" == "${DEFAULT_SCRATCH_ROOT}" ]]
    prepare_scratch
    validate_owned_scratch
    remove_scratch
    [[ ! -e "${SCRATCH_ROOT}" ]]
  ' _ "${SCRIPT}" "${default_tmpdir}"
pass "portable default canonicalizes a symlinked temporary directory"

unowned_root="${TEST_ROOT}/unowned-root"
mkdir -p "${unowned_root}"
printf 'keep\n' >"${unowned_root}/sentinel"
expect_failure "cleanup rejects an unowned existing directory" \
  env SITH_M0_SCRATCH_ROOT="${unowned_root}" SITH_M0_ALLOW_NON_EXTENDED=1 \
  bash -c 'source "$1"; remove_scratch' _ "${SCRIPT}"
[[ -f "${unowned_root}/sentinel" ]]
pass "rejected cleanup leaves the unowned directory intact"

mkdir -p "${TEST_ROOT}/writable-parent"
chmod 0770 "${TEST_ROOT}/writable-parent"
expect_failure "scratch creation rejects a group-writable parent" \
  env SITH_M0_SCRATCH_ROOT="${TEST_ROOT}/writable-parent/lab" \
  SITH_M0_ALLOW_NON_EXTENDED=1 bash -c 'source "$1"; prepare_scratch' _ "${SCRIPT}"

mkdir -p "${TEST_ROOT}/racy-ancestor/original-parent" "${TEST_ROOT}/racy-victim"
chmod 0770 "${TEST_ROOT}/racy-ancestor"
chmod 0700 "${TEST_ROOT}/racy-ancestor/original-parent"
env SITH_M0_SCRATCH_ROOT="${TEST_ROOT}/racy-ancestor/original-parent/lab" \
  SITH_M0_ALLOW_NON_EXTENDED=1 RACY_ANCESTOR="${TEST_ROOT}/racy-ancestor" \
  RACY_VICTIM="${TEST_ROOT}/racy-victim" bash -c '
    source "$1"
    enter_scratch_parent
    mv "${RACY_ANCESTOR}/original-parent" "${RACY_ANCESTOR}/held-parent"
    mkdir -m 0700 "${RACY_ANCESTOR}/original-parent"
    ln -s "${RACY_VICTIM}" "${RACY_ANCESTOR}/original-parent/lab"
    prepare_scratch
    [[ -f "${RACY_ANCESTOR}/held-parent/lab/.sith-m0-owned" ]]
    [[ ! -e "${RACY_VICTIM}/kubeconfig" ]]
    remove_scratch
    [[ ! -e "${RACY_ANCESTOR}/held-parent/lab" ]]
  ' _ "${SCRIPT}"
pass "held scratch parent defeats writable-ancestor replacement"

mkdir -p "${TEST_ROOT}/entry-race/original-parent"
chmod 0770 "${TEST_ROOT}/entry-race"
chmod 0700 "${TEST_ROOT}/entry-race/original-parent"
expect_failure "scratch entry rejects validate-to-cd inode replacement" \
  env SITH_M0_SCRATCH_ROOT="${TEST_ROOT}/entry-race/original-parent/lab" \
  SITH_M0_ALLOW_NON_EXTENDED=1 ENTRY_RACE="${TEST_ROOT}/entry-race" bash -c '
    source "$1"
    cd() {
      mv "${ENTRY_RACE}/original-parent" "${ENTRY_RACE}/moved-parent"
      mkdir -m 0700 "${ENTRY_RACE}/original-parent"
      builtin cd "$@"
    }
    enter_scratch_parent
  ' _ "${SCRIPT}"

expect_failure "Docker validation rejects a remote endpoint override" \
  env SITH_M0_SCRATCH_ROOT="${TEST_ROOT}/docker-root" SITH_M0_ALLOW_NON_EXTENDED=1 \
  DOCKER_HOST="tcp://127.0.0.1:2375" bash -c 'source "$1"; validate_local_docker_endpoint' _ "${SCRIPT}"

health_fallback_marker="${TEST_ROOT}/health-fallback"
health_fallback_output="$(env SITH_M0_SCRATCH_ROOT="${TEST_ROOT}/health-root" SITH_M0_ALLOW_NON_EXTENDED=1 \
  HEALTH_FALLBACK_MARKER="${health_fallback_marker}" bash -c '
    source "$1"
    proxy_health_port_available() { return 1; }
    verify_cluster_registration() { :; }
    verify_scoped_proxy() { : >"${HEALTH_FALLBACK_MARKER}"; }
    verify_spoke_ingress_boundary() { :; }
    verify_outbound_only() { :; }
    verify_lab
  ' _ "${SCRIPT}")"
[[ ! -e "${health_fallback_marker}" ]]
[[ "${health_fallback_output}" == *"deferring clusteradm proxy checks to the mandatory direct e2e gate"* ]] || {
  printf '[m0-safety] FAIL: occupied port did not log the direct-e2e fallback\n' >&2
  exit 1
}
[[ "${health_fallback_output}" == *"transport=direct-e2e-required"* ]] || {
  printf '[m0-safety] FAIL: occupied port did not select direct-e2e-required transport\n' >&2
  exit 1
}
pass "occupied ClusterProxy port defers clusteradm checks to direct e2e"

cleanup_marker="${TEST_ROOT}/forced-cleanup"
expect_failure "retained run fails closed when bootstrap rotation is unproven" \
  env SITH_M0_SCRATCH_ROOT="${TEST_ROOT}/rotation-root" SITH_M0_ALLOW_NON_EXTENDED=1 \
  KUBECTL_BIN=/usr/bin/false CLEANUP_MARKER="${cleanup_marker}" bash -c '
    source "$1"
    cluster_exists() { return 0; }
    delete_clusters() { : >"${CLEANUP_MARKER}"; return 0; }
    remove_scratch() { return 0; }
    KEEP_CLUSTERS=1
    BOOTSTRAP_TOKEN_ISSUED=1
    on_exit
  ' _ "${SCRIPT}"
[[ -f "${cleanup_marker}" ]]
pass "failed rotation forces cleanup despite keep mode"

expect_failure "kind enumeration failure is not treated as cluster absence" \
  env SITH_M0_SCRATCH_ROOT="${TEST_ROOT}/kind-root" SITH_M0_ALLOW_NON_EXTENDED=1 \
  KIND_BIN=/usr/bin/false bash -c 'source "$1"; cluster_exists sith-m0-hub' _ "${SCRIPT}"

token_flag_marker="${TEST_ROOT}/token-flag"
expect_failure "malformed token output still requires invalidation" \
  env SITH_M0_SCRATCH_ROOT="${TEST_ROOT}/token-root" SITH_M0_ALLOW_NON_EXTENDED=1 \
  CLUSTERADM_BIN=fake_clusteradm TOKEN_FLAG_MARKER="${token_flag_marker}" bash -c '
    fake_clusteradm() {
      case "$1" in
        init) return 0 ;;
        get) printf "malformed token response\n"; return 0 ;;
        *) return 1 ;;
      esac
    }
    source "$2"
    write_token_flag() {
      printf "%s\n" "${BOOTSTRAP_TOKEN_ISSUED}" >"${TOKEN_FLAG_MARKER}"
    }
    trap write_token_flag EXIT
    initialize_ocm
  ' _ unused "${SCRIPT}"
[[ "$(cat "${token_flag_marker}")" == "1" ]]
pass "token acquisition boundary is conservative"

addon_wait_marker="${TEST_ROOT}/addon-wait"
env SITH_M0_SCRATCH_ROOT="${TEST_ROOT}/addon-root" SITH_M0_ALLOW_NON_EXTENDED=1 \
  KUBECTL_BIN=fake_kubectl ADDON_WAIT_MARKER="${addon_wait_marker}" bash -c '
    attempts=0
    fake_kubectl() {
      for argument in "$@"; do
        if [[ "${argument}" == "get" ]]; then
          attempts=$((attempts + 1))
          [[ "${attempts}" -ge 3 ]]
          return
        fi
        if [[ "${argument}" == "wait" ]]; then
          printf "%s\n" "${attempts}" >"${ADDON_WAIT_MARKER}"
          return 0
        fi
      done
      return 1
    }
    sleep() { :; }
    source "$1"
    wait_for_addon_creation spoke-a cluster-proxy
  ' _ "${SCRIPT}"
[[ "$(cat "${addon_wait_marker}")" == "3" ]]
pass "addon wait tolerates asynchronous creation before availability"

expect_failure "unrelated hub exec failure cannot satisfy the active deny" \
  env SITH_M0_SCRATCH_ROOT="${TEST_ROOT}/probe-root" SITH_M0_ALLOW_NON_EXTENDED=1 \
  DOCKER_BIN=fake_docker bash -c '
    fake_docker() {
      [[ "$1" == "exec" && "$2" == "spoke-node" ]]
    }
    source "$2"
    firewall_probe_packets() { printf "0\n"; }
    probe_hub_connection_denied 192.0.2.10 6443 spoke-node
  ' _ unused "${SCRIPT}"

printf '[m0-safety] %d assertions passed\n' "${PASS_COUNT}"
