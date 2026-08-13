---
title: The SPA overflows horizontally below roughly 840px viewport width, app-wide
type: bug
status: open
created: 2026-08-12
priority: medium
source: measured in a real browser by the Phase 4 browser lane of the 2026-08-12-schedule-detail-page slice, and ruled out of scope there as app-wide
---

# The SPA overflows horizontally below roughly 840px viewport width, app-wide

## Summary

Below roughly 840px of viewport width, pages overflow the document horizontally and the whole page
gets a horizontal scrollbar - content is not merely cramped, it is off-screen and the layout
shifts as you scroll. Measured in a real browser (Vite dev server against a mock backend), not
inferred from the source:

| viewport | page-level horizontal overflow |
|---|---|
| 375px (phone) | 278px |
| 768px (tablet portrait) | 89px |
| ~785-800px (an un-maximized desktop window) | 58-73px |
| 1280px | 0px |

The third row is the reason this is medium rather than low. This is not only a mobile concern -
**a desktop user who does not maximize the browser window hits it**, which is an ordinary way to
use a dashboard beside an editor or a terminal.

## Repro / Symptoms

1. Open any list or detail page in the SPA.
2. Narrow the window below ~840px.
3. `document.documentElement.scrollWidth > document.documentElement.clientWidth`; a horizontal
   scrollbar appears on the page itself.

## Context

Found by the browser lane during Phase 4 of the 2026-08-12 schedule-detail-page slice and
**explicitly ruled out of scope there**, because it is not that slice's regression:
`web/src/workers/WorkerDetailPage.tsx:114` is byte-identical to
`web/src/schedules/ScheduleDetailPage.tsx:185` in the respect that matters, and every shipped table
uses the same fixed-px column template idiom. The new page reproduces an existing app-wide defect
rather than introducing one, and fixing it there would have meant editing shared primitives that
slice's scope guard forbade. That reasoning is recorded in
`docs/retros/2026-08-12-schedule-detail-page.md` (Deferred Findings 1).

There are two independent causes, and they need different fixes.

**Cause 1: unconditional two-column detail bodies.**

- `web/src/schedules/ScheduleDetailPage.tsx:185` - `grid grid-cols-2 gap-3`
- `web/src/workers/WorkerDetailPage.tsx:114` - `grid grid-cols-2 gap-3`
- `web/src/admin/server/StatSection.tsx:61` - `grid grid-cols-2 gap-3`

`grid-cols-2` has no breakpoint prefix, so two columns are forced at every width. **The one place
already doing it right is `web/src/admin/server/ServerTab.tsx:70`**, which uses
`grid grid-cols-1 gap-4 md:grid-cols-2`. That is the pattern; it exists in the codebase and simply
was not followed elsewhere.

**Cause 2: fixed-px table column templates with no horizontal scroll container.** Every
grid-pseudo-table declares a template whose fixed segments sum well past a narrow viewport, and
nothing wraps them in a scroll region:

| file | columns | template |
|---|---|---|
| `web/src/schedules/SchedulesTable.tsx:7` | 9 | `grid-cols-[1.4fr_120px_110px_90px_1fr_1fr_110px_1.3fr_150px]` |
| `web/src/admin/reservations/ReservationsTable.tsx:19` | 8 | `grid-cols-[1.3fr_110px_1.5fr_130px_130px_110px_110px_100px]` |
| `web/src/jobs/JobsTable.tsx:7` | 7 | `grid-cols-[90px_1fr_120px_150px_120px_70px_150px]` |
| `web/src/admin/invites/InvitesTable.tsx:9` | 6 | `grid-cols-[1.5fr_110px_110px_1.4fr_110px_1fr]` |
| `web/src/workers/WorkersTable.tsx:8` | 6 | `grid-cols-[1fr_120px_70px_140px_1.2fr_120px]` |
| `web/src/workers/WorkspacesPanel.tsx:8` | 6 | `grid-cols-[120px_90px_1fr_120px_90px_90px]` |
| `web/src/admin/users/UsersTable.tsx:9` | 5 | `grid-cols-[1.6fr_1fr_110px_120px_270px]` |
| `web/src/admin/enrollments/EnrollmentsTable.tsx:16` | 5 | `grid-cols-[1.6fr_130px_130px_120px_1fr]` |
| `web/src/jobs/TasksTable.tsx:5` | 5 | `grid-cols-[1fr_110px_80px_120px_1fr]` |
| `web/src/schedules/ScheduleRunsPanel.tsx:6` | 5 | `grid-cols-[130px_70px_110px_100px_1fr]` |

`SchedulesTable`'s nine columns are the worst case: its fixed segments alone total 580px before any
`fr` track gets a pixel.

The single existing precedent for the right shape is `web/src/jobs/TaskDag.tsx:49`, which wraps its
content in `<GlassPanel className="overflow-x-auto p-2">`. Nothing else in the app does.

