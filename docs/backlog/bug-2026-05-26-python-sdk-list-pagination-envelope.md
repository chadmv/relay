---
title: Python SDK list methods don't handle pagination envelope
type: bug
status: open
created: 2026-05-26
priority: high
source: surfaced while scoping the list-endpoint-sort design (docs/superpowers/specs/2026-05-26-list-endpoint-sort-design.md)
---

# Python SDK list methods don't handle pagination envelope

## Summary

The Python SDK predates cursor pagination. `list_jobs()` and `list_schedules()` in [python/src/relay/client.py](../../python/src/relay/client.py) iterate `response.json()` directly, assuming the server returns a bare JSON array. The server actually returns `{items, next_cursor, total}` (see [internal/api/jobs.go:289](../../internal/api/jobs.go:289) and `internal/api/pagination.go`), so iterating the response yields the string keys `"items"`, `"next_cursor"`, `"total"` and `Job.model_validate("items")` should raise. The SDK is also missing list methods for several paginated endpoints.

## Repro / Symptoms

Run [python/tests/integration/test_smoke.py:48](../../python/tests/integration/test_smoke.py:48) (`test_list_jobs_includes_recent_submission`) against a current `relay-server`. Expected: `pydantic.ValidationError` when validating the string `"items"` as a `Job`. If the test currently passes, the test is somehow skipped or the SDK conftest is mocking around the bug — either way the production code path is broken.

## Proposal

1. Update `list_jobs()` and `list_schedules()` to:
   - Accept optional `limit: int` and `cursor: str` parameters.
   - Read `response.json()["items"]` instead of iterating the top-level response.
   - Optionally expose `next_cursor` so callers can paginate. Either return a small `Page[Job]` wrapper, or provide a generator helper analogous to Go's `FetchAllPages[T]`.
2. Add the missing list methods: `list_workers`, `list_users`, `list_reservations`, `list_agent_enrollments`.
3. Once the sort feature ships (see `docs/superpowers/specs/2026-05-26-list-endpoint-sort-design.md`), add a `sort: str | None = None` parameter to each list method that passes through to `?sort=`.

## Acceptance / Done When

- `test_list_jobs_includes_recent_submission` passes against a paginated server.
- All paginated REST endpoints have a corresponding SDK method.
- The SDK exposes a documented way to fetch all pages and to fetch a single page with a cursor.
- The SDK accepts a `sort` parameter on every list method, validated server-side.

## Related

- `docs/superpowers/specs/2026-05-26-list-endpoint-sort-design.md` — sort feature that this bug blocks from being usable from Python
- `internal/relayclient/` — Go equivalent (`FetchAllPages[T]`, `PageEnvelope[T]`) for reference
- `docs/retros/2026-05-09-relay-mcp-server.md` — retro that landed the pagination envelope
