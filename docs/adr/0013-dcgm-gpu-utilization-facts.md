# ADR 0013: Privacy-minimized DCGM GPU utilization facts

- **Status:** Accepted
- **Date:** 2026-07-18
- **Issue:** [#286](https://github.com/ArdurAI/sith/issues/286)

## Context

E13 requires GPU cost/utilization columns where DCGM evidence exists, while explicitly forbidding a
physical-GPU sample from being presented as exact per-workload accounting. Sith also has no accepted
runtime owner for Prometheus/DCGM discovery, endpoint selection, authorization, persistence, cost
joins, or UI composition.

Prometheus documents `/api/v1/query` as its stable instant-query endpoint. A successful vector
contains a label set and one `[unix_timestamp, value]` sample per series, and the response can carry
warnings or info annotations alongside data. NVIDIA dcgm-exporter 4.6.0-4.8.3 was the current
release reviewed for this decision. Its default counters define `DCGM_FI_DEV_GPU_UTIL` as a percent
gauge with a product-dependent sample period. Its renderer emits whole-GPU labels and paired
`GPU_I_ID`/`GPU_I_PROFILE` labels for MIG; its Kubernetes transformation uses exact `pod`,
`namespace`, and `container` attributes. dcgm-exporter can use different collection paths for
exclusive, shared, and MIG workloads, so the presence of workload labels is evidence of exporter
attribution, not uniform accounting precision.

## Decision

1. Add a value-only `internal/connector/dcgm` projector for one already-authorized Prometheus
   instant-vector response. The caller must assert the exact expression
   `DCGM_FI_DEV_GPU_UTIL`, evaluation time, collection time, workspace, and source scope. The
   Prometheus API series limit and per-query lookback override must both be disabled, preventing an
   API-truncated vector from being asserted as complete without pretending to know server-level
   scrape freshness.
2. Accept only successful warning-free and info-free vector responses. Bind every sample timestamp
   to the asserted evaluation time and accept only canonical decimal utilization from 0 through
   100 percent.
3. Require current whole-GPU identity labels. Treat `GPU_I_ID` and `GPU_I_PROFILE` as a complete
   pair and distinguish `physical_gpu` from `mig_instance` device scope.
4. Treat `namespace`, `pod`, and `container` as an all-or-nothing workload identity. When present,
   emit `workload_best_effort` while retaining the underlying physical or MIG device scope. Never
   claim exact per-workload accounting.
5. Retain only utilization, device scope, attribution, model, paired MIG metadata, and explicit
   workload identity. Hash the selected native series identity with SHA-256. Do not retain raw GPU
   UUID, hostname, PCI bus, scrape target, job, instance, arbitrary pod labels, endpoint data, or
   credentials in payloads, display fields, or graph entities.
6. Validate unknown labels but discard them. If two native series differ only in discarded labels,
   their selected identities collide deliberately and the whole response fails instead of silently
   double-counting or collapsing evidence.
7. Bound response bytes, series, labels, label sizes, JSON depth, timestamps, encoded facts, and
   identity text. Reject malformed or duplicate-key JSON, partial label groups, ambiguous identity,
   non-finite/out-of-range values, and every late invalid series atomically.
8. Treat a successful empty vector as zero facts. It does not prove DCGM absence or complete
   coverage; a future runtime must model coverage explicitly.

## Consequences

- Sith gains a deterministic, reviewable GPU-utilization normalization seam without acquiring
  network, credential, Kubernetes RBAC, process, persistence, billing, optimization, or mutation
  authority.
- Whole-GPU, MIG, and exporter-attributed workload observations remain distinguishable without
  leaking raw hardware or scrape identity.
- Current CLI and Hub paths still cannot fetch, persist, aggregate, join with GPU cost, or display
  these facts. F13.3 and E13 remain incomplete.
- The projector does not know the exporter scrape age hidden behind Prometheus lookback semantics.
  Freshness/coverage and query-runtime design remain separate accepted-contract requirements.
- The projector cannot prove that a central Prometheus response contains only the caller-asserted
  scope; a future runtime must bind the authorized query target and coverage to exactly one scope.
- Runtime cost is bounded local CPU and memory. A future live query path will consume Prometheus
  compute and series cardinality and must be scoped, rate-limited, and observed independently.
- A future dcgm-exporter label-contract change requires a reviewed projector protocol revision;
  permissive aliasing is deliberately avoided.

## Alternatives considered

- **Add a direct Prometheus or DCGM client now:** held because endpoint provenance, TLS,
  authorization, retries, limits, and runtime ownership are unresolved.
- **Accept arbitrary PromQL:** rejected because transformed expressions can erase metric identity,
  change units, aggregate scopes, and overstate provenance.
- **Enable and retain arbitrary pod labels:** rejected because this adds pod-read RBAC,
  attacker-controlled metadata, inventory disclosure, and cardinality cost.
- **Present any pod-labelled series as exact per-pod utilization:** rejected because dcgm-exporter
  exclusive, shared, and MIG paths do not share one precision contract.
- **Infer idle GPU cost in this slice:** held until utilization, OpenCost cost, time-window,
  coverage, runtime composition, and presentation contracts are accepted together.

## Primary sources

- [Prometheus HTTP API](https://prometheus.io/docs/prometheus/latest/querying/api/)
- [NVIDIA dcgm-exporter 4.6.0-4.8.3 release](https://github.com/NVIDIA/dcgm-exporter/releases/tag/4.6.0-4.8.3)
- [NVIDIA default counters](https://github.com/NVIDIA/dcgm-exporter/blob/4.6.0-4.8.3/etc/default-counters.csv)
- [NVIDIA dcgm-exporter Kubernetes and MIG documentation](https://docs.nvidia.com/datacenter/dcgm/latest/gpu-telemetry/dcgm-exporter.html)
- [NVIDIA current renderer labels](https://github.com/NVIDIA/dcgm-exporter/blob/4.6.0-4.8.3/internal/pkg/rendermetrics/render_metrics.go)
- [NVIDIA current Kubernetes attribute names](https://github.com/NVIDIA/dcgm-exporter/blob/4.6.0-4.8.3/internal/pkg/transformation/const.go)
