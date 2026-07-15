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
The P1 `sith hub` runtime now mounts only the session-authenticated fleet read/refresh surface
below; API-key, OIDC, and cloud-proof exchange handlers remain intentionally unmounted until their
ingress and operator lifecycle are composed.

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
failure category. The pinned direct OCM ClusterProxy adapter reads the exact rotating
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

Every referenced key, certificate, or CA file must be a read-only regular file from a deployment
mount. The runtime obtains its Kubernetes identity only with in-cluster configuration; it has no
kubeconfig fallback and uses that identity through the fixed `sith-reader` Secret reader. It serves
only `POST /v1/workspaces/{workspace}/fleet:refresh`,
`GET /v1/workspaces/{workspace}/fleet`, and
`GET /v1/workspaces/{workspace}/fleet/images/{sha256:<64-lowercase-hex>}`, and
`GET /v1/workspaces/{workspace}/fleet/images/{sha256:<64-lowercase-hex>}/cves`, and
`GET /v1/workspaces/{workspace}/fleet/cves/{CVE-YYYY-N...}`. Every route requires an
exact signed Sith session, derives the workspace scope from its signed memberships, carries that
scope through the PEP and RLS seams, accepts no query parameters, and returns only normalized
coverage/fleet data under `Cache-Control: no-store`.

After deriving the signed scope, the hub mints one opaque local trace ID for the governed request.
It strips common caller-supplied trace and correlation carriers, never echoes or forwards them,
and carries the local ID through the PEP audit record and each snapshot transport attempt. The hub
logs only local trace ID, fixed stage, fixed outcome, and bounded duration; it records no workspace,
actor, spoke, endpoint, resource, selector, argument digest, credential, raw error, or returned
data in trace events. This is not a telemetry exporter: it adds no OpenTelemetry SDK, listener,
network egress, queue, trace store, persistence, or action-intent protocol.

Before a signed scope exists, the authentication gate emits one local WARN record for every
refusal with only the fixed `hub-auth` surface and `refused` outcome. It deliberately does not
distinguish credential failure modes or carry a trace/correlation ID, token, header, path, client
address, workspace, principal, or verifier error. The record is a passive alerting signal, not an
audit record, rate limiter, telemetry export, or additional authentication decision.

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
migration ledger, audits forced RLS, attempts to close its one owner connection, and exits. It never opens the
hub listener, creates a Kubernetes client, or starts collection.

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

This is not a published image reference. The fail-closed [`charts/sith-hub`](charts/sith-hub)
chart requires an explicit immutable `repository@sha256:...` image reference and refuses tags,
especially `latest`; it invokes `sith hub migrate` in a separate short-lived Job before the
non-owner hub Deployment starts. Its defaults intentionally cannot install until a release-bound
hub image and operator-provided Secret references exist; it never renders secret material. The
chart permits only fixed `light` and `heavy` resource profiles, which retain identical security,
credential, and RBAC controls. This first F9.3a profile slice does not claim a public image,
in-chart database, or HA; those parent-F9.3 topology and custody capabilities need later evidence.

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

`sith investigate` runs the deterministic R1-R6 catalog over the same cache: bad deploy,
OOMKilled, CrashLoopBackOff/repeated container failure, config drift, certificate expiry, and node
pressure. Every verdict includes its rule, exact cited signals, confidence state, missing lenses,
and an advisory command or PR change for the operator to inspect and run. The brain performs no
I/O and imports no connector planning, execution, intent, PEP, MCP, or local-operation path.

Phase-L kubeconfig hydration supplies LIVE pod/workload/node evidence and discrete Kubernetes
Events for TIMELINE when present. DESIRED and TELEMETRY remain unavailable unless a future
connector supplies entity-attached facts. Consequently, an OOM or repeated failure is detected
from LIVE evidence while its leak/spike/log-cause variant remains explicitly unconfirmed. A stale
or missing required lens downgrades the dependent verdict rather than being treated as negative
evidence. Identical unhealthy image digests on two or more contexts produce a fleet-wide verdict
ahead of per-cluster findings. Advisory output never executes or dispatches anything.

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

Release changes additionally require GoReleaser v2.17.0 and Syft v1.46.0. This gate builds all
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
key verifier and OIDC binding rows—has forced RLS, direct unscoped reads are default-deny, foreign writes fail, and
transaction-local scope does not bleed through a reused pool connection. It also exercises real
API-key issuance, current-membership exchange, bounded rotation overlap, immediate revocation,
OIDC binding resolution, and cross-workspace negative controls. It requires Docker and adds one
official PostgreSQL image pull plus a short-lived local container:

```bash
make e2e-postgres
```

The complete tenant-isolation suite combines the real PostgreSQL boundary with signed-token,
injected-header, scoped-query, and deterministic selector-fuzz invariants. It also removes and
weakens a live RLS policy and requires the suite to detect both mutations before restoring the
steady state. The native Go fuzzer runs exactly 50,000 generated selector mutations with four
workers; coverage no longer depends on CI runner speed, while a separate timeout catches hangs:

```bash
make e2e-isolation
```

The architecture, threat model, ADRs, and roadmap live under [`docs/`](docs/). Build-session
checkpoints are recorded under [`sessions/`](sessions/).
