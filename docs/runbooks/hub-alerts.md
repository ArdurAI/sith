# Sith hub alert runbook

These alerts consume only Sith's fixed-cardinality, process-wide metrics. They are a portable
F10.4a/F10.4b/F10.4c baseline, not the complete F10.4 SLO and error-budget contract. The fleet-read alert
covers sustained aggregate coverage degradation but does not establish snapshot-age freshness.
The rules do not cover governed dispatch success, PDP latency, KMS/signing, or the availability of
the scrape and notification pipeline.

The rules aggregate away every input-series label before producing an alert. Configure stable
Prometheus external labels if the notification must identify an environment. Do not add workspace,
spoke, actor, trace, intent, request, credential, endpoint, selector, or raw-error labels.

## Installation and validation

Sith does not install Prometheus, Alertmanager, a Prometheus Operator CRD, or a remotely reachable
metrics Service. First arrange an operator-owned same-Pod collector that scrapes the exact-loopback
endpoint described in the README and forwards the existing series to a rule evaluator. Then copy
`monitoring/sith-hub.rules.yml` into that evaluator's rule-file path and reload it using the
evaluator's documented mechanism.

Validate the unchanged file before loading it:

```bash
promtool check rules monitoring/sith-hub.rules.yml
cd monitoring && promtool test rules sith-hub.rules.test.yml
```

Notification routing and receivers remain operator-owned. Confirm the complete scrape → rule
evaluation → Alertmanager → receiver path with an external synthetic test; these white-box rules
cannot detect failure of their own monitoring path.

## SithHubPolicyAuditFailure

**Meaning.** A durable database append or structured process-log delivery failed. Sith deliberately
fails the governed read closed, so this is an immediate user-visible availability symptom.

**Triage.**

1. Check whether the `durable` or `process` error counter increased; do not attach raw errors or
   tenant identifiers to the alert.
2. For `durable`, check PostgreSQL availability, connection saturation, forced-RLS migration state,
   and storage health through the database operator's own observability surface.
3. For `process`, check whether the supervised local audit consumer is running and draining stderr;
   verify its configured command and resource limits without printing credentials or request data.
4. Restore the failed sink, then perform one authorized read and confirm both sink success counters
   increase in database-before-process order.

Do not bypass either sink to recover availability. That would turn an observable outage into an
unlogged authorization path.

## SithHubAuthRefusalDeliveryDrop

**Meaning.** Authentication was still refused, but its bounded structured warning could not be
delivered to the supervised local consumer. Authorization remains fail-safe; security evidence is
degraded.

**Triage.**

1. Check the supervised consumer's process state, stderr drain, restart behavior, and resource
   pressure. Do not log the rejected credential, header, path, client address, principal, or
   verifier error.
2. Confirm the drop counter stops increasing after delivery recovers.
3. Review the operator-owned log pipeline for matching gaps. Treat the metric as a signal, not as a
   reconstruction of the missing records.

## SithHubFederationSnapshotFailureRatioHigh

**Meaning.** More than 5% of at least 20 aggregate snapshot attempts failed over 15 minutes, and
the condition persisted for 10 minutes. The alert is a warning because current metrics intentionally
do not identify a tenant or spoke, and the threshold is an operational baseline rather than a
formal read-freshness SLO.

**Triage.**

1. Break down `sith_federation_spoke_snapshot_attempts_total` by its closed `outcome` label to
   distinguish transport, deadline, invalid-snapshot, store-error, and canceled failures.
2. Use the tenant-scoped fleet APIs and coverage assessment—not metric labels—to identify stale or
   unreachable scopes. Keep authorization and RLS boundaries intact.
3. For transport/deadline failures, inspect OCM addon health, projected service-account rotation,
   tunnel connectivity, and the hub's egress policy. For store errors, inspect PostgreSQL health.
4. Confirm the success counter resumes and the failure ratio falls naturally. Do not delete stale
   evidence or force a refresh outside the governed read path.

## SithHubFleetReadCoverageDegradationHigh

**Meaning.** More than 5% of at least 20 aggregate eligible fleet reads were `degraded` or `error`
over 15 minutes, and the condition persisted for 10 minutes. Eligible outcomes are `complete`,
`degraded`, and `error`. `degraded` includes incomplete or internally inconsistent coverage;
`error` means the authorized persisted read failed. A `complete` outcome means the existing coverage
contract reported no gaps—it does not guarantee snapshot age. Legitimate internally consistent
`empty` reads are excluded from the ratio.

**Triage.**

1. Compare the aggregate `degraded` and `error` counters. Do not add tenant, workspace, spoke,
   resource, principal, trace, endpoint, age, or raw-error labels to the metric or alert.
2. For `degraded`, use the tenant-scoped fleet API and its named stale, unreachable, truncated,
   unaccounted, and inconsistent coverage gaps to identify affected scopes. Keep PEP and RLS
   boundaries intact; the process-wide alert cannot safely identify them.
3. For `error`, inspect PostgreSQL availability, connection saturation, migrations, and the Hub
   process through their operator-owned observability surfaces. Do not expose wrapped reader errors
   as metric labels or alert annotations.
4. Confirm eligible reads resume with `complete` outcomes and the ratio falls naturally. Do not
   suppress evidence by treating degraded reads as complete, excluding scopes, bypassing the PEP,
   or weakening RLS.

## SithHubDatabaseReadinessDegradationHigh

**Meaning.** More than 5% of at least 20 aggregate completed database readiness checks were
`unavailable` over 15 minutes, and the condition persisted for 10 minutes. The alert is a warning:
it reports sustained failure of the Hub's application-pool PostgreSQL ping, not a formal availability
objective, fleet-freshness signal, or proof that the scrape and notification path is healthy.

**Triage.**

1. Compare the aggregate `ready` and `unavailable` counters. Do not add request, endpoint, tenant,
   workspace, spoke, credential, error, panic, or database detail to the metric or alert.
2. Inspect PostgreSQL availability, connection saturation, forced-RLS migration state, storage, and
   network policy through operator-owned surfaces. Keep credentials and query/error text out of logs.
3. Confirm Kubernetes is removing unready Pods from service and that healthy replicas retain enough
   capacity. Do not weaken or bypass `/readyz` to recover traffic.
4. After the database path recovers, confirm `ready` checks resume and the ratio resolves naturally.
   Separately test the external scrape-to-receiver path because this white-box rule cannot detect its
   own absence.

## Threshold and cost notes

The five expressions evaluate once per minute over existing series and produce at most five alert
instances per rule evaluator. They create no recording series, listener, exporter, remote-write
path, or storage. Both ratio alerts use a 5% threshold and a 10-minute hold, with the minimum volume
defined independently by each expression. The snapshot ratio requires 20 aggregate attempts. The
fleet-read ratio requires 20 eligible `complete|degraded|error` reads and excludes `empty` from
numerator and denominator, so legitimate zero-scope traffic neither fires nor hides the alert.
The database-readiness ratio requires 20 aggregate `ready|unavailable` checks, uses the same 5%
threshold and 10-minute hold, and cannot divide by zero.
Change these thresholds only through a reviewed local override backed by observed traffic and an
explicit response owner. Full SLO thresholds and burn-rate alerts wait for negotiated objectives
and the missing freshness, dispatch, and PDP production signals.
