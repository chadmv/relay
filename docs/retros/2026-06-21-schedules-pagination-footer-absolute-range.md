---
date: 2026-06-21
topic: schedules-pagination-footer-absolute-range
branch: claude/sad-feistel-4bc73c
pr: "2026-06-21 / schedules-pagination-footer-absolute-range"
merge: "2026-06-21 / schedules-pagination-footer-absolute-range"
---

# Session Retro: 2026-06-21 - Schedules pagination footer absolute X-Y range

**TL;DR:** Closed `bug-2026-06-21-schedules-pagination-footer-absolute-range`. `SchedulesPage`
rendered a page-count-only footer (`SHOWING {n} OF {total}`); it now shows an absolute `X-Y of total`
range computed from a running start-offset that accumulates actual page sizes, mirroring the Jobs
footer. Autopilot batch item 4 (frontend).

## What Was Built

- `web/src/schedules/SchedulesPage.tsx` - added `startOffset` + `offsets[]` state mirroring
  `cursorStack` depth, extracted `goNext`/`goPrev` handlers that mutate all three in lockstep with
  plain setters, reset all three in `chooseSort`, and replaced the footer span with
  `{x}-{y} of {total}` via the shared `computePageRange` helper (`web/src/lib/pageRange.ts`).
- `web/src/schedules/SchedulesPage.test.tsx` - three new tests asserting the literal range string on
  the first full page (`1-50 of 120`), a partial last page after paging forward (`51-63 of 63`), and
  after paging back (`1-50 of 63`).

## Key Decisions

- **Adapt, not copy-paste.** JobsPage uses a `cursor` + `stack` split; SchedulesPage uses a single
  `cursorStack` it slices. The offset accumulator was ported onto that model as a parallel `offsets`
  stack, advanced by the *actual* page size (`data.items.length`), not a fixed 50, so partial last
  pages stay exact.
- **Plain setters + isPlaceholderData gating.** Both pagination buttons stay disabled while
  `isPlaceholderData`, and `cursorStack`/`offsets`/`startOffset` are only ever mutated together inside
  `goNext`/`goPrev` - so the stacks cannot desync on a double-click (the same fix class as the earlier
  SPA cursor-corruption bug). Matches JobsPage's documented plain-vs-functional-setter rationale.

## Verification

- New tests proven RED against the old `SHOWING N OF total` footer (`Unable to find .../1-50 of 120/`),
  GREEN after the change.
- Full web suite (172 tests) + `tsc --noEmit` clean. Adversarial frontend review confirmed no
  stack-divergence path, correct partial-page math, surgical scope, plain hyphen, and `web/dist` not
  committed. No high/medium/low findings.

## Notes / Limitations

- Cosmetic nit deferred (not filed): JobsPage uses `toLocaleString()` for the range/total; SchedulesPage
  renders them raw, matching its own pre-existing style. Out of surgical scope; revisit only if a
  fleet grows large enough to want thousands separators.
