---
title: formatRelativeTime duplicated across workers and schedules modules
type: bug
status: open
created: 2026-06-05
priority: low
source: noticed during web Schedules list code review (retro 2026-06-05-web-schedules-list)
---

# formatRelativeTime duplicated across workers and schedules modules

## Summary
formatRelativeTime is now duplicated verbatim between web/src/workers/liveness.ts and web/src/schedules/format.ts; a second list page reusing it is the signal to extract a shared helper.

## Proposal
Extract a single relative-time helper (e.g. web/src/lib/time.ts) and have both liveness.ts and format.ts import it.

## Related
- web/src/workers/liveness.ts
- web/src/schedules/format.ts
- docs/retros/2026-06-05-web-schedules-list.md
