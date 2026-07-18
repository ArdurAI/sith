# E5 F5.9a — exact single-use approval grants

[G] Goal: implement GitHub issue #250 as the durable, execution-free core beneath F5.9 approval
elicitation. Keep all repository, worktree, test-cache, and durable knowledge artifacts on
`/Volumes/EXTENDED`; checkpoint the decision and evidence in both Notion and Obsidian.

[T] Research: MCP 2025-06-18 Elicitation defines `elicitation/create`, structured flat primitive
schemas, accept/decline/cancel outcomes, server/client validation, and the prohibition on requesting
sensitive information. PostgreSQL 18 documents that conditional `UPDATE ... RETURNING` can make
the one-row consumption result authoritative, FORCE RLS applies the workspace policy to the table
owner, and `FOR SHARE` blocks concurrent `UPDATE`/`DELETE` of current membership rows while a grant
or consume transaction validates them. `FOR KEY SHARE` was deliberately rejected because it does
not block a role-only update.

[D] Decision: expose a private-field `pep.ApprovalBinding` only from a validated immutable
`ProposalInput`. The binding carries the intent id, workspace, proposer, and complete resolved
proposal digest; it never carries raw arguments, targets, justification, credentials, tokens, or
elicitation content. The database stores that minimal binding with an authenticated distinct
approver and timestamps.

[D] Decision: preserve the approval row as durable evidence. The least-privilege application role
has `SELECT`, `INSERT`, and column-level `UPDATE (consumed_at)` only. It cannot change workspace,
intent, proposer, approver, digest, or approval time and cannot delete a row. This is distinct from
the append-only E6 policy-audit hash chain because an authorization grant must transition exactly
once while audit history never transitions.

[A] Action: added migration `0010_approval_grants.sql`, automatic migration privilege repair and
catalog validation, a current-membership and role-checked creation path, and a conditional exact
consume path. The catalog audit requires exactly one complete workspace policy per table; this
rejects additional permissive policies that PostgreSQL would otherwise OR with the intended RLS
predicate. Unknown id, foreign workspace, wrong intent, wrong digest, pre-approval time, stale
membership, and replay all return the same stable `ErrApprovalGrantUnavailable` classification.

[T] Test: pure Go package tests cover binding provenance, mutation rejection, opaque identifier
generation and entropy failure, schema privacy/RLS contracts, and fuzz the identifier vocabulary.
The digest-pinned PostgreSQL 18.4 integration test covers current role lookup, role refusal,
self-approval refusal, RLS foreign read/write/consume denial, altered and additional permissive
policy negative controls, immutable-column/delete privilege denial, correct consume,
approve-then-swap refusal, replay refusal, and exactly one success from two concurrent consumers.

[S] Security boundary: no MCP transport, Ardur PDP, expiry policy, multi-approver rule, credential,
connector, signed dispatch, shell/filesystem write, generic execute/apply surface, or production
mutation is added. A future dispatcher must still re-run the full PEP/PDP and exact binding check.

[C] Cost: one small indexed row and one short scoped transaction per approval. No service, queue,
polling, egress, or cloud resource is introduced. Retention and expiry policy remain explicit later
work rather than an invented default.

[V] Current evidence: focused pure Go and race tests pass; proposal-binding and approval-id fuzz
campaigns each execute 50,000 inputs; the real PostgreSQL 18.4 isolation suite passes at 75.6%
`hubdb` coverage with two additional 50,000-input repository fuzz campaigns. Full `make ci` passes
with zero lint findings and no reachable vulnerabilities. Reproducible release archives, SPDX
SBOMs, Homebrew formula, two-platform OCI layout, Helm 4.2.3 contract, OCI runtime contract, and a
fresh Kind v0.32.0 / Kubernetes v1.36.1 cluster pass. CodeRabbit's first pass found the additional
permissive-policy audit gap and two minor documentation/test issues; all were corrected, and its
second pass reports no findings. Hosted PR and exact post-merge `dev` evidence remain pending.

[R] Primary references:
- https://modelcontextprotocol.io/specification/2025-06-18/client/elicitation
- https://www.postgresql.org/docs/current/sql-update.html
- https://www.postgresql.org/docs/current/ddl-rowsecurity.html
- https://www.postgresql.org/docs/current/sql-select.html#SQL-FOR-UPDATE-SHARE
- https://www.postgresql.org/docs/current/explicit-locking.html#LOCKING-ROWS
