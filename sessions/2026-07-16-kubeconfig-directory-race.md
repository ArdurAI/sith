# Kubeconfig directory race-safe import

- Builder: gnanirahulnutakki
- Effort: deep
- Branch: `gnanirahulnutakki/fix/kubeconfig-directory-race`
- Issue: `#196`
- Status: ready for review

## Goal

Keep directory import bound to the originally selected directory and prevent validation-to-open races from reading replacement or external files.

## Scope

- Open one descriptor-backed `os.Root` after validating the selected directory identity.
- Traverse and open entries only through that root.
- Verify the opened root and regular files match the identities observed before parsing.
- Reject deferred local credential files and path-based exec commands after the root closes.
- Reject deferred CA, client certificate/key, token-file, and path-based exec reads; embedded data and PATH-based exec commands remain supported.
- Preserve entry-count, depth, and byte bounds plus relative, content-free diagnostics.
- Keep generic table pagination, watch bootstrap bounds, and hub concurrency findings out of this slice.

## Progress

[G] Repair the path-based validation-to-open race tracked by #196.
[S] Limit changes to kubeconfig directory traversal, focused adversarial tests, and roadmap evidence.
[A] Revalidated `origin/dev` at `f6fe9f4` and created an isolated EXTENDED worktree.
[T] Added deterministic pre-open root replacement, post-open pathname replacement, regular-file replacement, external-symlink swap, and replaced-ancestor regressions; the focused race suite passes.

## Verification

- `make ci` passed formatting, vet, lint, vulnerability scan, the full race suite, safety scripts, performance, subprocess e2e, and build.
- `make e2e-isolation` passed PostgreSQL RLS and 50,000 executions for each fleet-cache isolation fuzzer.
- `make e2e-kind` passed against two real kind clusters in 153.347 seconds.
- `make release-check` passed reproducible snapshot archives, SPDX SBOM validation, Homebrew formula generation, and multi-platform OCI layout verification.
- Red-team review added a live post-open non-symlink identity check and deterministic nested-ancestor replacement coverage.
- `README.md` documents the embedded credential-data and PATH-resolved exec-command constraints that prevent deferred reads from reopening the race.
- Notion decision log: `https://app.notion.com/p/39f2637edb078117a356d22f0fa569cb`
- Notion session checkpoint: `https://app.notion.com/p/39f2637edb07815c8f66eb0f714a41cf`

## Checkpoint

- `2026-07-16/kubeconfig-directory-race#1`
- `2026-07-16/kubeconfig-directory-race#2`

## Open questions

- None. Root or file identity ambiguity fails closed before parsing.
