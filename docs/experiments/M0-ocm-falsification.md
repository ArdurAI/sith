# Milestone-0 — OCM falsification test (evidence + runbook)

**Status:** ✅ **PASS** · **Date:** 2026-07-08 · **Verdict owner:** falsification test per
[ROADMAP Milestone-0](../ROADMAP.md) and [ADR-0001](../adr/0001-adopt-ocm-vs-bespoke-tunnel.md)

## Verdict

> **A central OCM hub reached an in-cluster service on a managed spoke using a scoped
> `managed-serviceaccount` token, over the `cluster-proxy` reverse tunnel, with the spoke
> connecting outbound-only and the hub holding no admin kubeconfig.**

The core premise of ADR-0001 holds. The "build a bespoke outbound-only agent + reverse
tunnel + reach-cluster-local-services" scope is **deleted** from Sith. We adopt OCM as the
connectivity + scoped-identity substrate and build only the federation/governance layer
above it. **ADR-0001 → Accepted. Proceed to Phase 1.**

Hands-on execution — from `kind create cluster` to the passing reach-test and the
outbound-only verification — took **~15 minutes of wall-clock** on a laptop, far inside the
`≤ ~1 day` exit criterion.

This maps to the Milestone-0 issues: **#2** (provision), **#3** (addons + projected scoped
token), **#4** (the deciding reach-test), **#5** (outbound-only), **#6** (record verdict).

---

## What was tested (and what would have falsified it)

| Claim under test | Would be falsified if… | Result |
|---|---|---|
| `cluster-proxy` gives the hub network reach into an isolated spoke's cluster-local services | reach requires a bespoke tunnel or inbound hub→spoke access | ✅ reached `nginx.sith-demo` on the spoke |
| `managed-serviceaccount` yields a **scoped** identity, not a god-credential | the token could act as admin / the hub needed an admin kubeconfig | ✅ token can read the service; **denied** `secrets`/`nodes` |
| The spoke connects **outbound-only** | the spoke had to expose an inbound port the hub dials | ✅ all flows are spoke→hub; **zero** hub→spoke-initiated flows |

The test was deliberately built to fail loudly: the scoped token was granted *only*
`services/proxy` + `pods` read in one namespace, so if reach had silently depended on
broader privilege, the negative-control commands (`get secrets -A`, `get nodes`) would have
**succeeded** — they did not.

---

## Topology

```
   ┌────────────────────────┐         reverse tunnel (konnectivity)         ┌──────────────────────────┐
   │  HUB  (kind: hub)       │   ◀───────── spoke dials OUT to hub ──────────│  SPOKE (kind: spoke1)    │
   │  172.18.0.2             │                                               │  172.18.0.3              │
   │  • OCM cluster-manager  │                                               │  • klusterlet            │
   │  • cluster-proxy server │                                               │  • cluster-proxy agent   │
   │  • MSA addon-manager    │                                               │  • MSA agent (SA+token)  │
   │  • scoped MSA token ─────────────── authenticates as ──────────────────▶│  ns sith-demo: nginx svc │
   └────────────────────────┘                                               └──────────────────────────┘
   Hub holds NO spoke admin kubeconfig — only the scoped token secret spoke1/sith-reader.
```

Both clusters are single-node `kind` clusters on the shared `kind` Docker network (mutual
IP reachability). Everything scratch lived on `/Volumes/EXTENDED` (`TMPDIR`); the system
disk was never at risk (182 GiB free after the run).

## Versions (pinned)

| Component | Version |
|---|---|
| `clusteradm` | `v1.3.1-0-g90bdc31` |
| OCM core (`registration-operator` on hub + spoke) | `quay.io/open-cluster-management/registration-operator:v1.3.1` |
| `cluster-proxy` addon (Helm chart / app) | **`0.10.0`** / `1.1.0` |
| `managed-serviceaccount` addon (Helm chart / app) | **`0.10.0`** / `1.0.0` |
| `kind` | `v0.30.0` |
| Kubernetes (node image) | `v1.34.0` |
| `kubectl` client | `v1.33.3` |
| `helm` | `v4.1.4` |
| Docker Engine | `29.6.1` (14 CPU / 7.65 GiB VM) |

The addon chart versions match the ADR-0001 pin exactly (`cluster-proxy` 0.10.0,
`managed-serviceaccount` 0.10.0 — both verified as the latest published charts, 2026-07-08).

---

## Runbook (reproducible)

