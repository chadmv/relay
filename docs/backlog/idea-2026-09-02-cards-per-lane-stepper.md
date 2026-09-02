---
title: A user-adjustable cards-per-lane cap for the Jobs Lanes view
type: idea
status: open
created: 2026-09-02
priority: low
source: 2026-09-01 jobs-lanes spec (lane F), Decision 2
---

# A user-adjustable cards-per-lane cap

## Summary
The Lanes view fetches a fixed 10 jobs per lane. The item that shipped it proposed a stepper (default
10, min 3, max 50) and the hi-fi carries one, inert. It was deferred because 50 jobs across five lanes
at a 3 s cadence is a five-fold row-volume argument the slice would have had to win. If wanted, the cap
becomes part of each lane's query key (which is when keepPreviousData, removed as inert, earns its
place back) and is persisted beside the view choice.

## Related
- web/src/jobs/lanes.ts (LANE_LIMIT), web/src/jobs/useJobLanes.ts
- [[idea-2026-06-05-jobs-lanes-swimlanes-view]] (closed)
