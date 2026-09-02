---
title: No web surface test renders the server's unsupported-sort 400
type: idea
status: open
created: 2026-09-02
priority: low
source: Phase 4 security lens on the 2026-09-01 pager chain (lane B of the web SPA batch)
---

# No web surface test renders the server's unsupported-sort 400

## Summary
toggleSort now constrains its field parameter at compile time, and every list request builds its
query with URLSearchParams, so a bad sort key cannot be typed today. The backstop for a future
loosening is parsePage's allow-list 400, pinned Go-side. No web test asserts what a surface renders
when that 400 arrives: the frontend tests pin only that a sort change drops the stale cursor.

## Proposal
One test per list surface (or one on the shared list-error path) that serves a 400 with the server's
unsupported sort key body and asserts the rendered error, so a broken list is visible rather than a
blank table.

## Related
- [[idea-2026-08-14-toggle-sort-generic]] (closed), internal/api/pagination.go (parseSort)
