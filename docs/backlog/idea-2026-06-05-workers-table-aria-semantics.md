---
title: Workers table uses div/button markup so aria-sort may not be announced
type: idea
status: open
created: 2026-06-05
priority: low
source: follow-up from bug-2026-06-03-workers-view-controls-aria (aria-sort added, but on plain buttons)
---

# Workers table uses div/button markup so aria-sort may not be announced

## Summary
`WorkersTable.tsx` renders the worker list as `<div>`/`<button>`/`<span>` elements with a CSS grid layout rather than a real `<table>`. The recently-added `aria-sort` attributes sit on plain `<button>` header cells, but per the ARIA spec `aria-sort` is only formally exposed on elements with the `columnheader`/`rowheader` role. Some screen readers will therefore not announce the sort state, so the attribute may be a no-op for assistive tech.

## Proposal
Give the table proper semantics so `aria-sort` (and the rows/cells generally) are conveyed to assistive tech. Either:
- Convert the markup to a real `<table>`/`<thead>`/`<tbody>` with `<th scope="col">` headers wrapping the sort buttons, or
- Keep the div layout but add ARIA roles (`role="table"`/`"grid"`, `role="row"`, `role="columnheader"`, `role="cell"`) throughout so the structure is valid and `aria-sort` lands on `columnheader` elements.

Keep the sort affordance operable: the clickable element should remain a button (or a header with an inner button) so keyboard/SR users can still trigger sorting.

## Acceptance / Done When
- The sortable column headers expose `role="columnheader"` (or are real `<th>`) with `aria-sort` reflecting the active sort.
- The data rows/cells are exposed with appropriate table/grid roles, so a screen reader reads the grid as a table.
- Sorting remains operable by keyboard, and existing WorkersTable tests still pass.

## Related
- `web/src/workers/WorkersTable.tsx` - the pseudo-table markup and sortable headers.
- [`closed/bug-2026-06-03-workers-view-controls-aria`](closed/bug-2026-06-03-workers-view-controls-aria.md) - added `aria-sort`/`aria-pressed`; this item addresses the deeper semantics gap noted there.
