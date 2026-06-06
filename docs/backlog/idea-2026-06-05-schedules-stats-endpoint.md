---
title: Fleet-wide schedules stats endpoint for the summary strip
type: idea
status: open
created: 2026-06-05
source: deferred from web Schedules list slice (retro 2026-06-05-web-schedules-list)
---

# Fleet-wide schedules stats endpoint for the summary strip

## Summary
The ENABLED/PAUSED summary strip on the Schedules page is page-scoped (counts only the loaded 50), not fleet-wide. A GET /v1/scheduled-jobs/stats aggregate (parallel to the workers stats endpoint added on 2026-06-04) would make the counts accurate across all schedules.

## Proposal
Add a stats endpoint returning enabled/paused/total counts (owner-scoped for non-admins, fleet-wide for admins), mirroring the workers /stats pattern, and consume it on SchedulesPage with a page-scoped fallback until the first response. Could also host the failed-runs-24h aggregate.

## Related
- internal/api/workers.go (handleWorkerStats, the precedent)
- web/src/schedules/SchedulesPage.tsx (summary strip)
- docs/backlog/idea-2026-06-05-failed-24h-stat.md
- docs/retros/2026-06-05-web-schedules-list.md
