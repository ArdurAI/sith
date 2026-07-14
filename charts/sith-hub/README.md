# Sith hub Helm chart

This chart is a fail-closed deployment contract for a released Sith hub image. The repository does not currently publish that image, so the chart has deliberately invalid default values and cannot be installed until an operator supplies an immutable `repository@sha256:<64 lowercase hex>` reference. Tags, including `latest`, are rejected by both the value schema and template logic.

The chart creates no `Secret`, `data`, or `stringData` block. An E3-approved KMS/ExternalSecret materializer must create these existing Secret objects before an installation:

| Value | Required Secret keys | Consumer |
| --- | --- | --- |
| `runtime.existingSecret` | `database-url`, `session-public.pem`, `server-tls.crt`, `server-tls.key`, `proxy-ca.crt`, `proxy-tls.crt`, `proxy-tls.key` | long-running `sith hub` Deployment |
| `migration.existingSecret` | `owner-database-url` | short-lived `sith hub migrate` hook Job |

`migration.applicationRole` is a non-secret PostgreSQL role name. The migration hook runs before install and upgrade, blocks the release if it fails, and receives no Kubernetes service-account token or runtime TLS material. The Deployment receives an in-cluster token only to read the fixed `sith-reader` managed-serviceaccount Secret; its ClusterRole permits exactly `get` on that one resource name and no list/watch or write verbs.

The chart pins workload hardening (UID/GID 65532, read-only root filesystem, RuntimeDefault seccomp, no privilege escalation, and all Linux capabilities dropped). It deliberately does not create a broad egress NetworkPolicy: the database and pinned OCM endpoints are deployment-specific, so operators must place the release in a namespace with an appropriate least-privilege egress policy. Deployment profiles, KMS provider resources, release-bound image publication, real install/upgrade proof, air-gap bundles, and addon packaging are later E9/E3 slices.

Validate supplied values before applying anything:

```bash
helm lint charts/sith-hub -f operator-values.yaml
helm template sith-hub charts/sith-hub --namespace sith-system -f operator-values.yaml
```
