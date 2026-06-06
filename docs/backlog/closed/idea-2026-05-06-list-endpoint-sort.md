---
id: idea-2026-05-06-list-endpoint-sort
title: Configurable sort order for list endpoints
type: idea
priority: low
status: closed
created: 2026-05-06
closed: 2026-06-05
resolution: fixed
---

# Configurable sort order for list endpoints

The current cursor pagination scheme is fixed to `created_at DESC, id DESC`. This is the right default for time-ordered lists, but some use-cases want different orderings.

## Examples

- `GET /v1/jobs?sort=status` — group by status for a kanban-style view
- `GET /v1/workers?sort=name` — restore the pre-pagination alphabetical order
- `GET /v1/jobs?sort=priority` — surface high-priority jobs first

## Design constraint

Each distinct sort key requires a different cursor scheme. A cursor that encodes `(created_at, id)` is only valid for `ORDER BY created_at DESC, id DESC`. If multiple sort orders are supported simultaneously, the cursor must encode the sort key, and the server must validate that the cursor's sort key matches the request's `?sort=` param.

A simpler first step: support only a small fixed set of sort options, each with its own composite index, and document that cursors are not interchangeable across sort orders.

## Resolution

Shipped 2026-05-27 (see `docs/retros/2026-05-27-list-endpoint-sort.md`). `?sort=` is supported on the jobs, workers, users, scheduled_jobs, reservations, and agent_enrollments list endpoints via `parseSort`/`SortSpec` in `internal/api/pagination.go`, each with a `*_sort_integration_test.go`. Follow-up items were spun off and tracked separately (sort-flag CLI help, EXPLAIN ANALYZE sort indexes, endpoint path in error message). This original idea was completed but never moved to closed/ at the time.
