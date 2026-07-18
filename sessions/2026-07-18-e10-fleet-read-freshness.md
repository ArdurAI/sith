# E10 F10.1f fleet-read freshness outcomes

## Scope

Issue [#260](https://github.com/ArdurAI/sith/issues/260) adds one bounded request-time freshness
result alongside the existing fleet-read coverage result. It does not add continuous monitoring,
per-spoke labels, an alert, an SLO, or an error budget.

## Decision

- Emit one validated `FleetReadObservation` after every authorized persisted read.
- Keep the existing coverage outcomes and add `fresh`, `stale`, `unknown`, `empty`, and `error`.
- Require a structurally valid cluster set with unique identities. Treat a validated stale scope
  with retained observation time as `stale`; use `unknown` for mismatched, invalid, or unobserved
  results and other non-stale degradation.
- Increment both counters only when the complete pair is valid, and isolate observer panic from the
  read result.
- Preinitialize all five freshness series and expose no tenant-proportional label.

## Cost and security boundary

The change adds five fixed process-local counter series and one increment per authorized read. It
adds no listener, Service, monitoring CRD, exporter, remote write, persistence, background task,
credential path, network request, or cloud resource. Workspace, identity, trace, request, spoke,
cluster, resource, selector, endpoint, credential, age, and raw-error dimensions remain absent.

## Primary references

- [Prometheus instrumentation](https://prometheus.io/docs/practices/instrumentation/)
- [Prometheus metric and label naming](https://prometheus.io/docs/practices/naming/)

## Verification status

- Focused race suites and full CI pass with zero lint findings and no reachable vulnerabilities.
- PostgreSQL 18.4 forced-RLS focused coverage is 72.8%; isolation coverage is 76.2%.
- Both 50,000-case workspace-isolation fuzz campaigns pass.
- Reproducible release archives, SPDX SBOMs, release OCI layout, Helm 4.2.3, and cross-platform OCI
  pass.
- Kubernetes v1.36.1 two-cluster Kind passes in 237.070 seconds.
- CodeRabbit found and drove fixes for unobserved timestamps, incomplete outcome coverage, and stale
  per-spoke documentation. The second complete 10-file review has no findings.

Hosted exact-head and post-merge gates remain required before closure.
