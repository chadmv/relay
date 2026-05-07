---
id: idea-2026-05-06-list-endpoint-filters
title: Advanced filter params for list endpoints
type: idea
priority: low
status: open
created: 2026-05-06
---

# Advanced filter params for list endpoints

Deferred from the cursor-pagination feature (2026-05-06). The pagination envelope and cursor scheme are in place; these filters would layer on top.

## Proposed filters

- **`GET /v1/jobs`**
  - `?status=running,queued` — multi-value status filter (comma-separated)
  - `?submitted_by=<user_id_or_email>` — "my jobs"
  - `?since=<RFC3339>` / `?until=<RFC3339>` — time-range filters
  - `?label.<key>=<value>` — JSONB containment filter on job labels (requires `pg_trgm` or GIN index)

- **`GET /v1/workers`**
  - `?status=online,idle` — multi-value status filter
  - `?label.<key>=<value>` — label filter

- **`GET /v1/users`**
  - `?q=<substring>` — name/email substring search (likely needs `pg_trgm` index)

- **`GET /v1/scheduled-jobs`**
  - `?enabled=true|false` — filter by enabled state

## Dependency note

Filters that change sort order (e.g., relevance-ranked `?q=` search) need a different cursor scheme than `(created_at DESC, id DESC)`. Address that before implementing ranked search.
