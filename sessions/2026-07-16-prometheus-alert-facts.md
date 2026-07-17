# Prometheus alert facts

- Builder: gnanirahulnutakki
- Effort: deep
- Branch: `gnanirahulnutakki/feat/prometheus-alert-facts`
- Issue: `#209`
- Status: ready for review

## Goal

Define Sith's first Prometheus normalization contract by projecting an already-authorized
`GET /api/v1/alerts` success response into bounded TELEMETRY facts without adding an HTTP client,
credential seam, telemetry store, or mutation path.

## Scope

- Accept only the documented success envelope and active-alert fields.
- Treat caller-provided workspace, cluster scope, and observation time as the trust boundary.
- Retain only `alertname` plus a small correlation allowlist; discard annotations, unknown labels,
  and any response-provided cluster identity.
- Attach only one unambiguous Pod, Node, Deployment, StatefulSet, or DaemonSet identity. Preserve
  every other valid alert as unattached evidence instead of guessing.
- Bound raw bytes, JSON depth, alerts, labels, label bytes, facts, and encoded payloads before
  integration code.
- Keep live fetching, authentication, endpoint discovery, persistence, and the E12 framework out of
  this contract-defining slice.

## Decision

Prometheus alert labels are untrusted evidence, not tenancy. The projector hashes only the
allowlisted, normalized alert identity into provenance, derives a safe graph resource name from the
same digest, rejects collisions within one response, and never permits a `cluster` label to replace
the trusted scope. Annotations are decoded only to validate the documented response shape and are
never projected or fingerprinted.

The alert `state` and finite value remain observations, while `activeAt` plus the allowlisted label
identity define one active alert instance. Input order therefore cannot change output order or
identity. Missing Kubernetes identity is valid abstention; multiple identity candidates are invalid
ambiguity and fail the entire projection.

## Trade-offs

- The Prometheus alerts endpoint is documented under API v1 but explicitly has weaker stability
  guarantees than the overarching v1 API. `ProtocolVersion = alerts/v1` isolates that source shape
  so future additive handling does not silently redefine normalized facts.
- Dropping unknown labels protects the privacy boundary but can collapse two source alerts that
  differ only by discarded labels. A same-response collision fails closed instead of coalescing;
  widening the allowlist requires a deliberate contract review.
- This pure projector creates no infrastructure and no runtime cloud cost. A future live adapter
  will add request volume, Prometheus query load, authentication, availability, and egress concerns.

## Verification

- Focused unit and graph tests pass with 94.0% statement coverage.
- The race-enabled package test passes.
- Adversarial coverage includes duplicate/trailing/malformed JSON, invalid UTF-8 and control data,
  excessive nesting, non-finite values, unsupported states, ambiguous identities, secret
  non-retention, deterministic ordering, empty-result abstention, and every declared size/count
  budget.
- The source boundary test rejects network, gRPC, client-go, process execution, plugin/syscall, and
  mutation seams.
- `README.md` was reviewed. No user-facing command, configuration, endpoint, or runtime behavior is
  introduced, so the roadmap and this checkpoint are the appropriate documentation surfaces.
- `make ci` passes formatting, vet, pinned lint with zero findings, the current vulnerability scan
  with no findings, the complete race suite, all shell policy suites, warm-cache performance,
  subprocess E2E, and the production build.
- `make e2e-isolation` passes PostgreSQL/RLS and both 50,000-execution workspace-isolation fuzzers.
- `make e2e-kind` passes in 242.829 seconds with repository-pinned kind v0.32.0 and Kubernetes
  v1.36.1. The machine's installed kind v0.30.0 failed first because it cannot load images into the
  pinned node's containerd config version 4; no source correction was required.
- `make release-check` passes two reproducible snapshots, archive and SPDX SBOM verification,
  Homebrew formula generation, and the multi-platform distroless OCI layout. An isolated temporary
  `GOPATH` was required because this machine's configured GOPATH root contains another `go.mod`,
  which caused Go 1.26.5 `go mod verify` to lose the worktree module context.
- Notion decision log: `https://app.notion.com/p/3a02637edb07814e89d7c416173ee653`.
- Notion session checkpoint: `https://app.notion.com/p/3a02637edb0781d3b512eb8fc68d9460`.
- Hosted PR and exact post-merge evidence remain pending.

## Checkpoint

- `2026-07-16/prometheus-alert-facts#1`
- `2026-07-16/prometheus-alert-facts#2`

## Open questions

- None for this slice. Live endpoint discovery and authentication belong to a later connector issue.
