# Session — 2026-07-12 — e1-google-service-account-federation

**Builder:** Gnani Rahul · **Branch:** gnanirahulnutakki/feat/e1-google-service-account-federation
**Slice(s):** E1 Google service-account identity verifier ([#94](https://github.com/ArdurAI/sith/issues/94)) · **Status:** validated, publication pending

---

## [G] Goal

Verify one short-lived Google-signed service-account ID token against an exact public issuer, JWKS,
audience, and Google-attested organization realm; normalize only the organization number plus
permanent numeric service-account ID into Sith's replay-safe cloud principal contract.

## [S] Scope

- Accept only the public `https://accounts.google.com` issuer and its explicit
  `https://www.googleapis.com/oauth2/v3/certs` JWKS endpoint in production; no discovery, endpoint,
  restricted, sovereign, private, or token-controlled fallback is permitted.
- Require RS256, `JWT`, one exact audience, immutable numeric `sub`, matching `azp`, verified
  service-account email, exact Google organization-number claim, expiry, and issued-at timestamp.
- Require IAM Credentials minting with `organizationNumberIncluded`; reject missing/null/non-numeric
  realm claims rather than inferring project ownership from email.
- Reuse #91 forced-RLS binding, HMAC-only replay consumption, fixed provider/workspace exchange, and
  short-lived Sith session. Do not log or persist private keys, raw ID tokens, or signing material.
- Out: self-signed service-account JWTs/assertions, user/agent/IAP tokens, Google token minting,
  project/email-derived realms, role/group/scope translation, and connector cloud credentials.

## [A] Research and design

- Google documents that service-account ID tokens are signed by the Google JWKS, identify a service
  account by an immutable numeric `sub`, set `azp` to that same ID, and are valid at most one hour.
  Google also distinguishes them from client-created, self-signed service-account JWTs/assertions.
- The IAM Credentials `generateIdToken` API can include an organization number only when requested;
  it emits that value as `google.organization_number`, or null when the account has no organization.
  That claim is the verifiable realm boundary, avoiding an email/project-name inference.
- The verifier reuses the established OIDC TLS 1.2+, no-proxy, redirect-confined, public-address
  transport and bounded JWK parser, but deliberately bypasses provider-controlled discovery. The
  configured Google JWKS host is an explicit exception to OIDC's same-origin discovery policy, not a
  relaxation of that generic policy.

## [T] Evidence so far

- #93 delivery was verified before branching: PR #97 merged `2c0df7b`; exact post-merge CI
  29185420419 passed release/SBOM in 58s and full build/race/RLS/binary/two-kind fan-out in 6m40s.
  Dependabot, code scanning, and secret scanning were all 0; no kind clusters remained.
- Primary Google documentation reviewed: service-account ID-token properties, self-signed assertion
  distinction, immutable service-account IDs, and the explicit `organizationNumberIncluded` claim.
- Focused package test: PASS. A TLS JWKS emulator covers a valid bound exchange and replay denial;
  wrong issuer/audience/realm/email/subject/actor/lifetime/header, unsigned proofs, and endpoint
  fallback configuration fail closed.

## [C] Checkpoint #1

- Final focused race suite: PASS. Full `make ci`: PASS with format/vet/golangci-lint clean,
  govulncheck reporting no findings, privacy boundary passing, all race coverage passing, and hubauth
  coverage at 85.2%.
- `make e2e-isolation`: PASS. PostgreSQL forced-RLS controls passed; hubauth coverage 85.2%,
  hubserver 89.5%, hubdb destructive suite 69.8%, and the selector fuzzer completed 50,000 runs.
- `make e2e-kind`: PASS in 80.222s against two temporary Kubernetes 1.36.1 clusters.
- `make release-check`: PASS. Two reproducible Darwin/Linux amd64/arm64 archive rebuilds, SPDX
  SBOMs, checksums, and Homebrew formula rendering passed.
- Manual red-team review: PASS. Checked closed issuer/JWKS pair, no discovery fallback, TLS/no-proxy
  public-address transport, redirect confinement, bounded replacement JWK cache, duplicate/hostile
  JOSE rejection, RS256-only signing, exact single audience, numeric typed organization realm,
  service-account-only claims, short lifetime, forced-RLS mapping, replay denial, and raw-proof
  non-persistence. CodeRabbit CLI is unavailable in this environment, so no external CodeRabbit
  approval is claimed.
- Final publication queues: Dependabot 0, code scanning 0, secret scanning 0. Cleanup found no kind
  clusters; Docker prune removed the disposable kind network, test images, and build cache, reclaiming
  2.727 GB without stopping active containers.
- Signed/DCO/GSTACK commit, PR, and exact post-merge evidence remain required.
