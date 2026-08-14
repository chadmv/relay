---
title: The SPA overflows horizontally below roughly 840px viewport width, app-wide
type: bug
status: closed
created: 2026-08-12
closed: 2026-08-14
resolution: fixed
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

**Start by reading the 2026-08-13 measurements at the bottom of this item before the Proposal.** Three
independent surfaces have now been measured and the third one changes where the investigation should
start: the driver at 375px looks like the **header nav bar**, not the tables this item's Proposal was
originally built around.

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

**Three candidate causes are enumerated below. They are independent, they need different fixes, and
they are listed in the order the 2026-08-13 measurement suggests they should be investigated - which
is NOT the order they were discovered in.** Cause 0 was added on 2026-08-13 and is the one with a
direct per-element measurement behind it; causes 1 and 2 were derived by reading the source after
measuring page totals, and neither has been measured as the widest element on any surface.

**Cause 0 (added 2026-08-13, measured): the header nav bar does not shrink.**

`web/src/shell/HoloShell.tsx:49-71` renders the app header as
`flex items-center justify-between border-b ... px-[22px] py-3`, containing a left group
(`flex items-center gap-6`) with the wordmark and a `<nav className="flex gap-0.5">` of route links,
and the user menu on the right. Nothing in that structure wraps, shrinks or scrolls: a `flex` row of
non-wrapping link labels plus 44px of horizontal padding establishes a **floor width** that every page
inherits, because the header is a sibling of `<main>` inside the same document.

On `/profile/sessions` at a 375px viewport the browser lane measured
`document.documentElement.scrollWidth` = **523px** (148px of overflow) with the **`HEADER` element at
523px and `MAIN` at only 391px**. The header was the widest element on the page. See the 2026-08-13
update for the full numbers and for what this does and does not prove.

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

**Step 0 (added 2026-08-13): measure the header before writing any code, and let that decide the
order of the rest.** At 375px, on a page with no table and no two-column body - `/profile/sessions` is
the one already measured - record `document.documentElement.scrollWidth` and then the `offsetWidth` of
`HEADER` and of `MAIN` separately. Then repeat on a table page. Two things to settle:

- **Is the header a floor?** If the header measures at or near 523px on both a table page and a
  table-less page, the header sets a minimum document width app-wide and no amount of work on the
  `Table` primitive can bring any page below it. That would make the header the **first** fix, not the
  last.
- **What does each cause contribute on top of that floor?** Schedule detail measured 653px total at
  375px while two other surfaces measured 523px. If the header explains 523 of the 653, the tables are
  a real but secondary contributor and B below is still needed - just not first.

This step is cheap (one browser, three numbers per surface) and it decides whether A, B or C is the
change that closes the Acceptance criteria. **Do not start from the `Table` primitive on the strength
of this item's original framing** - that framing predates the header measurement.

**C. Make the header shrink, wrap or scroll.** No fix is prescribed here, deliberately, because the
right answer is a design decision and the measurement in step 0 should inform it. The options a reader
will reach for are: let the nav wrap; give the nav its own `overflow-x-auto`; collapse the nav into a
disclosure below a breakpoint; reduce the horizontal padding below a breakpoint. **Read
`web/src/shell/HoloShell.tsx:29-49` before touching anything**: the header carries `relative z-10` and
a `backdrop-blur`, and its stacking behaviour was established by a measured 275-point hit test and is
depended on by `UserMenu`'s deliberately non-portalled dropdown. A structural change to the header must
not invalidate that; see [[idea-2026-08-12-document-z-index-layering-scale]].

**A. Give the two-column bodies a breakpoint.** Change the three `grid-cols-2` sites to
`grid-cols-1 ... md:grid-cols-2`, matching `ServerTab.tsx:70` exactly rather than inventing a
second convention. Decide the breakpoint once (`md` is what the existing correct site uses) and
apply it uniformly. This is close to mechanical and closes the detail pages. **Independent of C** and
safe to do in either order.

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

**Note on B's standing:** the 2026-08-13 update below originally concluded that the repeated 375px
breakage "is the argument for fixing it once in the shared `Table` primitive rather than per page".
That conclusion is now in question - the third measured surface renders **no table at all** and
overflows by the same amount. B is still probably needed; it is no longer established as the fix that
closes this item.

