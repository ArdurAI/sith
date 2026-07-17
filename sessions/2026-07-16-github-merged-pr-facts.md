# GitHub merged pull-request timeline facts

- Builder: gnanirahulnutakki
- Effort: deep
- Branch: `gnanirahulnutakki/feat/github-merged-pr-facts`
- Issue: `#212`
- Status: locally verified; awaiting signed PR and exact post-merge proof

## Goal

Define Sith's first GitHub normalization contract by projecting one already-authorized, already-
fetched REST `Get a pull request` response into bounded TIMELINE evidence without adding an HTTP
client, token/configuration seam, persistence, repository-to-workload inference, or mutation.

## Scope

- Pin the source shape to GitHub REST API version `2026-03-10`.
- Take workspace, host, owner, repository, pull number, and collection time only from trusted caller
  input. Response URLs and nested repository objects never define identity or tenancy.
- Emit one merge event only from internally consistent `merged=true` evidence. Valid unmerged pull
  requests abstain, including when GitHub supplies a pre-merge test-merge SHA.
- Retain only pull number, event kind, head/base/merge commit SHAs, and merge time.
- Keep every event unattached until a separately governed repository-to-workload relation exists.
- Bound source bytes, JSON depth, caller identity, fact count, encoded payload, and clock skew.

## Decision

Use an exact-key, allowlist-only JSON decoder around the documented GitHub response. Go's default
struct decoder accepts case-insensitive key aliases; exact-key extraction prevents casing variants
from redefining a required field while still allowing GitHub to add unrelated response fields.
Duplicate keys, trailing JSON, excessive nesting, invalid UTF-8, and type mismatches fail closed.

The source merge timestamp orders the TIMELINE fact, while caller collection time bounds it with a
five-minute clock-skew allowance. Strict zero-skew comparison would create avoidable outages on a
slightly skewed node; an event beyond the fixed allowance remains invalid. Both SHA-1 and SHA-256
hex commit identifiers are accepted so the fact contract does not hard-code one repository object
format.

## Security, operability, and cost

- Titles, bodies, users, labels, URLs, nested repository data, clone tokens, branch labels, and all
  unknown fields are discarded before graph construction and never echoed in validation errors.
- The source-level boundary test rejects network, filesystem, process, database, gRPC, client-go,
  credential, persistence, and mutation seams.
- This pure in-memory projector creates no infrastructure and no runtime cloud cost. A later live
  reader must use least-privilege GitHub read permission and account for API rate limits and egress.

## Progress and verification

[G] Normalize merged GitHub pull requests into bounded TIMELINE facts for #212.
[S] Pure response projection only; live auth/fetching, desired manifests, governed PR writes, and
repository-to-workload relations remain out of scope.
[A] Verified the E12 Wave-1 contract against repository specs and the current official GitHub REST
API versioning and pull-request endpoint documentation, then opened #212 from exact `origin/dev`.
[A] Implemented exact-key parsing, caller-trusted identity, honest unmerged abstention, bounded
clock skew, allowlisted payload construction, and permanently unattached graph facts for this slice.
[T] Race-enabled focused tests pass with 94.9% statement coverage. Adversarial cases cover secret
non-retention, exact-key aliases, duplicate/trailing/deep JSON, malformed types/times/SHAs, every
input budget, graph validity, determinism, abstention, and the no-capability boundary.
[T] A ten-second fuzz campaign completed more than 550,000 executions without a panic, invalid
fact, capability escape, excess fact count, or oversized payload.
[T] `make ci` passes formatting, vet, pinned lint with zero findings, vulnerability scanning with no
findings, the full race suite, policy tests, warm-view performance, subprocess E2E, and the build.
[A] Hosted review found two valid hardening gaps: the initial capability test blacklisted known APIs
instead of failing closed on any new import or declaration, and wrapped JSON errors could echo an
attacker-sized numeric literal. The final boundary exact-allowlists every production import, type,
value, function, and method, rejects injected interfaces, and returns fixed field-level decode
errors with an explicit overlong-number regression.
[T] After review hardening, `make ci` and `make e2e-isolation` pass again. The final
`make e2e-kind` passes the pinned Kubernetes fanout, OCI, and Argo suite in 237.406 seconds.
`make release-check` passes module verification, two reproducible release builds,
archive/SPDX SBOM inspection, Homebrew formula generation, and the multi-platform distroless OCI
layout. The release check used an isolated temporary `GOPATH` because this machine's configured
GOPATH root contains another `go.mod`.
[T] `README.md` was reviewed in full. No update is warranted because this slice adds no user-facing
command, configuration, authentication flow, endpoint, runtime connector, or supported behavior;
the roadmap and this engineering checkpoint are the correct documentation surfaces.

Primary compatibility references:

- <https://docs.github.com/en/rest/pulls/pulls?apiVersion=2026-03-10#get-a-pull-request>
- <https://docs.github.com/en/rest/about-the-rest-api/api-versions?apiVersion=2026-03-10>

## Checkpoint

- `2026-07-16/github-merged-pr-facts#1`
- `2026-07-16/github-merged-pr-facts#2`

## Open questions

- Live GitHub authentication and fetching remain a later connector child. That slice should use
  the documented least-privilege `Pull requests: read` permission and reuse this projector.
