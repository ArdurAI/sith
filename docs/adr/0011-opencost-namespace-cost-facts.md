# ADR 0011: Exact-decimal USD boundary for OpenCost namespace cost facts

**Status:** Accepted
**Date:** 2026-07-18
**Decision owners:** E13 / F13.1a ([#282](https://github.com/ArdurAI/sith/issues/282))

## Context

E13 needs a cost fact that can later be rolled up across clusters without inventing coverage,
mixing currencies, or turning a read integration into a billing or optimization engine. OpenCost's
allocation API returns an array of allocation sets. A namespace aggregation identifies each row by
its map key, allocation name, and properties. In v1.120.2 the response rounds amount fields to five
fractional digits, returns invalid non-finite values as `null`, and computes `totalCost` from CPU,
GPU, RAM, persistent-volume, network, load-balancer, shared, external, and adjustment values.

The allocation JSON does not carry a currency field. Collection time is also not the source
observation time: rereading an old window must not make historical cost appear fresh. Raw
allocation properties can contain provider IDs, labels, annotations, controllers, Pods, nodes, and
other metadata that this slice does not need.

## Decision

1. `internal/connector/opencost` is a pure projector over one already-authorized response. The
   trusted caller records one explicit UTC window, a step equal to that window, exact namespace
   aggregation, no filter or accumulation, and disabled idle, sharing, proportional-asset, and
   aggregated-metadata options. The projector performs no I/O or mutation.
2. The first normalized protocol is `allocation/namespace-usd-v1`. The caller must assert that the
   source values are USD; every other currency fails closed. Currency inference and conversion are
   forbidden.
3. The response must be a code-200 success with exactly one allocation set and no non-empty message
   or warning. A complete empty set produces zero facts. Every invalid row aborts the projection and
   returns no prefix.
4. The map key, allocation name, and `properties.namespace` must be identical and pass Kubernetes'
   DNS-label validation. `properties.cluster` must equal the trusted Sith scope. Synthetic idle,
   unmounted, unallocated, or other unscoped names are rejected.
5. Amounts are exact JSON decimals with at most five fractional digits and a USD 1 trillion bound.
   Base and total amounts are non-negative; adjustment fields may be negative. The projector
   recomputes OpenCost's component total with rational arithmetic and accepts at most `0.00010`
   difference, covering the upstream response-rounding envelope without binary floating-point
   drift.
6. Each row emits one deterministic `FactCost` / `LensTelemetry` fact attached to the exact cluster
   and namespace. `ObservedAt` is the allocation-window end. The allowlisted payload contains only
   namespace, window, USD, canonical five-decimal components, adjustments, and total.
7. Provider IDs, labels, annotations, controller/Pod/node identity, endpoints, collectors, unknown
   fields, and raw response metadata are discarded. Duplicate members, mixed-case aliases,
   trailing data, invalid UTF-8, excessive size/count/depth, `null` or malformed amounts, window or
   identity mismatch, and inconsistent totals fail closed.

## Consequences

- Namespace cost evidence is deterministic, private, bounded, and safe for later typed consumers.
- Historical responses retain their true window end, allowing a later F13.4 policy to derive
  staleness without a projector-selected objective.
- The USD-only contract is deliberately narrower than OpenCost deployments with another configured
  currency. Those clusters produce no fact until a source-bound currency contract is reviewed.
- GPU monetary cost is retained as a total component, but the fact makes no utilization,
  efficiency, idle-cost precision, DCGM, or MIG attribution claim.
- This slice creates no infrastructure, network call, storage, egress, logging-volume, cloud API,
  or recurring cost. A future transport must use least-privilege read-only OpenCost access and
  preserve the exact query contract.
- F13.1 is not complete until a reviewed live read path surfaces unavailable OpenCost coverage.
  F13.2 fleet/team rollup, F13.3 GPU columns, and F13.4 freshness/non-goal presentation remain open.

## Alternatives considered

- **Build the HTTP client and projector together:** rejected because endpoint discovery,
  authorization, TLS, request budgets, and availability are separate operational contracts.
- **Use `float64`:** rejected because stable fact identity and total validation require
  deterministic decimal behavior.
- **Infer USD or accept an unspecified currency:** rejected because future rollups must never mix
  unproven units.
- **Use collection time as `ObservedAt`:** rejected because it would make a historical window look
  fresh after rereading it.
- **Retain all OpenCost fields for future flexibility:** rejected because unneeded provider and
  workload metadata widens privacy and correlation risk.
- **Project synthetic idle or unmounted rows:** rejected because F13.1a promises only facts attached
  to real Kubernetes namespaces.

## Primary references

- [OpenCost allocation API](https://opencost.io/docs/integrations/api/)
- [OpenCost v1.120.2](https://github.com/opencost/opencost/releases/tag/v1.120.2)
- [OpenCost allocation response implementation](https://github.com/opencost/opencost/blob/v1.120.2/core/pkg/opencost/allocation_json.go)
- [OpenCost total-cost implementation](https://github.com/opencost/opencost/blob/v1.120.2/core/pkg/opencost/allocation.go)
- [OpenCost HTTP response envelope](https://github.com/opencost/opencost/blob/v1.120.2/core/pkg/protocol/http.go)
- [OpenCost API schema](https://github.com/opencost/opencost/blob/v1.120.2/docs/swagger.json)
