# Session — 2026-07-10 — slice-1-source-adapter

**Builder:** Gnani Rahul · **Model/effort:** engineering, max · **Branch:** feat/fleet-source-adapter
**Slice(s):** Slice 1 / #38 + #32 · **Status:** done

---

[G] Goal: Implement the source-abstract fleet model, seven-verb connector contract, local-kubeconfig
adapter, independent fan-out, and a real two-kind-cluster proof for Slice 1.
[S] Scope: additive `internal/fleet` types, `internal/connector`, the kubeconfig read adapter,
`fleet.Source` bridge, the one CLI injection point, unit tests, and kind e2e. Cache-first TUI,
per-pod operations, web UI, MCP, keychain, OCM transport, and governed writes are out of scope.
[A] Action: Merged Slice 0 and authoritative specification PRs into `dev`, promoted the tested
foundation to `main` through release PR #51, and branched `feat/fleet-source-adapter` from tested
`dev` tip `a9bf340`.
[A] Action: Verified client-go v0.36.2 as the current upstream module and kind v0.32.0 with the
digest-pinned Kubernetes v1.36.1 node image. ExecCredential v1 behavior remains delegated to
client-go so plugins execute locally and tokens are never persisted by Sith.
[A] Action: Added the source-abstract resource/fact/query/diff/graph model, additive stale coverage,
the seven capability interfaces, closed connector taxonomy/action vocabulary, atomic registry, and
the `connector.Reader` to `fleet.Source` bridge.
[T] Test: Race-enabled fleet/connector unit tests and the strict linter pass. Tests prove identity
equality, fail-safe query validation, capability declaration+implementation checks, atomic invalid
registration, deterministic lookup, typed-action isolation, and coverage-preserving source parity.
[C] Checkpoint #1: 7ad0759 — additive fleet and connector contract; next: local-kubeconfig
adapter and client-go fan-out.
[A] Action: Current client-go v0.36.2 requires Go 1.26, so raised the module and CI toolchain from
Go 1.25 to the supported Go 1.26 line instead of pinning an older Kubernetes client.
[T] Test: Rebuilt golangci-lint v2.12.2 with Go 1.26.5; the complete `make ci` gate passes on the
new toolchain with no code or output changes.
[C] Checkpoint #2: a53d262 — adopt the supported Go 1.26 toolchain required by current
client-go; next: implement the adapter.
[A] Action: Implemented the read-only local-kubeconfig adapter with independent bounded context
probes, dynamic clients, typed inventory reads/queries, explicit partial coverage, and preserved
last-seen timestamps when a previously reachable context becomes unavailable.
[T] Test: Adapter tests exercise concurrent success/failure, stale observation preservation,
typed label/name/image selectors, source-stamped evidence, unknown/unreachable reads, and an actual
ExecCredential v1 subprocess authenticated request to a TLS test API. Focused race tests, lint,
and 81.5% statement coverage pass.
[C] Checkpoint #3: bad1a1f — local-kubeconfig discovery/read/query adapter; next: bridge the
adapter into `sith clusters` and validate the real CLI path.
[A] Action: Replaced the Slice-0 stub at the single CLI injection point with
`connector.AsSource(kubeconfig.Default())`; default construction follows client-go's standard
`KUBECONFIG` path-list and `~/.kube/config` resolution without doing startup network I/O.
[A] Action: Updated the public README from the Slice-0 stub behavior to the real local-fleet
discovery and credential-locality contract.
[C] Checkpoint #4: 87053ca — production CLI bridge; next: prove two reachable kind clusters
plus one unreachable context through the built binary.
[A] Action: Added a hermetic real-cluster gate that creates two uniquely named kind clusters from
the digest-pinned Kubernetes v1.36.1 node image, merges their kubeconfigs with one deliberately
dead context, and cleans up only the clusters it created.
[T] Test: The gate asserts adapter discovery, a real namespace query returning source-stamped
facts from both API servers, honest 2/3 partial coverage, and the built `sith clusters --output
json` process over the same merged kubeconfig. CI installs pinned kind v0.32.0 before running it.
[R] Review: Red-team analysis added hard wall-clock isolation around client-go operations because
its exec authenticator does not itself bind helper-process lifetime to request context, rejected
invalid references/selectors before credential work, and made partial kind cleanup observable.
[R] Review: govulncheck v1.6.0 found two reachable `x/net` call-path vulnerabilities inherited
through client-go. Raised `x/net` to the fixed v0.55.0 floor and added a pinned CI/local scan;
the follow-up scan reports no reachable vulnerabilities.
[C] Checkpoint #5: this commit — reviewed real two-cluster fan-out gate; next: publish and merge.

---

**Session close:** implementation and review complete; publication pending · **Open questions touched:** none
