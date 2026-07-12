# E1 API-key session exchange

Issue: [#83](https://github.com/ArdurAI/sith/issues/83)

Branch: `gnanirahulnutakki/feat/e1-api-key-exchange`

## [G] Goal

Add machine authentication without turning a long-lived API key into a role-bearing bearer token.
The key may authenticate only a bounded exchange; normal hub access continues to require the
strict, short-lived Ed25519 Sith session profile.

## [S] Scope

- Issue a versioned, high-entropy identifier-plus-secret key and return plaintext exactly once.
- Persist only an HMAC-SHA-256 verifier under an injected pepper; never persist the raw key.
- Bind keys to an existing subject and resolve the subject's current membership during exchange.
- Mint a 15-minute Ed25519 session with pinned algorithm, type, issuer, audience, and key ID.
- Keep the API-key `SithKey` scheme exclusive to the exchange endpoint; retain `Bearer` for signed
  sessions only.
- Support expiry, at most 24 hours of rotation overlap, immediate revocation, generic no-store
  failures, and a bounded per-process attempt limiter.
- Add the credential table through a versioned migration with forced workspace RLS and the same
  catalog audit applied to every Sith table.

## [A] Analysis and red-team checks

- The workspace ID embedded in the opaque key selects only a fixed bootstrap lookup. It does not
  grant a scope: the keyed verifier must match in constant time, the stored subject must still be a
  member, and the returned row remains constrained by PostgreSQL RLS.
- The normal authentication middleware accepts only strict signed JWTs. Supplying the raw API key
  under `Bearer` fails in the JWT verifier before any authorization decision.
- Issuance, rotation, and revocation require the closed `manage-workspace` action. The database
  repeats the workspace and membership checks and never exposes an unscoped callback.
- Missing, malformed, oversized, forged, expired, retired, revoked, and cross-workspace inputs all
  produce the same external exchange error. Rate exhaustion is separately visible as HTTP 429 so
  clients can back off.
- The in-memory limiter has bounded attacker-controlled cardinality. Multi-replica production
  deployments still require a shared ingress or gateway limit.
- The HMAC pepper and Ed25519 private key are injected dependencies. No secret manager or key
  distribution mechanism is invented in this slice.
- No API key, verifier, signing key, session, or authorization header enters logging, fleet facts,
  audit payloads, or error bodies.

## [T] Tests and evidence

- `make ci`: PASS (format, vet, golangci-lint, govulncheck, race/coverage, source boundaries,
  operator-script safety, latency, binary e2e, and build). Hubauth coverage is `87.4%`; hubserver
  coverage is `88.7%`.
- `make e2e-isolation`: PASS against digest-pinned PostgreSQL 18.4; hubdb coverage is `70.5%` and
  the selector fuzzer completed exactly `50,000` mutations with four workers.
- The PostgreSQL suite includes foreign API-key writes, unscoped reads, cross-workspace lookup,
  one-time plaintext issuance, current-role exchange, single-successor rotation, retirement, and
  immediate revocation negative controls.
- `make e2e-kind`: PASS against two real Kubernetes 1.36.1 kind clusters in `88.502s` on the final
  source. An earlier preflight invocation used a mistyped, nonexistent digest and failed before a
  cluster was created; the rerun used the repository pin confirmed by the official
  [kind v0.32.0 release](https://github.com/kubernetes-sigs/kind/releases/tag/v0.32.0).
- `make release-check`: PASS with GoReleaser `v2.17.0`, Syft module `v1.46.0`, two reproducible
  Darwin/Linux amd64/arm64 archive builds, SPDX SBOMs, and a generated Homebrew formula.
- Manual source review: PASS for HMAC comparison, parser bounds, generic external failures,
  no-store responses, bounded limiter cardinality, current-membership lookup, active-only
  single-successor rotation, forced RLS, and plaintext non-persistence.
- Every Go file has an Apache-2.0 SPDX header; `git diff --check` passes.
- GitHub open security queues before publication: Dependabot `0`, code scanning `0`, secret
  scanning `0`.
- Cleanup: zero kind clusters; Docker prune reclaimed `1.21 GB` after the final destructive gates.

## [C] Checkpoint

- Signed/DCO/GSTACK feature commit: `68ec421` (`2026-07-11/e1-api-key-exchange#1`).
- Implementation and local validation are complete; PR, merge, and exact post-merge evidence remain
  pending.
