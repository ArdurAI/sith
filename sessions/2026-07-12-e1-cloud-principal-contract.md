# Session — 2026-07-12 — e1-cloud-principal-contract

**Builder:** Gnani Rahul · **Branch:** gnanirahulnutakki/feat/e1-cloud-principal-contract
**Slice(s):** E1 cloud-IAM principal contract ([#91](https://github.com/ArdurAI/sith/issues/91)) · **Status:** in-progress

---

## [G] Goal

Define and prove the provider-neutral, replay-safe cloud identity exchange seam required before
AWS, Azure Entra, and Google service-account proof verifiers can authenticate a hub caller.

## [S] Scope

- Cloud principal contract: provider, explicit realm, immutable subject, audience, issued-at, and
  expiry; provider verification remains a narrow injected port.
- Exact provider+realm+subject server-side membership bindings under forced RLS.
- One-way, bounded replay consumption and a workspace/provider-fixed exchange handler that mints the
  existing short-lived Sith session only after verification, current membership lookup, and replay
  consumption succeed.
- Out: AWS SigV4, Entra JWT, and Google ID-token parsing/verification; those are #92, #93, and #94.

## [A] Research and decisions

- AWS STS GetCallerIdentity, Microsoft Entra claim validation, Google service-account ID-token
  semantics, and Azure China authority separation were reviewed from primary provider
  documentation. A raw cloud proof is never a Sith session and never becomes a persisted record.
- Parent #85 was split into #91 (this contract), #92 (AWS), #93 (Azure), and #94 (Google) to
  preserve one provider/security boundary per PR. The split is documented on #85 and in the linked
  Notion decision note.
- Replay keys are HMAC digests of the raw proof, retained only until the verified proof expires.
  The first adapter is bounded in-memory and deliberately behind a port; a shared deployment may
  supply a durable atomic implementation without changing authentication behavior.
- Implemented the cloud principal service, in-memory replay adapter, fixed workspace/provider HTTP
  exchange seam, forced-RLS cloud identity binding store and migration, PostgreSQL controls, focused
  unit tests, and operator-facing boundary documentation.

## [T] Tests and evidence

- Focused unit/race suites for hubauth, hubserver, and hubdb: PASS.
- Real PostgreSQL 18.4 isolation suite: PASS. The new binding table is included in forced-RLS,
  unscoped-read, foreign-write, current-membership, and cross-workspace controls. Hubdb destructive
  coverage is 69.8%; the fixed 50,000-iteration selector fuzzer also passed.
- Final full CI: PASS (format, vet, golangci-lint, no govulncheck findings, race/coverage, source
  boundaries, operator-script safety, binary E2E, latency, and build). Hubauth coverage is 85.2%
  and hubserver coverage is 89.5%.
- Final real two-cluster kind fan-out: PASS in 83.569s.
- Final release verification: PASS; reproducible Darwin/Linux amd64/arm64 archives, SPDX SBOMs,
  checksums, and Homebrew formula rendering all succeeded.
- Manual red-team review: PASS. Verified generic external errors, provider/workspace-fixed handler,
  closed provider vocabulary, HMAC-only replay identifiers, bounded capacity, current-membership
  resolution before proof consumption, no raw proof persistence, exact provider/realm/subject RLS
  keys, and fail-closed invalid direct replay-guard construction.
- Final GitHub security queues: Dependabot 0, code scanning 0, secret scanning 0.
- Cleanup: kind get clusters reports none; Docker prune reclaimed 1.652 GB and left active
  containers running.

## [C] Checkpoint #1

- Signed/DCO feature commit: 11c045e (2026-07-12/e1-cloud-principal-contract#1). It contains the
  provider-neutral cloud principal and replay seam, fixed handler, RLS store and migration, tests,
  and operator documentation.

## [C] Checkpoint #2

- The validation evidence journal is ready for its signed documentation checkpoint
  (2026-07-12/e1-cloud-principal-contract#2). PR and exact post-merge evidence remain pending.
