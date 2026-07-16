# Fleet image correlation evidence

- Builder: gnanirahulnutakki
- Effort: deep
- Branch: `gnanirahulnutakki/fix/brain-image-evidence`
- Issue: `#182`
- Status: complete

## Goal

Require fresh, cited, deterministic image repo-digest evidence before Sith emits a fleet-wide failure correlation.

## Scope

- Reject stale repo-digest observations from fleet correlation.
- Cite one canonical digest observation for every correlated entity.
- Deduplicate repeated observations independently of input order.
- Preserve canonical rule, abstention, and ranking behavior.

## Tests

- `go test -race -count=1 ./internal/brain`
- Stale digest rejection, cited correlation, duplicate selection, full input-order reversal, and canonical source-reference tie-break coverage.
- `make ci`
- `make e2e-isolation`
- `make e2e-kind KIND=/Volumes/EXTENDED/MacData/tools/bin/kind`
- `GOPATH=/Volumes/EXTENDED/MacData/go make release-check GORELEASER=/Volumes/EXTENDED/MacData/tools/bin/goreleaser`

## Review

- Preserved base failure citations because the fleet verdict must prove both the failure and the shared image.
- Added a canonical full-reference tie-break after independent review found equal-time/source/value observations could remain input-order dependent.

## Checkpoint

- `2026-07-16/brain-image-evidence#1`
