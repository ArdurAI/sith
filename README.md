# Sith

**Status: Phase-L client plus E1 hub-authentication foundations and release supply-chain gate.** The CLI, TUI, browser IDE, optional MCP server,
deterministic advisory brain, and reproducible multi-platform release pipeline
discover every context resolved by client-go, hydrate one local in-memory fleet cache through
per-context watches, serve coverage-honest fleet search/correlation, and provide explicit-context
logs, exec, port-forward, describe, and YAML view/edit. Local mode requires no account, emits no
telemetry, and can expose workspace-scoped, audited fleet reads to local agents without giving them
cluster credentials. The brain explains supported degraded signals with cited rules and suggested
human-run commands while naming every missing evidence lens.

Sith is ArdurAI's single-binary, local-first Kubernetes fleet tool: **k9s for your whole fleet**.
It is designed to aggregate every kubeconfig context without an account, telemetry, or cluster
data leaving the machine. The same source-abstract fleet model will later power an optional
governed hub.

## Install

On macOS or Linux with Homebrew:

```bash
brew tap ArdurAI/tap
brew trust --formula ArdurAI/tap/sith
brew install sith
sith version
```

Homebrew 6 requires explicit trust for third-party taps. The formula-scoped command keeps the trust
boundary narrower than trusting every current and future formula in `ArdurAI/tap`. Older Homebrew
versions that do not implement tap trust can omit that line.

Release archives are also available for `darwin/amd64`, `darwin/arm64`, `linux/amd64`, and
`linux/arm64`. Every archive has a checksum, an SPDX SBOM, a keyless Sigstore bundle, SLSA build
provenance, and a platform-specific SBOM attestation. Verify those materials before installing;
the exact online and offline commands are in [`docs/RELEASE.md`](docs/RELEASE.md).

## Build and run

Sith requires a supported Go 1.26 toolchain.

```bash
make build
./bin/sith                       # interactive terminal: cache-first fleet view
./bin/sith tui                   # explicit equivalent
./bin/sith version
./bin/sith version --output json
./bin/sith clusters
./bin/sith get pods -A --all-clusters
./bin/sith search 'image:*log4j*'
./bin/sith correlate 'deploy/payments status!=Healthy'
./bin/sith investigate             # rank supported degraded signals across every context
./bin/sith investigate payments --context kind-dev --output json
./bin/sith audit verify ./sith-policy-audit.json # local integrity check; no hub or credentials
./bin/sith audit verify-pages ./audit-page-0001.json ./audit-page-0002.json # ordered snapshot
./bin/sith describe pod/api --context kind-dev -n apps
./bin/sith yaml secret/api-token --context kind-dev -n apps
./bin/sith logs api --context kind-dev -n apps --tail 100 -f
./bin/sith exec api --context kind-dev -n apps -it -- /bin/sh
./bin/sith port-forward service/api --context kind-dev -n apps :http
./bin/sith edit configmap/api-settings --context kind-dev -n apps
./bin/sith ui                    # loopback-only embedded fleet IDE
./bin/sith ui --kubeconfig-dir "$HOME/kubeconfigs" # import a folder of kubeconfig files for this UI session
./bin/sith desktop               # native macOS window for the same local fleet IDE
./bin/sith serve --mcp           # loopback-only MCP read server
./bin/sith serve --mcp --require-token
```

`sith clusters` follows standard client-go loading rules: set `KUBECONFIG` to an OS path-list or
use the default `~/.kube/config`. Exec-credential helpers run locally, exactly as they do for
`kubectl`; Sith accepts the client-go `v1` and `v1beta1` ExecCredential contracts independently
for every context. One broken helper only marks its context unreachable. Helper tokens and client
keys remain in the client-go transport's process memory and never enter Sith's config, fleet cache,
or filesystem.

Sith-owned persisted secrets use the host credential store under the fixed `io.ardur.sith`
service: macOS Keychain, Windows Credential Manager, or freedesktop Secret Service. If that store
is unavailable, the operation fails; there is no silent plaintext or encrypted-file fallback.
The optional local MCP capability is the first consumer: it uses a unique short-lived keychain
entry and deletes it on clean server shutdown.
The dependency can invoke the fixed macOS `/usr/bin/security` tool or the Linux session D-Bus only
during an explicit keychain operation; it creates no account, hosted service, or cloud cost.

The governed-hub foundation supports one deliberately narrow API-key exchange contract. An
administrator issues a high-entropy `sith_api_v1` key for an existing workspace member; only its
HMAC-SHA-256 verifier—not the plaintext secret—is stored with its subject and lifecycle metadata,
behind forced PostgreSQL RLS. The plaintext is returned once. Machine callers present that
credential only as `Authorization: SithKey ...` to the
exchange handler. Sith resolves the subject's current membership server-side and returns a
15-minute, type-pinned Ed25519 session for normal `Authorization: Bearer ...` requests. A raw API
key is never a role-bearing bearer token.

API keys expire, support an administrator-bounded rotation overlap of at most 24 hours, and can be
revoked immediately. Exchange responses, including generic failures, are non-cacheable. The
handler includes a bounded per-process attempt limiter; a replicated hub must additionally enforce
a shared limit at its ingress or gateway. Deployments must provide the HMAC pepper and Ed25519
private key through a secret manager, keep both out of logs and configuration repositories, and
rotate them under an explicit operational procedure. These are E1 library and HTTP boundaries.
The P1 `sith hub` runtime mounts the session-authenticated fleet read/refresh surface below.
API-key, raw OIDC-token, and cloud-proof exchange handlers remain intentionally unmounted until
their ingress and operator lifecycle are composed. The separately configured browser OIDC flow is
not a raw-token exchange: it completes authorization-code + PKCE server-side and only establishes
an HttpOnly browser session for the Hub console boundary.

Pinned OIDC federation uses the same exchange model. Each endpoint is fixed to one requested
workspace, and each provider configuration allowlists an exact HTTPS issuer, audience, token type,
asymmetric algorithm set, upstream lifetime, cache TTL, and key-count bound. Discovery metadata
must repeat the configured issuer exactly; JWKS stays on the same origin and is fetched through a
TLS 1.2+ transport that disables proxies, rejects private and special-use network targets, blocks
off-origin redirects, caps response size, and binds each connection to a validated DNS result.
Duplicate JSON claims and token-controlled `jku`, `x5u`, embedded-key, certificate-chain, critical,
nested, and compressed JOSE headers fail closed.

