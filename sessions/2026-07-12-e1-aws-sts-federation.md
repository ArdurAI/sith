# Session — 2026-07-12 — e1-aws-sts-federation

**Builder:** Gnani Rahul · **Branch:** gnanirahulnutakki/feat/e1-aws-sts-federation
**Slice(s):** E1 AWS STS identity proof verifier ([#92](https://github.com/ArdurAI/sith/issues/92)) · **Status:** in-progress

---

## [G] Goal

Verify a short-lived AWS-native identity proof against one explicitly configured regional STS
endpoint, then normalize only that verified caller into the existing replay-safe cloud principal
contract and current forced-RLS workspace membership mapping.

## [S] Scope

- Accept only a base64url-encoded, SigV4 pre-signed `GetCallerIdentity` URL with an explicit
  60-second-or-shorter expiry and `host;x-sith-audience` signed-header set.
- Pin a regional commercial, GovCloud, or China STS endpoint; reject the global endpoint, arbitrary
  hosts, ports, proxy-directed targets, endpoint fallback, redirects, credential-bearing URLs, and
  unknown or duplicate query keys.
- Reconstruct a header-minimal GET only to that pinned endpoint; parse a bounded STS XML response
  and normalize account plus immutable assumed-role ID. IAM users, federated users, and
  attacker-selected role-session names do not become Sith identities.
- Preserve #91's HMAC-only bounded replay guard and server-side RLS binding. No AWS access key,
  session token, signed request, signature, raw proof, or response is logged or persisted.
- Out: Azure #93 and Google #94, cloud token minting, general AWS API proxying, global STS support,
  and IAM-user long-lived credential authentication.

## [A] Research and implementation

- Reviewed AWS's GetCallerIdentity API and regional STS guidance. Header-signed SigV4 requests have
  no published explicit validity period, so the verifier deliberately requires a pre-signed request
  with `X-Amz-Expires` rather than inventing a lifetime for an authorization header.
- Implemented `AWSSTSVerifier` behind #91's `CloudProofVerifier` port with closed SigV4 query
  grammar, exact action/version/service/region/audience constraints, no redirect following, and a
  dedicated no-proxy TLS client.
- The normalized subject is `role-id:<AWS role ID>` from the verified STS `UserId`, not the assumed
  role ARN's session name. This makes a binding stable across short-lived role sessions while
  refusing long-lived IAM users.
- Added a TLS STS emulator integration test that receives the reconstructed request over HTTP, plus
  malformed, duplicate, endpoint fallback, expiry, wrong-service, wrong-audience, altered canonical
  request, long-lived identity, cross-partition, and wrong-account negatives.

## [T] Tests and evidence

- Focused hubauth unit suite: PASS.
- Focused hubauth race suite: PASS.
- `go vet ./internal/hubauth`: PASS.
- Final `make ci`: PASS. Format, vet, golangci-lint, govulncheck (no findings), race/coverage,
  privacy boundary, source-boundary/operator safety, binary E2E, latency, and build passed.
  Hubauth coverage is 84.6% and hubserver coverage is 89.5%.
- `make e2e-isolation`: PASS. Real PostgreSQL 18.4 forced-RLS controls passed; hubauth coverage is
  84.8%, hubserver 89.5%, hubdb destructive-suite 69.8%, and the fixed selector fuzzer completed
  50,000 iterations successfully.
- `make e2e-kind`: PASS in 92.119s against two temporary Kubernetes 1.36.1 kind clusters.
- `make release-check`: PASS. Reproducible Darwin/Linux amd64/arm64 archives, SPDX SBOMs,
  checksums, and Homebrew formula rendering passed.
- Manual red-team review: PASS. Checked fixed endpoint/host/region/partition rules, global and
  sovereign fallback denial, redirect/proxy containment, closed SigV4 grammar and duplicate query
  rejection, exact signed audience header, bounded expiry, altered-request STS rejection,
  response-size/XML/account/partition/role-ID checks, long-lived IAM-user denial, replay behavior,
  raw-proof/key non-persistence, and privacy allowlist scope. CodeRabbit CLI is unavailable in this
  environment, so no external CodeRabbit approval is claimed.
- `git diff --check`: PASS at the final local validation checkpoint.
- Final pre-publication queues: Dependabot 0, code scanning 0, secret scanning 0.
- Cleanup: kind reports no clusters; Docker prune removed the disposable kind network, pinned kind
  node image, PostgreSQL test image, and build cache, reclaiming 1.21 GB without stopping active
  containers.
- Remaining before publication: signed/DCO/GSTACK commits, PR CI, exact post-merge CI,
  issue/roadmap updates, and a final queue recheck.

## [C] Checkpoint #1

- Signed/DCO feature commit: 3530df4 (2026-07-12/e1-aws-sts-federation#1). It contains the pinned
  AWS STS verifier, TLS-emulator exchange and negative controls, privacy-boundary allowlist,
  README contract, and both the #92 journal and the missing #91 delivery evidence.

## [C] Checkpoint #2

- The final local validation and review evidence is ready for its signed documentation checkpoint
  (2026-07-12/e1-aws-sts-federation#2). PR and exact post-merge evidence remain pending.
