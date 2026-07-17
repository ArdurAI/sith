# Session — 2026-07-17 — hub-health-correlation

**Builder:** gnanirahulnutakki · **Effort:** deep · **Branch:**
`gnanirahulnutakki/feat/hub-health-correlation`
**Slice(s):** E8 / F8.1c · #220 · **Status:** ready for review

---

[G] Goal: render one tenant-scoped, coverage-honest answer to “where is this exact resource not
Healthy?” without adding a privileged browser path.
[S] Scope: compose the existing PEP-governed `hubfleet.Correlator` into the browser-OIDC Hub
runtime, add one GET-only console adapter, purpose-separated request proof, minimal response
projection, explicit-submit renderer, adversarial tests, and operator documentation. Out: polling,
collector refresh, connector calls, local operations, persistence, writes, selectors, Secret
resources, arbitrary health predicates, and service/workspace pickers.
[A] Action: added strict canonical query parsing before the correlator, fixed `health_not=Healthy`,
a 257-row sentinel for a 256-match response bound, and generic fail-closed errors for unsafe input,
PEP/storage failures, over-bound results, or unexpected stored fact shapes.
[A] Action: separated the correlation HMAC purpose from the fleet-snapshot proof while retaining
the exact signed session/workspace/expiry binding, same-origin Fetch Metadata check, no-store
headers, restrictive CSP, and bearer-auth refusal.
[A] Action: projected only cluster scope, exact resource identity, normalized health, observation
time, stale state, and bounded coverage. Raw observations, attributes, workspace fields,
provenance, native IDs, deep links, and source payloads never enter the browser response.
[A] Action: added a responsive, keyboard-accessible explicit-submit form and one fleet-wide answer
with named stale, unreachable, truncated, unaccounted, and inconsistent gaps. Rendering uses only
`textContent`/DOM construction and clears prior answers on failure.
[T] Test: focused race coverage passes at 85.9% for `internal/hubserver`; correlator and Hub runtime
race suites pass. Unsafe request and hostile stored-shape tests prove fail-closed behavior before a
query or browser projection respectively.
[T] Test: full `make ci` passes twice, including formatting, vet, lint with zero findings,
vulnerability scanning with no findings, the complete race suite, policy checks, performance
budget, subprocess E2E, and build. The second run includes the independent-review correction.
[T] Test: `make e2e-isolation` passes forced-RLS PostgreSQL integration plus both 50,000-execution
cross-workspace fuzzers. The existing database-backed two-spoke correlator coverage verifies the
same tenant-scoped read seam composed by this slice.
[T] Test: pinned real two-cluster kind fan-out, OCI, and Argo projection checks pass in 237.836
seconds. Isolated-GOPATH `make release-check` passes module verification, two reproducible release
builds, archive and SPDX SBOM checks, Homebrew generation, and multi-platform distroless OCI layout.
[T] Test: desktop and 390-pixel mobile rendering preserve the exact-query form, explicit-submit
behavior, adjacent named gaps, health/stale badges, and readable resource identity. JavaScript
syntax and reduced-motion behavior pass.
[T] Test: CodeRabbit found that the completed result hid the existing assistive-technology status
region. The renderer now keeps a visually hidden live completion announcement and avoids duplicate
live regions; the follow-up full-diff review reports zero findings. Its request to mark the session
merged was correctly deferred because no merge evidence existed yet.
[T] Test: `README.md` was reviewed and updated because this slice adds a supported Hub endpoint,
browser-visible behavior, security boundary, and per-submit database cost.
[C] Checkpoint #1: implementation, red-team tests, responsive inspection, independent review, and
all required local gates complete; next: create the signed DCO/GSTACK commit and open a small PR
into `dev`.

---

**Session close:** ready for review · **Open questions touched:** none