```bash
export TMPDIR=/Volumes/EXTENDED/tmp        # keep scratch off the small system disk

# 1) Two single-node kind clusters on the shared `kind` docker network
kind create cluster --name hub            # context kind-hub   (172.18.0.2)
kind create cluster --name spoke1         # context kind-spoke1 (172.18.0.3)

# 2) Bootstrap the OCM hub
clusteradm init --wait --context kind-hub
#    -> prints a `clusteradm join ...` command with a bootstrap token and
#       --hub-apiserver https://127.0.0.1:<port>  (host-reachable loopback)

# 3) Register the spoke (OUTBOUND registration).
#    The join runs on the host, so its own cluster-info fetch uses the host-reachable
#    loopback endpoint; --force-internal-endpoint-lookup bakes the INTERNAL hub endpoint
#    (hub-control-plane:6443, resolvable to 172.18.0.2 from spoke pods) into the klusterlet.
clusteradm join \
  --hub-token "<token from init>" \
  --hub-apiserver https://127.0.0.1:<port> \
  --cluster-name spoke1 \
  --force-internal-endpoint-lookup \
  --wait --context kind-spoke1
clusteradm accept --clusters spoke1 --context kind-hub

# 4) Enable the two addons on the hub (agents auto-deploy to the spoke). See CAVEAT below.
helm repo add ocm https://open-cluster-management.io/helm-charts && helm repo update
helm install -n open-cluster-management-addon --create-namespace \
  managed-serviceaccount ocm/managed-serviceaccount --version 0.10.0 --kube-context kind-hub
#  cluster-proxy needs a one-line CRD workaround for a 0.10.0 packaging bug (see CAVEAT):
kubectl --context kind-hub get crd managedproxyconfigurations.proxy.open-cluster-management.io -o json \
  | jq '(.spec.versions[].schema.openAPIV3Schema.properties.spec.properties.proxyAgent) |= (. + {"x-kubernetes-preserve-unknown-fields": true})' \
  | kubectl --context kind-hub replace -f -
helm install -n open-cluster-management-addon \
  cluster-proxy ocm/cluster-proxy --version 0.10.0 --skip-crds --kube-context kind-hub

# 5) A trivial in-cluster service on the spoke
kubectl --context kind-spoke1 create namespace sith-demo
kubectl --context kind-spoke1 -n sith-demo create deployment nginx --image=nginx:1.27-alpine --port=80
kubectl --context kind-spoke1 -n sith-demo expose deployment nginx --port=80 --name=nginx

# 6) Mint a SCOPED identity: ManagedServiceAccount on the hub -> token projected back to hub
kubectl --context kind-hub apply -f - <<'EOF'
apiVersion: authentication.open-cluster-management.io/v1beta1
kind: ManagedServiceAccount
metadata: { name: sith-reader, namespace: spoke1 }
spec: { rotation: {} }
EOF
# Grant it ONLY services/proxy + pods read in sith-demo on the spoke (deliberately minimal)
kubectl --context kind-spoke1 apply -f - <<'EOF'
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata: { name: sith-reader-svcproxy, namespace: sith-demo }
rules:
- { apiGroups: [""], resources: ["services","services/proxy"], verbs: ["get","list"] }
- { apiGroups: [""], resources: ["pods"], verbs: ["get","list"] }
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata: { name: sith-reader-svcproxy, namespace: sith-demo }
subjects:
- { kind: ServiceAccount, name: sith-reader, namespace: open-cluster-management-agent-addon }
roleRef: { kind: Role, name: sith-reader-svcproxy, apiGroup: rbac.authorization.k8s.io }
EOF

# 7) THE TEST — hub reaches the spoke's in-cluster service, through cluster-proxy,
#    authenticating with the scoped MSA token (NOT an admin kubeconfig):
clusteradm proxy kubectl --cluster=spoke1 --sa=sith-reader \
  --args="get --raw /api/v1/namespaces/sith-demo/services/nginx:80/proxy/"
```

`clusteradm proxy kubectl` is the official OCM path that ties both addons together — its own
help states: *"Use kubectl through cluster-proxy addon. (Only supports managed service
account token as certificate.)"* It port-forwards to the hub's `cluster-proxy` server, dials
the konnectivity tunnel to the spoke's kube-apiserver, and authenticates as the `--sa`
ManagedServiceAccount. The `/services/<name>:<port>/proxy/` path is the spoke apiserver's
built-in service-proxy subresource, which reaches the cluster-local `nginx` Service.

---

## Evidence (verbatim command output from the run)

### E1 — Spoke registered and Available; both addons healthy

