---
title: Jobs Timeline view (6h/24h/7d)
type: idea
status: closed
closed: 2026-09-03
resolution: fixed
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

## Resolution
Shipped in lane JF of the 2026-09-02 web-frontend batch: a created-time timeline view on the Jobs page over ?since/?until from PR #178, with a window picker, bars from created through started to finished (a never-started job is an instant marker), a truncation banner, and a stable query key whose fetch captures its own quantized anchor and refreshes on refetchInterval, so a walk slower than one tick still completes; a failed refresh keeps the last successful rows and says so beside them, and disabling or unmounting cancels the in-flight walk.
