# Workers table ARIA semantics - design

Date: 2026-06-05
Backlog item: [idea-2026-06-05-workers-table-aria-semantics](../../backlog/idea-2026-06-05-workers-table-aria-semantics.md)
Follow-up to: [bug-2026-06-03-workers-view-controls-aria](../../backlog/closed/bug-2026-06-03-workers-view-controls-aria.md)

## Problem

`web/src/workers/WorkersTable.tsx` renders the worker list as a CSS-grid pseudo-table:
an outer container `<div>`, a header-row `<div>` with the `COLS` grid class containing
two sortable `<button>`s plus four `<span>`s, and one grid-classed `<div>` per worker row.

A prior fix added `aria-sort` to the sortable `<button>` headers. But per the ARIA spec,
`aria-sort` is only formally exposed on elements with the `columnheader`/`rowheader` role.
Sitting on plain `<button>`s, some screen readers will not announce the sort state, so the
attribute may be a no-op for assistive tech. The rows and cells likewise carry no table
semantics, so the structure is not conveyed as a table at all.

## Goal

Give the table real semantics so `aria-sort` lands on a `columnheader` and the grid is
read as a table by assistive tech, without changing the visual layout or the component's
public API.

## Approach

Layer ARIA roles onto the existing CSS-grid structure. The `COLS` grid classes and all
Tailwind styling stay exactly as-is, so there is zero visual change. This was chosen over
converting to a real `<table>` element because a native `<table>` does not cooperate with
`display: grid` - it would require reimplementing the `grid-cols-[1fr_120px_70px_140px_1.2fr_120px]`
sizing with `table-fixed` column widths, a higher regression risk than is warranted for a
low-priority accessibility fix. The hybrid (real `<table>` forced to `display: grid`) was
rejected as the fragile worst-of-both.

`role="table"` (static data table) is used rather than `role="grid"`, which would imply
interactive grid-cell keyboard navigation the component does not provide.

### Markup changes (`WorkersTable.tsx`)

- **Container `<div>`** -> add `role="table"` and `aria-label="Workers"`.
- **Header row `<div>`** (the `COLS` header) -> add `role="row"`.
- **Sortable headers** (NAME, STATUS, LAST SEEN) -> wrap each `<button>` in a
  `<div role="columnheader" aria-sort={...}>`. The `aria-sort` attribute moves from the
  button onto this `columnheader` wrapper; the `<button>` stays inside as the
  keyboard/click-operable control, retaining its native button role and activation.
- **Non-sortable headers** (SLOTS, SPEC, LABELS `<span>`s) -> add `role="columnheader"`.
- **Each data row `<div>`** -> add `role="row"`.
- **Each data cell `<span>`** -> add `role="cell"`.

Result: assistive tech reads the structure as `table -> row -> columnheader/cell`, and
`aria-sort` sits on a `columnheader` as the spec requires.

### Why a wrapper instead of `role="columnheader"` on the button

Putting `role="columnheader"` directly on the `<button>` would override its implicit
`button` role - removing the "button" announcement and breaking the existing tests that
query `getByRole('button', ...)`. The wrapper keeps the button intact and lands the
`columnheader` role on a dedicated element.

### Unchanged

- Component props: `workers`, `sort`, `onSort`.
- The `caret()` and `ariaSort()` helpers.
- `WorkersPage.tsx`, `WorkersGrid.tsx`.

## Test changes (`WorkersTable.test.tsx`)

- The two `aria-sort` assertions: change `getByRole('button', { name: ... })` to
  `getByRole('columnheader', { name: ... })` to reflect the corrected location.
- The onSort-click test and the caret test stay as-is (still query `button`).
- Add one test asserting the new structure: `getByRole('table')` exists, and
  `columnheader`/`cell`/`row` roles are present.

Note on the backlog item's "existing tests still pass" criterion: the aria-sort assertions
are intentionally updated to query the `columnheader` rather than the button, because the
attribute legitimately moves to satisfy the ARIA spec. "Still pass" is read as "do not
break the click/caret/sort behavior", which holds.

## Acceptance mapping

| Backlog "Done When" | Covered by |
|---|---|
| Sortable headers expose `role="columnheader"` with `aria-sort` | columnheader wrappers + moved `aria-sort` |
| Rows/cells exposed with table roles | `role="table"`/`"row"`/`"cell"`/`"columnheader"` |
| Sorting still keyboard-operable | `<button>`s retained inside the header wrappers |
| Existing tests still pass | click/caret tests untouched; aria-sort tests updated to correct location |

## Implementation order (TDD)

1. Update/add the tests in `WorkersTable.test.tsx` first.
2. Adjust the markup in `WorkersTable.tsx` until the tests pass.
3. Run the web test suite (vitest) in `web/` and confirm green.
4. Close the backlog item (`git mv` to `docs/backlog/closed/`).
