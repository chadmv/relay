---
id: idea-2026-05-06-list-endpoint-sort
title: Configurable sort order for list endpoints
type: idea
priority: low
status: open
created: 2026-05-06
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
