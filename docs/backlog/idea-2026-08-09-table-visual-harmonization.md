---
title: "Harmonize the eight tables' frames and header spacing now that the structure is shared"
type: idea
status: open
created: 2026-08-09
priority: low
source: spec follow-ups 2 and 3 from the shared accessible-table primitive (2026-08-09), proposed and left unfiled
---

# Harmonize the eight tables' frames and header spacing now that the structure is shared

## Summary
The 2026-08-09 table-primitive migration was gated on visual neutrality, so it deliberately changed
no pixels. That leaves two cosmetic inconsistencies across the eight tables that are now cheap to
fix for the first time, because the structure they hang off is shared: **four of eight hand-roll the
glass frame instead of using `GlassPanel`**, and **the header row carries three different
spacing/tracking strings**. Neither is a defect; both are the same
primitive-exists-but-was-not-used disease the table item was about, one layer out.

## Context
Recorded as follow-ups 2 and 3 in `docs/superpowers/specs/2026-08-09-shared-accessible-table-primitive.md`
and excluded from that work on purpose: every one of these edits is *visible*, and the migration's
whole proof was that the five already-roled tables' test files needed zero changes. Making
`WorkersTable` gain a gradient and a shadow in the same change set would have spoiled that proof.

**Frames, verified 2026-08-09:**

- `web/src/components/holo/GlassPanel.tsx:8-10` is the canonical surface: gradient + border +
  `backdrop-blur-[8px]` + inset/drop shadow.
- `web/src/admin/users/UsersTable.tsx:70`, `web/src/admin/enrollments/EnrollmentsTable.tsx:37` and
  `web/src/admin/reservations/ReservationsTable.tsx:59` each inline **exactly GlassPanel's base minus
  its shadow**, as one literal string, three times.
- `web/src/workers/WorkersTable.tsx:31` uses a pre-upgrade flat
  `rounded-card border border-border bg-white/5 backdrop-blur`, so the workers list is visibly
  flatter than the jobs and schedules lists beside it.
- `JobsTable`, `SchedulesTable` and `TasksTable` already use `GlassPanel`; `WorkspacesPanel` has no
  frame by design (it renders inside the page's `Panel`).

**Header spacing, verified 2026-08-09** (`rg 'headerClassName=' web/src`): three variants across the
eight - `px-4 py-3 tracking-wider` (Jobs, Schedules, Workers), `px-4 py-2 tracking-wider` (Tasks,
Workspaces), `px-[18px] py-3 tracking-[0.16em]` (the three admin tables).

## Proposal
This needs a **design call first, then a small mechanical edit**. It is not an engineering decision,
and it should not be picked up as one.

1. Decide whether the admin tables' shadowless surface is intentional (a nested/secondary surface
   inside the admin console shell) or accidental drift. If intentional, it belongs in `GlassPanel`
   as a variant prop rather than as three inlined copies of its base string. If accidental, the three
   become `<GlassPanel>` and the literal disappears.
2. Decide whether `WorkersTable` should adopt the gradient surface the rest of the app upgraded to.
   This is the only genuinely visible change in the item.
3. Pick one header spacing/tracking pair, or two if the admin console is deliberately denser, and
   apply it. The `headerClassName` prop already exists as the single application point per table, so
   this is one string per file.

Keep the scope to frames and header spacing. Row padding, border colors and text sizes also vary and
are deliberately out of scope here - they are per-row density decisions, not container styling.

## Acceptance / Done When
- No file inlines `GlassPanel`'s base class string. Either the four hand-rolled frames use
  `GlassPanel`, or the shadowless variant is expressed as a `GlassPanel` option used by name.
- The number of distinct `headerClassName` values across the eight tables is reduced to one or two,
  with the reason for any second value written down.
- The eight tables' semantic tests still pass unedited: this is a visual change and must not move a
  role, an accessible name or a count. The five protected test files from the migration
  (`WorkersTable`, `UsersTable`, `EnrollmentsTable`, `ReservationsTable`, `TasksTable`) stay at a
  zero-line diff.
- Screenshots or a manual pass on the four affected pages, since the repo has no visual regression
  harness and the suite asserts almost none of this.

## Related
- `web/src/components/holo/GlassPanel.tsx`, `web/src/components/holo/Table.tsx`
- `web/src/workers/WorkersTable.tsx`, `web/src/admin/users/UsersTable.tsx`,
  `web/src/admin/enrollments/EnrollmentsTable.tsx`,
  `web/src/admin/reservations/ReservationsTable.tsx`
- Shipped the shared structure that makes this cheap:
  [[idea-2026-06-05-shared-accessible-table-primitive]]
- Sibling follow-ups from the same work: [[idea-2026-08-09-table-accessible-name-consistency]],
  [[idea-2026-08-09-tasks-table-grid-role-selection]],
  [[idea-2026-08-09-sort-caret-in-accessible-name]]
- No visual regression coverage exists to catch drift here: [[idea-2026-06-03-web-e2e-harness]]
