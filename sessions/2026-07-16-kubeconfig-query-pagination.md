# Kubeconfig query pagination

- Builder: gnanirahulnutakki
- Effort: deep
- Branch: `gnanirahulnutakki/fix/kubeconfig-query-pagination`
- Issue: `#185`
- Status: complete

## Goal

Bound Kubernetes resource-list response size and materialization across kubeconfig contexts without changing deterministic fleet-wide query limits.

## Scope

- Request Kubernetes lists in pages of at most 250 objects.
- Divide a fixed 10,000-object scan budget deterministically across sorted contexts.
- Stop pathological continuation chains after 128 pages per context.
- Discard partial scope facts when a continuation request fails or times out.
- Surface truncated contexts through coverage, cache state, text rendering, and brain abstention.
- Preserve last-known cache rows outside a truncated prefix and clear truncation only after a complete watch snapshot.

## Tests

- `go test -race -count=1 ./internal/fleet ./internal/connector ./internal/connector/kubeconfig ./internal/fleetcache ./internal/fleetrender ./internal/brain`
- Pagination, opaque continuation, deterministic multi-context budgets, cancellation, and fail-closed coverage unit regressions.
- Real two-cluster kind pagination and fleet-wide limit regression.
- `make ci`
- `make e2e-isolation` including 50,012 scoped fuzz executions.
- `make e2e-kind KIND=/Volumes/EXTENDED/MacData/tools/bin/kind`
- `GOPATH=/Volumes/EXTENDED/MacData/go make release-check GORELEASER=/Volumes/EXTENDED/MacData/tools/bin/goreleaser`

## Review

- Kept the scan budget independent of the caller's output limit so name/prefix filters and global sorting retain their prior semantics whenever coverage is complete.
- Marked truncated cache snapshots degraded and limited recovery to full watch snapshots rather than ordinary liveness events.
- Independent CodeRabbit review reported zero findings across all changed implementation and regression files.
- Generic server-side Table responses remain a separate bounded-materialization issue under `#190`.

## Checkpoint

- `2026-07-16/kubeconfig-query-pagination#1`
