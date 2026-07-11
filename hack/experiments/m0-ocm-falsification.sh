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

readonly KIND_BIN="${KIND_BIN:-kind}"
readonly KUBECTL_BIN="${KUBECTL_BIN:-kubectl}"
readonly CLUSTERADM_BIN="${CLUSTERADM_BIN:-clusteradm}"
readonly HELM_BIN="${HELM_BIN:-helm}"
readonly DOCKER_BIN="${DOCKER_BIN:-docker}"
readonly JQ_BIN="${JQ_BIN:-jq}"
readonly GO_BIN="${GO_BIN:-go}"

REPO_ROOT="$(git rev-parse --show-toplevel)"
readonly REPO_ROOT
readonly LAB_PREFIX="${SITH_M0_PREFIX:-sith-m0}"
readonly HUB_NAME="${LAB_PREFIX}-hub"
readonly SPOKE_A_NAME="${LAB_PREFIX}-spoke-a"
readonly SPOKE_B_NAME="${LAB_PREFIX}-spoke-b"
readonly HUB_CONTEXT="kind-${HUB_NAME}"
readonly SPOKE_A_CONTEXT="kind-${SPOKE_A_NAME}"
readonly SPOKE_B_CONTEXT="kind-${SPOKE_B_NAME}"
readonly SCRATCH_ROOT="${SITH_M0_SCRATCH_ROOT:-/Volumes/EXTENDED/tmp/sith-m0}"
readonly KUBECONFIG_PATH="${SCRATCH_ROOT}/kubeconfig"
readonly FIXTURE_IMAGE="sith-m0-fixture:v1"

KEEP_CLUSTERS="${SITH_M0_KEEP_CLUSTERS:-0}"
BOOTSTRAP_IDENTITY_ROTATED=0

export KUBECONFIG="${KUBECONFIG_PATH}"
export TMPDIR="${SCRATCH_ROOT}/tmp"
export HELM_CONFIG_HOME="${SCRATCH_ROOT}/helm/config"
export HELM_CACHE_HOME="${SCRATCH_ROOT}/helm/cache"
export HELM_DATA_HOME="${SCRATCH_ROOT}/helm/data"
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
  SITH_M0_SCRATCH_ROOT=<path>    Scratch path; defaults to EXTENDED storage.
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
  "${KIND_BIN}" get clusters 2>/dev/null | grep -Fxq "$1"
}

