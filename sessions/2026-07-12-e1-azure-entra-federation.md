# Session — 2026-07-12 — e1-azure-entra-federation

**Builder:** Gnani Rahul · **Branch:** gnanirahulnutakki/feat/e1-azure-entra-federation
**Slice(s):** E1 Azure Entra workload identity verifier ([#93](https://github.com/ArdurAI/sith/issues/93)) · **Status:** delivered

---

## [G] Goal

Verify a short-lived, tenant-specific Microsoft Entra workload identity token through an explicitly
configured authority and JWKS policy, then normalize only the configured tenant plus immutable
workload object identity into Sith's replay-safe cloud principal contract.

## [S] Scope

- Pin one Azure cloud authority, tenant UUID, token version/type, issuer, audience, asymmetric
  algorithm, JWKS policy, expiry, and replay identifier; reject `common`, `organizations`, authority
  fallback, token-controlled discovery, and cross-cloud keys.
- Require app-only workload semantics (`idtyp=app`) with immutable Entra object identity (`oid`),
  exact `tid`, and explicit actor/client identity when configured. Ignore upstream roles, groups,
  scopes, and workspace claims for Sith authorization.
- Reuse #91's RLS binding, HMAC-only replay guard, fixed provider/workspace exchange handler, and
  short-lived Sith session. Do not log or persist raw tokens or signing material.
- Out: Azure token minting, delegated user identities, Azure public/Gov/China fallback, and Google
  #94 verification.

## [A] Research and design

- Microsoft requires resources to validate exact audience, tenant, subject, and actor; it recommends
  immutable `tid`+`oid` identity rather than mutable display/user-name claims. App-only authorization
  also requires `idtyp=app` when using application identity claims.
- Entra access-token version controls the tenant-specific discovery path: v1 metadata omits `/v2.0`,
  while v2 metadata uses `/v2.0`. Tenant-independent `common` metadata is intentionally outside this
  slice because Sith binds one configured tenant authority per verifier.
- Azure public, US Government, China, and private authorities are distinct configuration profiles;
  the implementation will make authority host selection closed and will not infer one from token
  claims.

## [T] Evidence so far

- Reviewed Microsoft primary claim-validation, access-token, OIDC, and cloud-authority
  documentation before implementation.
- AWS #92 delivery was verified and recorded before branching: PR #96 merge `e92be16`, exact
  post-merge CI 29184666475 green; Dependabot, code scanning, and secret scanning are 0.
- Focused Entra unit/race suite: PASS. TLS discovery/JWKS emulator covers a valid pinned workload,
  current RLS membership exchange, replay denial, wrong tenant/audience/actor/version/type/expiry,
  and authority fallback configuration.
- `make ci`: PASS. Format, vet, golangci-lint, govulncheck (no findings), race/coverage, privacy
  boundary, binary E2E, and build passed. Hubauth coverage is 84.9%.
- `make e2e-isolation`: PASS. PostgreSQL forced-RLS controls passed; hubauth coverage 84.9%,
  hubserver 89.5%, hubdb destructive suite 69.8%, and the selector fuzzer completed 50,000 runs.
- `make e2e-kind`: PASS in 91.350s against two temporary Kubernetes 1.36.1 clusters.
- `make release-check`: PASS. Reproducible Darwin/Linux amd64/arm64 archives, SPDX SBOMs,
  checksums, and Homebrew formula rendering passed.
- Manual red-team review: PASS. Checked closed authority/cloud selection, exact tenant issuer and
  same-origin JWKS, duplicate/hostile JWT handling inherited from OIDC, RS256 key policy, bounded
  token lifetime, immutable tid+oid normalization, app-only/actor enforcement, no upstream-role
  authorization, RLS mapping, replay behavior, and raw-token non-persistence. CodeRabbit CLI is
  unavailable in this environment, so no external CodeRabbit approval is claimed.
- Final pre-publication queues: Dependabot 0, code scanning 0, secret scanning 0.
- Cleanup: kind reports no clusters; Docker prune removed the disposable kind network, pinned kind
  node image, PostgreSQL test image, and build cache, reclaiming 1.659 GB without stopping active
  containers.

## [C] Checkpoint #1

- Signed/DCO feature commit: 44660eb (2026-07-12/e1-azure-entra-federation#1). It contains the
  tenant-pinned Entra verifier, TLS/JWKS emulator and RLS/replay controls, reused OIDC verifier
  transport, README boundary, Azure journal, and the missing AWS #92 delivery evidence.

## [C] Checkpoint #2

- Signed/DCO documentation checkpoint: `6a0fdff`
  (`2026-07-12/e1-azure-entra-federation#2`).
- Delivery PR [#97](https://github.com/ArdurAI/sith/pull/97) merged cleanly into `dev` as
  `2c0df7b21bf4663bcc5db65682846624cb62cb85` on 2026-07-12 after both PR gates passed:
  build/vet/gofmt/lint/test/e2e in 6m57s and reproducible archives/SPDX SBOM/Homebrew formula in
  1m0s.
- Exact post-merge `dev` CI [29185420419](https://github.com/ArdurAI/sith/actions/runs/29185420419)
  passed: reproducible archives/SPDX SBOM/Homebrew formula in 58s and the full build, race,
  isolation, binary-smoke, and real two-kind-cluster fan-out suite in 6m40s.
- #93 is closed with the delivery evidence; parent E1 #85 and roadmap #19/#39 are updated before
  moving to Google service-account federation #94.
