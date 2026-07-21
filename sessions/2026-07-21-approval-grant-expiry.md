# E5 F5.9b — immutable approval-grant expiry

**Builder:** gnanirahulnutakki · **Effort:** deep · **Branch:**
`gnanirahulnutakki/approval-expiry-20260721`
**Slice(s):** E5 / F5.9b · #299 · **Status:** ready for review

---

[G] Goal: give every new approval grant one immutable, server-enforced 10-minute absolute
lifetime while preserving exact single-use, tenant isolation, privacy-minimized evidence, and
offline audit verification.

[D] Decision: PostgreSQL statement time is the only approval and consumption clock. The public
approval API accepts no timestamp, and the one conditional consumption update enforces the
half-open `approved_at <= consumed_at < expires_at` interval. Expired, legacy, unknown, foreign,
mismatched, and replayed grants share `ErrApprovalGrantUnavailable`.

[D] Decision: new rows use evidence version 2 and audit format 3. The evidence digest uses a new
domain and binds `expires_at`; the audit entry hash also uses a distinct format-3 domain. Existing
format-1 policy records and format-2 approval records remain independently rehashable.

[A] Action: added migration 0013 with immutable `expires_at = approved_at + interval '10 minutes'`
and evidence-version constraints. Legacy rows are retained, backfilled as evidence version 1, and
excluded from the new consume predicate. Their `consumed_at` value is not fabricated.

[A] Action: the legacy backfill temporarily removes FORCE RLS only inside the serializable
migration transaction. PostgreSQL holds an access-exclusive table lock, FORCE RLS is restored
immediately after the update, and any error rolls back the entire relaxation. The application
role remains unable to update expiry/evidence fields or delete rows.

[T] Test: pure Go unit tests and PostgreSQL-tag compilation pass. The real digest-pinned PostgreSQL
18.4 race suite proves an incremental 0012-to-0013 migration, fixed lifetime, legacy invalidation,
pre-approval/expiry refusal without row or audit mutation, one-winner concurrent consumption,
audit rollback, forced RLS, immutable columns, and mixed format-1/2/3 offline verification.

[T] Test: focused race suites and two 50,000-execution fuzz campaigns cover expiry-evidence framing
and portable format-3 chain integrity. Full `make ci` passes formatting, vet, lint, reachable
vulnerability scanning, all repository race tests, operator policy checks, performance budget,
subprocess E2E, and build. The real isolation gate reaches 76.7% `hubdb` coverage and adds two
100,000-execution tenant-isolation fuzz campaigns.

[T] Test: `make release-check` passes two reproducible builds, four platform archives, SPDX SBOMs,
Homebrew generation, and the two-platform distroless OCI layout. The pinned real two-cluster kind
suite passes in 242.456 seconds. CodeRabbit CLI 0.6.5 found one minor fixture-clarity issue; the
format-3 fixture now uses a real expiry-bound golden digest, and the second full review reports no
findings.

[S] Scope boundary: no MCP transport, Ardur PDP, multi-approver policy, configurable lifetime,
renewal, credential minting, connector execution, dispatch, shell/filesystem access, generic apply,
or production mutation is introduced.

[C] Cost: one timestamp and one small version field per grant, with no new service, queue, poller,
egress, or cloud resource. The consume path remains one indexed transactional update.

[R] Primary references:
- https://modelcontextprotocol.io/specification/2025-11-25/client/elicitation
- https://cheatsheetseries.owasp.org/cheatsheets/Transaction_Authorization_Cheat_Sheet.html
- https://www.rfc-editor.org/rfc/rfc6749.html#section-4.1.2
- https://www.postgresql.org/docs/current/sql-update.html
- https://www.postgresql.org/docs/current/functions-datetime.html
- https://www.postgresql.org/docs/current/ddl-rowsecurity.html

---

**Session close:** ready for review · **Open questions touched:** renewal remains outside F5.9b
