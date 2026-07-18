# Session — 2026-07-18 — E6 offline portable audit verifier

**Builder:** Gnani Rahul · **Branch:** `gnanirahulnutakki/feat/e6-offline-audit-verifier`
**Slice:** [#258](https://github.com/ArdurAI/sith/issues/258), E6 [#24](https://github.com/ArdurAI/sith/issues/24) · **Status:** locally verified

## [G] Goal

Let an incident responder or compliance recipient verify an F6.4a export from the serialized JSON
alone, without contacting a hub or claiming external authenticity.

## [D] Design

- Centralize the format-1 and format-2 canonical SHA-256 framing in `internal/auditrecord`; the
  PostgreSQL writer, retained-chain verifier, HTTP projection check, and offline verifier share it.
- Add `sith audit verify <export.json>` as a local-only command. It accepts exactly one stable,
  non-symlink regular file, bounds the read to 1 MiB, and retains the 512-entry document ceiling.
- Parse JSON case-sensitively and reject duplicate, unknown, malformed, or trailing content before
  checking the closed schema and every hash-bound field, link, and head.
- Emit only schema, workspace, count, head, and the explicit `internally-consistent` status.

## [S] Security and non-claims

The command opens no network connection, hub session, database, credential store, temporary file,
or telemetry path. It refuses directories, devices, FIFOs, symlinks, unstable files, oversized
inputs, unsupported formats, malformed shapes, and any hash mismatch. Internal consistency does
not prove origin and cannot detect wholesale replacement by a privileged store owner without an
external anchor. No Ardur decision-ledger, intent lifecycle, WORM, pagination, proposal, dispatch,
or execution claim is added.

## [T] Proof plan

- Immutable golden hashes for policy and approval formats plus negative controls for all bound
  fields and the closed invalid-request sentinel.
- CLI tests for bounded summaries, strict JSON, tamper, file type, symlink, FIFO, size, and race.
- PostgreSQL mixed-version export-to-offline verification, full CI, vulnerability, release/SBOM,
  Helm, OCI, two-cluster Kind, independent review, hosted exact-head, and post-merge proof.

## [V] Local proof

- Focused unit and race suites pass for `auditrecord`, CLI, Hub HTTP export, and storage.
- Immutable format-1 and format-2 golden hashes pass; 50,000 canonical-field mutations cannot
  verify without rehashing.
- PostgreSQL 18.4 forced-RLS integration passes at 72.8% focused and 76.2% isolation coverage,
  including a mixed-version database export verified by the portable algorithm.
- Full `make ci` passes with zero lint findings and no reachable vulnerabilities.
- Reproducible release/SPDX SBOM, release OCI layout, Helm 4.2.3, cross-platform OCI, and both
  50,000-case workspace isolation campaigns pass.
- Kubernetes v1.36.1 two-cluster Kind passes in 237.938 seconds.
- CodeRabbit 0.6.5 identified sub-microsecond timestamp malleability and invalid-UTF-8 handling;
  both were fixed with negative controls. The final complete 15-file review has no findings.

## [O] Operability and cost

One bounded local read and at most 512 SHA-256 computations. No cloud resource, object storage,
egress, background process, or recurring telemetry cost.

## [N] Next

Complete the full local gate matrix and adversarial review, then create one signed DCO/GSTACK PR
into `dev`. Do not close #258 before exact post-merge `dev` CI and CodeQL pass and all security
queues are empty.
