# Sith

**Status: Phase-L client plus release supply-chain gate.** The CLI, TUI, browser IDE, optional MCP server,
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
./bin/sith serve --mcp           # loopback-only MCP read server
./bin/sith serve --mcp --require-token
```

`sith clusters` follows standard client-go loading rules: set `KUBECONFIG` to an OS path-list or
use the default `~/.kube/config`. Exec-credential helpers run locally, exactly as they do for
`kubectl`; Sith does not copy kubeconfigs or credentials elsewhere.

Sith-owned persisted secrets use the host credential store under the fixed `io.ardur.sith`
service: macOS Keychain, Windows Credential Manager, or freedesktop Secret Service. If that store
is unavailable, the operation fails; there is no silent plaintext or encrypted-file fallback.
The optional local MCP capability is the first consumer: it uses a unique short-lived keychain
entry and deletes it on clean server shutdown.
The dependency can invoke the fixed macOS `/usr/bin/security` tool or the Linux session D-Bus only
during an explicit keychain operation; it creates no account, hosted service, or cloud cost.

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
`--no-open` suppresses browser launch. The browser renders the same cache, lenses, ordering,
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
client-go transport to the kubeconfig adapter, and rejects known telemetry SDKs and low-level
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
application role is a non-owner without `BYPASSRLS`, every current workspace table has forced RLS,
direct unscoped reads are default-deny, foreign writes fail, and transaction-local scope does not
bleed through a reused pool connection. It requires Docker and adds one official PostgreSQL image
pull plus a short-lived local container:

```bash
make e2e-postgres
```

The complete tenant-isolation suite combines the real PostgreSQL boundary with signed-token,
injected-header, scoped-query, and deterministic selector-fuzz invariants. It also removes and
weakens a live RLS policy and requires the suite to detect both mutations before restoring the
steady state. The query fuzzer runs for a bounded five seconds in CI rather than indefinitely:

```bash
make e2e-isolation
```

The architecture, threat model, ADRs, and roadmap live under [`docs/`](docs/). Build-session
checkpoints are recorded under [`sessions/`](sessions/).
