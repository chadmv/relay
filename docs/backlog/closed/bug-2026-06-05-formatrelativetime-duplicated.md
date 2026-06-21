---
title: formatRelativeTime duplicated across workers and schedules modules
type: bug
status: closed
created: 2026-06-05
closed: 2026-06-21
resolution: fixed
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

## Resolution
fixed (2026-06-21). The character-for-character identical `formatRelativeTime` (confirmed identical before merging) was extracted into a new shared module `web/src/lib/time.ts`; `web/src/workers/liveness.ts` and `web/src/schedules/format.ts` now `export { formatRelativeTime } from '../lib/time'` so all seven existing importers keep their import paths unchanged. Behavior is unchanged. New `web/src/lib/time.test.ts` covers the seconds/minutes/hours/days buckets and the future-clamps-to-0 case; full web suite (148 tests) and `tsc --noEmit` green.
