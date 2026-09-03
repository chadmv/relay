---
id: idea-2026-05-06-list-endpoint-filters
title: Advanced filter params for list endpoints
type: idea
priority: low
status: closed
closed: 2026-09-03
resolution: fixed
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

## Resolution
Closed at the fan-in of the 2026-09-02 web-frontend batch. Shipped: GET /v1/jobs ?q=, ?mine= (the caller's own jobs, in place of an arbitrary submitted_by), ?since= and ?until= (PR #178); GET /v1/scheduled-jobs ?enabled= and ?q=, and GET /v1/reservations ?worker_id= (PR #180); each with its frontend consumer (PRs #182, #183, #184). Not shipped and re-filed as one narrower item, [[idea-2026-09-03-list-filters-remainder-status-labels-users-q]]: multi-value ?status= on jobs and workers, ?label.<key>= on jobs and workers, and ?q= on users. The dependency note stands: ranked search needs a different cursor scheme than (created_at DESC, id DESC), and the substring ?q= shipped unranked for that reason.
