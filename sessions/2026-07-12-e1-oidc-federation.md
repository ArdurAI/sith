# E1 pinned OIDC federation

Issue: [#84](https://github.com/ArdurAI/sith/issues/84)

Branch: `gnanirahulnutakki/feat/e1-oidc-federation`

## [G] Goal

Federate explicitly configured upstream identities without accepting arbitrary third-party JWTs or
trusting upstream authorization claims. A verified issuer+subject may select one server-side
workspace binding; only Sith's current membership state determines the resulting session role.

## [S] Scope

- Pin exact HTTPS issuer, audience, type, asymmetric algorithms, token lifetime, cache lifetime,
  and JWKS key count for every provider.
- Require exact discovery issuer equality and same-origin JWKS; reject private/special-use network
  targets, non-443 production ports, proxies, off-origin redirects, oversized responses, and
  untrusted TLS.
- Reject duplicate JSON members, hostile JOSE key URLs/embedded keys/certificate chains, nested or
  compressed tokens, ambiguous key IDs, weak RSA keys, and non-canonical JWK encodings.
- Validate signature, issuer, audience, authorized party for multi-audience tokens, subject,
  expiry, not-before, issued-at, and bounded lifetime.
- Resolve one workspace-scoped issuer+subject binding through a fixed PostgreSQL query under forced
  RLS; ignore upstream role and workspace claims.
- Mint the existing short-lived, type-pinned Ed25519 Sith session.
- Replace rather than merge JWKS caches on rotation; never use an expired cache through an outage.

## [A] Analysis and red-team checks

- The workspace is fixed by the exchange route and is only a lookup selector. A matching binding
  must still exist under that workspace's RLS scope, so caller choice grants no authority.
- The production HTTP transport resolves and validates addresses itself, then dials the validated
  IP. This closes the validate-then-re-resolve DNS rebinding gap.
- Proxy use is disabled so ambient proxy configuration cannot silently broaden the trust boundary.
- A new key ID causes one refresh. The fetched key set atomically replaces the cache, so a retired
  key is no longer selectable after successful rotation.
- A still-valid cached key remains usable during a short metadata outage; once TTL expires, refresh
  failure denies exchange rather than using stale keys.
- No upstream token, signing key, Sith session, or authorization header is logged or persisted.
- Multi-replica hubs still require a shared ingress attempt limit; the handler's in-process limiter
  intentionally has bounded cardinality but is not distributed state.

## [T] Tests and evidence

- Pinned TLS test issuer, hostile-token matrix, JWKS rotation/retirement, outage, duplicate metadata,
  ambiguous keys, and untrusted TLS under the race detector: PASS.
- Digest-pinned PostgreSQL 18.4 OIDC binding RLS controls: PASS.
- The full CI and destructive isolation gates passed on the final source before handoff, including
  50,000 fixed-seed selector-fuzz mutations. Hubauth coverage was 84.6%, hubserver 89.2%, and
  hubdb destructive-suite coverage 70.0%.
- Fresh branch verification: make ci PASS after both signed commits (format, vet,
  golangci-lint, govulncheck with no vulnerabilities, race/coverage tests, source-boundary and
  operator-script safety checks, binary E2E, latency check, and reproducible build). Hubauth
  coverage remains 84.6% and hubserver coverage 89.2%.
- Final real integration rerun: make e2e-kind PASS in 88.207s against two Kubernetes 1.36.1 kind
  clusters; the test cleanup removed both clusters.
- Final release rerun: make release-check PASS. GoReleaser built and verified reproducible snapshot
  archives for Darwin/Linux on amd64/arm64, generated SPDX SBOMs, and rendered the Homebrew formula.
- Manual standards review against OpenID Connect Discovery 1.0 §4.1/§4.3 confirmed correct
  path-issuer discovery construction and exact issuer equality; no speculative code change was made.
- Git diff --check: PASS. GitHub open queues immediately before publication: Dependabot 0, code
  scanning 0, secret scanning 0.
- Cleanup: kind get clusters reports none. Docker system prune -f removed the residual kind network
  and image and reclaimed 913.1 MB without stopping the two active containers.

## [C] Checkpoint #1

- Signed/DCO feature commit: aff198a (2026-07-12/e1-oidc-federation#1). It contains the OIDC
  provider validation, strict JWKS client, PostgreSQL binding store, fixed workspace exchange
  handler, tests, migration, privacy allowlist, and operator documentation.

## [C] Checkpoint #2

- Signed/DCO evidence checkpoint: 71d0626 (2026-07-12/e1-oidc-federation#2).

## [C] Checkpoint #3

- The fresh CI evidence update is ready for its signed documentation checkpoint
  (2026-07-12/e1-oidc-federation#3). PR, merge, and exact post-merge evidence remain pending.
