---
date: 2026-06-21
topic: jobs-pagination-footer-absolute-range
branch: claude/distracted-allen-9c27c1
pr: "2026-06-21 / jobs-pagination-footer-absolute-range"
merge: "2026-06-21 / jobs-pagination-footer-absolute-range"
---

# Session Retro: 2026-06-21 - Jobs footer absolute X-Y range

**TL;DR:** Closed `bug-2026-06-05-jobs-pagination-footer-absolute-range`. The Jobs pagination footer
showed only a page count (`SHOWING N OF total`); it now shows an absolute `X-Y of total` range tracked
by a running start-offset that accumulates ACTUAL page sizes (so partial last pages and paging back
stay exact). Autopilot batch, item 5 of 7.

## What Was Built

- `web/src/jobs/pageRange.ts` (new) - pure `computePageRange(startOffset, pageSize)` -> `{x, y}`.
- `web/src/jobs/JobsPage.tsx` - a parallel `offsets` stack mirrors the cursor `stack`: `next()` pushes
  the leaving page's offset and advances by `data.items.length`; `prev()` pops; filter/sort reset both.
  Footer renders `X-Y of total` (plain ASCII hyphen) or `0 of total` when empty.
- `web/src/jobs/pageRange.test.ts`, `JobsPage.test.tsx` - 18 tests: full page, forward, back-restores,
  partial last page (`101-120 of 120`), empty, plus an anti-naive case proving `stack.length*pageSize`
  diverges on a partial page.

## Key Decisions

- **Accumulate actual page sizes, not pageSize * stackDepth.** The naive offset breaks on partial
  pages (the reason the footer was simplified to a count in the first place). The `offsets` stack
  records the real `items.length` per forward step, so the start offset is exact at any depth.
- **Gate offset mutations behind the existing in-flight guard.** This is stateful pagination - exactly
  the class PR #65 had a cursor-stack corruption bug in. The offsets mutations live inside
  `next()`/`prev()`, which only fire when the buttons are enabled, and both buttons already gate on
  `isPlaceholderData`. So the second stack inherits the same double-next-during-fetch protection; the
  reviewer verified (via `git show ac71ea4`) that PR #65's guard was button-level, matching here.
- **Plain ASCII hyphen, not the mock's en dash.** The backlog/mock text showed `1-50` with an en dash;
  the project forbids en/em dashes, so the engineer was told to use a hyphen and the reviewer scanned
  all four files for U+2013/U+2014 (none).

## Process Note

- This had real stateful logic, so verification was a full adversarial code review (offset-stack
  correctness across every transition, desync analysis, partial/empty edges, RED-distinguishing tests)
  rather than a conductor-only self-review - the right altitude for the risk. Returned clean.

## Backlog Triage

- Filed [[bug-2026-06-21-schedules-pagination-footer-absolute-range]] - SchedulesPage has the identical
  page-count-only footer. The reviewer flagged it; it is out of scope here (different pagination model -
  a `cursorStack` it slices, not a copy-paste port) but a clear, specific follow-up.
