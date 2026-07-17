#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0

set -Eeuo pipefail

readonly KIND_VERSION="v0.32.0"
readonly KIND_NODE_IMAGE="kindest/node:v1.36.1@sha256:3489c7674813ba5d8b1a9977baea8a6e553784dab7b84759d1014dbd78f7ebd5"
readonly CLUSTERADM_VERSION="v1.3.1-0-g90bdc31"
readonly CLUSTER_PROXY_VERSION="0.10.0"
readonly CLUSTER_PROXY_SHA256="30128f5f211d3c3d6ab1040929e0d1ca7565869935aa60130c5b197d0481c75d"
readonly MANAGED_SERVICEACCOUNT_VERSION="0.10.0"
readonly MANAGED_SERVICEACCOUNT_SHA256="ddd8b7da55667b534102397abd2e61988f9ead8a0e97b942731f869ed0b06dbf"
readonly GO_VERSION="go1.26.5"
readonly HELM_VERSION="v4.1.4"
readonly FIREWALL_CHAIN="SITH_M0_HUB_DENY"

readonly KIND_BIN="${KIND_BIN:-kind}"
readonly KUBECTL_BIN="${KUBECTL_BIN:-kubectl}"
readonly CLUSTERADM_BIN="${CLUSTERADM_BIN:-clusteradm}"
readonly HELM_BIN="${HELM_BIN:-helm}"
readonly DOCKER_BIN="${DOCKER_BIN:-docker}"
readonly JQ_BIN="${JQ_BIN:-jq}"
readonly GO_BIN="${GO_BIN:-go}"
readonly PYTHON_BIN="${PYTHON_BIN:-python3}"

REPO_ROOT="$(git rev-parse --show-toplevel)"
readonly REPO_ROOT
readonly LAB_PREFIX="${SITH_M0_PREFIX:-sith-m0}"
readonly HUB_NAME="${LAB_PREFIX}-hub"
readonly SPOKE_A_NAME="${LAB_PREFIX}-spoke-a"
readonly SPOKE_B_NAME="${LAB_PREFIX}-spoke-b"
readonly HUB_CONTEXT="kind-${HUB_NAME}"
readonly SPOKE_A_CONTEXT="kind-${SPOKE_A_NAME}"
readonly SPOKE_B_CONTEXT="kind-${SPOKE_B_NAME}"
DEFAULT_TMPDIR="$(${PYTHON_BIN} - "${TMPDIR:-/tmp}" <<'PY'
import os
import sys

print(os.path.realpath(sys.argv[1]))
PY
)"
readonly DEFAULT_TMPDIR
readonly DEFAULT_SCRATCH_ROOT="${DEFAULT_TMPDIR}/sith-m0-${EUID}/lab"
readonly SCRATCH_ROOT="${SITH_M0_SCRATCH_ROOT:-${DEFAULT_SCRATCH_ROOT}}"
SCRATCH_PARENT="$(dirname "${SCRATCH_ROOT}")"
readonly SCRATCH_PARENT
SCRATCH_NAME="$(basename "${SCRATCH_ROOT}")"
readonly SCRATCH_NAME
readonly KUBECONFIG_PATH="${SCRATCH_NAME}/kubeconfig"
readonly SCRATCH_MARKER="${SCRATCH_NAME}/.sith-m0-owned"
readonly FIXTURE_CONTEXT="${SCRATCH_NAME}/fixture-context"
readonly FIXTURE_IMAGE="sith-m0-fixture:v1"

KEEP_CLUSTERS="${SITH_M0_KEEP_CLUSTERS:-0}"
BOOTSTRAP_TOKEN_ISSUED=0
BOOTSTRAP_IDENTITY_ROTATED=0
SCRATCH_PARENT_ENTERED=0

export KUBECONFIG="${KUBECONFIG_PATH}"
export TMPDIR="${SCRATCH_NAME}/tmp"
export HELM_CONFIG_HOME="${SCRATCH_NAME}/helm/config"
export HELM_CACHE_HOME="${SCRATCH_NAME}/helm/cache"
export HELM_DATA_HOME="${SCRATCH_NAME}/helm/data"
export GOTOOLCHAIN="go1.26.5"
export GOPATH="${GOPATH:-/Volumes/EXTENDED/MacData/go}"

usage() {
  cat <<'EOF'
Usage: hack/experiments/m0-ocm-falsification.sh [run|verify|cleanup]

Commands:
  run      Create one hub and two spokes, run the complete M0 test, then clean up.
  verify   Re-run fail-closed assertions against an existing retained M0 lab.
  cleanup  Delete the three M0 kind clusters and remove isolated scratch state.

Environment:
  SITH_M0_KEEP_CLUSTERS=1        Retain clusters and scratch after `run`.
  SITH_M0_SCRATCH_ROOT=<path>    Scratch path; defaults to a private TMPDIR path.
  SITH_M0_ALLOW_NON_EXTENDED=1   Permit an explicit non-EXTENDED scratch path.
  SITH_M0_PREFIX=<name>          Override the disposable kind cluster prefix.

The runner never prints registration or MSA tokens and never reads the user's kubeconfig.
EOF
}

log() {
  printf '[m0] %s\n' "$*"
}

die() {
  printf '[m0] ERROR: %s\n' "$*" >&2
  exit 1
}

on_error() {
  local line=$1
  local exit_code=$2
  printf '[m0] ERROR: unhandled failure at line %s (exit %s)\n' "${line}" "${exit_code}" >&2
}

trap 'on_error "${LINENO}" "$?"' ERR

require_command() {
  command -v "$1" >/dev/null 2>&1 || die "required command not found: $1"
}

