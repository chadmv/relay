---
title: Include endpoint path in SortSpec "unsupported sort key" error
type: idea
status: closed
created: 2026-05-27
closed: 2026-06-05
resolution: fixed
priority: low
source: list-endpoint-sort retro (docs/retros/2026-05-27-list-endpoint-sort.md)
---

# Include endpoint path in SortSpec "unsupported sort key" error

## Summary

The list-endpoint-sort spec described the validation error as `"unsupported sort key 'X' for /v1/jobs; supported: ..."`, but `parseSort` is a shared utility that doesn't receive the endpoint path. The shipped error reads `"unsupported sort key 'X'; supported: ..."` — informative but missing the endpoint context that would help a developer who's hitting multiple list endpoints in one session.

## Proposal

Two options:

1. Add an `EndpointPath string` field to `api.SortSpec` and have each per-endpoint spec set it. `parseSort` formats the error with the path when set.
2. Have `parsePage` (which has the `*http.Request` and can read `r.URL.Path`) format the error rather than `parseSort`.

Option 2 is slightly cleaner because it keeps `SortSpec` purely declarative — the spec is what's allowed, not where it lives. The downside is that `parseSort`'s own error message no longer contains the path, so any direct unit test of `parseSort` needs the path injected differently.

Pick option 2; format the error in `parsePage`.

## Acceptance / Done When

- The 400 response body for an invalid `?sort=` key contains the request path.
- Existing tests that match the error message via `Contains(..., "unsupported sort key 'X'")` still pass.

## Related

- `internal/api/pagination.go` — `parseSort` and `parsePage`
- `internal/api/pagination_test.go` — `TestParsePage_UnknownSortKey_400`
- Per-endpoint sort integration tests in `internal/api/*_sort_integration_test.go`

## Resolution

Implemented as proposed (option 2). `parsePage` in `internal/api/pagination.go:254-258` reformats the `unsupportedSortKeyError` with `r.URL.Path`: `"unsupported sort key '%s' for %s; supported: %s"`. `SortSpec` stays purely declarative — no `EndpointPath` field. Both acceptance criteria are covered by `TestParsePage_UnknownSortKey_400` (`internal/api/pagination_test.go:362`), which asserts the body contains both `for /v1/jobs` and `unsupported sort key 'labels'`. This item was already filed under closed/ but its frontmatter status was never flipped; corrected here.
