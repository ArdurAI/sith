# E14 R7 honest image-pull failure rule

**Builder:** Gnani Rahul · **Branch:** `gnanirahulnutakki/feat/e14-image-pull-rule`

**Slice:** E14 R7 / [#268](https://github.com/ArdurAI/sith/issues/268) · **Status:** locally verified

**Base:** `origin/dev` at `91f4a47da7691843fd9e00b257849e63243646fd`

## [G] Goal

Add deterministic, evidence-cited detection for exact Kubernetes `ImagePullBackOff` and
`ErrImagePull` waiting reasons without guessing the underlying cause.

## [S] Boundary

- Reuse only sanitized LIVE `pod.reason` evidence already projected from bounded Pod status.
- Emit a sensitive-marked, read-only `kubectl describe pod` advisory that Sith never runs.
- Do not retain image references, registry credentials, Secrets, Event messages, or raw payloads.
- Do not claim registry auth, inspect credentials, probe a registry/network, add a connector,
  correlate fleet-wide, create a typed intent, or bypass blocked F14.6.

## [T] Verification plan

- Exact-reason, near-miss, stale-LIVE abstention, cache projection, replay determinism/schema, CLI,
  and no-write-boundary tests.
- Full local, security, CodeRabbit, signed DCO/GSTACK, exact-head, merge, and exact post-merge gates.

## [R] Primary-source check

- Kubernetes documents `Waiting` as the container state used while startup operations such as an
  image pull are incomplete, with a `Reason` that summarizes the state:
  <https://kubernetes.io/docs/concepts/workloads/pods/pod-lifecycle/#container-states>.
- Kubernetes recommends `kubectl describe pods` to inspect container state and recent Events and
  notes that image-pull failure is a common reason for a waiting Pod:
  <https://kubernetes.io/docs/tasks/debug/debug-application/debug-pods/>.

These sources support symptom detection and the read-only inspection command. They do not support
choosing a cause from the waiting reason alone, so R7 retains the full uncertainty boundary.

## [C] Cost and operability

R7 reuses the existing bounded cache and pure in-memory evaluator. It adds no cloud resource,
credential, network call, watch, storage, telemetry volume, or egress cost. Operator-run
`kubectl describe pod` may expose sensitive detail, so the advisory is explicitly marked sensitive
and is never executed by the brain.

## [V] Local verification — 2026-07-18

- Exact-reason, canonical/lowercase case-folding, hostile near-miss, stale/unavailable LIVE,
  citation identity, shell-quoted advisory, no-PR-diff, cache projection, replay, CLI text/JSON,
  and no-fleet-correlation tests pass under the race detector.
- Full `make ci` passes: formatting, vet, lint, `govulncheck`, repository race coverage, shell
  policies, pinned Prometheus rule fixtures, performance, end-to-end tests, and binary build.
- PostgreSQL 18.4 forced-RLS/isolation tests plus two 50,000-case workspace fuzz campaigns pass.
- Reproducible release/archive/SPDX-SBOM and immutable two-platform OCI layout verification pass.
- Helm 4.2.3, standalone cross-platform OCI, and digest-pinned Kubernetes v1.36.1 two-cluster kind
  gates pass; kind completed in 238.980 seconds and left no clusters or Sith test containers.
- GitHub secret scanning found zero secrets in 22,207 changed bytes.
- CodeRabbit CLI 0.6.5 reviewed all 13 changed/new files against exact base
  `91f4a47da7691843fd9e00b257849e63243646fd`. Its valid R7-identity assertion finding is fixed;
  its request to reject lowercase `errimagepull` was declined because the issue explicitly requires
  exact case-insensitive matching. The second complete review reports zero findings.
- README review is complete. The investigation section documents R7's uncertainty, sensitive
  read-only advisory, retained-data exclusions, and explicit exemption from fleet correlation.

Remaining before completion: signed DCO/GSTACK commit, push and PR into `dev`, exact-head
CI/CodeQL plus empty review/security queues, merge preserving the tested head, and exact
post-merge `dev` CI/CodeQL proof.