redact_tokens() {
  sed -E 's/eyJ[A-Za-z0-9._=-]+/<redacted-token>/g'
}

cluster_exists() {
  local clusters

  clusters="$(${KIND_BIN} get clusters 2>/dev/null)" ||
    die "cannot enumerate kind clusters; refusing to infer absence"
  printf '%s\n' "${clusters}" | grep -Fxq "$1"
}

validate_scratch_root() {
  local canonical_root

  [[ -n "${SCRATCH_ROOT}" ]] || die "scratch root must not be empty"
  [[ "${SCRATCH_ROOT}" == /* ]] || die "scratch root must be absolute"
  canonical_root="$(${PYTHON_BIN} - "${SCRATCH_ROOT}" <<'PY'
import os
import sys

print(os.path.realpath(sys.argv[1]))
PY
)"
  [[ "${SCRATCH_ROOT}" == "${canonical_root}" ]] ||
    die "scratch root must be canonical and contain no symlink or parent traversal components"

  case "${SCRATCH_ROOT}" in
    /Volumes/EXTENDED/*) ;;
    *)
      [[ "${SCRATCH_ROOT}" == "${DEFAULT_SCRATCH_ROOT}" || "${SITH_M0_ALLOW_NON_EXTENDED:-0}" == "1" ]] ||
        die "scratch must use the private default or explicitly set SITH_M0_ALLOW_NON_EXTENDED=1"
      ;;
  esac

  case "${SCRATCH_ROOT}" in
    / | /Volumes | /Volumes/EXTENDED) die "refusing unsafe scratch root" ;;
  esac
}

scratch_directory_identity() {
  "${PYTHON_BIN}" - "$1" <<'PY'
import os
import stat
import sys

parent = sys.argv[1]
try:
    parent_stat = os.lstat(parent)
    if not stat.S_ISDIR(parent_stat.st_mode) or stat.S_ISLNK(parent_stat.st_mode):
        raise ValueError
    if parent_stat.st_uid != os.geteuid():
        raise ValueError
    if parent_stat.st_mode & (stat.S_IWGRP | stat.S_IWOTH):
        raise ValueError
    print(f"{parent_stat.st_dev}:{parent_stat.st_ino}")
except (OSError, ValueError):
    raise SystemExit(1)
PY
}

validate_scratch_parent() {
  scratch_directory_identity "${SCRATCH_PARENT}" >/dev/null
}

prepare_default_scratch_parent() {
  [[ "${SCRATCH_ROOT}" == "${DEFAULT_SCRATCH_ROOT}" ]] || return 0
  [[ ! -e "${SCRATCH_PARENT}" && ! -L "${SCRATCH_PARENT}" ]] || return 0

  umask 077
  mkdir -p -m 0700 -- "${SCRATCH_PARENT}"
}

enter_scratch_parent() {
  local entered_identity
  local expected_identity

  if [[ "${SCRATCH_PARENT_ENTERED}" == "1" ]]; then
    return 0
  fi
  validate_scratch_root
  expected_identity="$(scratch_directory_identity "${SCRATCH_PARENT}")" ||
    die "scratch parent must be current-user-owned and not group/world writable"
  cd -P "${SCRATCH_PARENT}" || die "cannot enter the trusted scratch parent"
  entered_identity="$(scratch_directory_identity .)" ||
    die "entered scratch parent is not current-user-owned and private"
  [[ "${entered_identity}" == "${expected_identity}" ]] ||
    die "scratch parent changed during validation; refusing the raced path"
  SCRATCH_PARENT_ENTERED=1
}

scratch_is_owned() {
  "${PYTHON_BIN}" - "${SCRATCH_NAME}" "${SCRATCH_MARKER}" <<'PY'
import os
import stat
import sys

root, marker = sys.argv[1:]
try:
    root_stat = os.lstat(root)
    marker_stat = os.lstat(marker)
    if not stat.S_ISDIR(root_stat.st_mode) or stat.S_ISLNK(root_stat.st_mode):
        raise ValueError
    if not stat.S_ISREG(marker_stat.st_mode) or stat.S_ISLNK(marker_stat.st_mode):
        raise ValueError
    if root_stat.st_uid != os.geteuid() or marker_stat.st_uid != os.geteuid():
        raise ValueError
    with open(marker, encoding="utf-8") as handle:
        if handle.read() != "sith-m0-owned-v1\n":
            raise ValueError
except (OSError, ValueError):
    raise SystemExit(1)
PY
}

validate_owned_scratch() {
  enter_scratch_parent
  scratch_is_owned || die "scratch root is not an owned Sith M0 directory; refusing mutation"
}

validate_local_docker_endpoint() {
  local context
  local endpoint

  if [[ -n "${DOCKER_HOST:-}" && "${DOCKER_HOST}" != unix://* ]]; then
    die "DOCKER_HOST must use a local unix socket for this credential-bearing lab"
  fi
  context="$(${DOCKER_BIN} context show 2>/dev/null)" || die "cannot resolve Docker context"
  endpoint="$(${DOCKER_BIN} context inspect "${context}" --format '{{.Endpoints.docker.Host}}' 2>/dev/null)" ||
    die "cannot inspect Docker endpoint"
  [[ "${endpoint}" == unix://* ]] ||
    die "Docker context ${context} is not local (${endpoint}); refusing to expose lab state"
}

check_tools() {
  local clusteradm_version
  local helm_version
  local kind_version
  local go_version

  for command_name in "${KIND_BIN}" "${KUBECTL_BIN}" "${CLUSTERADM_BIN}" "${HELM_BIN}" \
    "${DOCKER_BIN}" "${JQ_BIN}" "${GO_BIN}" "${PYTHON_BIN}" awk grep sed shasum; do
    require_command "${command_name}"
  done

  kind_version="$(${KIND_BIN} version)"
  [[ "${kind_version}" == *"kind ${KIND_VERSION}"* ]] ||
    die "kind ${KIND_VERSION} is required; got: ${kind_version}"

  clusteradm_version="$(${CLUSTERADM_BIN} version 2>/dev/null)"
  [[ "${clusteradm_version}" == *"${CLUSTERADM_VERSION}"* ]] ||
    die "clusteradm ${CLUSTERADM_VERSION} is required"

  helm_version="$(${HELM_BIN} version --short 2>/dev/null)"
  [[ "${helm_version}" == "${HELM_VERSION}"* ]] ||
    die "Helm ${HELM_VERSION} is required; got: ${helm_version}"

  go_version="$(${GO_BIN} env GOVERSION)"
  [[ "${go_version}" == "${GO_VERSION}" ]] ||
    die "Go ${GO_VERSION} is required; got: ${go_version}"

  validate_local_docker_endpoint
  "${DOCKER_BIN}" info >/dev/null 2>&1 || die "Docker engine is unavailable"
}

prepare_scratch() {
  prepare_default_scratch_parent
  enter_scratch_parent
  [[ ! -e "${SCRATCH_NAME}" && ! -L "${SCRATCH_NAME}" ]] ||
    die "scratch root already exists; use verify or cleanup before a fresh run"

  umask 077
  mkdir -m 0700 "${SCRATCH_NAME}"
  printf 'sith-m0-owned-v1\n' >"${SCRATCH_MARKER}"
  chmod 0600 "${SCRATCH_MARKER}"
  mkdir -p "${TMPDIR}" "${HELM_CONFIG_HOME}" "${HELM_CACHE_HOME}" \
    "${HELM_DATA_HOME}" "${SCRATCH_NAME}/charts"
  : >"${KUBECONFIG_PATH}"
  chmod 0600 "${KUBECONFIG_PATH}"
}

ensure_lab_absent() {
  local cluster
  for cluster in "${HUB_NAME}" "${SPOKE_A_NAME}" "${SPOKE_B_NAME}"; do
    cluster_exists "${cluster}" &&
      die "kind cluster ${cluster} already exists; use cleanup or verify"
  done
  return 0
}

delete_clusters() {
  local cluster
  local failed=0

  for cluster in "${SPOKE_B_NAME}" "${SPOKE_A_NAME}" "${HUB_NAME}"; do
    if cluster_exists "${cluster}"; then
      "${KIND_BIN}" delete cluster --name "${cluster}" || failed=1
    fi
  done
  return "${failed}"
}

remove_scratch() {
  if [[ "${SCRATCH_ROOT}" == "${DEFAULT_SCRATCH_ROOT}" && ! -e "${SCRATCH_PARENT}" && ! -L "${SCRATCH_PARENT}" ]]; then
    return 0
  fi
  enter_scratch_parent
  [[ -e "${SCRATCH_NAME}" || -L "${SCRATCH_NAME}" ]] || return 0
  validate_owned_scratch
  rm -rf -- "${SCRATCH_NAME}"
}

rotate_bootstrap_identity() {
  local namespace="open-cluster-management"
  local account="agent-registration-bootstrap"
  local old_uid
  local new_uid

  if [[ "${BOOTSTRAP_IDENTITY_ROTATED}" == "1" || "${BOOTSTRAP_TOKEN_ISSUED}" == "0" ]]; then
    return 0
  fi
  if ! cluster_exists "${HUB_NAME}"; then
    BOOTSTRAP_IDENTITY_ROTATED=1
    return 0
  fi
  if ! "${KUBECTL_BIN}" --context "${HUB_CONTEXT}" -n "${namespace}" get serviceaccount \
    "${account}" >/dev/null 2>&1; then
    printf '[m0] ERROR: cannot prove registration bootstrap invalidation\n' >&2
    return 1
  fi

  old_uid="$(${KUBECTL_BIN} --context "${HUB_CONTEXT}" -n "${namespace}" get serviceaccount \
    "${account}" -o jsonpath='{.metadata.uid}')" || return 1
  "${KUBECTL_BIN}" --context "${HUB_CONTEXT}" -n "${namespace}" delete serviceaccount \
    "${account}" --wait=true >/dev/null || return 1
  "${KUBECTL_BIN}" --context "${HUB_CONTEXT}" -n "${namespace}" create serviceaccount \
    "${account}" >/dev/null || return 1
  new_uid="$(${KUBECTL_BIN} --context "${HUB_CONTEXT}" -n "${namespace}" get serviceaccount \
    "${account}" -o jsonpath='{.metadata.uid}')" || return 1
  if [[ "${old_uid}" == "${new_uid}" ]]; then
    printf '[m0] ERROR: bootstrap ServiceAccount UID did not rotate\n' >&2
    return 1
  fi
  BOOTSTRAP_IDENTITY_ROTATED=1
  log "registration bootstrap credential invalidated"
}

on_exit() {
  local exit_code=$?
  local cleanup_failed=0
  local rotation_failed=0

  trap - EXIT
  set +e
  rotate_bootstrap_identity || rotation_failed=1
  if [[ "${KEEP_CLUSTERS}" == "1" && "${rotation_failed}" == "0" ]]; then
    log "retaining lab because SITH_M0_KEEP_CLUSTERS=1"
  else
    if [[ "${KEEP_CLUSTERS}" == "1" && "${rotation_failed}" != "0" ]]; then
      printf '[m0] ERROR: refusing to retain a lab with unproven bootstrap invalidation\n' >&2
    fi
    if delete_clusters; then
      remove_scratch || cleanup_failed=1
    else
      cleanup_failed=1
      printf '[m0] ERROR: cluster deletion failed; retaining scratch for recovery\n' >&2
    fi
  fi
  if [[ "${rotation_failed}" != "0" || "${cleanup_failed}" != "0" ]]; then
    [[ "${exit_code}" != "0" ]] || exit_code=1
  fi
  exit "${exit_code}"
}

create_clusters() {
  local cluster
  for cluster in "${HUB_NAME}" "${SPOKE_A_NAME}" "${SPOKE_B_NAME}"; do
    log "creating kind cluster ${cluster}"
    "${KIND_BIN}" create cluster --name "${cluster}" --image "${KIND_NODE_IMAGE}" \
      --kubeconfig "${KUBECONFIG_PATH}" --wait 180s
  done
}

enforce_spoke_ingress_boundary() {
  local base_chain
  local cluster
  local hub_ip
  local node
  local node_ip

  hub_ip="$(${DOCKER_BIN} inspect "${HUB_NAME}-control-plane" \
    --format '{{.NetworkSettings.Networks.kind.IPAddress}}')"
  [[ "${hub_ip}" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]] ||
    die "unexpected hub container IP"

  for cluster in spoke-a spoke-b; do
    node="${LAB_PREFIX}-${cluster}-control-plane"
    node_ip="$(${DOCKER_BIN} inspect "${node}" \
      --format '{{.NetworkSettings.Networks.kind.IPAddress}}')"
    "${DOCKER_BIN}" exec "${node}" iptables -N "${FIREWALL_CHAIN}" 2>/dev/null ||
      "${DOCKER_BIN}" exec "${node}" iptables -F "${FIREWALL_CHAIN}"
    # The first rule is a probe-specific counter. Verification requires its packet count
    # to increase, so Docker/command failures cannot masquerade as a denied connection.
    "${DOCKER_BIN}" exec "${node}" iptables -A "${FIREWALL_CHAIN}" -s "${hub_ip}" \
      -d "${node_ip}" -p tcp --dport 6443 -m conntrack --ctstate NEW -j REJECT
    "${DOCKER_BIN}" exec "${node}" iptables -A "${FIREWALL_CHAIN}" -s "${hub_ip}" \
      -m conntrack --ctstate NEW -j REJECT
    # A hub pod that is not SNATed enters through eth0 with its pod-CIDR source. Matching
    # the ingress interface avoids blocking spoke-local 10.244/16 traffic on pod veths.
    "${DOCKER_BIN}" exec "${node}" iptables -A "${FIREWALL_CHAIN}" -i eth0 \
      -s 10.244.0.0/16 -m conntrack --ctstate NEW -j REJECT
    for base_chain in INPUT FORWARD; do
      if ! "${DOCKER_BIN}" exec "${node}" iptables -C "${base_chain}" \
        -j "${FIREWALL_CHAIN}" >/dev/null 2>&1; then
        "${DOCKER_BIN}" exec "${node}" iptables -I "${base_chain}" 1 \
          -j "${FIREWALL_CHAIN}"
      fi
    done
    log "${cluster}: rejecting hub-initiated node and pod connections"
  done
}

initialize_ocm() {
  local hub_api
  local hub_token
  local join_command

  log "initializing OCM hub"
  "${CLUSTERADM_BIN}" init --wait --context "${HUB_CONTEXT}" 2>&1 | redact_tokens

  # Treat token acquisition as a side-effect boundary. Even malformed output or an
  # interruption after this point requires proven invalidation or forced teardown.
  BOOTSTRAP_TOKEN_ISSUED=1
  join_command="$(${CLUSTERADM_BIN} get token --context "${HUB_CONTEXT}" |
    awk '/clusteradm join/ {print; exit}')"
  hub_token="$(printf '%s\n' "${join_command}" |
    awk '{for (i=1;i<=NF;i++) if ($i=="--hub-token") {print $(i+1); exit}}')"
  hub_api="$(printf '%s\n' "${join_command}" |
    awk '{for (i=1;i<=NF;i++) if ($i=="--hub-apiserver") {print $(i+1); exit}}')"
  [[ -n "${hub_token}" ]] || die "clusteradm did not provide a bootstrap token"
  [[ "${hub_api}" =~ ^https://127\.0\.0\.1:[0-9]+$ ]] ||
    die "clusteradm returned an unexpected hub API endpoint"

  log "joining both spokes with an in-memory bootstrap credential"
  {
    "${CLUSTERADM_BIN}" join --hub-token "${hub_token}" --hub-apiserver "${hub_api}" \
      --cluster-name spoke-a --force-internal-endpoint-lookup \
      --context "${SPOKE_A_CONTEXT}"
    "${CLUSTERADM_BIN}" join --hub-token "${hub_token}" --hub-apiserver "${hub_api}" \
      --cluster-name spoke-b --force-internal-endpoint-lookup \
      --context "${SPOKE_B_CONTEXT}"
  } 2>&1 | redact_tokens
  hub_token=""
  join_command=""

  "${CLUSTERADM_BIN}" accept --clusters spoke-a,spoke-b --wait --context "${HUB_CONTEXT}"
  rotate_bootstrap_identity || die "registration bootstrap credential could not be invalidated"

  for cluster in spoke-a spoke-b; do
    "${KUBECTL_BIN}" --context "${HUB_CONTEXT}" wait "managedcluster/${cluster}" \
      --for=condition=ManagedClusterConditionAvailable --timeout=300s
  done
}

verify_chart_digest() {
  local archive=$1
  local expected=$2
  local actual
  actual="$(shasum -a 256 "${archive}" | awk '{print $1}')"
  [[ "${actual}" == "${expected}" ]] ||
    die "chart digest mismatch for $(basename "${archive}")"
}

wait_for_addon_creation() {
  local cluster=$1
  local addon=$2
  local deadline=$((SECONDS + 300))
  local observation
  local uid
  local condition
  local ready_uid=""
  local remaining

  while (( SECONDS < deadline )); do
    remaining=$((deadline - SECONDS))
    if ! observation="$("${KUBECTL_BIN}" --context "${HUB_CONTEXT}" -n "${cluster}" get \
      "managedclusteraddon/${addon}" --ignore-not-found --request-timeout="${remaining}s" \
      -o=go-template='{{.metadata.uid}}{{"\t"}}{{range .status.conditions}}{{if eq .type "Available"}}{{.status}}{{"|"}}{{end}}{{end}}' \
      2>/dev/null)"; then
      if (( SECONDS >= deadline )); then
        die "timed out waiting for ${cluster} managedclusteraddon/${addon} availability"
      fi
      die "cannot read current ${cluster} managedclusteraddon/${addon} state"
    fi
    if (( SECONDS >= deadline )); then
      die "timed out waiting for ${cluster} managedclusteraddon/${addon} availability"
    fi

    if [[ -z "${observation}" ]]; then
      ready_uid=""
      sleep 1
      continue
    fi
    if [[ "${observation}" != *$'\t'* ]]; then
      die "${cluster} managedclusteraddon/${addon} returned malformed status"
    fi
    uid="${observation%%$'\t'*}"
    condition="${observation#*$'\t'}"
    if [[ ! "${uid}" =~ ^[A-Za-z0-9._:-]{1,128}$ ]]; then
      die "${cluster} managedclusteraddon/${addon} returned malformed identity"
    fi

    case "${condition}" in
      'True|')
        if [[ "${ready_uid}" == "${uid}" ]]; then
          log "${cluster} managedclusteraddon/${addon} is Available"
          return 0
        fi
        ready_uid="${uid}"
        continue
        ;;
      '' | 'False|' | 'Unknown|')
        ready_uid=""
        ;;
      *)
        die "${cluster} managedclusteraddon/${addon} returned malformed Available status"
        ;;
    esac
    sleep 1
  done
  die "timed out waiting for ${cluster} managedclusteraddon/${addon} availability"
}

install_addons() {
  local cluster_proxy_chart="${SCRATCH_NAME}/charts/cluster-proxy-${CLUSTER_PROXY_VERSION}.tgz"
  local msa_chart="${SCRATCH_NAME}/charts/managed-serviceaccount-${MANAGED_SERVICEACCOUNT_VERSION}.tgz"

  log "downloading and verifying pinned addon charts"
  "${HELM_BIN}" repo add ocm https://open-cluster-management.io/helm-charts
  "${HELM_BIN}" repo update ocm
  "${HELM_BIN}" pull ocm/cluster-proxy --version "${CLUSTER_PROXY_VERSION}" \
    --destination "${SCRATCH_NAME}/charts"
  "${HELM_BIN}" pull ocm/managed-serviceaccount --version "${MANAGED_SERVICEACCOUNT_VERSION}" \
    --destination "${SCRATCH_NAME}/charts"
  verify_chart_digest "${cluster_proxy_chart}" "${CLUSTER_PROXY_SHA256}"
  verify_chart_digest "${msa_chart}" "${MANAGED_SERVICEACCOUNT_SHA256}"

  # The two 0.10.0 charts each own ManagedClusterSetBinding/global. Separate namespaces
  # preserve independent Helm lifecycle ownership; cluster-proxy remains in the namespace
  # expected by clusteradm proxy.
  "${HELM_BIN}" upgrade --install managed-serviceaccount "${msa_chart}" \
    --namespace open-cluster-management-managed-serviceaccount --create-namespace \
    --kube-context "${HUB_CONTEXT}" --wait --timeout 5m

  # cluster-proxy 0.10.0 emits proxyAgent.additionalValues, but its bundled CRD does not
  # declare the field. Preserve unknown proxyAgent fields until the pinned release is fixed.
  "${HELM_BIN}" show crds "${cluster_proxy_chart}" |
    "${KUBECTL_BIN}" --context "${HUB_CONTEXT}" apply -f -
  "${KUBECTL_BIN}" --context "${HUB_CONTEXT}" get crd \
    managedproxyconfigurations.proxy.open-cluster-management.io -o json |
    "${JQ_BIN}" '(.spec.versions[].schema.openAPIV3Schema.properties.spec.properties.proxyAgent) |= (. + {"x-kubernetes-preserve-unknown-fields": true})' |
    "${KUBECTL_BIN}" --context "${HUB_CONTEXT}" replace -f -
  "${HELM_BIN}" upgrade --install cluster-proxy "${cluster_proxy_chart}" \
    --namespace open-cluster-management-addon --create-namespace --skip-crds \
    --kube-context "${HUB_CONTEXT}" --wait --timeout 5m

  for cluster in spoke-a spoke-b; do
    wait_for_addon_creation "${cluster}" cluster-proxy
    wait_for_addon_creation "${cluster}" managed-serviceaccount
  done
}

docker_go_arch() {
  local architecture
  architecture="$(${DOCKER_BIN} info --format '{{.Architecture}}')"
  case "${architecture}" in
    arm64 | aarch64) printf 'arm64\n' ;;
    amd64 | x86_64) printf 'amd64\n' ;;
    *) die "unsupported Docker architecture: ${architecture}" ;;
  esac
}

build_fixture() {
  local go_arch
  go_arch="$(docker_go_arch)"
  log "building deterministic ${go_arch} spoke-local fixture"
  mkdir -m 0700 "${FIXTURE_CONTEXT}"
  CGO_ENABLED=0 GOOS=linux GOARCH="${go_arch}" "${GO_BIN}" build -trimpath \
    -o "${FIXTURE_CONTEXT}/fixture" "${REPO_ROOT}/tests/e2e/fixture/main.go"
  "${DOCKER_BIN}" build --platform "linux/${go_arch}" \
    -f "${REPO_ROOT}/tests/e2e/fixture/Dockerfile" -t "${FIXTURE_IMAGE}" "${FIXTURE_CONTEXT}"
  "${KIND_BIN}" load docker-image "${FIXTURE_IMAGE}" --name "${SPOKE_A_NAME}"
  "${KIND_BIN}" load docker-image "${FIXTURE_IMAGE}" --name "${SPOKE_B_NAME}"
}

deploy_fixture() {
  local cluster=$1
  local context=$2
  "${KUBECTL_BIN}" --context "${context}" apply -f - <<EOF
apiVersion: v1
kind: Namespace
metadata:
  name: sith-demo
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: fixture
  namespace: sith-demo
spec:
  replicas: 1
  selector:
    matchLabels:
      app: fixture
  template:
    metadata:
      labels:
        app: fixture
    spec:
      containers:
      - name: fixture
        image: ${FIXTURE_IMAGE}
        imagePullPolicy: Never
        env:
        - name: SITH_FIXTURE_CLUSTER
          value: ${cluster}
        ports:
        - containerPort: 8080
          name: http
---
apiVersion: v1
kind: Service
metadata:
  name: fixture
  namespace: sith-demo
spec:
  selector:
    app: fixture
  ports:
  - name: http
    port: 8080
    targetPort: http
EOF
  "${KUBECTL_BIN}" --context "${context}" -n sith-demo rollout status deployment/fixture \
    --timeout=180s
}

install_vulnerability_report_fixture() {
  local context=$1
  local image_id
  local digest
  image_id="$(${KUBECTL_BIN} --context "${context}" -n sith-demo get pod -l app=fixture \
    -o jsonpath='{.items[0].status.containerStatuses[0].imageID}')"
  if [[ ! "${image_id}" =~ (sha256:[a-f0-9]{64}) ]]; then
    die "fixture runtime image ID did not contain one canonical digest"
  fi
  digest="${BASH_REMATCH[1]}"

  # The fixture is a static, pre-existing Kubernetes-native report. It deliberately does not
  # install or execute a scanner, and its CRD preserves only the report shape exercised by Sith.
  "${KUBECTL_BIN}" --context "${context}" apply -f - <<'EOF'
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: vulnerabilityreports.aquasecurity.github.io
spec:
  group: aquasecurity.github.io
  names:
    kind: VulnerabilityReport
    listKind: VulnerabilityReportList
    plural: vulnerabilityreports
    singular: vulnerabilityreport
  scope: Namespaced
  versions:
  - name: v1alpha1
    served: true
    storage: true
    schema:
      openAPIV3Schema:
        type: object
        x-kubernetes-preserve-unknown-fields: true
EOF
  "${KUBECTL_BIN}" --context "${context}" wait --for=condition=Established \
    customresourcedefinition/vulnerabilityreports.aquasecurity.github.io --timeout=60s
  "${KUBECTL_BIN}" --context "${context}" apply -f - <<EOF
apiVersion: aquasecurity.github.io/v1alpha1
kind: VulnerabilityReport
metadata:
  name: fixture-runtime-image
  namespace: sith-demo
report:
  artifact:
    digest: ${digest}
    repository: ignored.example/fixture
    tag: mutable-and-ignored
  scanner:
    name: ignored
    vendor: ignored
    version: ignored
  vulnerabilities:
  - vulnerabilityID: CVE-2026-0001
    severity: HIGH
    description: not-retained
  - vulnerabilityID: CVE-2026-0002
    severity: MEDIUM
    links:
    - https://ignored.invalid/not-retained
EOF
}

create_scoped_identity() {
  local cluster=$1
  local context=$2
  "${KUBECTL_BIN}" --context "${HUB_CONTEXT}" apply -f - <<EOF
apiVersion: authentication.open-cluster-management.io/v1beta1
kind: ManagedServiceAccount
metadata:
  name: sith-reader
  namespace: ${cluster}
spec:
  rotation: {}
EOF
  "${KUBECTL_BIN}" --context "${HUB_CONTEXT}" -n "${cluster}" wait \
    managedserviceaccount/sith-reader --for=condition=SecretCreated --timeout=180s

  "${KUBECTL_BIN}" --context "${context}" apply -f - <<'EOF'
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: sith-reader-svcproxy
  namespace: sith-demo
rules:
- apiGroups: [""]
  resources: ["services", "services/proxy", "pods"]
  verbs: ["get", "list"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: sith-reader-svcproxy
  namespace: sith-demo
subjects:
- kind: ServiceAccount
  name: sith-reader
  namespace: open-cluster-management-agent-addon
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: sith-reader-svcproxy
EOF

  # Cross-namespace inventory is intentionally limited to the three resource kinds
  # normalized by Sith. This does not grant Secrets, Nodes, writes, list/watch of the
  # hub API, or an arbitrary Kubernetes API surface.
  "${KUBECTL_BIN}" --context "${context}" apply -f - <<'EOF'
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: sith-reader-inventory
rules:
- apiGroups: [""]
  resources: ["pods"]
  verbs: ["list"]
- apiGroups: ["apps"]
  resources: ["deployments"]
  verbs: ["list"]
- apiGroups: ["argoproj.io"]
  resources: ["rollouts"]
  verbs: ["list"]
- apiGroups: ["aquasecurity.github.io"]
  resources: ["vulnerabilityreports"]
  verbs: ["list"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: sith-reader-inventory
subjects:
- kind: ServiceAccount
  name: sith-reader
  namespace: open-cluster-management-agent-addon
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: sith-reader-inventory
EOF

  # Projected secrets may contain only the token and CA, never a kubeconfig.
  "${KUBECTL_BIN}" --context "${HUB_CONTEXT}" -n "${cluster}" get secret sith-reader -o json |
    "${JQ_BIN}" -e '(.data | keys | sort) == ["ca.crt", "token"]' >/dev/null
}

verify_cluster_registration() {
  local cluster
  for cluster in spoke-a spoke-b; do
    "${KUBECTL_BIN}" --context "${HUB_CONTEXT}" get "managedcluster/${cluster}" -o json |
      "${JQ_BIN}" -e 'any(.status.conditions[]; .type == "ManagedClusterConditionAvailable" and .status == "True")' \
      >/dev/null
    for addon in cluster-proxy managed-serviceaccount; do
      "${KUBECTL_BIN}" --context "${HUB_CONTEXT}" -n "${cluster}" get \
        "managedclusteraddon/${addon}" -o json |
        "${JQ_BIN}" -e 'any(.status.conditions[]; .type == "Available" and .status == "True")' \
        >/dev/null
    done
  done
}

proxy_health_port_available() {
  "${PYTHON_BIN}" - <<'PY'
import socket

try:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as listener:
        listener.bind(("127.0.0.1", 8090))
except OSError:
    raise SystemExit(1)
PY
}

verify_scoped_proxy() {
  local cluster
  local response
  local secrets_output
  local nodes_output

  for cluster in spoke-a spoke-b; do
    response="$(${CLUSTERADM_BIN} proxy kubectl --context "${HUB_CONTEXT}" --cluster="${cluster}" \
      --sa=sith-reader --args='get --raw /api/v1/namespaces/sith-demo/services/fixture:8080/proxy/' \
      2>&1)"
    [[ "${response}" == *"sith fixture cluster=${cluster}"* ]] ||
      die "scoped proxy did not reach ${cluster} fixture"

    # clusteradm 1.3.1 returns zero even when its inner kubectl is forbidden. Match the
    # authenticated identity and fail-closed authorization response instead of trusting rc.
    secrets_output="$(${CLUSTERADM_BIN} proxy kubectl --context "${HUB_CONTEXT}" \
      --cluster="${cluster}" --sa=sith-reader --args='get secrets -A' 2>&1)"
    [[ "${secrets_output}" == *'Error from server (Forbidden)'* ]] ||
      die "${cluster} token did not fail closed for secrets"
    [[ "${secrets_output}" == *'system:serviceaccount:open-cluster-management-agent-addon:sith-reader'* ]] ||
      die "${cluster} secrets denial used an unexpected identity"
    [[ "${secrets_output}" == *'cannot list resource "secrets"'* ]] ||
      die "${cluster} secrets denial was not the expected RBAC decision"

    nodes_output="$(${CLUSTERADM_BIN} proxy kubectl --context "${HUB_CONTEXT}" \
      --cluster="${cluster}" --sa=sith-reader --args='get nodes' 2>&1)"
    [[ "${nodes_output}" == *'Error from server (Forbidden)'* ]] ||
      die "${cluster} token did not fail closed for nodes"
    [[ "${nodes_output}" == *'system:serviceaccount:open-cluster-management-agent-addon:sith-reader'* ]] ||
      die "${cluster} nodes denial used an unexpected identity"
    [[ "${nodes_output}" == *'cannot list resource "nodes"'* ]] ||
      die "${cluster} nodes denial was not the expected RBAC decision"

    log "${cluster}: scoped reach PASS; secrets/nodes denied"
  done
}

firewall_probe_packets() {
  local node=$1

  "${DOCKER_BIN}" exec "${node}" iptables -nvx -L "${FIREWALL_CHAIN}" 1 |
    awk '$3 == "REJECT" {print $1; found=1} END {if (!found) exit 1}'
}

probe_hub_connection_denied() {
  local destination=$1
  local port=$2
  local source_node=$3
  local after_packets
  local before_packets
  local probe_rc

  [[ "${destination}" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]] ||
    die "unexpected probe destination"
  [[ "${port}" =~ ^[0-9]+$ ]] || die "unexpected probe port"

  "${DOCKER_BIN}" exec "${source_node}" timeout 3 bash -c \
    "exec 3<>/dev/tcp/${destination}/${port}" >/dev/null 2>&1 ||
    die "negative-control listener ${destination}:${port} is not reachable from its spoke node"
  before_packets="$(firewall_probe_packets "${source_node}")" ||
    die "cannot read the hub-deny probe counter"
  if "${DOCKER_BIN}" exec "${HUB_NAME}-control-plane" timeout 3 bash -c \
    "exec 3<>/dev/tcp/${destination}/${port}" >/dev/null 2>&1; then
    probe_rc=0
  else
    probe_rc=$?
  fi
  after_packets="$(firewall_probe_packets "${source_node}")" ||
    die "cannot read the hub-deny probe counter after the active check"
  if [[ "${probe_rc}" == "0" ]]; then
    die "hub initiated a forbidden connection to ${destination}:${port}"
  fi
  [[ "${after_packets}" -gt "${before_packets}" ]] ||
    die "hub probe failed without hitting the spoke firewall; refusing a false PASS"
}

verify_spoke_ingress_boundary() {
  local base_chain
  local cluster
  local hub_ip
  local node
  local node_ip

  hub_ip="$(${DOCKER_BIN} inspect "${HUB_NAME}-control-plane" \
    --format '{{.NetworkSettings.Networks.kind.IPAddress}}')"
  for cluster in spoke-a spoke-b; do
    node="${LAB_PREFIX}-${cluster}-control-plane"
    node_ip="$(${DOCKER_BIN} inspect "${node}" \
      --format '{{.NetworkSettings.Networks.kind.IPAddress}}')"

    for base_chain in INPUT FORWARD; do
      "${DOCKER_BIN}" exec "${node}" iptables -C "${base_chain}" \
        -j "${FIREWALL_CHAIN}" >/dev/null ||
        die "${cluster} is missing the ${base_chain} hub-ingress deny jump"
    done
    "${DOCKER_BIN}" exec "${node}" iptables -C "${FIREWALL_CHAIN}" -s "${hub_ip}" \
      -m conntrack --ctstate NEW -j REJECT >/dev/null ||
      die "${cluster} is missing the hub-node source deny"
    "${DOCKER_BIN}" exec "${node}" iptables -C "${FIREWALL_CHAIN}" -i eth0 \
      -s 10.244.0.0/16 -m conntrack --ctstate NEW -j REJECT >/dev/null ||
      die "${cluster} is missing the external hub-pod source deny"
    probe_hub_connection_denied "${node_ip}" 6443 "${node}"
    # Each single-node kind cluster reuses 10.244.0.0/24. A direct pod-IP probe from the
    # hub would therefore hit a colliding hub-local pod, not the selected spoke. The
    # FORWARD policy is asserted above; the unique node API supplies the active denial probe.
    log "${cluster}: active hub-to-node probe denied; hub-to-pod FORWARD deny present"
  done
}

verify_outbound_only() {
  local cluster
  local node
  local node_ip
  local hub_ip
  local counts
  local outbound
  local inbound

  hub_ip="$(${DOCKER_BIN} inspect "${HUB_NAME}-control-plane" \
    --format '{{.NetworkSettings.Networks.kind.IPAddress}}')"
  for cluster in spoke-a spoke-b; do
    node="${LAB_PREFIX}-${cluster}-control-plane"
    node_ip="$(${DOCKER_BIN} inspect "${node}" \
      --format '{{.NetworkSettings.Networks.kind.IPAddress}}')"
    # Values enter the intentionally single-quoted awk program through -v.
    # shellcheck disable=SC2016
    counts="$(${DOCKER_BIN} exec "${node}" awk -v hub="${hub_ip}" -v node="${node_ip}" '
      {
        src=""; dst=""; dport=""
        for (i=1;i<=NF;i++) {
          if (src=="" && $i ~ /^src=/) src=substr($i,5)
          else if (dst=="" && $i ~ /^dst=/) dst=substr($i,5)
          else if (dport=="" && $i ~ /^dport=/) dport=substr($i,7)
        }
        if (src ~ /^10\.244\./ && dst==hub && dport=="6443") outbound++
        if (src==hub && (dst==node || dst ~ /^10\.244\./)) inbound++
      }
      END {print outbound+0, inbound+0}
    ' /proc/net/nf_conntrack)"
    outbound="${counts%% *}"
    inbound="${counts##* }"
    [[ "${outbound}" -gt 0 ]] || die "no spoke-originated hub connection found for ${cluster}"
    [[ "${inbound}" -eq 0 ]] || die "hub-originated spoke connection found for ${cluster}"
    log "${cluster}: outbound flows to hub:6443=${outbound}; hub-initiated flows=0"
  done
}

verify_lab() {
  local transport_mode="clusteradm-scoped"

  verify_cluster_registration
  if proxy_health_port_available; then
    "${CLUSTERADM_BIN}" proxy health --context "${HUB_CONTEXT}"
    verify_scoped_proxy
  else
    transport_mode="direct-e2e-required"
    log "local port 8090 is occupied; deferring clusteradm proxy checks to the mandatory direct e2e gate"
  fi
  verify_spoke_ingress_boundary
  verify_outbound_only
  log "M0_RESULT=PASS topology=hub+2-spokes identity=scoped-msa transport=${transport_mode} boundary=active-deny"
}

run_lab() {
  local started_at
  local finished_at
  started_at="$(date +%s)"
  prepare_default_scratch_parent
  enter_scratch_parent
  check_tools
  ensure_lab_absent
  prepare_scratch
  create_clusters
  enforce_spoke_ingress_boundary
  initialize_ocm
  install_addons
  build_fixture
  deploy_fixture spoke-a "${SPOKE_A_CONTEXT}"
  deploy_fixture spoke-b "${SPOKE_B_CONTEXT}"

  install_vulnerability_report_fixture "${SPOKE_A_CONTEXT}"
  install_vulnerability_report_fixture "${SPOKE_B_CONTEXT}"
  create_scoped_identity spoke-a "${SPOKE_A_CONTEXT}"
  create_scoped_identity spoke-b "${SPOKE_B_CONTEXT}"
  verify_lab
  finished_at="$(date +%s)"
  log "elapsed_seconds=$((finished_at - started_at))"
}

cleanup_lab() {
  check_tools
  delete_clusters || die "cluster deletion failed; retaining scratch for recovery"
  remove_scratch
  log "cleanup complete"
}

main() {
  local command="${1:-run}"
  case "${command}" in
    run)
      trap on_exit EXIT
      trap 'exit 130' INT TERM
      run_lab
      ;;
    verify)
      enter_scratch_parent
      check_tools
      validate_owned_scratch
      verify_lab
      ;;
    cleanup)
      cleanup_lab
      ;;
    -h | --help | help)
      usage
      ;;
    *)
      usage >&2
      die "unknown command: ${command}"
      ;;
  esac
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  main "$@"
fi
