---
title: "TasksTable row selection is inert to assistive tech: aria-selected under role=table"
type: idea
status: open
created: 2026-08-09
priority: low
source: deferred review finding from the shared accessible-table primitive (2026-08-09)
---

# TasksTable row selection is inert to assistive tech: aria-selected under role=table

## Summary
`web/src/jobs/TasksTable.tsx` renders each task row as a `<button as>` carrying `aria-selected`,
inside a `role="table"`. `aria-selected` is only meaningful under `grid`, `treegrid`, `listbox` and
friends - under `role="table"` it is not surfaced, so a screen-reader user gets no confirmation of
which task is selected even though that selection is what drives the Spec and Log panes. The
`as="button"` also overrides the native `button` role with `row`, so the user is not told the row is
activatable either.

## Context
Pre-existing behavior, faithfully carried through the 2026-08-09 shared-Table-primitive migration
(that work's refactor half forbade any behavior change, and `TasksTable.test.tsx` is one of the five
files held byte-identical to prove it). The spec for that work named `role="grid"` an explicit
non-goal.

What changed is the cost and shape of the fix. `role="table"` is now hardcoded at
`web/src/components/holo/Table.tsx` and `role="cell"` alongside it, and seven other tables depend on
that component - so this is no longer a one-file change, and `Table.test.tsx` now asserts
`aria-selected` as a supported `TableRow` prop, which blesses the current arrangement as API.

## Proposal
Add a `role?: 'table' | 'grid'` prop to `Table` that also switches `TableCell` to `gridcell` (the
columns context is already there to carry it), and have `TasksTable` pass `role="grid"`.

A `grid` brings a keyboard model with it, which is the real work and the reason this is not a
one-liner: arrow-key cell/row navigation, a single tab stop into the widget, and a defined
focus-restoration rule. Decide whether the tasks list wants full grid navigation or whether a
`listbox`/`option` shape fits better - the rows are selected as whole units, not navigated cell by
cell, so `listbox` may be the honest match and would not require the `Table` change at all.

## Acceptance / Done When
- Selecting a task is announced by a screen reader, and the row advertises that it is activatable.
- Whichever role is chosen carries the keyboard model that role requires, not just the attribute.
- `TasksTable.test.tsx`'s existing assertions are revisited deliberately (this work is allowed to
  change them - unlike the migration, it is a behavior change on purpose).

## Related
- `web/src/jobs/TasksTable.tsx`, `web/src/components/holo/Table.tsx`
- Shipped the primitive this now depends on:
  [[idea-2026-06-05-shared-accessible-table-primitive]]
- Adjacent a11y debt: [[idea-2026-07-01-confirmdialog-focus-trap-hardening]]
- **Direct precedent, closed 2026-08-13:** [[feature-2026-06-05-usermenu-panel-menu-roles]] asked the
  same shape of question - should this surface adopt an interactive ARIA role? - and was closed by
  answering **no** and correcting the advertised contract instead, because `role="menuitem"` would
  have replaced the link role on entries that are really links. Read its Resolution before starting
  here. The parallel is exact and cuts both ways: `role="grid"` likewise **replaces** `role="table"`
  and obliges a full keyboard model (arrow navigation, one tab stop), so the honest question is not
  "add `aria-selected`" but "is this a grid, and are we implementing one?" If the answer is no, the
  correct fix may be to stop implying selection semantics rather than to complete them. That is the
  decision this item must actually make.

## Notes
Low priority because the visual selection cue (`border-l-2 border-accent bg-accent/[0.08]`) is
correct and the pane content changes, so a sighted user is fully served; the gap is specific to
assistive tech.
