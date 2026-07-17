# Argo CD Application graph facts

- Builder: gnanirahulnutakki
- Effort: standard
- Branch: `gnanirahulnutakki/feat/argocd-application-facts`
- Issue: `#206`
- Status: locally verified; awaiting signed PR and exact post-merge proof

## Goal

Establish the first hand-written Wave-1 Argo CD fact contract by projecting one already-authorized
`Application` CRD into bounded, deterministic LIVE, DESIRED, TIMELINE, and drift graph evidence.

## Decision

Build the pure projector before a network client or the E12 gRPC framework. The repository requires
the day-1 adapters to prove their behavior before generalization, and this boundary avoids inventing
new credential/configuration APIs while kubeconfig pagination work remains active in #190.

The projector receives one already-fetched object plus explicit workspace, scope, and observation
time. It performs no network access, credential loading, shell execution, planning, verification,
sync, or rollback. A later reader can reuse this exact fact contract without changing graph or brain
semantics.

## Safety and boundedness

- Validate the Argo API group/kind and cluster/namespace-bounded identity before emitting facts.
- Serialize allowlisted fields only. Repository and destination URLs lose userinfo, query, and
  fragment data; raw Helm/Kustomize/plugin parameters and status messages are never copied.
- Cap sources at 16, retained history at the newest 32 entries, total facts at 36, and every encoded
  fact payload at 16 KiB.
- Mark the oldest retained history entry when earlier history was truncated.
- Missing status, sync, health, or history emits fewer facts instead of fabricated evidence.
- Keep a source-level test that rejects network, dynamic-client, shell, and mutation seams.

## Progress

[G] Add a production-safe Argo Application fact projector for #206.
[S] Limit this slice to pure normalization plus real API-server proof; discovery/query and mutations
remain explicit non-goals.
[A] Reconciled #46 and #30 against live code and specs. E14's local rule engine is shipped; its
governed renderer remains blocked on E4/E5. E12 explicitly requires hand-written adapters before
the subprocess framework.
[A] Opened #206 and created a dedicated EXTENDED worktree from the exact `origin/dev` merge head.
[A] Implemented deterministic desired, health, sync-drift, and bounded sync-history projections with
OTel-aligned cluster/namespace identity.
[T] Added malformed identity/status, URL credential stripping, raw Helm parameter non-retention,
payload budget, history truncation, abstention, determinism, and no-mutation boundary tests.
[T] Added a real kind regression that installs a minimal Application CRD, creates and reads one
Application through the API server, projects it, assembles the graph, and proves secret markers are
absent.
[R] Red-team review caught that valid Helm OCI repositories can omit a URL scheme. Updated the
sanitizer to accept documented scheme-less OCI and SCP-like SSH forms while stripping userinfo and
rejecting query/fragment credentials, local paths, and unsupported schemes.

## Verification

- Focused race tests and privacy-boundary tests pass; final projector coverage is 80.5%.
- `golangci-lint`, `go vet`, `govulncheck`, full repository race tests, policy scripts, tagged E2E,
  and the production build pass under `make ci`.
- `make e2e-isolation` passes, including 100,024 final workspace-isolation fuzz executions.
- Focused real-kind projection passes in 32.783 seconds with pinned kind v0.32.0; the complete kind
  fanout, OCI, and Argo suite passes in 190.748 seconds.
- The final full kind fanout, OCI, and Argo suite passes in 193.362 seconds.
- `make release-check` passes two reproducible GoReleaser builds, archive/SBOM verification, and
  the multi-architecture distroless OCI layout contract.
- README was reviewed. No update is warranted because this slice adds no user-facing command,
  network reader, configuration, credential flow, or supported runtime behavior yet.
- The final full-gate rerun after the red-team OCI compatibility correction passes.
- Notion decision: `3a02637e-db07-8194-8160-e78cb189cc86`.
- Notion session: `3a02637e-db07-8143-9bf5-f5629753a902`.

Primary compatibility references:

- <https://argo-cd.readthedocs.io/en/stable/user-guide/application-specification/>
- <https://argo-cd.readthedocs.io/en/latest/user-guide/helm/>
- <https://argo-cd.readthedocs.io/en/latest/user-guide/oci/>

## Checkpoint

- `2026-07-16/argocd-application-facts#1`

## Open questions

- Network discovery/query composition remains a later child slice. It must reuse existing
  kubeconfig authorization and bounded pagination rather than add another credential store.