Also to settle: whether a horizontally scrolling table is the right UX at 375px at all, or whether
some columns should drop below a breakpoint. **Scrolling is the honest minimum** - it makes every
column reachable and no data disappear - and column-dropping is a per-table product decision that
should not block the scroll fix.

Deliberately **not** proposed: a responsive redesign, a mobile navigation shell, or touch targets.
This item is about content being unreachable, not about the app being pleasant on a phone.

## Acceptance / Done When

- At 375px, 768px and 800px, on the jobs list, workers list, schedules list, admin console, the
  job / worker / schedule detail pages, **and `/profile/sessions` (the table-less control surface)**:
  `document.documentElement.scrollWidth` equals `clientWidth`. **Measured in a real browser** - jsdom
  computes no layout, so a Vitest assertion here would be vacuous.
- **No single element is wider than the viewport at 375px**, header included. Assert per-element
  `offsetWidth` on `HEADER` and `MAIN`, not only the document total - a document-level assertion alone
  cannot tell you which fix worked, which is how this item spent two measurements pointing at the
  wrong cause.
- Where a table is wider than the viewport, it scrolls **within its own container** and every
  column remains reachable; no data is hidden and no row is clipped.
- Behaviour at 1280px and above is unchanged - compare against the current rendering rather than
  asserting the new one is fine.
- The header's stacking behaviour is unchanged: the `UserMenu` dropdown still paints over `<main>` at
  every tested width.
- One documented convention for the two-column breakpoint, and one for the table scroll container,
  so the next page and the next table get it right by default.

## Related

- Source, cause 0: `web/src/shell/HoloShell.tsx:49-71` (the header, its `flex` nav and its `px-[22px]`),
  and `:29-49` (the stacking comment and the 275-point hit test that a header change must not
  invalidate)
- Source, cause 1: `web/src/schedules/ScheduleDetailPage.tsx:185`,
  `web/src/workers/WorkerDetailPage.tsx:114`, `web/src/admin/server/StatSection.tsx:61`; the
  correct precedent at `web/src/admin/server/ServerTab.tsx:70`
- Source, cause 2: the nine `COLS` constants in the table above; the shared primitive at
  `web/src/components/holo/Table.tsx` and its no-frame contract; the correct precedent at
  `web/src/jobs/TaskDag.tsx:49`
- Measurement and scoping decision: `docs/retros/2026-08-12-schedule-detail-page.md`; the 2026-08-13
  header measurement: `docs/retros/2026-08-13-cross-generation-401.md`
- A header restructure interacts with the two `z-50`s and the non-portalled dropdown:
  [[idea-2026-08-12-document-z-index-layering-scale]]
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

## Update 2026-08-13 (a): the Invites tab

Measured again in a real browser during `2026-08-13-admin-invites-tab`, on the Invites tab: **no
overflow at 1280px or 768px, 148px of overflow at 375px** (`scrollWidth` 523 vs `clientWidth` 375).
Recorded at the time as the same Cause 2 idiom already listed here, so the new table was added to the
enumeration above rather than filed separately.

**That attribution is now in doubt.** Only the page total was measured; no per-element width was taken,
so "the table did it" was an inference from the source, not an observation. See update (b).

## Update 2026-08-13 (b): `/profile/sessions`, and the header

Measured in a real browser by the Phase 4 browser lane of the `2026-08-13-cross-generation-401` slice,
on `/profile/sessions` at a 375px viewport:

| measurement | value |
|---|---|
| `document.documentElement.scrollWidth` | **523px** |
| `clientWidth` | 375px |
| overflow | **148px** |
| widest offending element | **`HEADER` at 523px** |
| `MAIN` | 391px |

Three things follow, stated at exactly the strength of the evidence.

1. **This is a third independent surface**, after schedule detail (2026-08-12) and the Invites tab
   (update (a)). The 375px breakage is confirmed app-wide by three separate browser sessions.
2. **On this one surface, the header was measured as the widest element**, at 523px against a `MAIN` of
   391px. That is a direct per-element measurement, and it is the only per-element measurement any of
   the three sessions has taken.
3. **This surface renders no table.** `web/src/profile/` contains no `grid-cols-[...]` template and no
   consumer of the shared `Table` primitive - `SessionsTab` currently renders explanatory copy, not a
   list. So Cause 2 cannot explain the overflow here, and this item's Proposal, which points the reader
   at `web/src/components/holo/Table.tsx`, would not have closed this page.

