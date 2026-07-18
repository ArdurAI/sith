# Session — 2026-07-18 — approval-lifecycle-audit

**Builder:** gnanirahulnutakki · **Effort:** deep · **Branch:**
`gnanirahulnutakki/feat/approval-lifecycle-audit`
**Slice(s):** E6 / F6.1b · #252 · **Status:** ready for review

---

[G] Goal: append privacy-minimized approval creation and consumption evidence to the tenant hash
chain atomically with each successful single-use grant mutation.
[S] Scope: evolve the retained PostgreSQL chain format, reuse one transaction-local append
primitive, bind lifecycle pairs to the exact immutable grant, preserve format-1 verification,
add hostile rollback and mixed-chain tests, and document rollout ordering. Out: endpoints, MCP,
PDP policy, multi-approver policy, expiry, credentials, signing, dispatch, connectors, filesystem
or shell access, generic execution, and production mutation.
[A] Action: added migration 0011 with closed format-2 lifecycle kinds and shapes, format-1 writer
defaults for schema rollout, forced-RLS-compatible columns, and immutable application-role
privileges.
[A] Action: refactored the chain append into a transaction-local primitive. Approval creation and
consumption append `approval-created` and `approval-consumed` after their row mutation but before
transaction commit, so an append failure rolls the mutation back.
[A] Action: used a domain-separated SHA-256 digest over the opaque 128-bit grant ID, workspace,
intent ID, proposer, approver, resolved proposal digest, and approval timestamp. The lifecycle
entry contains no raw target, arguments, justification, credentials, tokens, elicitation content,
or free-form reason.
[A] Action: retained policy decisions on exact format 1 and introduced format 2 only for approval
lifecycle entries. README documents that verifiers must upgrade before lifecycle traffic is
enabled during a rolling deployment.
[T] Test: package and race suites pass. Two 50,000-execution fuzz campaigns cover format-1 chain
field framing and approval evidence framing.
[T] Test: the PostgreSQL 18.4 integration suite proves tenant RLS, immutable privileges,
mixed-format verification, concurrent one-winner consumption, stable refused paths, retained-row
and hash/head tamper detection, creation rollback on audit failure, and consumption rollback on
audit failure.
[T] Test: full `make ci` passes with formatting, vet, lint, reachable-vulnerability scanning,
complete race tests, policy checks, alert validation, performance budget, subprocess E2E, and
build. `make release-check` passes reproducible archives, SPDX SBOMs, Homebrew generation, and the
multi-platform distroless OCI layout.
[T] Test: Helm and OCI E2E pass. The pinned two-cluster Kind suite passes in 239.070 seconds.
[T] Test: CodeRabbit CLI 0.6.5 reviewed all seven changed files against `origin/dev` and returned
zero findings. `README.md` was reviewed and updated for the new security and rolling-upgrade
boundary.
[C] Checkpoint #1: implementation, rollback falsification, mixed-chain compatibility, full gates,
documentation, and independent review complete; next: open the signed DCO/GSTACK PR into `dev`.

---

**Session close:** ready for review · **Open questions touched:** none
