# Session — 2026-07-18 — E6 bounded verified audit export

**Builder:** Gnani Rahul · **Branch:** `gnanirahulnutakki/feat/e6-bounded-audit-export`
**Slice:** [#256](https://github.com/ArdurAI/sith/issues/256), E6 [#24](https://github.com/ArdurAI/sith/issues/24) · **Status:** locally verified

## [G] Goal

Expose the existing privacy-minimized policy and approval hash chain as one portable workspace
compliance record without claiming the future unified action/Ardur decision ledger.

## [D] Design

- Add the closed `export-audit` action and `audit.export` verb. Only a signed workspace admin may
  pass both the PEP and the independent store-side role check.
- Accept one exact bearer-only, query-free, body-free
  `GET /v1/workspaces/{workspace}/audit/export` route. Browser cookies, filters, selectors, raw
  payloads, non-canonical paths, and spoofed identity/correlation carriers are not accepted.
- Durably audit the authorization decision before reading the export. The successful document
  therefore contains the decision that authorized its own disclosure.
- Read and verify the complete retained chain in one forced-RLS repeatable-read transaction, return
  only after commit, then structurally revalidate and encode the finished document at the HTTP
  boundary. Network backpressure cannot pin the database snapshot.
- Limit one online document to 512 entries and four concurrent process requests. Return one generic
  unavailable response for saturation, oversize, tamper, uninitialized state, or store failure.

## [S] Security and non-claims

The export contains only actor/role, closed operation and verdict metadata, approval evidence
digests, timestamps, trace identifiers, and SHA-256 chain links already retained by Sith. It adds no
target, arguments, selector, justification, credential, policy digest, request/response payload,
connector, KMS, filesystem, process, Kubernetes client, background job, object store, or mutation
surface. It is not an intent-correlated decision ledger, WORM storage, external anchoring,
pagination, or E6 completion.

## [T] Local proof

- Unit and race suites pass for the portable schema, tenant role matrix, PEP, store, handler,
  runtime composition, and structural privacy boundary.
- PostgreSQL 18.4 proves forced-RLS isolation, own-authorization inclusion, complete-chain
  verification, tamper refusal, exact 512-entry success, exact 513-entry refusal, and admin-only
  store access.
- Full `make ci` passes after the final payload-free request hardening; `govulncheck` reports no
  reachable vulnerability.
- Reproducible release/SBOM, multi-platform OCI, Helm, two-cluster Kind, and two 50,000-case
  cross-workspace plus two 50,000-case audit-framing fuzz gates pass.
- CodeRabbit 0.6.5 found documentation/test-policy improvements and one body-framing omission. All
  were corrected; the final complete 17-file review reports no findings.

## [O] Operability and cost

Each successful request adds one audit row, scans at most 512 retained rows, buffers a bounded JSON
document, and incurs normal HTTP egress. There is no new cloud resource or recurring stream. Larger
workspaces need a separately reviewed pagination or asynchronous export protocol.

## [N] Next

Create one signed DCO/GSTACK commit and PR into `dev`; require exact-head CI, CodeQL, empty review
threads and security queues, merge, and exact post-merge `dev` CI/CodeQL before closing #256.
