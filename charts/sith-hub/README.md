# Sith hub Helm chart

This chart is a fail-closed deployment contract for a released Sith hub image. The repository does not currently publish that image, so the chart has deliberately invalid default values and cannot be installed until an operator supplies an immutable `repository@sha256:<64 lowercase hex>` reference. Tags, including `latest`, are rejected by both the value schema and template logic.

This `F9.3a` slice provides only fixed resource envelopes. It does not claim the parent F9.3 end state of a minimal in-chart Postgres for light or an HA hub with external Postgres/cloud KMS for heavy: those need separate E3 custody and topology evidence before they can be rendered safely.

The chart creates no `Secret`, `data`, or `stringData` block. An E3-approved KMS/ExternalSecret materializer must create these existing Secret objects before an installation:

| Value | Required Secret keys | Consumer |
| --- | --- | --- |
| `runtime.existingSecret` | `database-url`, `session-public.pem`, `server-tls.crt`, `server-tls.key`, `proxy-ca.crt`, `proxy-tls.crt`, `proxy-tls.key`; plus `session-private.pem` when browser OIDC is enabled | long-running `sith hub` Deployment |
| `migration.existingSecret` | `owner-database-url` | short-lived `sith hub migrate` hook Job |

`migration.applicationRole` is a non-secret PostgreSQL role name. The migration hook runs before install and upgrade, blocks the release if it fails, and receives no Kubernetes service-account token or runtime TLS material. The Deployment receives an in-cluster token only to read the fixed `sith-reader` managed-serviceaccount Secret; its ClusterRole permits exactly `get` on that one resource name and no list/watch or write verbs.

The same hook creates the forced-RLS approval-grant table used by F5.9a. The application role may
insert an exact digest-bound grant and atomically set only its `consumed_at` column; it cannot
rewrite or delete the workspace, intent, proposer, approver, or resolved digest. This adds one
small indexed row and one scoped transaction per approval, with no queue, polling loop, Secret,
Service, or cloud dependency.

The chart permits exactly two fixed profiles for both the hub and its migration hook:

| Profile | Requests | Limits | Intended envelope |
| --- | --- | --- | --- |
| `light` | 100m CPU, 128Mi memory | 500m CPU, 512Mi memory | development and lab scheduling envelope |
| `heavy` | 500m CPU, 512Mi memory | 2 CPU, 2Gi memory | larger production-like scheduling envelope |

The heavy profile reserves five times the requested CPU and four times the requested memory, so it carries a correspondingly higher node-pool cost. These are fixed scheduling bounds, not measured capacity claims; no arbitrary resource override or third profile is accepted. Both profiles use the same immutable image requirement, existing Secret references, migration isolation, RBAC, probes, and pod/container hardening. They do not change replica count, database custody, or KMS materialization, so `heavy` does not claim unproven high availability.

Both profiles use HTTPS application probes on the existing `https` container port. `/healthz`
checks only process responsiveness. `/readyz` pings the existing least-privilege application
PostgreSQL pool under the Hub's one-second deadline. Probe responses are empty and reveal no
dependency or error details. A PostgreSQL outage therefore removes a Pod from Service endpoints
without making liveness fail and causing a restart storm. The probes add no plaintext listener,
Service port, credential, or OCM/spoke dependency check.

The chart pins workload hardening (UID/GID 65532, read-only root filesystem, RuntimeDefault seccomp, no privilege escalation, and all Linux capabilities dropped). It deliberately does not create a broad egress NetworkPolicy: the database and pinned OCM endpoints are deployment-specific, so operators must place the release in a namespace with an appropriate least-privilege egress policy. KMS provider resources, release-bound image publication, real install/upgrade proof, air-gap bundles, and addon packaging are later E9/E3 slices.

`runtime.browserOIDC` is disabled only when all three of `issuer`, `clientID`, and `redirectURI` are empty. Setting any one enables a fail-closed validation requiring all three. The configured client ID is the exact ID-token audience; the callback must be the same HTTPS URL registered at the issuer and supplied to the Hub. In that mode the Hub mounts `session-private.pem`, checks that it matches `session-public.pem`, completes authorization-code + PKCE (`S256`) server-side, and returns a short-lived `__Host-` `Secure`, `HttpOnly`, strict-same-site session cookie. The chart never renders any of this secret material. The Hub needs narrowly allowlisted egress only to the configured issuer's discovery, JWKS, and token endpoints; the operator's browser navigates to the issuer authorization endpoint.

`runtime.metrics.listenAddress` is opt-in and must be exactly `127.0.0.1:<port>` or
`[::1]:<port>`, with a non-zero port. When set, the chart provides only the corresponding
container-port metadata and `SITH_HUB_METRICS_LISTEN_ADDR`; it creates no Service port, ingress,
scrape annotation, exporter, or automatic sidecar. The Hub serves only `GET /metrics` there from
its isolated low-cardinality registry. A same-Pod collector can reach it over `localhost` because
containers in a Pod share a network namespace; that is deliberately a local operational trade-off,
not a public or tenant-visible metrics endpoint. See the [Kubernetes Pod networking
documentation](https://kubernetes.io/docs/concepts/workloads/pods/#how-pods-manage-multiple-containers).
The registry includes fixed `sith_hub_readiness_checks_total{outcome}` and
`sith_hub_readiness_check_duration_seconds{outcome}` families for completed database-aware
readiness checks, where `outcome` is only `ready` or `unavailable`. These process-local series carry
no tenant, spoke, request, endpoint, credential, or raw-error label.
The portable repository rules include an install-neutral warning when more than five percent of at
least twenty aggregate checks are `unavailable` over fifteen minutes for ten minutes. The chart does
not install or configure the scraper, rule evaluator, Alertmanager, or receiver; see
[`docs/runbooks/hub-alerts.md`](../../docs/runbooks/hub-alerts.md).

The Hub also starts a restricted same-container child for the one closed authentication-refusal
record. Sith supplies only an inherited bounded Unix datagram descriptor and stderr: no
configuration, environment, listener, socket pathname, network endpoint, or Secret value. The
child still shares the container's mounts, UID, and network namespace, so it is a bounded delivery
and shutdown boundary, not a filesystem or network sandbox. A full or dead local transport drops
the fixed record without delaying the governed response and increments the unlabeled
`sith_auth_refusal_delivery_drops_total` counter. Enabling `runtime.metrics` is the only way to
scrape that process-wide loss signal; the chart still renders no related Service port, ingress,
sidecar, queue, exporter, or remote telemetry path.

Validate supplied values before applying anything:

```bash
helm lint charts/sith-hub -f operator-values.yaml
helm template sith-hub charts/sith-hub --namespace sith-system -f operator-values.yaml
```
