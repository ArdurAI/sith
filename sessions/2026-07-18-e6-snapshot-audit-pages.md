# Session — 2026-07-18 — E6 snapshot-bound audit pages

**Builder:** Gnani Rahul · **Branch:** `gnanirahulnutakki/feat/e6-snapshot-audit-pages`
**Slice:** [#276](https://github.com/ArdurAI/sith/issues/276), E6
[#24](https://github.com/ArdurAI/sith/issues/24) · **Status:** complete local proof; ready for signed commit

## [G] Goal

Export retained Sith audit chains beyond the existing 512-entry complete-document ceiling as
bounded pages tied to one immutable workspace snapshot, then verify the full page sequence offline.

## [D] Design

- Preserve the exact query-free complete export route and schema.
- Add a distinct page route: no query for page one; exactly one canonical `cursor` query for a
  continuation; reject every other query, body, cookie, selector, or caller trace input.
- Durably authorize every page request with the existing admin-only `export-audit` action and
  `audit.export` verb before storage work.
- Fix page one to the current head after its authorization row. Later page-request authorization
  rows remain durable in the live chain but outside the original snapshot.
- Carry a fixed-size versioned base64url continuation bound to a domain-separated workspace digest,
  snapshot head sequence/hash, next sequence, and expected previous hash. It is not a credential.
- Validate live/snapshot head and page-boundary anchors under forced RLS and Repeatable Read, read
  and validate at most 512 consecutive rows, commit, then encode the finished page.
- Verify page files incrementally with `sith audit verify-pages`; require one same-workspace,
  same-snapshot, genesis-to-head sequence without gaps, replay, or reordering.

## [S] Security and non-claims

The route accepts only a signed workspace-admin session. Cursor possession grants nothing.
Malformed, foreign, altered, skipped, replayed, swapped, stale, saturated, or integrity-invalid
requests fail without disclosing store details. Page documents retain only the already-sanitized
policy and approval events. This is not asynchronous export, WORM retention, external anchoring or
authenticity, the Ardur decision ledger, a complete action lifecycle, or E6 completion.

## [T] Focused proof

- Portable contract and cursor tests pass under the race detector.
- A 50,000-execution cursor mutation fuzz campaign cannot bind a changed continuation to the
  original page.
- CLI, storage, HTTP, and runtime focused race suites pass.
- PostgreSQL 18.4 forced-RLS integration passes at 76.6% package coverage. It proves exact 512/513
  paging, an append after page one that cannot move the fixed snapshot, offline full-sequence
  verification, role/workspace refusal, and altered head/previous-anchor rejection.
- Complete repository CI passes with zero lint findings, no reachable vulnerabilities, race
  coverage, policy scripts, e2e tests, and a production build.
- Both 50,000-execution tenant-isolation fuzz campaigns pass.
- Reproducible four-platform release archives, SPDX SBOMs, the Homebrew artifact, and the
  multi-platform release-hub OCI layout pass verification.
- Helm and cross-platform OCI contract suites pass under the race detector.
- The pinned Kubernetes 1.36.1 two-cluster fleet, image, and Argo projection suite passes in
  243.827 seconds; teardown leaves no Kind clusters or Sith/Kind containers.
- CodeRabbit's first complete-diff pass found two valid minor issues: canonical timestamp
  structural validation and transaction-order wording. Both were corrected; regression coverage
  passes and the second complete-diff pass reports zero findings across all 16 changed files.
- A high-signal secret scan reports no credential or private-key material in the review scope.

## [O] Operability and cost

Each page adds one audit row, reads at most 512 entries plus fixed anchors, performs bounded SHA-256
work, and incurs one response of egress. Total work is linear in retained entries and page count.
No object store, background job, queue, external service, or recurring cloud resource is added.

## [P] Primary sources

- [PostgreSQL 18 Repeatable Read](https://www.postgresql.org/docs/18/transaction-iso.html#XACT-REPEATABLE-READ)
- [PostgreSQL 18 row security](https://www.postgresql.org/docs/18/ddl-rowsecurity.html)
- [RFC 4648 base64url](https://www.rfc-editor.org/rfc/rfc4648.html#section-5)
- [Go encoding/base64](https://pkg.go.dev/encoding/base64)

## [N] Next

Create one signed DCO/GSTACK commit with the mandated author, push the branch, and open the PR to
`dev`. Require exact-head CI, CodeQL, and hosted CodeRabbit; merge without rewriting the signed
head, prove the exact post-merge `dev` checks, close #276, update E6 #24 without claiming the epic is
complete, and recheck the GitHub security queues.
