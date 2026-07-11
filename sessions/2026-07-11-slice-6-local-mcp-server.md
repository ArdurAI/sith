# Session — 2026-07-11 — slice-6-local-mcp-server

**Builder:** Gnani Rahul · **Model/effort:** engineering, max · **Branch:** gnanirahulnutakki/feat/local-mcp-server
**Slice(s):** Slice 6 / #37 · **Status:** ready-for-PR

---

[G] Goal: Expose the local fleet model through four workspace-scoped, audited, read-only MCP tools
without adding a privileged data path, account, telemetry, or non-loopback listener.
[S] Scope: official Go MCP SDK v1.6.1, stateless Streamable HTTP, `fleet.inventory`,
`fleet.health`, `fleet.correlate`, `fleet.cve-search`, shared cache workspace enforcement,
canonical CVE observations, structured audit, and optional short-lived keychain token. MCP writes,
hub OAuth, tenant identity registration, remote bind, and CVE scanner ingestion are out.
[A] Action: Started from released `origin/dev` 52500d2 after Slice 5 reached main at a1502c4,
post-merge CodeQL passed for Actions/JavaScript/Go, issue #36 closed, security queues were zero,
Obsidian ingest completed, and the Slice 5 worktree/branch were removed.
[A] Action: Verified current official MCP primary sources and selected Go SDK v1.6.1. Reviewed four
published high-severity SDK advisories; all affect versions at or below v1.4.0. Explicitly enabled
Go cross-origin middleware because the current SDK zero value is permissive, while retaining its
default localhost DNS-rebinding defense.
[A] Action: Made workspace an unavoidable cache-query argument for CLI, TUI, web, and MCP. Store
discovery metadata is workspace-owned; record and scope metadata are filtered together; empty scope
fails closed; guessed foreign scopes return only an unreachable caller-supplied placeholder.
[A] Action: Added canonical CVE observations and image/CVE cache predicates. The MCP CVE tool can
query real findings when a scanner adapter supplies them and can search current inventory by image;
it does not invent CVEs when no scanner feed exists.
[A] Action: Added four cache-only MCP tools with `readOnlyHint:true`, `destructiveHint:false`,
`idempotentHint:true`, and `openWorldHint:false`. Outputs omit raw `Fact.Observed` evidence and
audit records omit arguments, results, and secrets.
[A] Action: Added loopback-only serving with exact Host/path, bounded HTTP timeouts, stateless
request authentication, execution-time scope/expiry/audience checks, and optional unique
keychain-backed tokens. The server prints only the key reference and deletes the token on clean
shutdown; `sith mcp-token` is the explicit reveal operation.
[T] Test: Full `make ci` is green: formatting, vet, zero lint issues, govulncheck with no known
vulnerabilities, race/coverage, warm-view performance, compiled-binary E2E, and build. MCP coverage
is 79.6%; privacy coverage remains 100%.
[T] Test: Protocol tests use the official SDK client and prove exact tool annotations, structured
outputs, workspace record/scope isolation, guessed-scope fail-closed behavior, audit allow/deny,
HTTP plus execution-time bearer enforcement, hostile Host/Origin rejection, CVE search, and token
lifecycle without printing the secret.
[T] Test: Compiled-binary E2E starts `sith serve --mcp`, performs an official-client handshake,
lists four tools, calls inventory, and records zero HTTP/HTTPS proxy-sentinel egress.
[T] Test: The final digest-pinned two-cluster kind gate passed under `-race` in 73.265s. It proves MCP
inventory from both live contexts, one unhealthy deployment correlation, log4j image search, and
read-only annotations alongside the existing CLI/web/local-operation coverage. Docker cleanup
reclaimed 913.1 MB after the final gate.
[T] Test: The full binary cross-compiles with CGO disabled for darwin/amd64, linux/amd64, and
windows/amd64. Real OS-keychain mutation remains excluded because it can prompt and alter the
developer credential store; fake-store lifecycle tests prove token creation/deletion.
[C] Cost: No account, hosted service, cloud resource, or recurring cost is introduced. Runtime
cost is loopback HTTP/cache serialization plus the existing tier-1 Kubernetes watches; optional
auth adds two keychain operations per server lifecycle.

---

**Session close:** final current-tree gates green; ready for signed commit stack and PR · **Open questions touched:** Q14 locked to loopback trust plus optional short-lived keychain token