The verified upstream issuer and subject select a server-side binding under the requested
workspace's forced RLS policy. Upstream workspace and role claims are ignored. Sith reads the
member's current role and issues the same 15-minute Ed25519 session used by API-key exchange.
Unknown keys trigger one bounded JWKS refresh that atomically replaces the cache, enabling key
rollover without retaining retired keys; an expired cache is never used through an issuer outage.
This follows [OpenID Connect Discovery 1.0](https://openid.net/specs/openid-connect-discovery-1_0.html),
[RFC 7517](https://www.rfc-editor.org/rfc/rfc7517.html), and the
[JWT BCP, RFC 8725](https://www.rfc-editor.org/rfc/rfc8725.html). Discovery and refresh add small
outbound request and availability dependencies but create no cloud resources by themselves.

When all browser-OIDC deployment inputs are configured, the Hub additionally exposes a fixed
`GET /v1/workspaces/{workspace}/console/login` start route and the exact configured callback path.
It uses one issuer-pinned public client with Authorization Code + PKCE `S256`; the verifier, code,
upstream ID token, and minted Sith JWT remain server-side. A bounded, single-use, process-local
transaction binds the exact workspace, state, nonce, redirect URI, issuer, client ID, and verifier.
The callback consumes that transaction before redeeming the code, resolves the current membership
through forced RLS, and returns only a short-lived `__Host-sith-session` cookie (`Secure`,
`HttpOnly`, `Path=/`, no `Domain`, `SameSite=Lax`). Lax allows that new session to reach the safe
top-level console `GET` whose navigation began at the external IdP, while excluding unsafe
cross-site methods. Its short-lived `__Host-sith-oidc-tx` transaction cookie is also `SameSite=Lax`
so the top-level IdP callback can return; it contains an opaque random binding, not a credential.
Restart, expiry, replay, malformed/duplicate provider
JSON, failed PKCE, wrong state/nonce/issuer/audience, or an unavailable provider fails closed. No
login artifact, token, or proof is persisted or logged. On success, the callback redirects only to
the transaction-bound `GET /v1/workspaces/{workspace}/console` path; it accepts no return URL.

That Hub-only console verifies the HttpOnly session server-side, resolves the workspace from signed
membership, and reads the existing persisted `hubfleet.Source` through the PEP. Its fixed
`GET /v1/workspaces/{workspace}/console/fleet` adapter requires a short-lived process-key-signed
CSRF token bound to the exact session, workspace, and expiry in a custom same-origin header. The
separate `GET /v1/workspaces/{workspace}/console/correlate` adapter accepts only one canonical exact
non-Secret resource identity and the fixed `health_not=Healthy` condition. It calls the existing
tenant-scoped `hubfleet.Correlator` once with a 257-row sentinel bound, then emits at most 256
minimal health matches; an over-bound or malformed stored result fails closed instead of appearing
complete. The correlation proof has a distinct HMAC purpose and cannot be replaced by the fleet
proof. Its response omits raw observations, attributes, workspace fields, provenance, native IDs,
deep links, and source payloads.

The separate `GET /v1/workspaces/{workspace}/console/inventory` adapter accepts one closed OCM
inventory kind (`Deployment`, `Pod`, or `Rollout`) plus optional exact namespace and name. It uses
the dedicated `fleet.inventory.search` PEP verb and its own non-interchangeable session/workspace
proof, performs one 257-row-sentinel persisted read, and emits at most 256 strictly decoded records.
The browser receives only resource identity, observation freshness, generation, and typed
availability/readiness counts. Pod image digests, raw observations, display hints, source payloads,
workspace fields, attributes, and provenance remain behind the evidence boundary; malformed or
over-bound stored results fail closed.

The separate `GET /v1/workspaces/{workspace}/console/cves` adapter accepts one canonical uppercase
CVE identifier and reuses the existing tenant-scoped `hubfleet.CVESearcher` with its dedicated
`fleet.cve.identifier.search` PEP verb. Its fourth non-interchangeable session/workspace proof and
same-origin Fetch Metadata gate precede one 257-row-sentinel persisted read. The browser receives at
most 256 records containing only the cluster scope, immutable image digest, exact identifier,
canonical severity, observation time, and derived staleness. Raw observations, the complete
identifier list, display hints, source payloads, workspace fields, attributes, and provenance stay
behind the evidence boundary; mutable image references, selector mismatches, malformed or duplicate
stored facts, and over-bound results fail closed.

The session JWT never enters HTML, JavaScript, a URL, browser storage, or a log. All console
responses are `no-store` and use a restrictive same-origin CSP; all data adapters reject
cross-site Fetch Metadata. The UI shows reachability, observation times, non-Healthy matches,
normalized inventory, and runtime CVE evidence beside stale/unreachable/truncated/unaccounted
coverage without claiming an empty or partial answer is healthy, complete, or CVE-free.
Correlation, inventory, and CVE lookup run only after explicit submit. The console performs no automatic
polling and cannot invoke collector refresh, a connector, local `exec`/edit/log/port-forward
operations, or any write. The bearer fleet API remains bearer-only, and no generic cookie
authentication middleware exists. Query identities are ordinary resource metadata, not secrets;
they may appear in access logs. Each submit adds one bounded Hub database read and no cloud,
scanner, or spoke egress.

Cloud-IAM identity starts from the same fail-closed exchange boundary. The foundation accepts only
a verifier-normalized provider, explicit realm, immutable subject, audience, and bounded lifetime;
it maps that identity through a current forced-RLS membership binding and consumes only an HMAC
proof digest until expiry. AWS now accepts only a base64url-encoded, pre-signed SigV4
`GetCallerIdentity` URL for one configured regional STS endpoint (commercial, GovCloud, or China),
with a 60-second-or-shorter `X-Amz-Expires` and an exact `x-sith-audience` signed header. Sith
reconstructs a header-minimal GET only to that endpoint, disables redirects, accepts only a
short-lived assumed-role response, and binds the STS account plus immutable role ID through RLS.
It never stores or logs an AWS access key, session token, signature, or raw proof. No provider or
endpoint fallback is accepted.

Azure Entra workload federation accepts only tenant-specific v2.0 `JWT` access tokens from one
configured Microsoft public, US Government, or China authority. The verifier pins the derived
tenant issuer, audience, RS256 key policy, JWKS origin, expiry, `tid`, immutable workload `oid`,
and app-only `idtyp=app`; an optional configured `azp` further pins the actor. Tenant-independent
`common`/`organizations`, delegated identities, token-controlled authority selection, upstream
roles/groups/scopes, and cloud fallback are rejected or ignored. The resulting tenant+object
identity still needs the same server-side RLS binding and one-time replay consumption before Sith
issues a session.

Google service-account federation accepts only Google-signed public-cloud ID tokens with the exact
`https://accounts.google.com` issuer and `https://www.googleapis.com/oauth2/v3/certs` JWKS endpoint.
It requires RS256, one exact audience, a verified `*.gserviceaccount.com` email, matching immutable
numeric `sub`/`azp`, a one-hour-or-shorter lifetime, and an explicitly configured Google
organization number carried in the `google.organization_number` claim. Operators must mint with the
IAM Credentials `organizationNumberIncluded` option; Sith does not infer a project or realm from an
email address. Self-signed service-account JWT assertions, user tokens, unbound organizations,
alternate/restricted/sovereign/private endpoints, upstream authorization claims, and raw-token
persistence are rejected. The resulting organization+service-account ID still resolves only through
the server-side RLS binding and replay guard before a Sith session is issued.

This AWS contract uses [STS GetCallerIdentity](https://docs.aws.amazon.com/STS/latest/APIReference/API_GetCallerIdentity.html)
and regional STS endpoint guidance: the global STS endpoint is deliberately rejected because Sith
requires a configured regional authority and bounded proof lifetime.

The Google contract follows the [service-account ID-token profile](https://cloud.google.com/docs/authentication/token-types)
and [IAM Credentials `generateIdToken` contract](https://cloud.google.com/iam/docs/reference/credentials/rest/v1/projects.serviceAccounts/generateIdToken):
service-account ID tokens are Google-JWKS signed, while client-created service-account assertions are
not accepted as Sith proofs.

The Phase-1 read-federation foundation persists a tenant-scoped, bounded snapshot from each
registered OCM spoke through the same normalized fleet model used locally. A transport receives
only the workspace boundary and registered managed-cluster reference; it never receives a raw
kubeconfig, endpoint, or token through the Sith collector contract. Only normalized `inventory`,
`health`, and bounded immutable-image `cve` facts are accepted, source-stamped,
freshness-bounded, and stored behind forced RLS.
Failed refreshes retain the last snapshot as explicitly stale evidence and record only a closed
failure category. Concurrent refresh requests are authorized independently and then coalesced only
within the same validated workspace. The shared refresh runs on a locally traced context detached
from individual callers but bounded by the hub lifecycle, so one canceled caller cannot cancel its
peers, shutdown cancels outstanding work, and no request credential, context value, or trace identity
crosses caller boundaries; completed and panicking refresh flights are removed. Within one refresh,
transport and validation use a bounded worker pool: `CollectorConfig` defaults to four
concurrent spokes and accepts only explicit limits from 1 through 64. Persistence and coverage
mutation remain serialized, so one store failure cancels the remaining workers before returning;
per-spoke deadlines and sorted stale/unreachable coverage remain unchanged. The pinned
direct OCM ClusterProxy adapter reads the exact rotating
`sith-reader` managed-serviceaccount Secret for a registered spoke, opens a short-lived
Konnectivity tunnel only to that spoke, and verifies both proxy mTLS and the spoke Kubernetes
certificate; it never forwards a caller `Authorization` header, stores a credential, disables
TLS verification, lists or watches Secrets, or carries raw Kubernetes objects across the
collector seam. Its fixed read surface is Pods, Deployments, Rollouts, and optional
`aquasecurity.github.io/v1alpha1` `VulnerabilityReport` resources; it never discovers arbitrary
CRDs. Its executable two-spoke M0 gate is `make e2e-ocm`, which now also drives a
signed-session request through the TLS hub runtime across both spokes. The same model now answers
a read-only, exact cross-cluster correlation such as “every deployment
named `payments` that is not Healthy” within one workspace. Matching is by exact kind/name/namespace
rather than a prefix. Every returned cluster and matching fact retains its source identity and
observation time, and every answer retains full stale/unreachable coverage rather than claiming
that a partial fleet is complete.

### Governed hub runtime (P1)

`sith hub` is an in-cluster, TLS-only process. It has no listener default and exits non-zero before
opening a listener, database pool, or Kubernetes client unless all of these deployment inputs are
present and valid:

- `SITH_HUB_LISTEN_ADDR` and `SITH_HUB_DATABASE_URL` (the database must use the existing
  non-owner, forced-RLS application role and TLS);
- `SITH_HUB_SESSION_ISSUER`, `SITH_HUB_SESSION_AUDIENCE`, `SITH_HUB_SESSION_KEY_ID`, and
  `SITH_HUB_SESSION_PUBLIC_KEY_FILE` (a static Ed25519 PKIX public key; no remote discovery);
- `SITH_HUB_SERVER_TLS_CERT_FILE` and `SITH_HUB_SERVER_TLS_KEY_FILE` for the hub HTTPS listener;
- `SITH_HUB_PROXY_ADDRESS`, `SITH_HUB_PROXY_SERVER_NAME`, `SITH_HUB_PROXY_CA_FILE`,
  `SITH_HUB_PROXY_CERT_FILE`, `SITH_HUB_PROXY_KEY_FILE`, and `SITH_HUB_KUBE_API_SERVER_NAME` for
  the direct ClusterProxy mTLS path.

Browser OIDC is disabled only when all four of these optional inputs are absent. Setting any one
requires all four before startup: `SITH_HUB_BROWSER_OIDC_ISSUER`,
`SITH_HUB_BROWSER_OIDC_CLIENT_ID`, `SITH_HUB_BROWSER_OIDC_REDIRECT_URI`, and
`SITH_HUB_SESSION_PRIVATE_KEY_FILE`. The client ID is the exact accepted ID-token audience; the
redirect URI must be the exact HTTPS callback registered with the issuer. The private PKCS#8
Ed25519 key must be a read-only deployment mount and match the configured public key. This uses a
public OIDC client with PKCE, never a frontend client secret or refresh token. The pinned provider
must be reachable through the existing no-proxy, TLS 1.2+, public-network-only discovery/JWKS
transport; deployments must allow only that issuer's required endpoints.

The existing TLS listener also serves two fixed, unauthenticated, body-free Kubernetes probe
routes. `GET /healthz` is dependency-free process liveness. `GET /readyz` performs one
application-pool PostgreSQL ping under a one-second server deadline; failure returns only an empty
`503`, never a database error or endpoint. Every other method, query, encoded variant, or path is
rejected. PostgreSQL deliberately affects readiness rather than liveness so an outage removes the
Pod from service without creating a dependency-driven restart storm. OCM/spoke reachability does
not participate because partial fleet coverage must remain visible rather than making the whole
Hub unready.

Each valid completed `GET /readyz` database check also records one
`sith_hub_readiness_checks_total{outcome}` attempt and one
`sith_hub_readiness_check_duration_seconds{outcome}` observation in the existing isolated metrics
registry. `outcome` is only `ready` or `unavailable`; database errors, deadline expiry, caller
cancellation, and recovered checker panic collapse to `unavailable`. Rejected methods, queries,
paths, and encoded variants emit no observation. Invalid metric values are discarded, observer
panics cannot change probe behavior, and no request, endpoint, credential, tenant, spoke, error, or
panic detail becomes a label. These series are visible only through the opt-in loopback metrics
listener below and add no listener, Service, exporter, persistence, or remote telemetry path.

The optional `SITH_HUB_METRICS_LISTEN_ADDR` is disabled unless it is exactly
`127.0.0.1:<non-zero-port>` or `[::1]:<non-zero-port>`. When configured, it exposes only a
separate plaintext `GET /metrics` listener backed by Sith's isolated, bounded-label registry. It
is not a tenant route, Service port, ingress, exporter, or telemetry backend, and cannot bind to a
hostname, wildcard, or cluster-routable address. A same-Pod collector may scrape it over
`localhost`; that preserves process-wide operational visibility without disclosing counters to a
workspace principal. This trade-off intentionally requires the operator to supply their own
collector and does not provide cross-Pod or remote scraping.

Every Hub starts one restricted local child for the already-sanitized authentication-refusal
event. The request path writes only a two-byte closed record to a bounded Unix datagram socket;
when the socket is full or the child has died, the event is dropped immediately and increments the
unlabeled `sith_auth_refusal_delivery_drops_total` self-observation counter. Sith supplies the
child only that descriptor and stderr; it validates each fixed record and writes the single
structured warning there. It shares the container's mounts, UID, and network namespace, so this
is a bounded delivery and shutdown boundary rather than a filesystem or network sandbox. A blocked
stderr can therefore block only the child, which the Hub kills and reaps on shutdown. This adds no
listener, Service, exporter, queue, persistence, remote telemetry, request metadata, credential
data, or raw payload retention. The drop counter is scrapeable only when the same optional loopback
metrics endpoint above is enabled.

The same already-sanitized bearer and browser-session boundaries increment exactly two
preinitialized `sith_auth_attempts_total{outcome="accepted|refused"}` series. `accepted` means the
local verifier succeeded and is emitted before workspace authorization; `refused` covers the
existing uniform authentication rejection paths. Every refusal also increments the legacy
unlabeled `sith_auth_refusals_total` counter exactly once. Runtime fanout is panic-isolated per
destination: metrics consume both outcomes, while the process audit child and structured-log
adapter remain refusal-only. No credential mode, failure reason, tenant, workspace, actor,
principal, token, IP, path, method, request, trace, or correlation value becomes a label or record.
These counters do not cover OIDC provider exchange/callback failures, authorization denials,
handler outcomes, or every future authentication mode. The portable package below consumes them
only for one aggregate refusal-only warning; they do not provide a generic refusal ratio,
brute-force detector, actor attribution, SLO, error budget, page, or complete security control, and
they add no new scrape or storage path.

Every referenced key, certificate, or CA file must be a read-only regular file from a deployment
mount. The runtime obtains its Kubernetes identity only with in-cluster configuration; it has no
kubeconfig fallback and uses that identity through the fixed `sith-reader` Secret reader. It serves
only `POST /v1/workspaces/{workspace}/fleet:refresh`,
`GET /v1/workspaces/{workspace}/fleet`, and
`GET /v1/workspaces/{workspace}/fleet/images/{sha256:<64-lowercase-hex>}`, and
`GET /v1/workspaces/{workspace}/fleet/images/{sha256:<64-lowercase-hex>}/cves`, and
`GET /v1/workspaces/{workspace}/fleet/cves/{CVE-YYYY-N...}`. The separate
`GET /v1/workspaces/{workspace}/audit/export` route returns the existing retained audit chain only
to a signed workspace admin. The distinct
`GET /v1/workspaces/{workspace}/audit/export/pages` route returns the first bounded page of a
larger immutable snapshot; a continuation accepts only the exact
`?cursor=<canonical-base64url>` query. The complete export and fleet routes reject every query, and
the page route rejects every other key, duplicate, escaping, or alternate encoding. All bearer
routes require an exact signed Sith session, derive workspace scope from signed memberships, and
carry it through the PEP and RLS seams. Fleet routes return only normalized coverage/fleet data
under `Cache-Control: no-store`; both audit routes return versioned JSON attachments under the same
bearer-only, tenant-scoped cache boundary.

After deriving the signed scope, the hub mints one opaque local trace ID for the governed request.
It strips common caller-supplied trace and correlation carriers, never echoes or forwards them,
and carries the local ID through the PEP audit record and each snapshot transport attempt. The hub
logs only local trace ID, fixed stage, fixed outcome, and bounded duration; it records no workspace,
actor, spoke, endpoint, resource, selector, argument digest, credential, raw error, or returned
data in trace events. This is not a telemetry exporter: it adds no OpenTelemetry SDK, listener,
network egress, queue, trace store, persistence, or action-intent protocol.

The PEP decision record itself is durable. Before an allowed governed operation can proceed, the
hub appends the privacy-minimized decision to a workspace-scoped PostgreSQL hash chain, then emits
the structured process log. The application role can insert and read immutable audit entries but
cannot update or delete them; forced RLS isolates both entries and their per-workspace chain heads.
Approval creation and consumption append distinct format-versioned lifecycle entries to that same
tenant chain in the exact transaction that mutates the single-use grant. An audit failure therefore
rolls back the approval mutation. Each lifecycle pair carries only a one-way, domain-separated
digest of the immutable grant binding—never raw targets, arguments, or justification content.
New grants use format 3: PostgreSQL mints one immutable absolute expiry exactly 10 minutes after
approval, checks `approved_at <= statement_timestamp() < expires_at` in the same conditional
consumption update, and binds both timestamps into the lifecycle evidence digest. An expired,
legacy, missing, foreign, mismatched, or replayed grant returns the same unavailable result; an
expired refusal retains the row, leaves `consumed_at` unset, and appends no success event.
Both audit routes use the dedicated `export-audit` action and `audit.export` PEP verb. Sith durably
appends an authorization decision before every read. The complete route verifies the head and all
retained history in one forced-RLS Repeatable Read snapshot and remains limited to 512 entries. For
larger chains, the page route fixes the first request's current head and returns at most 512
consecutive entries. Every continuation is independently authenticated, authorized, and audited;
those later authorization rows remain in the live chain but cannot move the established snapshot
and appear in a future export.

The continuation is a fixed-size, versioned, canonical base64url descriptor bound to a
domain-separated workspace digest, snapshot head sequence/hash, next sequence, and expected prior
hash. It is not a credential. The database rechecks the live and snapshot head anchors plus the
page boundary under forced RLS; a changed, foreign, skipped, or reordered continuation fails with
the same unavailable response. Each transaction commits before JSON serialization, so slow clients
cannot pin a database snapshot and late validation cannot produce a partial successful page. The
process admits at most four complete or paged exports concurrently. Each document contains only
the existing privacy-minimized policy and approval events plus their SHA-256 links.

`sith audit verify <export.json>` provides the corresponding offline integrity check. It accepts
one non-symlink regular file of at most 1 MiB, rejects duplicate, unknown, case-mismatched, or
trailing JSON, and recomputes every versioned entry hash, sequence link, and declared head. The
command performs no hub request, database access, credential lookup, temporary-file creation, or
telemetry and emits only a bounded summary. A successful result means the document is internally
consistent; without an external anchor it does not prove origin or detect wholesale replacement by
a privileged store owner. Verification costs one bounded local read and at most 512 SHA-256
computations, with no cloud, storage, egress, or recurring-service cost.
`sith audit verify-pages <page.json> [page.json...]` performs the same strict bounded-file and
canonical-hash checks one page at a time, then requires one ordered same-workspace,
same-snapshot genesis-to-head sequence with no missing, replayed, or swapped page. Success is still
internal consistency, not external authenticity. Paging adds one small audit row, at most 512
entry reads plus fixed anchors, and one response of egress per page; work is linear in retained
entries and page count, with no object store, queue, worker, or recurring cloud resource.

Responses use `X-Content-Type-Options: nosniff` and the fixed `sith-policy-audit.json` or
`sith-policy-audit-page.json` attachment filename. Authentication is bearer-only; neither route
uses browser cookies for authority, and the page route rejects every `Cookie` header. Filters,
raw-payload selection, asynchronous export, WORM retention, external anchoring, the future
intent-correlated Ardur decision ledger, and an E6-complete claim remain out of scope.
Either database or structured-process-log delivery failure blocks the operation, so production
database and logging availability and latency are part of the governed-read availability budget.
The optional loopback metrics surface exposes that boundary as
`sith_policy_audit_attempts_total{sink,outcome}` and
`sith_policy_audit_duration_seconds{sink,outcome}`. `sink` is only `durable` or `process`, and
`outcome` is only `success` or `error`; invalid observations are discarded, and no tenant, actor,
intent, trace, policy argument, or raw error becomes a label. A durable error intentionally has no
matching process attempt because the database append must succeed first. A process error therefore
appears after a durable success. Operators can build failure-rate and latency signals from
`rate(sith_policy_audit_attempts_total{outcome="error"}[5m])` and
`histogram_quantile(0.95, sum by (le, sink)
(rate(sith_policy_audit_duration_seconds_bucket[5m])))`. These fixed series add no listener,
exporter, persistence, or tenant-proportional cardinality beyond the existing opt-in metrics path.
Each authorized persisted fleet read also increments exactly one
`sith_federation_fleet_read_results_total{outcome}` series. `outcome` is the closed vocabulary
`complete`, `degraded`, `empty`, or `error`: internally inconsistent or incomplete coverage and a
result/coverage count mismatch are always `degraded`, and only a consistent zero-scope result is
`empty`. The observer runs after PEP
authorization, carries no workspace, spoke, resource, selector, principal, trace, age, or raw-error
label, and is panic-isolated from read behavior. This fixed four-series counter is an F10.1d
coverage-SLI substrate; it is not a read-freshness objective or error-budget policy.
The same authorized read also increments exactly one
`sith_federation_fleet_read_freshness_total{outcome}` series. The five closed outcomes are
`fresh`, `stale`, `unknown`, `empty`, and `error`. `fresh` requires a non-empty, internally
consistent, complete result where every returned cluster has a unique identity and non-zero
observation time. A structurally valid result with a proven stale retained scope is `stale`; unseen,
inconsistent, mismatched, or otherwise non-stale degraded coverage is `unknown`. Only a consistent zero-scope
result is `empty`, and a storage failure before a result exists is `error`. Coverage and freshness
are emitted as one validated pair, so an invalid dimension discards both observations rather than
fabricating a partial result. The fixed series carry no tenant-proportional labels and add no
listener, storage, exporter, background task, or network path. They are request-time SLI substrate:
a workspace with no reads emits no events, and this is not continuous snapshot-age monitoring, a
per-spoke series, an alert, SLO, or error budget. This follows Prometheus guidance to keep label
cardinality bounded and use counters for completed online-serving requests; see the
[instrumentation](https://prometheus.io/docs/practices/instrumentation/) and
[metric naming](https://prometheus.io/docs/practices/naming/) guidance.
The portable [hub alert rules](monitoring/sith-hub.rules.yml) turn the established policy-decision,
audit, auth-log delivery, aggregate snapshot failure, eligible fleet-read coverage, proven
fleet-read staleness, database-readiness, bounded authentication outcomes, and traffic-independent
build-info presence signals into nine bounded, fixture-tested alerts; the
[runbook](docs/runbooks/hub-alerts.md) documents installation and response. Load the rule file only
after arranging an operator-owned same-Pod scrape/forwarding path. Sith does not render a Service,
ServiceMonitor, PrometheusRule, Alertmanager receiver, exporter, or remote-write configuration.
These rules are an F10.4a/F10.4b/F10.4c/F10.4d/F10.4e/F10.4f/F10.4g baseline, not read-freshness,
dispatch-success, or PDP-latency SLOs or error budgets. Sustained `degraded|error` outcomes among
eligible `complete|degraded|error` fleet reads now produce one aggregate warning, but `complete`
remains a coverage-contract outcome rather
than a snapshot-age guarantee. Separately, sustained `stale` outcomes among at least twenty
freshness-eligible `fresh|stale` reads produce one aggregate warning; `unknown`, `error`, and `empty`
are excluded because none proves snapshot age. More than five percent `unavailable` among at least
twenty completed database-readiness checks over fifteen minutes produces one aggregate warning only
after a ten-minute hold; it is a control-plane dependency symptom, not a paging objective. Formal targets and budgets
require a separately reviewed F10.4 follow-up. If no `sith_build_info` sample reaches the evaluator
for ten minutes and the absence persists for five more, one aggregate warning reports loss of the
expected Hub telemetry path. Load the portable package only where that path is intentionally
installed. The warning cannot detect failure of its own evaluator, Alertmanager, or receiver, so an
external synthetic remains required for end-to-end metamonitoring.
More than five percent `error` among at least twenty eligible
`allow|deny|require-approval|error` decisions over fifteen minutes produces one aggregate warning
after a ten-minute hold. `deny` and `require-approval` remain valid decisions in its denominator,
not failures. This is a fail-closed PEP symptom, not an external Ardur PDP-latency SLI or SLO.
At least twenty aggregate `refused` authentication attempts with zero `accepted` attempts over
fifteen minutes, sustained for ten minutes, produce one aggregate warning. Any accepted attempt in
the same window suppresses it. At least one accepted-outcome sample must also have reached the rule
evaluator during the last ten minutes; a missing or stale accepted series stays quiet rather than
turning partial telemetry into a refusal-only claim. This is not proof of brute force, credential
stuffing, account compromise, a specific actor, or a negotiated authentication SLO.
Chain verification detects retained-row edits,
deletion, reordering, broken links, and head mismatch. It does not make a WORM or non-repudiation
claim: detecting wholesale replacement by a privileged database owner requires a later externally
anchored checkpoint or immutable copy.

Before a signed scope exists, the authentication gate emits one local WARN record for every
refusal with only the fixed `hub-auth` surface and `refused` outcome. It deliberately does not
distinguish credential failure modes or carry a trace/correlation ID, token, header, path, client
address, workspace, principal, or verifier error. Because it precedes any signed scope or action
intent, this signal is outside the later E6 intent-correlated audit ledger contract. The record is a
passive alerting signal, not an audit record, rate limiter, telemetry export, or additional
authentication decision.

The image route answers one exact, immutable runtime digest question across registered spokes. The
direct reader accepts only canonical digests normalized from ordinary
`Pod.Status.ContainerStatuses[].ImageID`; PodSpec image strings, init and ephemeral container
statuses, mutable tags, malformed values, and ambiguous runtime IDs abstain. Sith makes no registry
request, image pull, SBOM retrieval, vulnerability-feed lookup, or credential use for this read.
The result remains coverage-honest: matching Pod inventory facts retain source and freshness, and
unreachable or stale spokes are reported rather than assumed clean.

The CVE route answers the narrower question “which already-reported CVE facts match this exact
runtime-proven digest?” The direct reader uses only the optional, fixed Kubernetes
`aquasecurity.github.io/v1alpha1` `VulnerabilityReport` resource and accepts a report only when
its canonical artifact digest matches an ordinary Pod-status digest from the same snapshot. It
retains only that digest, sorted CVE IDs, and the highest normalized severity; raw report content,
package data, descriptions, links, scanner metadata, registry values, and workload metadata are
discarded. Sith does not install or execute a scanner, pull an image, request an SBOM, query a
registry or vulnerability feed, or use a new credential. A missing report CRD yields no positive
CVE fact, not a clean-image claim; any other report-list failure makes the existing snapshot stale
and unreachable under the same coverage contract.

The inverse CVE route accepts one exact, canonical upper-case CVE identifier only—no case
normalization, lists, globs, severity filters, or arbitrary JSON selectors. It returns the same
bounded normalized image facts and coverage metadata as the image route, scoped through the
signed workspace membership, PEP, and forced-RLS query. An empty result is only an absence of
currently reported runtime-proven evidence; it is never a claim that the workspace or fleet is
free of that CVE.

### Hub schema migration

Run `sith hub migrate` as a short-lived deployment Job before starting `sith hub`. It accepts only
`SITH_HUB_MIGRATION_OWNER_DATABASE_URL` and `SITH_HUB_APPLICATION_DATABASE_ROLE`; mount the owner
database URL from the deployment secret provider and set the application role explicitly. The
command requires TLS for any non-local database target, applies the checksum-locked serializable
migration ledger, creates the tenant-scoped policy-audit chain and exact single-use approval-grant
store, narrows the application role's audit and approval-table privileges, audits forced RLS plus
both immutable-entry contracts, attempts to close its one owner connection, and exits. Approval
rows contain only opaque identifiers, proposer/approver identity, the resolved proposal digest,
an evidence version, and lifecycle timestamps. The application role may insert them and update only
`consumed_at`; it cannot rewrite or delete the approved identity, digest, approval time, expiry, or
evidence version. The migration process never opens the hub listener, creates a Kubernetes client,
or starts collection.

Migration 0011 preserves defaults for older format-1 audit writers, but older verifiers do not
understand format-2 approval lifecycle entries. During a rolling upgrade, run the migration, upgrade
all verifier-capable hub instances, and only then enable traffic that creates or consumes approvals.
Migration 0012 only extends the retained action constraint with the closed `export-audit` value;
deploy it before exposing the audit-export route so its authorizing decision can be appended.
Migration 0013 backfills legacy approval rows under one transactional access-exclusive owner lock,
immediately restores forced RLS, and marks those rows as legacy so they cannot be consumed by the
new evidence contract. It also enables audit format 3. Run the migration before deploying format-3
writers and upgrade every verifier before enabling approval traffic; older writers fail closed
because the new immutable fields have no permissive defaults. Sith currently exposes no runtime
approval/dispatch path, so this ordering does not interrupt a supported write API.

The normal hub process continues to use only `SITH_HUB_DATABASE_URL` for the non-owner application
role. Do not reuse the migration-owner credential in the hub Deployment or place either database
URL, certificates, tokens, or private keys in chart values or logs.

### OCI image deployment contract

The hub OCI recipe uses the digest-pinned distroless static Debian 12 runtime and contains only a
static Linux Sith binary running as UID/GID `65532`. It has no shell, package manager, default
configuration, Kubernetes credential, certificate, database URL, or secret. The source test builds
and inspects both `linux/amd64` and `linux/arm64` variants without publishing; the native image must
also run with a read-only filesystem, no network, no Linux capabilities, and no privilege
escalation, then complete the same contract as a hardened Job on each of two Kind clusters.
The no-network setting applies only to those isolated image checks. A deployed hub needs narrowly
allowlisted egress to its configured runtime dependencies, including its database and, when
enabled, the pinned OIDC discovery and JWKS endpoints.

Hub OCI images are published only by a completed, signed release tag. An organization package admin
must make the `sith-hub` Container package public once before the first Hub release; each completed
release then proves anonymous access to its release-bound digest and attaches it as
`sith_<version>_hub.image`; follow the
[release verification guide](docs/RELEASE.md#verify-a-hub-oci-image) before supplying it to the
fail-closed [`charts/sith-hub`](charts/sith-hub) chart. The chart requires an explicit
`repository@sha256:...` image reference and refuses tags, especially `latest`; it invokes `sith hub
migrate` in a separate short-lived Job before the non-owner hub Deployment starts. Its defaults
intentionally cannot install until an operator provides that digest and the existing Secret
references; it never renders secret material. Older releases can lack this image artifact. The
chart permits only fixed `light` and `heavy` resource profiles, which retain identical security,
credential, and RBAC controls. This first F9.3a profile slice does not claim in-chart database or
HA topology; those parent-F9.3 capabilities need later evidence.

`sith serve --mcp` exposes `fleet.inventory`, `fleet.health`, `fleet.correlate`, and
`fleet.cve-search` over MCP Streamable HTTP. All four tools are cache-only and carry
`readOnlyHint:true`; they use the exact workspace-required query path used by the CLI, TUI, and web
API. The server binds loopback only, requires an exact Host/path, enables DNS-rebinding and
cross-origin protections, and emits structured audit records without raw arguments, results, or
tokens. Its output is a reviewed projection and excludes raw Kubernetes evidence payloads.

Loopback trust is the account-free default. For machines where another same-user process is in the
threat model, add `--require-token`. Sith generates a listener-bound capability, stores it only in
the OS keychain, and prints a key reference—not the secret. Retrieve it explicitly for client
configuration while the server is running:

```bash
sith mcp-token --key mcp/session/<key-printed-by-serve>
```

This is a narrow local capability, not a full OAuth flow; governed hub mode will use OAuth 2.1 and
RFC 8707 audience-bound tokens. A crashed local server can leave an unusable session entry, but the
entry cannot authorize a new server because listener sessions and key names are unique.

Scripted `get` calls require either `--all-clusters` or one explicit `--context`. Text, JSON, YAML,
wide, and source-abstract name outputs are supported. Search and correlation run over the same
normalized in-memory records; partial results name stale/unreachable contexts. The cache is not
persisted to disk, so raw workload specifications do not become a new plaintext credential-adjacent
artifact.

`sith investigate` runs the deterministic R1-R6 catalog plus adjacent R7 over the same cache: bad
deploy, OOMKilled, CrashLoopBackOff/repeated container failure, config drift, certificate expiry,
node pressure, and exact `ImagePullBackOff`/`ErrImagePull` detection. R7 proves only an image-pull
failure or backoff, not registry authentication, an invalid reference, reachability, rate limiting,
platform mismatch, or another underlying cause. Its sensitive-marked advisory is only a read-only
`kubectl describe pod` command; it retains no image reference, registry credential, Secret, Event
message, or raw payload.

The brain package also exposes adjacent R8 to callers that already hold reviewed Argo CD graph
facts. R8 fails closed unless an attached, workspace-valid Application TIMELINE `FactChange` has
exact `argocd` source and provenance, protocol `1.0.0`, matching source/entity identity, a closed
payload with consistent failure kind and phase, an exact event time, and explicit caller-supplied
coverage. Only operation phase `Failed` or `Error` is accepted; missing or mismatched evidence,
`OutOfSync`, health, and successful or running operations do not prove failure. R8 discards the
revision and raw source payload, reports only that Argo recorded a failed operation, and offers the
sensitive read-only advisory
`kubectl --context {context} describe application.argoproj.io {name} -n {namespace}`. The
cache-backed `sith investigate` command does not fetch Argo Applications or infer Argo coverage,
so R8 appears there only after a future reader supplies the same validated graph evidence and
explicit TIMELINE coverage.

Adjacent R9 accepts one already-authorized GitHub REST `Get a workflow run` response through a
bounded, API-versioned projector. It emits an unattached TIMELINE fact only when trusted caller
identity agrees with the response and GitHub reports exact status `completed` with conclusion
`failure`, `timed_out`, or `startup_failure`. Incomplete runs and completed non-failure conclusions
abstain; unknown states, identity mismatches, duplicate JSON members, malformed timestamps, and
ambiguous facts fail closed. The graph bridge admits only exact `github` source/provenance,
`workflow-runs/2026-03-10`, a closed payload, consistent run/attempt/native identity, matching event
time, and explicit caller-supplied TIMELINE coverage. R9 discards job, step, log, actor, branch,
commit, URL, and raw response data; it reports only that GitHub recorded a completed workflow-run
failure and leaves the cause unresolved. Its sensitive advisory tells a human to inspect the run's
failed jobs and logs before considering a rerun. It adds no GitHub client, token loading, fetch,
retention, repository-to-workload correlation, alert, typed intent, PEP handoff, mutation, or
execution. The cache-backed `sith investigate` path cannot produce R9 until a future reader supplies
validated workflow-run graph facts and explicit TIMELINE coverage.

The brain's existing R3 CrashLoop rule can also consume the bounded Elasticsearch
`search/ecs-v1` facts produced from an already-fetched, complete Search API response. The graph
bridge requires exact Elasticsearch source/provenance, an attached Pod identity, a SHA-256
native/resource identity that recomputes from the retained sanitized aggregate and Pod, and the
closed `logs.cause` values `panic`, `missing-config`, or
`dependency-failure`. It revalidates source bounds, discards count/container/window metadata, and
preserves only the Pod, cause, last classified event time, source, and stale flag. Fact presence
does not infer TELEMETRY coverage, and evidence for one Pod cannot strengthen another. Raw logs,
index/document IDs, query text, labels, URLs, credentials, and user data do not enter the brain or
CLI output. This path adds no Elasticsearch HTTP client, endpoint/index configuration, credential,
query execution, persistence, fleet correlation, typed intent, mutation, or execution; the
cache-backed `sith investigate` command still does not fetch Elasticsearch data.

The E13 cost boundary likewise exposes a pure OpenCost `allocation/namespace-usd-v1` projector to
callers that already hold one authorized `/allocation` response for an explicit UTC window. It
accepts only the exact namespace aggregation contract with one set, disabled idle/sharing options,
and a trusted USD source assertion. Every valid Kubernetes namespace becomes exactly one
`FactCost` / `LensTelemetry` fact attached to its exact `{cluster, namespace}` identity, with
canonical five-decimal USD amounts and an observation time equal to the allocation-window end. The
projector validates the exact OpenCost component total with decimal arithmetic and discards provider
IDs, labels, annotations, workload identity, endpoints, and unknown fields. Any malformed, warned,
partial, duplicate-key, identity/window-mismatched, oversized, or invalid-total response is rejected
as a whole and emits zero facts; invalid rows are never filtered into a partial success. A successful
empty allocation map also emits zero facts, while missing OpenCost coverage is never estimated. This
package can preserve each successful projection in a per-scope snapshot, including an empty fact
set, and combine snapshots for one exact window into a deterministic workspace USD total. The
caller supplies the complete expected-scope set; output names every expected, reported,
successful-empty, and missing scope, and missing scopes never contribute synthetic zero cost.
Every fact is revalidated before all component and total amounts are summed with exact decimal
arithmetic. A rollup carries the source window end only when at least one scope reported and selects
no stale threshold.

The rollup is an offline workspace computation core, not a live Hub feature. The OpenCost path adds
no OpenCost client, port-forward, service or ingress discovery, arbitrary endpoint, credentials,
Kubernetes Service-proxy RBAC, OCM transport, persistence, runtime wiring, per-team attribution,
UI/API, billing, optimization, mutation, currency conversion, freshness objective, or
GPU-utilization claim. The current CLI and Hub do not fetch, persist, roll up, or display these
facts yet.

The bounded F13.3 GPU boundary separately exposes a pure DCGM
`prometheus/gpu-utilization-vector-v1` projector for callers that already hold one authorized
Prometheus instant-vector response and assert the exact query `DCGM_FI_DEV_GPU_UTIL`, with the API
series limit and per-query lookback override both disabled. This avoids a caller presenting an
API-truncated vector as complete while leaving the server's configured lookback/freshness policy
explicitly unresolved. It requires
the reviewed current dcgm-exporter whole-GPU identity labels, accepts paired `GPU_I_ID` and
`GPU_I_PROFILE` MIG evidence, and treats `namespace`/`pod`/`container` only as an all-or-nothing
`workload_best_effort` attribution. A fact always distinguishes its physical-GPU or MIG device
scope; exporter workload labels are never presented as precise per-workload accounting. Values
must be exact decimal percentages from 0 through 100 at the asserted query time. Unknown labels are
bounded but discarded, while raw GPU UUID, hostname, PCI bus, scrape target, job, instance, and
arbitrary pod-label data never enter the fact payload, display fields, or graph entity; selected
native identity is represented only by SHA-256. Duplicate projected identities, partial MIG or
workload labels, warnings or infos, non-vector or duplicate-key JSON, timestamp mismatch,
non-finite/out-of-range samples, and oversized input fail atomically. A successful empty vector
emits zero facts and does not by itself prove DCGM coverage.

This DCGM core adds no Prometheus or DCGM client, discovery, endpoint or credential handling, new
RBAC, arbitrary PromQL, range aggregation, stale threshold, coverage rollup, persistence, cost or
idle-cost join, team mapping, UI/API, billing, optimization, rightsizing, mutation, or execution.
The current CLI and Hub do not fetch, persist, aggregate, or display these GPU utilization facts.

Every verdict includes its rule, exact cited signals, confidence state, missing lenses, and an
advisory command or PR change for the operator to inspect and run. R1, R2, and R4 additionally name
an inert closed remediation verb plus the authoritative provenance a later reviewed renderer would
require; the candidate contains no target, arguments, approval, or execution capability. The brain
performs no I/O. It imports only the side-effect-free closed intent vocabulary—not connector
planning or execution, PEP, MCP, persistence, network, or local-operation paths.

The contract-only GitOps provenance resolver is the next deliberately separate stage. It accepts
only a confirmed, entity-local R2/R4 `gitops.open-pr` candidate and exactly one immutable
`gitops-provenance/v1` bundle from the pinned canonical GitHub source contract. The bundle binds one
workspace and affected resource to a repository, non-symbolic base branch, exact base commit,
single update path, observed blob, exact desired content, evidence references, a maximum five-minute
validity interval, and the live planning handler's adapter version plus argument-schema digest.
Resolution reuses the GitHub handler's pure validation/canonicalization seam, rechecks the handler
contract, request cancellation, and bundle freshness after canonicalization, and returns only the
normalized repository target, exact canonical arguments and SHA-256 digest, copied evidence
references, or a closed abstention reason. It rejects missing, duplicate, stale, future, foreign,
unattached, drifted, handler-mutated, and handler-invalid inputs without I/O.

Provenance readiness is not a PEP proposal, policy verdict, approval, credential, dispatch, Git
write, or execution capability. The resolver does not query GitHub and cannot independently prove
remote ref, commit, tree, or blob state; a future authorized read adapter must acquire those exact
facts and construct the bundle. That adapter's API calls, rate-limit/egress impact, credential
custody, and freshness policy are outside this offline slice.

The approved next decomposition now defines the observed half independently as an immutable
`git-source-snapshot/v1`. It contains one workspace and affected resource, one pinned GitHub source
identity and repository, a configured non-symbolic base ref plus exact resolved commit, one safe
repository-relative path, and exact current file bytes plus the matching Git blob identity. The
constructor recomputes the blob object ID, copies and canonically orders attached subject/blob
evidence, and bounds content, evidence count, and validity. A pure trusted-time check classifies the
snapshot as fresh, future, or stale; it performs no I/O.

The separately gated output half now exists as immutable `desired-change/v1`. A `DesiredChange`
defensively embeds one exact validated snapshot, one lowercase canonical `<transformer>/<version>`
identity, exact bounded non-NUL UTF-8 output bytes, and 2–32 unique stable evidence references that
must attach the snapshot's affected resource and observed blob. It preserves bytes without Unicode,
line-ending, whitespace, or YAML normalization, rejects exact no-op output, and canonically orders
copied evidence.

There is deliberately no exported desired-change constructor. Until a concrete deterministic
transformer or declarative renderer is separately reviewed, request and runtime code cannot label
arbitrary bytes as trusted output. The snapshot still contains no desired bytes, and neither
contract contains PR metadata, handler binding, actor, intent, policy decision, approval,
credential, endpoint, persistence, dispatch, mutation, or execution state. Neither is wired into
the Brain, resolver, connector runtime, PEP, or Hub, so R2 and R4 remain advisory-only. These
offline contracts add no API request, egress, storage, cloud resource, telemetry cardinality, or
recurring cost; a future live adapter and transformer separately own credentials, rate limits,
freshness, format semantics, and evidence-to-output policy.

Phase-L kubeconfig hydration supplies LIVE pod/workload/node evidence and discrete Kubernetes
Events for TIMELINE when present. DESIRED and TELEMETRY remain unavailable unless a future
connector supplies entity-attached facts. Consequently, an OOM or repeated failure is detected
from LIVE evidence while its leak/spike/log-cause variant remains explicitly unconfirmed. A stale
or missing required lens downgrades the dependent verdict rather than being treated as negative
evidence. For correlation-eligible canonical rules only, identical unhealthy image digests on two
or more contexts produce a fleet-wide verdict ahead of per-cluster findings. Adjacent R7, R8, and
R9 are never fleet-correlated; each remains entity-local. Advisory output never executes or
dispatches anything.

Initial list-watch hydration is a complete, consistent snapshot rather than a bounded prefix: each
scope and kind uses 250-object pages under one request deadline, with hard limits of 10,000 objects
and 128 pages. Sith emits a watch error and does not open the stream when a continuation fails,
the collection changes resource version between pages, or either budget is exceeded.

The TUI opens only when stdin and stdout are terminals; redirected bare invocations remain
script-safe and print help. Tier-1 lenses are Pods, Deployments, Events, and Nodes. Use `:` for
lens/context commands (including `:<kind>` for an API-discovered generic resource rendered with
the server's print columns), `/` to filter the current lens, `Ctrl-K` for whole-fleet fuzzy/
structured search, number keys for cluster scope, `c` for coverage, and `Ctrl-R` for a non-blocking
refresh.
On a selected row, `d` opens describe, `y` opens masked YAML, `l` follows pod logs, `s` opens a
pod shell, `f` prompts for a loopback port mapping, and `e` opens a YAML edit. `Esc` returns to the
exact prior fleet scope and row; `:pf` lists persistent port-forwards and `x` closes the newest
active one.
The UI uses Bubble Tea v2.0.8 core only; tables and search remain local so no optional styling or
component dependency enters the binary.

`sith ui` serves a build-free frontend embedded in the same Go binary. It binds to
`127.0.0.1` on an available port by default; `--address` accepts loopback addresses only and
`--no-open` suppresses browser launch. `--kubeconfig-dir <directory>` imports a bounded recursive
set of regular kubeconfig files for that UI session. It does not replace the standard
`KUBECONFIG`/`~/.kube/config` mode, write or persist a config, or follow directory symlinks. The
supplied root must be an existing real directory; a file, symlink, or missing root fails before the
local listener starts. Invalid, oversized, unreadable, or symlinked entries beneath a valid root
are skipped with safe warnings that do not expose kubeconfig contents or an absolute local path.
Directory-imported configs must embed certificate-authority, client-certificate, client-key, and
token data rather than defer those reads to local file paths. Exec credential commands must be a
PATH-resolved program name, not a relative or absolute path. These constraints keep credential and
plugin resolution from reopening the directory replacement race after the bounded import completes.
Each imported source is labeled by its relative filename; contexts with the same name remain
isolated, and selecting a source in the context rail filters to its contexts. The import is limited
to 128 traversed filesystem entries (including ignored symlinks and directories), 4 MiB per regular
kubeconfig file, and eight nested directory levels. The browser renders the same cache, lenses, ordering,
coverage, search/correlation grammar, and per-resource operations as the CLI/TUI. Its local HTTP
boundary requires an exact Host/Origin and a per-process capability header, uses a restrictive
Content Security Policy, and loads no remote assets. YAML apply additionally requires a short-lived,
single-use server preview token bound to the exact target and manifest; Secret edit requires an
explicit reveal-and-edit confirmation before unredacted data enters the browser.

Local resource operations always require or derive one explicit cached context and use that
context's existing kubeconfig identity directly. They are deliberately separate from Sith's
governed Intent/PEP action model. Secret YAML is redacted unless `--show-secrets` is explicit;
interactive CLI/TUI Secret edit is refused because it would create a plaintext temporary file.
An explicit user-managed `--file` remains available for secured automation, and browser Secret
editing stays in memory behind its explicit disclosure action. Non-Secret edit files use mode
`0600`, are capped at 10 MiB, and are applied only after strict server dry-run and a displayed
diff. Port-forward accepts loopback addresses only (`localhost`, `127.0.0.1`, or `::1`). Streaming
can hold API connections for its lifetime, but it creates no cloud resources or persistent local
cache.

On macOS, `sith desktop` runs the same embedded fleet IDE in a native Wails v2 window. It uses an
in-process WebView origin (`wails://wails`), so it does not open a TCP listener. The **Import folder**
control appears only in that window and opens a native directory chooser; it passes the selection to
the identical bounded, in-memory kubeconfig importer used by `sith ui --kubeconfig-dir`. The UI
receives success, cancellation, or a sanitized failure category—never the selected absolute path or
kubeconfig content. Build
an ad-hoc-signed Apple Silicon development bundle with `make desktop-build`; public releases remain
blocked on Developer ID signing, notarization, stapling, and E9 release provenance, so this is not yet
a distributed replacement for Lens.

Each active lens holds one Kubernetes watch per reachable context after its initial list. A
two-minute safety rediscovery recovers contexts that were offline at launch; it is not the primary
resource refresh path. Very large context/lens counts therefore trade API-server connection and
relist cost for continuous low-latency deltas.

Run the full local quality gate with golangci-lint v2.12.2 and govulncheck v1.6.0 on `PATH`:

```bash
make ci
```

Release changes additionally require GoReleaser v2.17.0 and Syft v1.49.0. This gate builds all
four archives twice and refuses the change if their SHA-256 digests differ:

```bash
make release-check
```

The gate also compiles the binary under a functional HTTP/HTTPS egress sentinel and exercises
local commands, deterministic investigation, plus the running web UI and MCP server with an official SDK client. A source
boundary exact-allowlists production network, filesystem-write, and subprocess imports, confines
local-mode client-go transport to the kubeconfig adapter, permits only the separately reviewed
tenant-scoped direct OCM adapter in governed mode, and rejects known telemetry SDKs and low-level
network bypasses. Together these checks prove the reviewed paths; they are regression controls
rather than an operating-system network sandbox.

The real multi-cluster gate creates two temporary kind clusters with a digest-pinned node image,
checks one additional unreachable context, and proves CLI, advisory-brain, web-IDE, and MCP context isolation for
search/correlation, logs, exec, YAML/Secret handling, describe/events, preview-gated edit, and
loopback TCP forwarding against a scratch fixture image. MCP additionally proves live inventory,
unhealthy-workload correlation, and image/CVE search over the same two clusters. The brain fixture
uses one real repeatedly failing container per cluster and proves same-digest fleet correlation,
cited LIVE evidence, and clean TELEMETRY abstention. It removes both
clusters afterward. The gate requires a running Docker engine and kind v0.32.0, and consumes
additional CI time, disk, and memory:

```bash
make e2e-kind
```

The hub tenancy gate starts a temporary, digest-pinned PostgreSQL 18.4 container and proves the
application role is a non-owner without `BYPASSRLS`, every current workspace table—including API
key verifier, OIDC binding, and approval-grant rows—has forced RLS, direct unscoped reads are
default-deny, foreign writes fail, and transaction-local scope does not bleed through a reused pool
connection. It also proves same-workspace current-role checks, proposer/approver separation,
approve-then-swap refusal, immutable grant fields, replay refusal, and exactly one winner under a
concurrent approval-consumption race. It additionally exercises real
API-key issuance, current-membership exchange, bounded rotation overlap, immediate revocation,
OIDC binding resolution, and cross-workspace negative controls. It requires Docker and adds one
official PostgreSQL image pull plus a short-lived local container:

```bash
make e2e-postgres
```

The complete tenant-isolation suite combines the real PostgreSQL boundary with signed-token,
injected-header, scoped-query, and deterministic selector-fuzz invariants. It also removes,
weakens, and adds a second permissive live RLS policy and requires the suite to detect all three
mutations before restoring the steady state. The native Go fuzzer runs exactly 50,000 generated
selector mutations with four workers; coverage no longer depends on CI runner speed, while a
separate timeout catches hangs:

```bash
make e2e-isolation
```

The architecture, threat model, ADRs, and roadmap live under [`docs/`](docs/). Build-session
checkpoints are recorded under [`sessions/`](sessions/).
