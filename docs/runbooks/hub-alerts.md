# Sith hub alert runbook

These alerts consume only Sith's fixed-cardinality, process-wide metrics. They are a portable
F10.4a/F10.4b/F10.4c/F10.4d/F10.4e/F10.4f/F10.4g baseline, not the complete F10.4 SLO and error-budget
contract.
The two fleet-read alerts cover sustained aggregate coverage degradation and proven stale reads;
neither defines a freshness objective or continuously monitors snapshot age without request traffic.
The rules do not cover governed dispatch success, PDP latency, or KMS/signing. The missing-telemetry
warning detects loss of expected Sith samples only while its rule evaluator remains healthy; it
cannot prove the evaluator, Alertmanager, receiver, or complete notification path is available.
The authentication warning reports only sustained refusal-only aggregate traffic. It cannot
attribute a caller or distinguish an attack from expired credentials, rollout drift, or another
authentication-path outage.

The rules aggregate away every input-series label before producing an alert. Configure stable
Prometheus external labels if the notification must identify an environment. Do not add workspace,
spoke, actor, trace, intent, request, credential, endpoint, selector, or raw-error labels.

## Installation and validation

Sith does not install Prometheus, Alertmanager, a Prometheus Operator CRD, or a remotely reachable
metrics Service. First arrange an operator-owned same-Pod collector that scrapes the exact-loopback
endpoint described in the README and forwards the existing series to a rule evaluator. Then copy
`monitoring/sith-hub.rules.yml` into that evaluator's rule-file path and reload it using the
evaluator's documented mechanism. Loading this package declares that the environment expects a Sith
Hub telemetry path. Do not load it in an environment where no Hub is expected; otherwise the
missing-telemetry warning is correctly indistinguishable from a broken scrape/forwarding path.

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

## SithHubPolicyDecisionErrorRatioHigh

**Meaning.** More than 5% of at least 20 aggregate eligible
`allow|deny|require-approval|error` policy decisions ended in `error` over 15 minutes, and the
condition persisted for 10 minutes. `error` means the policy hook failed, returned an invalid
decision, or mandatory policy-audit delivery failed. Every case blocks the governed operation.
`deny` and `require-approval` are valid decisions and remain in the denominator, not the numerator.
This is a PEP symptom, not an external Ardur PDP-latency signal or SLO.

**Triage.**

1. Compare aggregate `error` with `allow`, `deny`, and `require-approval`. Do not add verb, tenant,
   workspace, actor, intent, trace, request, reason, credential, endpoint, selector, or raw-error
   labels to the alert.
2. Check the policy hook's availability and decision-contract validation through operator-owned
   logs and traces. Keep hook errors and decision payloads out of metric labels and annotations.
3. Check both mandatory audit sinks and PostgreSQL health. The existing critical
   `SithHubPolicyAuditFailure` alert is the more immediate signal for any individual sink error.
4. Confirm valid decisions resume and the ratio resolves naturally. Do not bypass policy or audit,
   reinterpret errors as denies, or weaken the fail-closed path to recover availability.

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

## SithHubAuthenticationRefusalOnly

**Meaning.** At least 20 aggregate completed authentication attempts were `refused` and none were
`accepted` over 15 minutes, and that condition persisted for 10 minutes. Any accepted attempt in
the same window suppresses the warning. At least one recent scraped sample from the preinitialized
accepted-outcome series must have reached the evaluator during the last 10 minutes. That sample
proves series visibility, not an accepted authentication event. The rule stays quiet when that
series is absent or stale, because partial telemetry cannot prove refusal-only traffic. This is an
operational symptom, not proof of brute force, credential stuffing, account compromise, or a
specific actor.

**Triage.**

1. First confirm `sith_build_info` and both preinitialized `accepted|refused` outcome series are
   current. Check the operator-owned scrape and forwarding path without widening the loopback
   listener or exposing credentials.
2. Verify whether a recent credential rotation, session-key change, deployment rollout, verifier
   configuration change, or clock problem could make every completed verifier decision fail. Use
   sanitized operator-owned logs; do not add credential, reason, tenant, workspace, actor,
   principal, IP, path, request, endpoint, or verifier-error data to the metric or alert.
3. Confirm known-valid bearer and browser-session authentication through an authorized smoke test.
   Keep the later workspace-authorization result separate: a valid credential that receives a 403
   is still an `accepted` authentication attempt.
4. If repeated refusals remain suspicious, investigate through the approved security-log and
   identity-provider surfaces. Do not infer an attack, block an identity, or weaken authentication
   from this process-wide aggregate alone.

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

