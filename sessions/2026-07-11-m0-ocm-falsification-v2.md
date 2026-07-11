# Session — 2026-07-11 — m0-ocm-falsification-v2

**Builder:** Gnani Rahul · **Model/effort:** GPT-5, max · **Branch:** gnanirahulnutakki/exp/m0-ocm-falsification-v2
**Slice(s):** Milestone-0 / E0 / issues #2–#6 · **Status:** in-progress

---

[G] Goal: Revalidate the OCM falsification decision against the filed Milestone-0 acceptance criteria: one hub plus two spokes, pinned `cluster-proxy` and `managed-serviceaccount`, spoke-local reach through scoped projected tokens, and outbound-only proof.
[S] Scope: A disposable three-cluster kind lab, a fail-closed reproducible experiment runner, corrected ADR/runbook evidence, and issue closure. Phase-1 product code, hub services, and deployment packaging are out of scope.
[A] Action: Reconciled live issue #2–#6 and stale PR #13 with `dev`. The retained experiment document had been incorporated through the planning baseline, but explicitly tested one spoke while the authoritative roadmap requires two; selected a fresh three-cluster run rather than treating the earlier claim as sufficient.
[T] Test: Current tools and upstream pins verified: kind v0.32.0, digest-pinned Kubernetes v1.36.1 nodes, clusteradm/OCM v1.3.1, Helm v4.1.4, `cluster-proxy` and `managed-serviceaccount` charts v0.10.0. Both chart versions remain the latest published upstream releases; their downloaded archive SHA-256 values are pinned by the runner.
[A] Action: Built the live hub + `spoke-a` + `spoke-b` lab with isolated kubeconfig, Helm state, and scratch on `/Volumes/EXTENDED`. Registered both spokes, invalidated the ephemeral bootstrap credential after one diagnostic path exposed it, and enabled both addon pairs. Confirmed two upstream chart defects: `cluster-proxy`'s CRD/schema skew and same-namespace `ManagedClusterSetBinding/global` ownership conflict; applied the narrow schema compatibility patch and isolated addon Helm ownership by namespace.
[T] Test: Both `ManagedCluster` resources and all four `ManagedClusterAddOn` resources report `Available`; `clusteradm proxy health` probes both tunnels. Projected `sith-reader` tokens reach distinct local fixtures on both spokes while actual-token requests for cluster-wide secrets and nodes return `Forbidden` for the expected ServiceAccount identity. Conntrack original-direction tuples show 4 and 5 spoke-pod flows to hub `:6443`, with zero hub-originated flows into either spoke.
[A] Action: Added a fail-closed M0 runner that pins node and chart digests, keeps all credentials isolated, never prints tokens, invalidates the registration credential after join, detects the chart caveats, verifies both positive and negative authorization controls, and tears down by default.
[T] Test: `bash -n`, ShellCheck, help output, and `git diff --check` pass. The runner first verified the interactively assembled lab, then its cleanup path removed all three clusters. A clean-room `run` recreated the complete environment from zero and returned `M0_RESULT=PASS` in 158 seconds. A 1.2 KiB asciicast captured the retained lab's health, scoped reach/denial, and outbound-only summary without credentials or user input.
[C] Checkpoint #1: reproducible three-cluster M0 runner — next: correct the ADR/runbook evidence, review, PR, and issue closure.

---

**Session close:** in progress · **Open questions touched:** none
