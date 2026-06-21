---
title: Schedules pagination footer lacks absolute X-Y range
type: bug
status: closed
created: 2026-06-21
closed: 2026-06-21
resolution: fixed
priority: low
source: code review of jobs-pagination-footer-absolute-range (2026-06-21)
---

# Schedules pagination footer lacks absolute X-Y range

## Summary
`web/src/schedules/SchedulesPage.tsx` still renders the page-count-only footer
`SHOWING {schedules.length} OF {total}`, the same UX gap the Jobs footer just
fixed. It should show an absolute `X-Y of total` range (plain ASCII hyphen, no
en/em dash) computed from a running start-offset that accumulates actual page
sizes.

## Context
Surfaced during the code review of the Jobs footer fix
([[bug-2026-06-05-jobs-pagination-footer-absolute-range]], closed 2026-06-21).
SchedulesPage uses a slightly different pagination model than JobsPage (a
`cursorStack` it slices, rather than the cursor/`stack` split), so the
offset-accumulator port is NOT a copy-paste - it needs adapting to that model.
The pure `computePageRange` helper added at `web/src/jobs/pageRange.ts` is
reusable (consider moving it to a shared `web/src/lib/` location if a third
consumer appears).

## Proposal
Port the running-offset approach: accumulate actual page sizes as the user
pages forward/back, render `X-Y of total` (X = startOffset+1, Y =
startOffset+pageRows) with a plain hyphen, `0 of total` when empty, and gate
any offset-stack mutation behind the same in-flight/`isPlaceholderData` guard
the cursor stack uses so the two stacks cannot desync.

## Related
- `web/src/schedules/SchedulesPage.tsx` (footer + cursorStack)
- `web/src/jobs/pageRange.ts` (reusable range helper)
- `web/src/jobs/JobsPage.tsx` (reference implementation)
- [[bug-2026-06-05-jobs-pagination-footer-absolute-range]]

## Resolution
Fixed 2026-06-21 (autopilot batch, item schedules-pagination-footer-absolute-range). `SchedulesPage`
gained a `startOffset` + `offsets[]` accumulator mirroring its `cursorStack` depth, with extracted
`goNext`/`goPrev` handlers that mutate `cursorStack`/`offsets`/`startOffset` in lockstep via plain
setters (and `chooseSort` resets all three). The footer now renders `{x}-{y} of {total}` (plain hyphen)
from the shared `computePageRange` helper, advancing by the actual page size so partial last pages stay
exact. Both pagination buttons remain gated on `isPlaceholderData` so the stacks cannot desync. Three
new SchedulesPage.test.tsx cases (first page, partial last page, page-back) proven RED against the old
`SHOWING N OF total` footer; full web suite (172) + tsc green; adversarial review found no
high/medium/low issues.
