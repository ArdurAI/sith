# Bounded generic Table materialization

- Builder: gnanirahulnutakki
- Effort: deep
- Branch: `gnanirahulnutakki/fix/table-materialization`
- Issue: `#190`
- Status: ready for review

## Goal

Bound the second server-side Kubernetes Table query used to render generic resources so a remote API server cannot bypass the fleet query's materialization budget.

## Scope

- Paginate Table lists with opaque continuation tokens and the existing 250-row query page size.
- Cap each decoded Table page at 4 MiB and the complete Table request at 16 MiB.
- Retain display fields only for objects that survive query selection inside the per-scope item budget.
- Reject ignored row limits, continuation cycles, expired or invalid continuation responses, and page/byte-budget exhaustion without restarting.
- Keep the unbounded dynamic watch bootstrap tracked by #192 out of this slice.

## Progress

[G] Repair generic server-table over-materialization tracked by #190.
[S] Limit production changes to the kubeconfig Table transport, query retention contract, call sites, and related evidence.
[A] Added bounded streaming decode, opaque pagination, row/byte budgets, retain-key filtering, and a Table-only error-body guard at the existing reviewed kubeconfig HTTP boundary.
[A] Preserved HTTP status classification while replacing untrusted non-2xx bodies with constant-size synthetic Kubernetes Status responses.
[T] Added adversarial unit cases for opaque multi-page tokens, selected-row retention, ignored limits, continuation cycles, expired and invalid continuations, body/token non-disclosure, and oversized pages.
[T] Expanded the real two-cluster kind gate with 260 ConfigMaps and proved a second-page object retains the API server's Data column.

## Verification

- Focused kubeconfig and privacy race suites passed; targeted golangci-lint returned zero findings.
- `make ci` passed formatting, vet, lint with zero findings, vulnerability scanning with no findings, the full race suite, privacy/safety contracts, performance, binary E2E, and build.
- `make e2e-isolation` passed digest-pinned PostgreSQL RLS and both 50,000-execution workspace-isolation fuzzers.
- `make e2e-kind` passed the real two-cluster pagination and immutable OCI image contracts in 210.245 seconds on the final tree.
- `make release-check` passed module verification, reproducible GoReleaser snapshots, SPDX SBOM validation, Homebrew formula generation, and the multi-platform OCI layout.
- Secret-pattern preflight passed.
- CodeRabbit identified two valid test/status-semantic findings; both were repaired, and the exact final tree returned zero findings across all nine files then in scope.
- Hosted PR and exact post-merge `dev` gates remain pending.

## Checkpoint

- `2026-07-16/table-materialization#1`

## Open questions

- None. The watch bootstrap's dynamic List remains separately tracked by #192.
