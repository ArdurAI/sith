# Fleet-cache workspace scope metadata

- Builder: gnanirahulnutakki
- Effort: deep
- Branch: `gnanirahulnutakki/fix/fleetcache-workspace-scope`
- Issue: `#183`
- Status: complete

## Goal

Keep discovery metadata for identically named cluster scopes isolated by workspace.

## Scope

- Key scope metadata by workspace and scope name.
- Preserve fail-closed behavior for guessed and ambiguous scopes.
- Ensure refreshing one workspace cannot mutate or remove another workspace's metadata.
- Keep record and coverage mutation-contract redesign in a separate issue.

## Tests

- `go test -race -count=1 ./internal/fleetcache`
- `make ci`
- `make e2e-isolation`
- `make e2e-kind`
- `make release-check`

## Review

- An independent review found ambiguous success still cleared global coverage failures despite leaving workspace metadata untouched.
- Fixed by rejecting non-unique scope membership before any reachability, coverage, or last-error mutation, with a prior-watch-failure regression.

## Checkpoint

- `2026-07-16/fleetcache-workspace-scope#1`
