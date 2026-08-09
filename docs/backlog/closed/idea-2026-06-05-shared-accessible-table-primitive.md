---
title: Extract a shared accessible-table primitive - 5 tables duplicate the pattern and 3 lack it entirely
type: idea
status: closed
created: 2026-06-05
closed: 2026-08-09
resolution: fixed
priority: medium
source: 2026-06-05 workers-table-aria-semantics retro; re-measured 2026-08-09
---

# Extract a shared accessible-table primitive - 5 tables duplicate the pattern and 3 lack it entirely

## Summary
The web app has **eight** CSS-grid pseudo-tables. Five wire ARIA roles by hand and duplicate the
same helpers; **three have no table semantics at all**, so a screen reader gets undifferentiated
divs on the jobs list, the schedules list and the worker-detail workspaces panel. The original
question - "extract once a second instance appears?" - is settled by force of numbers; what is left
is doing it, and the un-roled three are a correctness gap rather than tidiness.

## Context
Filed 2026-06-05 when `WorkersTable.tsx` was the only instance, and written speculatively ("if any
emerge", "once a second instance appears"). Re-measured 2026-08-09 after the admin console added
three tables in a single day.

**Role-layered by hand (5)** - 70 `role="table"/"row"/"columnheader"/"cell"` attributes between them:

| File | role attrs | columnheaders | aria-sort |
|---|---|---|---|
| `web/src/workers/WorkersTable.tsx` | 15 | 6 | 3 |
| `web/src/admin/users/UsersTable.tsx` | 13 | 5 | 3 |
| `web/src/admin/enrollments/EnrollmentsTable.tsx` | 13 | 5 | 2 |
| `web/src/admin/reservations/ReservationsTable.tsx` | 16 | 5 | 1 |
| `web/src/jobs/TasksTable.tsx` | 13 | 5 | 0 (not sortable) |

**No table semantics (3)** - same `const COLS = 'grid grid-cols-[...]'` shape, header row of bare
`<span>` labels, zero `role=`/`aria-sort`:

- `web/src/jobs/JobsTable.tsx`
- `web/src/schedules/SchedulesTable.tsx`
- `web/src/workers/WorkspacesPanel.tsx`

Duplicated code, concretely: a private `ariaSort(field, sort)` helper is defined **four** times
(`WorkersTable.tsx:15`, `UsersTable.tsx:24`, `EnrollmentsTable.tsx:23`, `ReservationsTable.tsx:29`),
each with the same three-way return and a different local sort type; and every one of the eight
declares its own `COLS` grid-template constant that the header row and the body rows must be kept
in agreement about by hand.

That the two most-used list pages are the ones missing semantics is the argument for extraction
rather than for eight more inline fixes: a primitive would have made them correct by construction,
and instead the pattern was copied only where someone remembered.

## Proposal
Extract a small primitive into `web/src/components/holo/` (alongside `GlassPanel`, `Chip`,
`PillButton`, …), then migrate the eight.

Shape it around what the instances actually vary on, rather than generalising early - the project's
"don't force the primitive" rule from the Holo relayout applies:

- The grid template is per-table and must be shared between header and body, so it belongs as one
  prop rather than duplicated constants.
- Sortability is optional: `TasksTable` has columnheaders and no sort, and the three un-roled ones
  are not sortable either. Do not require a sort type.
- The `ariaSort` three-way mapping is genuinely identical everywhere - make it generic over the
  sort-field type once.
- Row-level extras differ a lot (link cells, status dots, gradient avatars, chips, reduced-opacity
  archived rows, inline edit inputs). Keep cell rendering in the caller; the primitive owns
  structure and semantics only.

Migrate the un-roled three as part of this, since fixing them is the point rather than a side
effect. Note `WorkspacesPanel.tsx` is admin-gated and lower traffic, so it can go last.

## Acceptance / Done When
- A shared accessible-table primitive exists in `web/src/components/holo/` with its own tests.
- All eight grid pseudo-tables consume it, including the three that currently have no roles.
- `ariaSort` exists once, generic over the sort-field type; no file defines a private copy.
- No file declares a `COLS` constant that the primitive could own.
- Existing table tests still pass unchanged where they assert semantics, proving the migration
  preserved them rather than redefining them - the five role-layered tables already have assertions
  worth keeping honest.
- A screen-reader-observable check that the jobs and schedules lists now expose rows and column
  headers, RED against today's markup.

## Related
- `web/src/components/holo/` (where it belongs), `web/src/components/holo/index.ts`
- The eight consumers listed above
- [[idea-2026-06-05-workers-table-aria-semantics]] (closed) - the retro that raised this
- Adjacent a11y debt with the same accumulation shape:
  [[idea-2026-07-01-confirmdialog-focus-trap-hardening]],
  [[feature-2026-06-05-usermenu-panel-menu-roles]]

## Notes
Priority raised low -> medium on 2026-08-09. Not because extraction became more attractive, but
because three surfaces silently shipped without semantics while this sat open - which is what the
item was filed to prevent. The cost also grows monotonically: every new table is another hand-wired
copy, and every future a11y fix is an eight-place change.

## Resolution
Shipped 2026-08-09 (autopilot, shared-accessible-table-primitive). `web/src/components/holo/Table.tsx`
provides Table + TableRow + TableCell with declarative header config and the grid template carried on
a React context, so the header and body cannot be put out of agreement by hand. All eight grid
pseudo-tables consume it, including the three that had no table semantics at all (JobsTable,
SchedulesTable, WorkspacesPanel) - the correctness half of this item. `ariaSort` and `sortCaret` exist
once, replacing four duplicated pairs plus ReservationsTable's local SortHeader.

The primitive deliberately renders no frame: the caller keeps its wrapper, which made the migration
visually neutral across four different frame styles and kept footers, error banners and dialogs inside
the visual surface but outside the `role="table"` subtree, where they would be invalid children.

Neutrality was proved rather than asserted: the five already-roled tables' test files are byte-identical
to their pre-migration versions (a zero-line `git diff`), so the refactor could not have quietly
redefined the semantics it was preserving. The three newly-roled tables got RED-proven role/row/
columnheader/cell count tests plus a structural within-row assertion. `WorkerDetailPage.test.tsx` was
the one sanctioned edit, since its page-global "no table anywhere" assertion passed only because of the
defect this work fixed.

Acceptance criterion 3 was corrected during specification: "no file declares a COLS constant the
primitive could own" is unsatisfiable, because Tailwind v4's static scan requires the grid-template
literal to stay in the consumer file. Restated and met as: declared once, applied to one element,
passed as `columns`.

Review: 0 high; all three lenses independently found the same top issue (rest props spread after
`role`, letting a caller strip the semantics the primitive exists to guarantee). Fixed by spreading
first and by typing the props as `Omit<..., 'role' | 'dangerouslySetInnerHTML'>`. Suite 761 -> 780
tests. Three a11y follow-ups filed rather than folded in:
[[idea-2026-08-09-tasks-table-grid-role-selection]],
[[idea-2026-08-09-table-accessible-name-consistency]],
[[idea-2026-08-09-sort-caret-in-accessible-name]].
