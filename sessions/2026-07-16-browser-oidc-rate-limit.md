# Session — 2026-07-16 — browser-oidc-rate-limit

**Builder:** gnanirahulnutakki · **Effort:** deep · **Branch:** gnanirahulnutakki/fix/browser-oidc-rate-limit
**Slice(s):** Phase 1 browser identity hardening · #180 · **Status:** ready for review

---

[G] Goal: fix #180 so every browser OIDC login or callback request consumes exactly one rate-limit attempt without weakening transaction replay, PKCE, nonce, cookie, or token-disclosure controls.
[S] Scope: browser OIDC request accounting and focused regression coverage. Out: limiter algorithm changes, distributed rate limiting, bearer-route authentication, provider verification, and session lifetime policy.
[A] Action: removed the callback-only second limiter debit; the common request admission path remains the single fail-closed limiter boundary for both login and callback requests.
[A] Action: added a production-boundary regression proving ten full login flows consume exactly twenty requests and that the next request is rejected before a transaction or provider exchange is created.
[A] Action: addressed independent review feedback by asserting the guarded limiter count after every login and callback phase, preventing compensating debit errors from satisfying only the aggregate limit.
[T] Test: focused `go test -race -count=1 ./internal/hubserver`, `make ci`, `make e2e-isolation` with PostgreSQL RLS plus 50,000 fuzz executions, `make e2e-kind`, and `make release-check` all passed. A second independent review completed with no findings.
[C] Checkpoint #1: implementation, regression, red-team review, and required local gates complete; next: create the signed DCO/GSTACK commit and open the small PR into `dev`.

<!-- append further G/S/A/T/C entries below as the session proceeds -->

---

**Session close:** ready for review · **Open questions touched:** none
