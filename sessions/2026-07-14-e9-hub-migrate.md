# Session — 2026-07-14 — e9-hub-migrate

**Builder:** Gnani Rahul · **Branch:** `gnanirahulnutakki/feat/e9-hub-migrate`
**Slice(s):** E9 / [#129](https://github.com/ArdurAI/sith/issues/129) · **Status:** ready-to-commit

---

[G] Goal: add the isolated schema-migration entry point needed before the E9 Helm deployment
chart can safely run `sith hub` with only its non-owner application database role.

[S] Scope: `sith hub migrate`, environment loading for exactly one owner database URL and one
application role, existing checksum-ledger/RLS migration enforcement, focused documentation, and
real PostgreSQL and multi-cluster evidence. Helm templates, chart values, database provisioning,
the hub listener, Kubernetes clients, collection, and any upstream ClusterGateway release change
remain out of scope.

[A] Action: added a one-shot `hubdb.Migrate` boundary that rejects missing/ambiguous URLs,
same-owner/application roles, and remote plaintext transport before dialing. It opens one owner
connection, applies existing serializable checksum-locked migrations and forced-RLS audit, then
performs a bounded five-second best-effort close. Transaction commit is the success boundary, so a
post-commit close acknowledgement cannot misclassify a completed migration Job as failed.

[A] Action: added `sith hub migrate` and a separate runtime environment loader. The subcommand
does not construct the hub TLS listener, in-cluster Kubernetes identity/client, OCM transport, PEP,
collector, or application pool. Documentation explicitly keeps the owner credential out of the hub
Deployment, chart values, and logs.

[T] Test: final focused race suites passed. `make e2e-postgres` passed with 71.3% `hubdb`
coverage, including first-run and idempotent migration via the new public seam. `make e2e-isolation`
passed (`hubdb` 73.5%) and completed the fixed 50,002-execution cross-workspace fuzz campaign.
Final `make ci` passed formatting, vet, lint, `govulncheck`, full race tests, safety harness, E2E,
and build.

[T] Test: final `make e2e-kind` passed in 88.8 seconds. Final `make e2e-ocm` passed the real
hub-plus-two-spoke M0 lab: scoped managed-serviceaccount identity, active hub-to-node/pod deny,
and direct proxy/runtime tests all passed; the harness deleted all temporary Kind clusters. Local
port 8090 was occupied by an unrelated service, so only the auxiliary `clusteradm` proxy probe was
deferred; the mandatory direct E2E gate passed. Final `make release-check` passed module
verification, reproducible Darwin/Linux amd64/arm64 artifacts, SPDX SBOMs, and checksums.

[R] Review: manual red-team review covered role separation, remote plaintext rejection, no owner
credential path into the long-running hub, cancellation-safe bounded cleanup, RLS preservation,
and documentation secrecy. CodeRabbit CLI v0.6.5 reviewed the uncommitted diff three times. It
first identified post-commit close status and unbounded cleanup; both were fixed and revalidated.
The final review completed with zero findings.

[S] Security: immediately before publication, GitHub queues were Dependabot 0, code scanning 0,
and secret scanning 0. Docker prune reclaimed 1.217 GB of only unused resources; no Kind clusters
remain and the two pre-existing unrelated containers remain running.

[C] Checkpoint #1: next: read README once more, create SSH-signed DCO/GSTACK commit as
`gnani.nutakki@gmail.com`, push, open PR into `dev`, force-merge only after green review/CI, verify
the exact post-merge `dev` CI and security queues, close #129, update #27/#39, then take the next
unblocked backlog slice.

---

**Session close:** migration-command prerequisite ready for independent review and landing ·
**Open questions touched:** none
