---
title: PATCH /v1/workers/{id} accepts max_slots 0 or negative silently, disabling dispatch with none of the disable endpoint's visibility
type: bug
status: open
created: 2026-08-23
priority: low
source: 2026-08-23 deep roadmap refresh - gaps agent finding
---

# PATCH /v1/workers/{id} accepts max_slots 0 or negative silently, disabling dispatch with none of the disable endpoint's visibility

## Summary
`handleUpdateWorker` merges `body.MaxSlots` with no bounds check
(`internal/api/workers.go:405-408`) and the column has no CHECK constraint (`max_slots INT NOT
NULL DEFAULT 1`, `internal/store/migrations/000001_initial.up.sql:31`), so a typo'd `0` or `-1` is
stored and the worker silently stops receiving dispatches via the `free <= 0` skip
(`internal/scheduler/dispatch.go:228-231`) - duplicating the disable endpoint's effect while the
worker's status stays `online`.

## Context
Same validates-almost-nothing shape as
[[bug-2026-08-09-create-reservation-500-on-client-error]], on a different surface no item covered.
The SPA's worker-edit form client-validates `max_slots` (the 2026-07-01 worker-mutations slice),
which is exactly why the server-side gap went unnoticed - the API remains the contract, and the
CLI or a raw client bypasses the form.

## Acceptance / Done When
- `max_slots < 1` on the PATCH returns a 400 with a fixed message; a migration decision (CHECK
  constraint or not) is recorded either way.
- A regression test drives `0` and `-1` through the handler (RED at HEAD: 200 + silent dispatch
  stop).

## Related
- `internal/api/workers.go:405-408`, `internal/store/query/workers.sql:18-22`, `internal/scheduler/dispatch.go:228-231`
- [[bug-2026-08-09-create-reservation-500-on-client-error]] - the sibling validation-funnel item
