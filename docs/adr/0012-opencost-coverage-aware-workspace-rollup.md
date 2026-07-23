# ADR 0012: Coverage-aware workspace rollup for OpenCost cost facts

**Status:** Accepted
**Date:** 2026-07-18
**Decision owners:** E13 / F13.2a ([#284](https://github.com/ArdurAI/sith/issues/284))

## Context

ADR 0011 and issue 282 define an exact-decimal USD projector for one already-authorized OpenCost
namespace-allocation response. A successful response may contain an empty allocation map and
correctly emit zero facts. A later fleet rollup cannot infer from zero facts whether the cluster
reported an empty result or never reported at all. Treating both cases as zero cost would violate
E13's central coverage guardrail.

The live access path is also unresolved. OpenCost documents its API on port 9003 through an
operator-run Kubernetes port-forward and notes that deployments may expose a Service or Ingress.
Sith has local, Hub, and security-held OCM environments, but no accepted contract assigns OpenCost
endpoint discovery, authentication, TLS, or credential forwarding to one of them. Those choices
must not leak into the normalization or aggregation core.

Per-team grouping is similarly premature: the normalized cost fact deliberately discards labels,
and Sith does not yet have a canonical team-attribution identity for a namespace. Guessing from a
workload or arbitrary label would create unstable cross-tenant accounting semantics.

## Decision

1. `ProjectNamespaceCostSnapshot` wraps a successful F13.1a projection in a value-only envelope
   containing its exact workspace, cluster scope, UTC window, trusted USD unit, and facts. Presence
   of the snapshot is the reporting signal; an empty fact slice is a successful empty report.
2. `RollupWorkspaceCosts` accepts one explicit expected-scope set plus at most one successful
   snapshot per reporting scope. Expected scopes are bounded, unique, and caller-authoritative.
3. Every snapshot must match the requested workspace, exact UTC window, and USD unit. Every fact is
   revalidated against the closed cost taxonomy, TELEMETRY lens, cluster/namespace entity,
   OpenCost provenance and protocol, canonical payload bytes, native SHA-256 identity, and source
   observation time.
4. Invalid, duplicate, foreign, stale-marked, oversized, or ambiguous input aborts the entire
   operation and returns no partial rollup. Duplicate namespaces within one cluster are rejected.
5. All fifteen component, adjustment, and total values are parsed as exact rational decimals and
   summed independently. Output uses canonical five-decimal strings; binary floating point is not
   used.
6. Coverage separately names expected, reported, successful-empty, and missing scopes. A missing
   scope contributes no fact and no synthetic zero. `complete` is true only when every expected
   scope has a successful snapshot.
7. The rollup preserves the allocation-window end as `observed_at` when at least one scope
   reported. With no report, `observed_at` is absent. No collection time or stale objective is
   invented.
8. The computation is bounded to 256 scopes, 1,024 facts per scope, 4,096 facts total, 8 MiB of
   normalized payload, and a 256 KiB result. Each of the fifteen cost fields is accumulated and
   checked independently: one fact's absolute field value cannot exceed `1,000,000,000,000`, and
   one rollup's absolute field total cannot exceed `4,096,000,000,000,000` (the per-fact limit
   multiplied by the 4,096-fact limit).

## Consequences

- A workspace total can never silently present partial OpenCost coverage as complete.
- Successful empty reports remain distinguishable from unavailable OpenCost without retaining raw
  responses or adding a sentinel fact.
- The result retains aggregate amounts, coverage metadata (expected, reported, successful-empty,
  and missing categories plus `complete`), and optional `observed_at` only. Namespace names,
  provider IDs, labels, annotations, workload identity, endpoints, credentials, and unknown source
  fields do not survive.
- Historical evidence remains tied to its source window, allowing F13.4 to select a freshness
  objective later without retroactively changing fact semantics.
- This is an offline workspace computation core. It does not provide the live F13.1 adapter,
  persistence, Hub/runtime composition, an API or UI, team rollups, or F13.2 completion.
- Runtime expense is bounded local CPU and memory. The slice creates no cloud resource, network
  call, storage, telemetry-volume, egress, or recurring-service cost.

## Alternatives considered

- **Treat zero facts as zero cost:** rejected because it conflates successful empty coverage with a
  missing cluster.
- **Emit a synthetic zero-cost fact:** rejected because a sentinel would look like observed
  namespace cost and contaminate the fact model.
- **Accept arbitrary OpenCost URLs and credentials in the core:** held because this requires an
  explicit SSRF, redirect, TLS, endpoint-provenance, and credential-forwarding decision.
- **Use the Kubernetes Service proxy:** held because it adds `services/proxy` RBAC and does not
  solve the security-held OCM transport.
- **Group by an arbitrary team label now:** rejected because no canonical, tenant-scoped team
  identity exists and F13.1a intentionally discards labels.
- **Use `float64`:** rejected because deterministic fleet totals require exact decimal behavior.
- **Stamp collection time or choose a stale threshold:** rejected because rereading historical
  evidence must not make it fresh, and the objective belongs to F13.4.

## Primary references

- [OpenCost allocation API](https://opencost.io/docs/integrations/api/)
- [OpenCost installation and access](https://opencost.io/docs/installation/install/)
- [OpenCost v1.120.2](https://github.com/opencost/opencost/releases/tag/v1.120.2)
- [ADR 0011](0011-opencost-namespace-cost-facts.md)
- [F13.1a issue 282](https://github.com/ArdurAI/sith/issues/282)
- [E13 transport escalation](https://github.com/ArdurAI/sith/issues/31#issuecomment-5013914477)