**What is confirmed NOT the cause:** the browser lane verified that a page's *own* scroll regions
are correctly contained - the schedule detail job-spec `<pre>` reports `scrollHeight` 14789 against
`clientHeight` 345 with zero contribution to page overflow. The overflow is layout, not content.

## Proposal

Two separable changes; the first is small enough to do on its own.

**A. Give the two-column bodies a breakpoint.** Change the three `grid-cols-2` sites to
`grid-cols-1 ... md:grid-cols-2`, matching `ServerTab.tsx:70` exactly rather than inventing a
second convention. Decide the breakpoint once (`md` is what the existing correct site uses) and
apply it uniformly. This is close to mechanical and closes the detail pages.

**B. Give the tables a horizontal scroll container - once, not nine times.** The natural place is
the shared `Table` primitive (`web/src/components/holo/Table.tsx`), but read its header comment
first: **it deliberately renders no frame**, precisely so its eight consumers could keep their own
visually different wrappers and so footers and error banners stay inside the visual surface but
outside the `role="table"` subtree. So the fix is a design decision, not a one-liner:

- Option B1: `Table` gains an optional scroll wrapper (opt-in prop, or unconditional if it can be
  made visually neutral). Cheapest per consumer; needs care that it does not become the frame the
  primitive refuses to be, and that the footer/banner placement contract still holds.
- Option B2: each consumer's existing wrapper gains `overflow-x-auto` plus a `min-w` on the grid,
  following `TaskDag.tsx:49`. No primitive change and no contract risk, at the cost of nine edits
  and a convention that has to be remembered by the tenth table.

Also to settle: whether a horizontally scrolling table is the right UX at 375px at all, or whether
some columns should drop below a breakpoint. **Scrolling is the honest minimum** - it makes every
column reachable and no data disappear - and column-dropping is a per-table product decision that
should not block the scroll fix.

Deliberately **not** proposed: a responsive redesign, a mobile navigation shell, or touch targets.
This item is about content being unreachable, not about the app being pleasant on a phone.

## Acceptance / Done When

- At 375px, 768px and 800px, on the jobs list, workers list, schedules list, admin console, and the
  job / worker / schedule detail pages: `document.documentElement.scrollWidth` equals
  `clientWidth`. **Measured in a real browser** - jsdom computes no layout, so a Vitest assertion
  here would be vacuous.
- Where a table is wider than the viewport, it scrolls **within its own container** and every
  column remains reachable; no data is hidden and no row is clipped.
- Behaviour at 1280px and above is unchanged - compare against the current rendering rather than
  asserting the new one is fine.
- One documented convention for the two-column breakpoint, and one for the table scroll container,
  so the next page and the next table get it right by default.

## Related

- Source, cause 1: `web/src/schedules/ScheduleDetailPage.tsx:185`,
  `web/src/workers/WorkerDetailPage.tsx:114`, `web/src/admin/server/StatSection.tsx:61`; the
  correct precedent at `web/src/admin/server/ServerTab.tsx:70`
- Source, cause 2: the nine `COLS` constants in the table above; the shared primitive at
  `web/src/components/holo/Table.tsx` and its no-frame contract; the correct precedent at
  `web/src/jobs/TaskDag.tsx:49`
- Measurement and scoping decision: `docs/retros/2026-08-12-schedule-detail-page.md`
- **Check before starting**: [[idea-2026-08-12-detail-page-state-triad-primitive]] touches the same
  three detail pages. Either order is fine, concurrent is not, and they must not be folded together
  - that one is behavior-preserving and gated on a zero test diff, this one changes rendering and
  needs new assertions.
- Same table surface, different concern: [[idea-2026-08-09-table-visual-harmonization]],
  [[idea-2026-08-09-table-accessible-name-consistency]]
- The gap that makes this expensive to verify and easy to regress:
  [[idea-2026-06-03-web-e2e-harness]] - **this item is the strongest argument for that one.**

## Notes

Worth recording how this was found, because it is the transferable part: the Phase 4 browser lane
could not screenshot (compositing was unavailable), so it measured with `elementFromPoint`,
`scrollWidth` and `scrollHeight` instead. **The measurement found something a screenshot at the
default window size would not have**, and nothing in `npm test` can find it at all, because jsdom
performs no layout. Until an e2e harness exists, this class of defect is invisible to the project's
entire automated gate.

## Update 2026-08-13
Measured again in a real browser during `2026-08-13-admin-invites-tab`, on the Invites tab: **no
overflow at 1280px or 768px, 148px of overflow at 375px** (`scrollWidth` 523 vs `clientWidth` 375).
That is the same Cause 2 idiom already listed here, not a new one - the new table was simply added to
the enumeration above rather than filed separately. Two independent browser measurements now agree
that the 375px breakage is app-wide and reproduces on whichever surface happens to be under test,
which is the argument for fixing it once in the shared `Table` primitive rather than per page.
