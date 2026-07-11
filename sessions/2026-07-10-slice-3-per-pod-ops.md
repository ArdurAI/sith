# Session — 2026-07-10 — slice-3-per-pod-ops

**Builder:** Gnani Rahul · **Model/effort:** engineering, max · **Branch:** gnanirahulnutakki/feat/per-pod-ops
**Slice(s):** Slice 3 / #35 · **Status:** ready-for-PR

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
[C] Checkpoint #1: c64e8bb — direct local-operation engine and security boundary; next: CLI/TUI surfaces.
[A] Action: Added explicit-context CLI commands for describe, raw/masked YAML, logs, exec, service/
pod port-forward, and YAML edit. Exec requires a `--` argv boundary and never invokes a shell;
SIGINT owns cancellation so terminals and tunnels restore cleanly.
[A] Action: The CLI/TUI edit workflow uses a 0600, 10 MiB-bounded temporary file, invokes the
user's editor directly, shows the API server's strict dry-run diff, prompts unless `--yes` is
explicit, and revalidates via dry-run immediately before the actual update.
[A] Action: Added cache-row TUI actions `d/y/l/s/f/e`, scrollable detail/log panels, terminal
release/restore for exec and edit, resize propagation, exact scope/cursor restoration, and
persistent loopback port-forwards managed through `:pf` and `x`.
[T] Test: CLI tests prove pre-client explicit-context rejection, exact target/log/exec argument
mapping, server rejection passthrough, and preview-before-apply order. TUI tests prove action I/O
is deferred from navigation, logs/YAML return to the same row, port-forward persistence/closure,
and preview-before-apply edit. Race tests and the full linter pass.
[C] Checkpoint #2: 303544a — scriptable and interactive local-operation surfaces; next: real two-cluster proof and closure.
[A] Action: Expanded the existing digest-pinned two-kind-cluster scenario with a locally built
scratch fixture. Both clusters receive context-stamped running pods, services with named target
ports, ConfigMaps, Secrets, and related Events; no fixture image pull or external service is used.
[T] Test: The compiled Sith binary proves context-isolated logs, exact remote exec argv, YAML,
default Secret masking and explicit reveal, object+Event describe, server-dry-run ConfigMap edit
with fresh-read persistence, and real loopback HTTP forwarding through a Service in both clusters.
The complete existing+new gate passes in 65.72 seconds; temporary clusters/images were removed and
`docker system prune -f` reclaimed 1.869 GB.
[R] Review: The first real run caught and fixed a ConfigMap generic-lens regression caused by its
new static local-edit resource mapping; server print columns remain authoritative for generic
fleet rendering. Manual red-team review then found and fixed unsafe path-segment reliance and
expanded the AST no-governed-path assertion across core, adapter, CLI, and TUI files.
[R] Review: CodeRabbit CLI preflight reported not installed, so the documented local fallback was
used after a changed-surface credential scan. Full format, vet, lint, build, module verification,
race, tagged binary smoke, warm-cache p95, and govulncheck gates pass; govulncheck reports no
reachable vulnerabilities. GitHub reports zero open Dependabot, CodeQL, or secret-scanning alerts.
[A] Action: Updated README command examples, local/governed identity boundary, Secret/edit/
loopback safety, stream cost, TUI keybindings, and the expanded real-cluster gate.
[C] Checkpoint #3: this commit — real-cluster proof, documentation, and security/review closure; next: publish PR and require green CI.

---

**Session close:** ready for PR · **Open questions touched:** D1 uses the recommended distinct local-apply boundary; D2 uses explicit local streaming interfaces outside the locked seven-verb registry