**A hypothesis, clearly labelled as one and not yet tested:** the header sets a floor of roughly 523px
on every page, and table pages add to it. It is consistent with the numbers - 523px on two surfaces and
653px (375 + 278) on schedule detail, which has both a table and a `grid-cols-2` body - and it is
consistent with the header's source, a non-wrapping `flex` row with 44px of padding and no `min-w-0`.
**It has not been measured on a table page.** Step 0 of the Proposal is the experiment that would
confirm or kill it, and it is two browser measurements.

**What this changes about the item, and what it does not.** It does not retract causes 1 or 2: three
`grid-cols-2` sites with no breakpoint and ten fixed-px table templates with no scroll container are
still true statements about the source, and B is still probably needed for tables to be usable at 375px.
What it changes is **where the next implementer starts.** Investigate the header first. If the header is
a floor, fixing the tables alone cannot satisfy this item's first acceptance criterion on any page.

## Resolution

Fixed in the 2026-08-13-narrow-viewport-overflow slice. Acceptance is a measurement, and it was
taken in a real browser twice by two independent parties: `documentElement.scrollWidth <=
clientWidth` at **both 375px and 320px** on all 17 surfaces, with **populated** tables.

**This item was wrong about the cause twice, and both errors came from measuring the convenient
state rather than the populated one.** Its original framing said fix it once in the shared `Table`
primitive. A later amendment blamed the header nav instead, on the strength of the first
per-element measurement anyone had taken. Both were incomplete. The header-only reading came from
surfaces whose tables were empty or in card view - the Invites empty state, Workers in Grid view,
and `/profile/*`, which has no table at all. With rows present the tables dominate: `/jobs` reached
763px against a 523px header floor.

There were **four** independent causes, not one, and no proper subset satisfied the acceptance
predicate on even a single page:

1. **Header floor.** `HoloShell`'s `<nav>` of non-wrapping route labels set a 494-523px floor on
   every shell page. `/auth`, which has no shell, never overflowed - the clean control that
   confirmed the shell was the source. Fixed by letting the nav shrink and scroll.
2. **Multi-column detail bodies.** `grid-cols-2`/`grid-cols-4` at four sites, now breakpointed.
   This one persisted past 768px, unlike every other cause.
3. **Fixed-px table templates.** Fixed at the primitive: `Table` gained a required `minWidth` that
   publishes one grid string to the header row and every body row and wraps the `role="table"`
   subtree in a scroll container. The argument for the primitive over per-consumer edits was
   alignment, not edit count - under negative free space an `fr` track falls back to its content
   minimum, and the header row and body rows are separate grid containers, so a hand-applied
   min-width desynchronizes exactly what the primitive exists to keep in agreement.
4. **Non-wrapping breadcrumb, toolbar and tab-bar rows** - a cause this item never named, found
   only because fixing the header unmasked it.

Two decisions were taken **without a hi-fi reference**, since the Holo hi-fi is silent on narrow
viewports, and both are the reviewer's to overrule. The nav scrolls rather than collapsing to a
disclosure: invisible at any width where the content fits, no new state or a11y surface, revertible
by deleting three class strings. And the scroll container is the `<nav>`, never the `<header>` -
`overflow` on the header would clip the UserMenu dropdown, so that hazard carries its own test and
was hit-tested in a browser at 375/768/1280px.

Review found one real regression the slice introduced - `StatSection` breakpointed its container to
`grid-cols-1 md:grid-cols-2` but left the cell at `col-span-2`, forcing an implicit second track
below `md` - plus two mutation proofs that reddened at an earlier assertion than claimed, leaving
the assertions they called load-bearing unreached. Both were re-run against discriminating
mutations and confirmed genuine.

The `minWidth` prop is **required**, not optional: all ten consumers pass it, so `tsc` enforces it
at every call site including aliased imports, which is stronger than the source-scanning guard test
it replaced - a guard a lane proved could be reddened by an innocent JSX comment.

Two follow-ups are filed rather than fixed here: nothing enforces numerically that `minWidth`
exceeds the sum of a consumer's fixed tracks, and the `overflow-y: auto` clipping audit is a
point-in-time claim with no test behind it.
