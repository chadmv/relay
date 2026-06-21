---
title: Jobs pagination footer lacks absolute X-Y range
type: bug
status: closed
created: 2026-06-05
closed: 2026-06-21
resolution: fixed
priority: low
source: jobs-list-frontend retro (2026-06-05)
---

# Jobs pagination footer lacks absolute X-Y range

## Summary
The Jobs table pagination footer shows only the current-page count (`SHOWING N OF total`), not an absolute `X–Y of total` range. With opaque cursors and possibly-partial pages, the true start offset is not computable client-side without tracking cumulative page sizes.

## Context
The design mock shows `1–50 of 2,341`. The naive `stack.length * 50` offset breaks on partial pages, so the footer was simplified to a page count. To restore an accurate absolute range, track a running offset by accumulating actual page sizes as the user pages forward/back.

## Related
- `web/src/jobs/JobsPage.tsx` (pagination footer + cursor stack)
- `docs/retros/2026-06-05-web-jobs-list.md`

## Resolution
fixed (2026-06-21). The Jobs footer now shows an absolute `X-Y of total` range (plain ASCII hyphen) instead of `SHOWING N OF total`. A parallel `offsets` stack mirrors the cursor `stack`: `next()` pushes the leaving page's start-offset and advances by the actual `data.items.length`, `prev()` pops it, and filter/sort changes reset both - so partial last pages stay exact (e.g. 101-120 of 120) and paging back restores the prior range. The range math lives in a pure `computePageRange` helper. Offset mutations sit behind the same `isPlaceholderData` button guard as the cursor stack (PR #65), so a double-next during an in-flight fetch cannot desync the two stacks. Empty list renders `0 of total`. Code review (offset-stack correctness across all transitions, partial/empty cases, no en/em dash, RED-distinguishing tests incl. an anti-naive case) returned no high/medium findings. 18 new/updated tests, full web suite 161 green, `tsc` clean. The identical footer gap on SchedulesPage was filed as a separate follow-up.
