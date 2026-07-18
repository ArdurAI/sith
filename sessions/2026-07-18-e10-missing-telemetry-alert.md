# Session — 2026-07-18 — E10 missing Hub telemetry alert

**Builder:** Gnani Rahul · **Branch:** `gnanirahulnutakki/feat/e10-missing-telemetry-alert`
**Slice:** E10 F10.4d / [#254](https://github.com/ArdurAI/sith/issues/254) · **Status:** ready for commit
**Decision record:** [Notion](https://app.notion.com/p/3a12637edb07814a95a1f70de98f1ba1)

## [G] Goal

Detect total loss of expected Sith Hub telemetry at the portable rule evaluator without adding a
runtime heartbeat, depending on operator-specific target labels, or claiming that a white-box rule
can prove its own notification path.

## [R] Research

- Prometheus documents `absent_over_time` for detecting when no series exists during a bounded
  interval.
- Prometheus metamonitoring guidance calls for confidence that monitoring is working and recommends
  external black-box coverage for failures invisible to the internal stack.
- The existing `sith_build_info` gauge is set to one at metrics construction, requires no traffic,
  and is registered atomically with the isolated Sith registry.

## [D] Decision

- Alert on aggregate absence of all `sith_build_info` samples over ten minutes, continuously for a
  five-minute hold.
- Emit at most one warning with static labels and annotations; no source labels may propagate.
- Treat loading the portable rule package as the operator's declaration that a Hub scrape/forwarding
  path is expected. Environments without an expected Hub must not load it.
- Keep `up`, job/instance naming, Kubernetes metrics and CRDs, alert routing, and full-path synthetic
  monitoring outside Sith's portable contract.

## [S] Security, operability, and cost boundary

No tenant, workspace, spoke, actor, request, endpoint, credential, target, or raw-error label is
added. The slice adds one range-vector rule evaluation per minute and at most one alert instance. It
adds no listener, Service, ServiceMonitor, PrometheusRule, exporter, remote write, storage, cloud
resource, spoke egress, or action capability.

## [T] Verification plan

- Pinned Prometheus 3.13.1 parse and fixture tests for initial absence, hold timing, current/recent
  presence, transient gaps, sustained disappearance, multiple series, hostile labels, firing, and
  recovery.
- Go contract for the exact expression, hold, static output, six-rule group bound, and runbook link.
- Full race/CI, vulnerability, forced PostgreSQL/RLS isolation, release/SBOM/OCI, Helm, real Kind,
  CodeRabbit, CodeQL, security queues, and exact post-merge `dev` proof.

## [A] Progress

- Revalidated live `dev`, E10 issue state, duplicates, the traffic-independent sentinel, and primary
  Prometheus guidance.
- Created issue 254 as an E10 sub-issue, the Notion decision page, the Obsidian decision/checkpoint,
  and this isolated EXTENDED-drive worktree.
- Added the sixth portable rule, deterministic timing fixtures, a static Go contract, README and
  EPICS status, and an operator runbook with an explicit expected-environment precondition.

## [V] Local evidence

- Pinned Prometheus 3.13.1 parses all six rules. Fixtures prove initial hold timing, current and
  recent presence, a bounded transient gap, sustained disappearance, multiple series, hostile
  source labels, firing, recovery, and static output.
- Full `make ci` passes formatting, vet, zero-finding lint, reachable-vulnerability scanning, all
  race tests, shell policy checks, the six-rule contract, performance, binary E2E, and build.
- Forced PostgreSQL 18.4/RLS isolation passes with `hubdb` coverage at 75.9%; both cross-workspace
  fuzz campaigns complete 50,000 executions.
- Reproducible release archives, SPDX SBOMs, Homebrew formula, release-derived multi-architecture
  OCI, standalone OCI, and pinned Helm 4.2.3 contracts pass.
- Digest-pinned Kubernetes v1.36.1 two-cluster Kind validation passes in 236.578 seconds.
- CodeRabbit CLI 0.6.5 found one minor structural-test gap: the Go contract did not pin the new
  annotation strings independently of the YAML fixture. The contract now pins both exact strings;
  focused race and Prometheus tests pass, and the second complete seven-file review has no findings.

## [C] Checkpoint 1

Implementation and all local gates are complete. Next: create one signed DCO/GSTACK commit, push,
open the PR into `dev`, and hold completion until exact-head and post-merge CI, CodeQL, review, and
security queues are green.