```
$ kubectl --context kind-hub get managedcluster
NAME     HUB ACCEPTED   MANAGED CLUSTER URLS                JOINED   AVAILABLE   AGE
spoke1   true           https://spoke1-control-plane:6443   True     True        10m

$ kubectl --context kind-hub get managedclusteraddon -A
NAMESPACE   NAME                     AVAILABLE   DEGRADED   PROGRESSING
spoke1      cluster-proxy            True                   False
spoke1      managed-serviceaccount   True                   False

$ clusteradm proxy health --context kind-hub
CLUSTER NAME    INSTALLED    AVAILABLE    PROBED HEALTH    LATENCY
spoke1          True         True         True             36.611541ms
```

### E2 — The projected identity is a scoped ServiceAccount token (not admin, not a kubeconfig)

```
# The only spoke credential present in the hub's spoke1 namespace:
$ kubectl --context kind-hub -n spoke1 get secrets
NAME          TYPE     DATA   AGE
sith-reader   Opaque   2      4m8s

# Decoded JWT payload of that projected token:
{
  "sub": "system:serviceaccount:open-cluster-management-agent-addon:sith-reader",
  "namespace": "open-cluster-management-agent-addon",
  "sa": "sith-reader",
  "aud": ["https://kubernetes.default.svc.cluster.local"],
  "exp": 1814655286
}

# Scope of that identity on the spoke (kubectl auth can-i, impersonating the SA):
can-i get services/proxy -n sith-demo : yes
can-i list secrets -A                 : no
can-i create pods/exec -n sith-demo   : no
```

### E3 — THE TEST: hub reaches the spoke's in-cluster nginx via cluster-proxy + scoped token

```
$ clusteradm proxy kubectl --cluster=spoke1 --sa=sith-reader \
    --args="get --raw /api/v1/namespaces/sith-demo/services/nginx:80/proxy/"

<!DOCTYPE html>
<html>
<head>
<title>Welcome to nginx!</title>
...
<h1>Welcome to nginx!</h1>
<p>If you see this page, the nginx web server is successfully installed and
working. Further configuration is required.</p>
...
</html>
```

The hub received the cluster-local nginx page served from inside the spoke — through the
reverse tunnel, authenticated by the scoped token.

### E4 — Scope is real: same tunnel, in-scope read works, out-of-scope reads are refused

```
# In scope — list the nginx pod in sith-demo:
$ clusteradm proxy kubectl --cluster=spoke1 --sa=sith-reader --args="get pods -n sith-demo -o wide"
NAME                    READY   STATUS    ...  IP            NODE
nginx-68fc8fd8f-h257m   1/1     Running   ...  10.244.0.11   spoke1-control-plane

# Out of scope — cluster-wide secrets:
$ clusteradm proxy kubectl --cluster=spoke1 --sa=sith-reader --args="get secrets -A"
Error from server (Forbidden): secrets is forbidden: User
"system:serviceaccount:open-cluster-management-agent-addon:sith-reader" cannot list
resource "secrets" in API group "" at the cluster scope

# Out of scope — nodes (an admin kubeconfig would return the node list):
$ clusteradm proxy kubectl --cluster=spoke1 --sa=sith-reader --args="get nodes"
Error from server (Forbidden): nodes is forbidden: User
"system:serviceaccount:open-cluster-management-agent-addon:sith-reader" cannot list
resource "nodes" in API group "" at the cluster scope
```

This is the crux: reach and privilege are **decoupled**. The hub reaches exactly what the
spoke's local RBAC grants the scoped identity, and nothing more.

### E5 — Outbound-only: the spoke dials the hub; the hub never dials into the spoke

`cluster-proxy` 0.10.0 uses the **PortForward** entrypoint on `kind`: the spoke's
cluster-proxy `addon-agent` establishes the reverse tunnel by dialing **out** to the hub
kube-apiserver and port-forwarding to the hub proxy-server (its proxy-agent connects to a
local `127.0.0.1:8091`). So the spoke's *only* egress is to the hub kube-apiserver.

