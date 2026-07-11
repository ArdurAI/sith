# ADR-0007 — Local MCP transport, scope, and authentication

**Status:** Accepted · **Date:** 2026-07-11

## Context

Slice 6 exposes the local fleet cache to external agents through four read tools. That surface must
remain easier than an unsanctioned Kubernetes MCP server without creating a privileged data path,
login wall, telemetry channel, or new credential leak. It also arrives while MCP is still evolving:
the roadmap references a 2026-07-28 release candidate that is not yet stable.

The stable primitives used here are Streamable HTTP from the
[2025-06-18 transport specification](https://modelcontextprotocol.io/specification/2025-06-18/basic/transports),
the specification's [tool annotations](https://modelcontextprotocol.io/specification/2025-06-18/server/tools),
and execution-time audience validation aligned with the
[authorization specification](https://modelcontextprotocol.io/specification/2025-06-18/basic/authorization).
The implementation uses the official Go SDK v1.6.1. Its four published high-severity advisories
affect versions at or below v1.4.0; v1.6.1 is outside every vulnerable range.

## Decision

1. **Streamable HTTP on loopback only.** `sith serve --mcp` binds `127.0.0.1` by default and
   rejects every non-loopback address. It serves one exact `/mcp` endpoint with stateless JSON
   responses, bounded HTTP timeouts, exact Host/path checks, SDK DNS-rebinding protection, and an
   explicitly configured Go cross-origin protection middleware. We do not rely on the SDK's
   permissive zero-value origin default.
2. **Cache-only, read-only tools.** `fleet.inventory`, `fleet.health`, `fleet.correlate`, and
   `fleet.cve-search` read the hydrated in-memory cache. Tool execution never opens a Kubernetes
   connection. All four carry `readOnlyHint:true`, `destructiveHint:false`,
   `idempotentHint:true`, and `openWorldHint:false`; those annotations are hints, while the
   server-side cache-only implementation is the enforcement.
3. **Workspace is required by the store API.** CLI, TUI, web, and MCP all call the same
   workspace-required cache query. Facts and discovery-scope metadata are filtered at that layer;
   an empty workspace fails closed, and even a guessed cross-workspace scope returns no stored
   reachability metadata.
4. **Loopback trust by default; optional short-lived local capability.** Default local mode has no
   login wall. `--require-token` generates a unique bearer capability for the exact listener URL,
   stores it only in the OS keychain, prints only its key reference, and deletes it on clean
   shutdown. `sith mcp-token --key ...` is the explicit reveal operation for client configuration.
   Middleware verifies the token on every HTTP request; each tool verifies scope, expiry, and
   audience again at execution. This local capability is not advertised as a full OAuth flow.
   Hub mode will implement OAuth 2.1 protected-resource discovery and RFC 8707 tokens separately.
5. **Audited without payload leakage.** Every tool execution records tool, workspace, actor,
   allow/deny, record count, duration, and sanitized error. Raw arguments, results, bearer tokens,
   and Kubernetes evidence are excluded. MCP outputs use a reviewed projection and never expose the
   cache's raw `Fact.Observed` payload.
6. **Stable primitives only.** Preview 2026 protocol features do not enter the Phase-L binary.
   The SDK is pinned and guarded by `govulncheck`, GitHub security scanning, protocol tests, and
   compiled-binary E2E.

## Consequences

**Positive**

- Agents see the same workspace-scoped answers as human surfaces and cannot choose a workspace in
  tool arguments.
- The optional token defends against shadow local clients without requiring an account or a local
  OAuth server.
- Stateless request authentication prevents an authenticated session from becoming a bearer-free
  long-lived capability.
- Raw Kubernetes objects and potential Secret payloads are not part of the MCP output schema.

**Negative / limitations / cost**

- Loopback is not an operating-system sandbox; another process running as the same user can connect
  when optional token auth is disabled. Users with that threat model should enable
  `--require-token`.
- The local capability is intentionally narrower than standards-complete OAuth 2.1. Reusing it for
  hub mode is forbidden.
- One watch per active tier-1 lens and reachable context remains the dominant runtime cost. MCP adds
  only a loopback HTTP server, cache serialization, and optional keychain operations; it creates no
  cloud resources or recurring service cost.
- A crashed process may leave an unusable expired session entry in the keychain. It cannot authorize
  a new server because every session uses a unique key and listener-bound verifier.

## Alternatives considered

- **STDIO only.** Simpler and naturally process-scoped, but it does not satisfy the locked
  loopback-server contract or support multiple local clients. Rejected as the sole transport.
- **Hand-written JSON-RPC/MCP.** Reduces dependencies but creates protocol drift and security review
  burden. Rejected in favor of the official SDK plus explicit hardening.
- **Always require a token.** Stronger against same-user shadow clients, but recreates a first-run
  login/configuration wall. Rejected; token auth remains a deliberate option.
- **Full local OAuth 2.1 authorization server.** Standards-complete but disproportionate for a
  single-user, account-free local binary. Deferred to governed hub mode.