## SithHubFleetReadStalenessHigh

**Meaning.** More than 5% of at least 20 aggregate freshness-eligible fleet reads were proven
`stale` over 15 minutes, and the condition persisted for 10 minutes. Eligible outcomes are only
`fresh` and `stale`: both require a structurally valid result with unique cluster identities and
non-zero observation times. `unknown`, `error`, and `empty` are excluded because they do not prove
snapshot age. This warning is request-time evidence, not continuous age monitoring or a formal SLO.

**Triage.**

1. Compare the aggregate `fresh` and `stale` counters. Do not add tenant, workspace, spoke, cluster,
   resource, principal, trace, endpoint, age, or raw-error labels to the metric or alert.
2. Use the tenant-scoped fleet API and retained observation timestamps—not process-wide metric
   labels—to identify the stale authorized scope. Keep PEP and RLS boundaries intact.
3. Inspect the relevant OCM addon health, projected service-account rotation, tunnel connectivity,
   Hub egress policy, snapshot persistence, and PostgreSQL health through operator-owned surfaces.
4. Confirm new authorized reads return `fresh` and the ratio resolves naturally. Do not hide stale
   evidence by rewriting timestamps, excluding registered clusters, treating unknown results as
   fresh, bypassing the PEP, or weakening RLS.

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

## SithHubTelemetryMissing

**Meaning.** The rule evaluator received no `sith_build_info` sample for ten minutes and the absence
continued through the five-minute hold. Build info is set to one when the Hub constructs its
isolated registry and requires no requests, so this is a traffic-independent warning that the
expected Hub metrics, loopback scrape, or forwarding path is missing.

**Triage.**

1. Confirm the Hub process and its opt-in exact-loopback metrics listener are running with the
   intended version and configuration. Do not widen the listener to a wildcard or cluster-routable
   address.
2. Check the operator-owned same-Pod collector's loopback scrape, forwarding queue, authentication,
   backpressure, and last successful sample timestamp. Keep credentials and response bodies out of
   logs and alert labels.
3. Confirm the rule evaluator is accepting the forwarded `sith_build_info` series. Do not add a
   fixed `job`, `instance`, Pod, workspace, or endpoint dependency to the portable rule.
4. Confirm the alert resolves when any current Sith build-info sample arrives. Separately exercise
   an external synthetic from collection through Alertmanager to the receiver; this rule cannot fire
   if its own evaluator is down.

## Threshold and cost notes

The nine expressions evaluate once per minute over existing series and produce at most nine alert
instances per rule evaluator. They create no recording series, listener, exporter, remote-write
path, or storage. The five ratio alerts use a 5% threshold and a 10-minute hold, with the minimum
volume defined independently by each expression. The snapshot ratio requires 20 aggregate attempts. The
fleet-read ratio requires 20 eligible `complete|degraded|error` reads and excludes `empty` from
numerator and denominator, so legitimate zero-scope traffic neither fires nor hides the alert.
The fleet-read staleness ratio separately requires 20 eligible `fresh|stale` reads. It excludes
`unknown`, `error`, and `empty` from both numerator and denominator, so unproven age cannot trigger
or dilute the warning.
The database-readiness ratio requires 20 aggregate `ready|unavailable` checks, uses the same 5%
threshold and 10-minute hold, and cannot divide by zero.
The policy-decision ratio requires 20 aggregate `allow|deny|require-approval|error` decisions and
counts only `error` as failure. It aggregates away the closed verb and every source label.
The refusal-only authentication warning requires at least 20 aggregate refusals, zero accepted
attempts over the same 15-minute window, at least one recent scraped sample from the preinitialized
accepted-outcome series during the most recent 10 minutes, and a 10-minute hold. That sample proves
series visibility, not an accepted event. It is deliberately not a refusal ratio: without an
environment-specific baseline, a generic percentage would create an arbitrary security threshold.
Missing or stale accepted-series data cannot satisfy the expression.
The missing-telemetry warning evaluates the existing traffic-independent `sith_build_info` gauge,
tolerates the most recent ten minutes of samples, and then requires five continuous minutes of
absence. Its installation precondition is the operator's explicit expectation signal; the rule
does not require or preserve source labels.
Change these thresholds only through a reviewed local override backed by observed traffic and an
explicit response owner. Full SLO thresholds and burn-rate alerts wait for negotiated objectives,
continuous freshness monitoring, and the missing dispatch and PDP production signals.