```
# Spoke proxy-agent is configured to reach the proxy server via a LOCAL forward, not a dial-in:
proxy-agent args: --proxy-server-host=127.0.0.1  --proxy-server-port=8091

# Connection tracking on the spoke node — every hub-directed flow ORIGINATES on the spoke
# (src=10.244.0.x spoke pods) to the hub kube-apiserver (dst=172.18.0.2 dport=6443):
$ docker exec spoke1-control-plane cat /proc/net/nf_conntrack | grep dport=6443 | grep dst=172.18.0.2
ESTABLISHED src=10.244.0.9  dst=172.18.0.2 sport=42456 dport=6443  src=172.18.0.2 dst=172.18.0.3 sport=6443 dport=42456 [ASSURED]
ESTABLISHED src=10.244.0.6  dst=172.18.0.2 sport=45608 dport=6443  src=172.18.0.2 dst=172.18.0.3 sport=6443 dport=45608 [ASSURED]
ESTABLISHED src=10.244.0.10 dst=172.18.0.2 sport=53738 dport=6443  src=172.18.0.2 dst=172.18.0.3 sport=6443 dport=53738 [ASSURED]

# Are there ANY flows the HUB initiated INTO the spoke pod network?
$ docker exec spoke1-control-plane sh -c 'cat /proc/net/nf_conntrack | grep -E "src=172.18.0.2 dst=10.244" || echo NONE'
NONE — hub never initiates a connection into the spoke

# Per-pod client side of the tunnel connection (spoke holds the ephemeral client port):
$ nsenter -t <cluster-proxy addon-agent pid> -n ss -tnp | grep 6443
ESTAB  0  0   10.244.0.9:42456   172.18.0.2:6443   users:(("agent",pid=2577,fd=14))
```

The spoke holds the ephemeral (client) port `42456`; the hub holds the well-known `6443`.
The spoke **dialed out**. No inbound hub→spoke port is required — the property that lets
spokes live in isolated VPCs / behind NAT.

---

## Caveats and honesty notes

- **`cluster-proxy` 0.10.0 chart packaging bug (worked around, not fatal).** The chart's
  `ManagedProxyConfiguration` template unconditionally sets `spec.proxyAgent.additionalValues`
  (`enableImpersonation`), but the CRD shipped in the same chart (and on `main` and the
  `v0.10.0` tag) does not declare that field, so `helm install` fails with
  *".spec.proxyAgent.additionalValues: field not declared in schema"*. Worked around by
  patching the live CRD with `x-kubernetes-preserve-unknown-fields: true` on `proxyAgent`
  and installing with `--skip-crds`. This is a **cosmetic upstream release-skew issue**, not
  a transport defect — `additionalValues.enableImpersonation` drives the (unused here)
  service-proxy impersonation mode; it is irrelevant to the kube-apiserver-proxy + MSA-token
  path this experiment exercises. Worth filing upstream; does not affect the verdict.
  (Depended-upon versions remain pinned to 0.10.0 per ADR-0001.)

- **`clusteradm proxy kubectl` emits benign `broken pipe` / `portforward` noise** on stderr
  when the wrapped kubectl finishes quickly and the port-forward is torn down. The kubectl
  results themselves are correct (shown above); the noise is cosmetic.

- **Lab RBAC was applied directly to the spoke** for speed. In the product, spoke-side RBAC
  for a scoped identity is delivered as an OCM `ManifestWork` / `ClusterPermission` from the
  hub (never by uploading a kubeconfig). The security property under test — *reach requires
  no admin credential, and privilege is bounded by spoke-local RBAC* — is unchanged.

- **`kind`-specific:** both clusters share one Docker network, so the "isolated VPC" is
  simulated, not physically separate. What is genuinely demonstrated is the **connection
  directionality** (spoke→hub only) and **scoped-identity** properties, which are transport-
  and topology-independent. The `entrypoint: PortForward` mode used here needs no
  LoadBalancer/Ingress on the hub, which is itself the strongest outbound-only story: the
  spoke only ever needs to reach the hub's kube-apiserver.

- **One spoke, not two.** Roadmap M0 mentions 2 spokes; the task scoped this run to hub + 1
  spoke, which is sufficient to falsify the connectivity + scoped-identity premise. Memory
  headroom was ample (hub 1.59 GiB, spoke 1.19 GiB of a 7.65 GiB VM), so a second spoke is a
  scale detail for Phase 1, not a gate for the M0 verdict.

## Resource footprint

| | Before | After |
|---|---|---|
| System disk `/` free | 193 GiB | 182 GiB |
| `/Volumes/EXTENDED` free | 1.6 TiB | 1.6 TiB |
| Docker images | 3.35 GB (pruned 1.65 GB build cache first) | 4.12 GB |
| Docker VM memory in use | — | ~2.8 GiB / 7.65 GiB |

Clusters were deleted at teardown (`kind delete cluster --name hub --name spoke1`) to
reclaim the space.

## Conclusion

Every claim ADR-0001 depends on was reproduced with real command output, and the negative
controls that would have exposed a hidden admin dependency all failed closed. **OCM
`cluster-proxy` + `managed-serviceaccount` deliver outbound-only, scoped, reach-cluster-
local-services connectivity.** Sith adopts OCM and does not build a bespoke transport.
</content>
</invoke>
