# Bounded watch bootstrap snapshots

- Builder: gnanirahulnutakki
- Effort: deep
- Branch: `gnanirahulnutakki/fix/watch-bootstrap`
- Issue: `#192`
- Status: ready for review

## Goal

Prevent kubeconfig list-watch hydration from materializing an unbounded collection before opening
each per-scope, per-kind watch stream.

## Scope

- Page the dynamic List at 250 objects under one absolute request deadline.
- Accept only complete snapshots within 10,000 objects and 128 pages per scope and kind.
- Preserve opaque continuation tokens and require one stable, nonempty resource version across pages.
- Emit `WatchError` and open no stream when limits, continuation, cancellation, or snapshot
  consistency checks fail.
- Keep the already-landed generic Kubernetes Table transport bound from #190 unchanged.

## Progress

[G] Repair unbounded watch bootstrap materialization tracked by #192.
[S] Limit production changes to kubeconfig watch bootstrap loading plus operator and roadmap evidence.
[A] Added complete, consistent paginated bootstrap loading and opened each watch from the verified
snapshot resource version only after all pages succeed.
[A] Rejected empty or changed resource versions, nil responses, ignored limits, continuation cycles,
expired/failed continuations, cancellation, and object/page budget exhaustion without retaining
partial objects.
[A] Sanitized continuation failures so opaque tokens and remote response text never enter emitted
errors.
[T] Added direct pagination, budget, consistency, cancellation, non-disclosure, and fail-closed
adapter regressions under the race detector.
[T] Extended the real two-cluster kind gate with a direct ConfigMap watch and proved
`sith-table-page-259` appears in the completed snapshot before the stream opens.
[R] Red-team review found two client-go fake constraints: root fake list actions discard supplied
pagination options and the object tracker accepts numeric watch resource versions only. Tests now
record options before that fake boundary and use the tracker's real numeric RV while pure unit tests
retain opaque token and RV coverage.

## Verification

- The complete kubeconfig race suite passed, plus 100 repeated race runs of the pagination,
  cancellation, non-disclosure, and fail-closed watch tests.
- Secret-pattern preflight found no credential literals or private-key material in the changed tree.
- CodeRabbit returned no actionable findings on the implementation diff; strict manual red-team
  review added nil-response and generic continuation non-disclosure coverage.
- `make ci` passed formatting, vet, lint with zero findings, `govulncheck` with no vulnerabilities,
  full repository race/coverage tests, policy scripts, tagged E2E, performance, and build on the
  rebased final tree.
- `make e2e-isolation` passed digest-pinned PostgreSQL RLS and 100,020 final workspace-isolation
  fuzz executions.
- `make e2e-kind` passed the combined real-cluster fanout/watch pagination, immutable OCI, and Argo
  projection contracts in 236.712 seconds on the rebased final tree.
- `make release-check` passed reproducible multi-platform archives, SPDX SBOM verification,
  Homebrew generation, and the multi-architecture distroless OCI layout.
- Hosted PR and exact post-merge `dev` gates remain pending.

## Checkpoint

- `2026-07-16/watch-bootstrap#1`

## Open questions

- None.
