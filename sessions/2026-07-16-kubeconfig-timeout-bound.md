# Kubeconfig timeout containment

- Builder: gnanirahulnutakki
- Effort: deep
- Branch: `gnanirahulnutakki/fix/kubeconfig-timeout-bound`
- Issue: `#181`
- Status: complete

## Goal

Bound kubeconfig operations whose underlying client-go credential helper ignores context cancellation while preserving progress for healthy contexts.

## Scope

- Add adapter-owned admission control for timed operations.
- Retain per-context capacity until the underlying operation exits.
- Limit retained operations per kubeconfig context without a globally exhaustible gate.
- Cover ignored cancellation with unit and real subprocess regressions.

## Actions

- Reproduced unbounded goroutine growth with a probe that waits after its caller times out.
- Confirmed client-go's exec authenticator starts credential helpers without `CommandContext`.

## Tests

- `go test -race -count=1 ./internal/connector/kubeconfig`
- Synthetic ignored-cancellation containment and healthy-peer regression.
- Real client-go exec credential subprocess containment, recovery, and secret-exclusion regression.
- `make ci`
- `make e2e-isolation` including 50,000 fuzz executions.
- `make e2e-kind KIND=/Volumes/EXTENDED/MacData/tools/bin/kind`
- `make release-check`

## Review

- Added explicit reachability validation after independent review.
- Replaced the global limit with per-context quarantine after a two-wedged-context red-team case showed global capacity could deny healthy peers.
- Closed the simultaneous admission-release and deadline race with cancellation checks under and after gate acquisition.
- Kept pre-existing query pagination and result-budget work in a separate issue.

## Checkpoint

- `2026-07-16/kubeconfig-timeout-bound#1`
