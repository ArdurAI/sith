# Session — 2026-07-17 — hub-fleet-console

**Builder:** gnanirahulnutakki · **Effort:** deep · **Branch:**
`gnanirahulnutakki/feat/hub-fleet-console`
**Slice(s):** E8 / F8.1b · #218 · **Status:** ready for review

---

[G] Goal: render one tenant-scoped, coverage-honest Hub fleet console for #218 without exposing a
privileged browser path.
[S] Scope: the existing browser OIDC success redirect, a separate cookie/session adapter over the
persisted `hubfleet.Source`, fixed embedded HTML/CSS/JavaScript, production-boundary tests, and
operator documentation. Out: collector refresh, connector calls, local operations, inventory
records, correlation, service selection, proposals, approvals, writes, and generic cookie auth.
[A] Action: mounted exact GET-only page, fleet, and asset routes only when browser OIDC is fully
configured. The callback redirects to the transaction-bound workspace path and accepts no return
URL. The bearer fleet API remains bearer-only.
[A] Action: bound the fleet read to one exact `__Host-sith-session` cookie, signed membership, the
existing PEP, same-origin Fetch Metadata, and a five-minute process-key HMAC proof scoped to the
session, workspace, fleet-read purpose, and expiry. Restart, duplicate credentials, foreign scope,
missing or expired proof, methods, queries, and reader errors fail closed with generic responses.
[A] Action: built the responsive coverage rail and cluster ledger with current, stale, partial,
unreachable, inconsistent, and unaccounted evidence. Named gaps are visible text as well as rail
segments, invalid reads clear the prior timestamp, empty scope makes no health claim, and the
renderer uses no browser storage, inline/external assets, automatic polling, or DOM HTML injection.
[A] Action: pinned the production file, import set, assets, routes, and forbidden mutation
capabilities structurally. Added a real `ServeMux` test plus adversarial session, CSRF, tenant,
reader-error, asset, and configuration coverage.
[T] Test: focused race tests pass with 86.2% statement coverage for `internal/hubserver`; Hub runtime
and privacy boundary tests pass, JavaScript syntax is valid, and source formatting is clean.
[T] Test: full `make ci` passes formatting, vet, lint with zero findings, vulnerability scanning
with no findings, the complete race suite, operator policy tests, performance budget, subprocess
E2E, and build.
[T] Test: `make e2e-isolation` passes forced-RLS PostgreSQL coverage and two 50,000-execution
cross-workspace fuzzers. `make e2e-kind` passes the pinned real two-cluster fan-out, OCI, and Argo
tests in 237.786 seconds.
[T] Test: isolated-GOPATH `make release-check` passes module verification, two reproducible release
builds, archive and SPDX SBOM verification, Homebrew formula generation, and the multi-platform
distroless OCI layout after the final asset changes.
[T] Test: rendered desktop and 390-pixel mobile views preserve coverage hierarchy, named-gap
legibility, keyboard focus, and readable cluster status. Reduced-motion behavior is present.
[T] Test: CodeRabbit first identified inaccessible hover-only gap names and a stale timestamp after
failed reads. Both were corrected; the full CI gate passed again and the follow-up review completed
with no findings.
[T] Test: `README.md` was reviewed and updated because this slice changes the supported browser
OIDC completion path and adds a user-visible Hub endpoint and security boundary.
[C] Checkpoint #1: implementation, red-team review, responsive visual inspection, independent
review, and all required local gates complete; next: create the signed DCO/GSTACK commit and open a
small PR into `dev`.

Primary compatibility references:

- <https://cheatsheetseries.owasp.org/cheatsheets/Cross-Site_Request_Forgery_Prevention_Cheat_Sheet.html>
- <https://developer.mozilla.org/en-US/docs/Web/Security/Practical_implementation_guides/Cookies>
- <https://developer.mozilla.org/en-US/docs/Web/HTTP/Reference/Headers/Content-Security-Policy/default-src>
- <https://www.rfc-editor.org/rfc/rfc9700>

---

**Session close:** ready for review · **Open questions touched:** none
