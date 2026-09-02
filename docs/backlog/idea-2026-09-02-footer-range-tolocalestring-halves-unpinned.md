---
title: The range half of every paginated footer's toLocaleString is unpinned, and the suite depends on the runner locale
type: idea
status: open
created: 2026-09-02
priority: low
source: Phase 4 correctness lens on the 2026-09-01 pager chain (lane B of the web SPA batch)
---

# The range half of every paginated footer's toLocaleString is unpinned

## Summary
All seven paginated surfaces render the row range and the total through toLocaleString(). Mutating
the range half to plain interpolation survives the whole web suite: no test pages past row 999, so
only the total half is guarded. Separately, the assertions that do exist (1-50 of 2,341 and '1,234')
depend on the runner's ICU locale, which the project has chosen not to fix at the call sites.

## Proposal
- One assertion per footer that pages forward past row 1000 (total 2341, 1001-1050 of 2,341), or one
  shared footer-formatting helper with its own test so seven copies become one.
- Decide the locale question once: keep bare toLocaleString() and pin the CI locale, or move all
  seven to an explicit 'en-US' as LogView.tsx does. The 2026-09-01 pager chain rejected the
  seven-file change as a product regression for non-US readers; that call stands until this item
  reopens it.

## Related
- [[bug-2026-08-14-schedules-footer-range-not-localized]] (closed)
- web/src/lib/pageRange.ts, the seven surfaces' footer props