validate_scratch_root() {
  case "${SCRATCH_ROOT}" in
    /Volumes/EXTENDED/*) ;;
    *)
      [[ "${SITH_M0_ALLOW_NON_EXTENDED:-0}" == "1" ]] ||
        die "scratch must remain on /Volumes/EXTENDED (or explicitly set SITH_M0_ALLOW_NON_EXTENDED=1)"
      ;;
  esac

  [[ "${SCRATCH_ROOT}" != "/" ]] || die "refusing unsafe scratch root"
  [[ -n "${SCRATCH_ROOT}" ]] || die "scratch root must not be empty"
}

check_tools() {
  local clusteradm_version
  local helm_version
  local kind_version
  local go_version

  for command_name in "${KIND_BIN}" "${KUBECTL_BIN}" "${CLUSTERADM_BIN}" "${HELM_BIN}" \
    "${DOCKER_BIN}" "${JQ_BIN}" "${GO_BIN}" awk grep sed shasum; do
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

  "${DOCKER_BIN}" info >/dev/null 2>&1 || die "Docker engine is unavailable"
}

prepare_scratch() {
  validate_scratch_root
  mkdir -p "${TMPDIR}" "${HELM_CONFIG_HOME}" "${HELM_CACHE_HOME}" "${HELM_DATA_HOME}" \
    "${SCRATCH_ROOT}/charts"
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
  for cluster in "${SPOKE_B_NAME}" "${SPOKE_A_NAME}" "${HUB_NAME}"; do
    if cluster_exists "${cluster}"; then
      "${KIND_BIN}" delete cluster --name "${cluster}"
    fi
  done
}

remove_scratch() {
  validate_scratch_root
  if [[ -d "${SCRATCH_ROOT}" ]]; then
    rm -rf "${SCRATCH_ROOT}"
  fi
}

rotate_bootstrap_identity() {
  local namespace="open-cluster-management"
  local account="agent-registration-bootstrap"
  local old_uid
  local new_uid

  if [[ "${BOOTSTRAP_IDENTITY_ROTATED}" == "1" ]] || ! cluster_exists "${HUB_NAME}"; then
    return
  fi
  if ! "${KUBECTL_BIN}" --context "${HUB_CONTEXT}" -n "${namespace}" get serviceaccount \
    "${account}" >/dev/null 2>&1; then
    return
  fi

  old_uid="$(${KUBECTL_BIN} --context "${HUB_CONTEXT}" -n "${namespace}" get serviceaccount \
    "${account}" -o jsonpath='{.metadata.uid}')"
  "${KUBECTL_BIN}" --context "${HUB_CONTEXT}" -n "${namespace}" delete serviceaccount \
    "${account}" --wait=true >/dev/null
  "${KUBECTL_BIN}" --context "${HUB_CONTEXT}" -n "${namespace}" create serviceaccount \
    "${account}" >/dev/null
  new_uid="$(${KUBECTL_BIN} --context "${HUB_CONTEXT}" -n "${namespace}" get serviceaccount \
    "${account}" -o jsonpath='{.metadata.uid}')"
  [[ "${old_uid}" != "${new_uid}" ]] || die "bootstrap ServiceAccount UID did not rotate"
  BOOTSTRAP_IDENTITY_ROTATED=1
  log "registration bootstrap credential invalidated"
}

on_exit() {
  local exit_code=$?
  trap - EXIT
  set +e
  rotate_bootstrap_identity
  if [[ "${KEEP_CLUSTERS}" == "1" ]]; then
    log "retaining lab because SITH_M0_KEEP_CLUSTERS=1"
  else
    delete_clusters
    remove_scratch
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

initialize_ocm() {
  local hub_api
  local hub_token
  local join_command

  log "initializing OCM hub"
  "${CLUSTERADM_BIN}" init --wait --context "${HUB_CONTEXT}" 2>&1 | redact_tokens

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
      --cluster-name spoke-a --force-internal-endpoint-lookup --wait \
      --context "${SPOKE_A_CONTEXT}"
    "${CLUSTERADM_BIN}" join --hub-token "${hub_token}" --hub-apiserver "${hub_api}" \
      --cluster-name spoke-b --force-internal-endpoint-lookup --wait \
      --context "${SPOKE_B_CONTEXT}"
  } 2>&1 | redact_tokens
  hub_token=""
  join_command=""

  "${CLUSTERADM_BIN}" accept --clusters spoke-a,spoke-b --wait --context "${HUB_CONTEXT}"
  rotate_bootstrap_identity

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

install_addons() {
  local cluster_proxy_chart="${SCRATCH_ROOT}/charts/cluster-proxy-${CLUSTER_PROXY_VERSION}.tgz"
  local msa_chart="${SCRATCH_ROOT}/charts/managed-serviceaccount-${MANAGED_SERVICEACCOUNT_VERSION}.tgz"

  log "downloading and verifying pinned addon charts"
  "${HELM_BIN}" repo add ocm https://open-cluster-management.io/helm-charts
  "${HELM_BIN}" repo update ocm
  "${HELM_BIN}" pull ocm/cluster-proxy --version "${CLUSTER_PROXY_VERSION}" \
    --destination "${SCRATCH_ROOT}/charts"
  "${HELM_BIN}" pull ocm/managed-serviceaccount --version "${MANAGED_SERVICEACCOUNT_VERSION}" \
    --destination "${SCRATCH_ROOT}/charts"
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
    "${KUBECTL_BIN}" --context "${HUB_CONTEXT}" -n "${cluster}" wait \
      managedclusteraddon/cluster-proxy --for=condition=Available --timeout=300s
    "${KUBECTL_BIN}" --context "${HUB_CONTEXT}" -n "${cluster}" wait \
      managedclusteraddon/managed-serviceaccount --for=condition=Available --timeout=300s
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
  CGO_ENABLED=0 GOOS=linux GOARCH="${go_arch}" "${GO_BIN}" build -trimpath \
    -o "${SCRATCH_ROOT}/fixture" "${REPO_ROOT}/tests/e2e/fixture"
  "${DOCKER_BIN}" build --platform "linux/${go_arch}" \
    -f "${REPO_ROOT}/tests/e2e/fixture/Dockerfile" -t "${FIXTURE_IMAGE}" "${SCRATCH_ROOT}"
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
  verify_cluster_registration
  "${CLUSTERADM_BIN}" proxy health --context "${HUB_CONTEXT}"
  verify_scoped_proxy
  verify_outbound_only
  log "M0_RESULT=PASS topology=hub+2-spokes identity=scoped-msa transport=outbound-only"
}

run_lab() {
  local started_at
  local finished_at
  started_at="$(date +%s)"
  check_tools
  validate_scratch_root
  ensure_lab_absent
  prepare_scratch
  create_clusters
  initialize_ocm
  install_addons
  build_fixture
  deploy_fixture spoke-a "${SPOKE_A_CONTEXT}"
  deploy_fixture spoke-b "${SPOKE_B_CONTEXT}"
  create_scoped_identity spoke-a "${SPOKE_A_CONTEXT}"
  create_scoped_identity spoke-b "${SPOKE_B_CONTEXT}"
  verify_lab
  finished_at="$(date +%s)"
  log "elapsed_seconds=$((finished_at - started_at))"
}

cleanup_lab() {
  check_tools
  validate_scratch_root
  delete_clusters
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
      check_tools
      validate_scratch_root
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

main "$@"
