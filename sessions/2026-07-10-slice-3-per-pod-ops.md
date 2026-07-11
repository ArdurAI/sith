# Session — 2026-07-10 — slice-3-per-pod-ops

**Builder:** Gnani Rahul · **Model/effort:** engineering, max · **Branch:** gnanirahulnutakki/feat/per-pod-ops
**Slice(s):** Slice 3 / #35 · **Status:** in-progress

---

[G] Goal: Ship logs, exec, port-forward, describe, and YAML view/edit as explicit-context local
Kubernetes operations using the user's kubeconfig identity, with no governed intent/PEP path.
[S] Scope: source-agnostic local-operation contracts, local-kubeconfig implementation, CLI/TUI
entrypoints, safe YAML edit preview/apply, lifecycle ownership, and real two-cluster tests. Hub-mode
governed actions, fleet-wide mutation, persistence, telemetry, and the Slice-4 web UI are out.
[A] Action: Started from released `origin/dev` merge 285596a after PR #53, post-merge dev CI, the
dev-to-main release PR #54, and main CodeQL all passed. GitHub Dependabot, CodeQL, and secret-alert
queues are empty. Selected the spec's recommended D1 boundary: a separate local-apply interface,
never a capability on the governed Executor/Intent path.
[A] Action: Added a source-neutral `localops.Client` contract for object view/describe, logs,
exec, port-forward, and a distinct preview/apply boundary. Local implementation files are covered
by an AST boundary test that rejects imports of the governed connector/Intent/PEP path.
[A] Action: Implemented single-context object reads, event composition, default Secret masking,
strict identity/resourceVersion validation, server-side dry-run, and update apply through the
user's dynamic client. Direct operations bootstrap only the named kubeconfig context; they do not
probe unrelated contexts or mark full fleet discovery complete.
[A] Action: Implemented typed pod logs, WebSocket-first exec with SPDY fallback, and WebSocket/
SPDY port-forward with deterministic service-to-ready-pod and named-port resolution. Container
ambiguity fails closed, all request path segments are validated, and binds are restricted to
localhost/127.0.0.1/::1.
[T] Test: Race tests cover Secret masking/reveal, object+event describe, edit identity and dry-run
sequence, explicit-context-only bootstrap, URL-segment rejection, log/container options, exact
exec argv/query/stream wiring, loopback enforcement, service pod/port selection, session cleanup,
and the cross-package governed-path boundary. Focused golangci-lint reports zero issues.
[C] Checkpoint #1: this commit — direct local-operation engine and security boundary; next: CLI/TUI surfaces.

---

**Session close:** in progress · **Open questions touched:** D1 uses the recommended distinct local-apply boundary; D2 uses explicit local streaming interfaces outside the locked seven-verb registry
