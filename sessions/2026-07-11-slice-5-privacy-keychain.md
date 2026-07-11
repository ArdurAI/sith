# Session — 2026-07-11 — slice-5-privacy-keychain

**Builder:** Gnani Rahul · **Model/effort:** engineering, max · **Branch:** gnanirahulnutakki/feat/privacy-keychain
**Slice(s):** Slice 5 / #36 · **Status:** ready-for-PR

---

[G] Goal: Make local mode's no-account, no-telemetry, credential-locality, and OS-keychain custody
promises executable and testable before Slice 6 introduces the optional local MCP token.
[S] Scope: cross-platform OS-keychain abstraction with fail-loud errors and no file fallback,
locked local privacy posture, production network/persistence import boundaries, compiled-binary
egress sentinel, and plaintext-Secret temp-file prevention. MCP serving/auth, encrypted file
fallbacks, hub identity, telemetry opt-in, and account flows are out.
[A] Action: Started from released `origin/dev` eaf7daf after Slice 4 feature/release CI, post-merge
main CodeQL, issue closure, security-alert audit, Obsidian ingest, and worktree cleanup completed.
GitHub Dependabot, CodeQL, and secret-scanning queues were empty.
[A] Action: Selected `github.com/zalando/go-keyring` v0.2.8 after verifying the current tag,
documented macOS Keychain / Windows Credential Manager / Linux Secret Service backends, error
contract, and zero published GitHub security advisories. Sith adds no plaintext or encrypted-file
fallback: backend failure is classified as keychain unavailable and returned to the caller.
[A] Action: Added a context-aware, fixed-service keychain store with validated names, a 2 KiB
cross-platform secret ceiling, not-found/unavailable classification, secret-redacted errors, and
fake-backend tests for roundtrip, cancellation, invalid input, missing secret, and fail-loud
unavailability without fallback files.
[A] Action: Centralized the permanent local posture (no account, no telemetry) and added an AST
boundary that exact-allowlists direct `net/*` imports, confines client-go transport to the local
kubeconfig adapter, rejects common telemetry SDKs, bounds filesystem-write primitives, and forbids
filesystem packages in the keychain layer. The final red-team pass also exact-allowlisted reviewed
subprocess sites and rejected direct raw-socket, x/net, gRPC, and QUIC bypass imports.
[A] Action: Refused interactive CLI/TUI Secret editing before object read or temp-file creation;
explicit `--file` input remains allowed because Sith did not create/persist it, while the web
editor remains in-memory behind its explicit disclosure action.
[T] Test: `make ci` is green: formatting, vet, golangci-lint (zero issues), govulncheck (no known
vulnerabilities), race/coverage, the warm-view performance gate, compiled-binary E2E, and build.
The compiled binary exercised version,
clusters, get, search, correlate, help, and the serving web UI under a functional HTTP/HTTPS proxy
sentinel with zero non-cluster egress attempts. Privacy coverage is 100%; keychain coverage is
78.7%.
[T] Test: The digest-pinned, two-cluster kind fan-out gate passed under `-race` in 71.378s, proving
the current privacy changes preserve real local multi-cluster behavior. Docker cleanup removed the
temporary network/image/cache and reclaimed 913.1 MB.
[T] Test: The keychain package cross-compiles with CGO disabled for darwin/amd64, linux/amd64, and
windows/amd64. A real OS-keychain mutation test is deliberately excluded because it can prompt and
would alter the developer's credential store; backend-contract tests cover custody behavior.
[R] Review: The macOS backend uses fixed `/usr/bin/security` and sends the encoded value over
stdin, Windows uses Credential Manager APIs, and Linux uses session D-Bus Secret Service. Backend
error text is intentionally suppressed so it cannot echo a secret. Upstream calls are not
context-cancelable after start; Sith prechecks context cancellation but cannot interrupt an OS
prompt already in progress.
[C] Cost: No cloud resources or hosted services are introduced. Keychain subprocess/D-Bus work
occurs only when a caller explicitly performs a secret operation; kind verification used temporary
local Docker resources that were removed after the gate.

---

**Session close:** ready for signed commit stack and PR · **Open questions touched:** Q14 keychain mechanism is ready for Slice 6; Q15 remains permanent no-telemetry in Phase L
