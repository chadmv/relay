---
title: Jobs Timeline view (6h/24h/7d)
type: idea
status: open
created: 2026-06-05
source: jobs-list-frontend retro (2026-06-05)
---

# Jobs Timeline view (6h/24h/7d)

## Summary
Add the Timeline view from the design handoff to the Jobs page: a time-windowed (6h/24h/7d) gantt-style layout of jobs. Being window-bounded, it needs no cursor pagination, but it does need a backend time-window query the API does not currently expose.

## Context
Deferred from the first jobs-list slice (Table view only). The hi-fi `HoloTimeline` component is the reference. Requires a new server endpoint or query parameter to fetch jobs within a time window.

## Related
- `web/src/jobs/` feature
- `internal/api/jobs.go` (would need a time-window list variant)
- `docs/retros/2026-06-05-web-jobs-list.md`
