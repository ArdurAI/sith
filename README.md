# Sith

**Status: Slice 5 local privacy boundary.** The CLI and embedded browser IDE discover every context
resolved by client-go, hydrate one local in-memory fleet cache through per-context watches, serve
coverage-honest fleet search/correlation, and provide explicit-context logs, exec, port-forward,
describe, and YAML view/edit. Local mode requires no account, emits no telemetry, and includes a
fail-loud OS-keychain custody layer for the optional local token introduced in the next slice.

Sith is ArdurAI's single-binary, local-first Kubernetes fleet tool: **k9s for your whole fleet**.
It is designed to aggregate every kubeconfig context without an account, telemetry, or cluster
data leaving the machine. The same source-abstract fleet model will later power an optional
governed hub.

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
./bin/sith describe pod/api --context kind-dev -n apps
./bin/sith yaml secret/api-token --context kind-dev -n apps
./bin/sith logs api --context kind-dev -n apps --tail 100 -f
./bin/sith exec api --context kind-dev -n apps -it -- /bin/sh
./bin/sith port-forward service/api --context kind-dev -n apps :http
./bin/sith edit configmap/api-settings --context kind-dev -n apps
./bin/sith ui                    # loopback-only embedded fleet IDE
```

`sith clusters` follows standard client-go loading rules: set `KUBECONFIG` to an OS path-list or
use the default `~/.kube/config`. Exec-credential helpers run locally, exactly as they do for
`kubectl`; Sith does not copy kubeconfigs or credentials elsewhere.

Sith-owned persisted secrets use the host credential store under the fixed `io.ardur.sith`
service: macOS Keychain, Windows Credential Manager, or freedesktop Secret Service. If that store
is unavailable, the operation fails; there is no silent plaintext or encrypted-file fallback.
The current slice establishes and tests this custody boundary without creating a stored secret.
The dependency can invoke the fixed macOS `/usr/bin/security` tool or the Linux session D-Bus only
during an explicit keychain operation; it creates no account, hosted service, or cloud cost.

Scripted `get` calls require either `--all-clusters` or one explicit `--context`. Text, JSON, YAML,
wide, and source-abstract name outputs are supported. Search and correlation run over the same
normalized in-memory records; partial results name stale/unreachable contexts. The cache is not
persisted to disk, so raw workload specifications do not become a new plaintext credential-adjacent
artifact.

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

The gate also compiles the binary under a functional HTTP/HTTPS egress sentinel and exercises
local commands plus the running web UI. A source boundary exact-allowlists production network,
filesystem-write, and subprocess imports, confines client-go transport to the kubeconfig adapter,
and rejects known telemetry SDKs and low-level network bypasses. Together these checks prove the
reviewed paths; they are regression controls rather than an operating-system network sandbox.

The real multi-cluster gate creates two temporary kind clusters with a digest-pinned node image,
checks one additional unreachable context, and proves CLI plus web-IDE context isolation for
search/correlation, logs, exec, YAML/Secret handling, describe/events, preview-gated edit, and
loopback TCP forwarding against a scratch fixture image. It removes both clusters afterward. The
gate requires a running Docker engine and kind v0.32.0, and consumes additional CI time, disk,
and memory:

```bash
make e2e-kind
```

The architecture, threat model, ADRs, and roadmap live under [`docs/`](docs/). Build-session
checkpoints are recorded under [`sessions/`](sessions/).
