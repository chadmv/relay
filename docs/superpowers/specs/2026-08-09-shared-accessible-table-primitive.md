# Shared Accessible-Table Primitive - Design

Date: 2026-08-09
Status: Draft (autonomous cycle; conductor review)
Backlog item: `docs/backlog/idea-2026-06-05-shared-accessible-table-primitive.md`

## Overview

The web app has eight CSS-grid pseudo-tables. Five wire ARIA roles by hand and
duplicate the same two helpers; three have no table semantics at all, so a screen
reader gets undifferentiated `div`s on the jobs list, the schedules list and the
worker-detail workspaces panel.

This spec extracts one small primitive into `web/src/components/holo/` and
migrates all eight onto it. The work has two halves that must not be confused:

- **A refactor half** (the five role-layered tables). Zero observable change.
  The proof is that their existing semantic assertions pass with their test files
  edited by zero lines.
- **A correctness half** (the three un-roled tables). A real, screen-reader
  observable behavior change. It needs its own tests, each proven RED against
  today's markup.

Frontend-only. No Go, no endpoint, no schema, no new dependency.

## Inventory, verified 2026-08-09

The backlog item's history is that it was filed with a wrong count, so the
inventory was re-derived from the tree rather than trusted. **It holds.** Method
and result:

- `rg 'role="table"|role="row"|role="columnheader"|role="cell"|role="rowgroup"|role="grid"' web/src`
  returns exactly 70 attributes across exactly 5 files, at the per-file counts the
  item lists (Workers 15, Users 13, Enrollments 13, Reservations 16, Tasks 13).
- `rg 'grid-cols-\[' web/src` returns 13 hits in 12 files. Five are not tables and
  are correctly excluded: `WorkersGrid.tsx` and the two `repeat(auto-fill,...)`
  card grids in `WorkersPage.tsx` and `WorkerDetailPage.tsx` are card layouts with
  no header row; `LogView.tsx:54` is a two-track log line. `JobsTable.tsx` has two
  hits because one is a nested progress-bar grid inside a cell.
- `rg 'grid-cols-[0-9]' web/src` returns 4 hits, none of them tabular.
- Cross-checked from the other direction with a grep for the header-row class
  signature (`text-[10px] tracking-wider text-fg-mute` and the `0.16em` variant),
  which returns the same eight table files plus six unrelated form labels.

So: **eight consumers, five roled, three not.** No correction to the count.

Four findings the item does not record, all of which shape the design:

1. **`caret()` is duplicated four times as well as `ariaSort()`**, in the same
   four files, identical apart from the local sort-field type. The dedupe surface
   is eight helper definitions, not four.
2. **`ReservationsTable.tsx:52-71` already contains a local `SortHeader`
   component.** Someone reached for exactly the abstraction this spec proposes,
   one file wide. That is independent in-repo evidence for the unit to extract,
   and it is why headers below are declarative config rather than children.
3. **Three of the eight have non-row content sitting where `role="table"` would
   put it in an invalid position.** `JobsTable` and `SchedulesTable` render a
   `footer` slot inside the same `GlassPanel` as the rows; `WorkspacesPanel`
   renders an error banner and a `ConfirmDialog` as siblings of its rows. The
   naive migration - put `role="table"` on the existing wrapper - would make each
   of those an invalid child of a `table` role. This is the single most important
   structural constraint on the design, and it decides the frame question below.
4. **The visual frame varies four ways.** `TasksTable`, `JobsTable` and
   `SchedulesTable` use `GlassPanel`. The three admin tables inline GlassPanel's
   base classes minus its shadow. `WorkersTable` uses neither: a hand-written
   `rounded-card border border-border bg-white/5 backdrop-blur`, which predates the
   gradient fidelity upgrade. `WorkspacesPanel` has no frame at all (it renders
   inside the page's `Panel`). Harmonizing these is a visual change and is out of
   scope; see Follow-ups.

## What the eight actually vary on

The project's "don't force the primitive" rule from the Holo relayout applies, so
this is the evidence the API is shaped against.

| Dimension | Varies? | Consequence |
|---|---|---|
| Grid template | Yes, all eight differ | One prop, one declaration site, shared by header and rows |
| Header labels, right-alignment | Yes | Declarative `headers` config |
| Sortability | Yes: 4 sortable, 4 not | `field` optional; no sort type required |
| `ariaSort` / caret mapping | No, identical | Define once, generic over the field type |
| Header row padding + tracking | Yes: `px-4 py-3`, `px-4 py-2`, `px-[18px] py-3` + `0.16em` | Caller supplies; must not be baked into the base |
| Header row `border-b border-border font-mono text-[10px] text-fg-mute` | No, identical in all eight | Primitive owns |
| Data row border color, padding, text size | Yes (`border/40` vs `accent/[0.06]`, `py-2`/`py-2.5`, `11px`/`11.5px`) | Caller supplies |
| Data row `items-center` | No, identical in all eight | Primitive owns |
| Row element | Yes: `div` in seven, `button` in `TasksTable` | `as` prop plus rest-prop passthrough |
| Row-level extras | Yes, heavily | Cells stay entirely with the caller |
| Frame | Yes, four ways | Caller owns; primitive renders no frame |
| Empty / loading state | Yes, four ways | Caller owns |
| Footer slot | Two of eight | Caller owns, outside the table subtree |

The class-override column deserves its own note. `UsersTable.tsx:11-13` already
carries the finding: two competing Tailwind padding utilities on one element
resolve by stylesheet order, not by class-attribute order, so a caller
`className` cannot reliably override a base class. **Therefore the primitive's
base may contain only utilities that are byte-identical across all eight consumers
today.** Everything in the "varies" rows above is caller-supplied, not
override-supplied.

## Approaches considered

**A. Semantic wrapper set: `Table` + `TableRow` + `TableCell`, headers as config,
grid template via context. (Recommended, adopted.)**

The primitive owns roles, `aria-label`, `aria-sort`, the sort button and caret,
and the grid template applied to both header and rows. The caller owns the frame,
the cells, the empty and loading states, and any footer. This is the largest
extraction that stays entirely inside what all eight share, and it removes all
eight helper definitions and the header/body template-agreement hazard.

**B. Minimal role wrappers only, header cells passed as children.**

`Table`/`TableRow`/`TableCell` and nothing else. Simpler and more flexible, but it
leaves `ariaSort`, `caret`, the sort `<button>` markup and the whole header row
duplicated at every call site. That is most of the duplication the item exists to
remove, and it would leave the next table free to ship a header with no
`aria-sort` again. Rejected.

**C. Fully data-driven `DataTable` with column definitions that include cell
renderers.**

Rejected, and the item pre-rejects it. The row extras are not uniform enough:
`UsersTable` holds inline rename state inside a cell, `WorkspacesPanel` owns a
`ConfirmDialog`, `ReservationsTable` renders per-row mutation buttons whose
accessible names embed row identity, `TasksTable` rows are selection controls.
Every one of those comes back as a render prop, which is the caller's JSX with
extra indirection. The frame, empty-state and footer variance would push further
still.

**D. Adopt a headless table library (TanStack Table or similar).**

Rejected. Sorting here is server-side (the sort value is a query parameter, and
`onSort` fires a refetch), pagination is cursor-based and already solved, and
nothing is filtered or grouped client-side. What is actually missing is ARIA
structure, which is the one thing a headless table library does not supply. A new
dependency for zero of its features.

## The primitive

New file `web/src/components/holo/Table.tsx`, exported from
`web/src/components/holo/index.ts`, with a colocated `Table.test.tsx`. Follows the
existing holo conventions: literal class strings with a comment saying why, `as`
plus rest-prop passthrough modelled on `GlassPanel`.

```tsx
export type SortDirection = 'ascending' | 'descending' | 'none'

// One definition, generic over the sort-field type. `sort` is the wire-format
// sort value: the field name, optionally prefixed with '-' for descending.
export function ariaSort<F extends string>(field: F, sort: string): SortDirection
export function sortCaret<F extends string>(field: F, sort: string): string

export interface TableColumn<F extends string = string> {
  label: string
  field?: F                 // present => sortable: button + caret + aria-sort
  align?: 'right'
  className?: string        // per-header extras
}

interface TableProps<F extends string> {
  label: string             // required; becomes aria-label on role="table"
  columns: string           // the `grid-cols-[...]` literal, from the caller
  headers: TableColumn<F>[]
  sort?: string
  onSort?: (field: F) => void
  headerClassName?: string  // the caller's spacing and tracking deltas
  className?: string
  children?: ReactNode      // the rows
}

export function Table<F extends string>(props: TableProps<F>): JSX.Element
export function TableRow(props: { as?: ElementType; className?: string; children?: ReactNode; [k: string]: unknown }): JSX.Element
export function TableCell(props: { className?: string; children?: ReactNode; [k: string]: unknown }): JSX.Element
```

Rendered structure:

```
<div role="table" aria-label={label} className={className}>
  <div role="row" class="grid {columns} border-b border-border font-mono text-[10px] text-fg-mute {headerClassName}">
    <span role="columnheader">LABEL</span>                                   // not sortable
    <div role="columnheader" aria-sort="none|ascending|descending">          // sortable
      <button type="button" class="text-left">LABEL ▼</button>
    </div>
  </div>
  {children}                                                                 // the caller's TableRows
</div>
```

Decisions, each with its reason:

- **`aria-sort` is emitted only on sortable headers.** Applying `aria-sort="none"`
  to a static header advertises a sort affordance that does not exist, and it
  would also change the markup of the five migrated tables, which the refactor
  half forbids.
- **The grid template travels by React context, not by prop drilling.** `Table`
  publishes the `columns` string; `TableRow` consumes it. The defect the item
  names is that "the header row and the body rows must be kept in agreement by
  hand"; a prop on both would preserve exactly that hazard. The context value is
  the raw string, never a fresh object literal, so it is referentially stable
  across renders. `TableRow` rendered outside a `Table` throws with a message
  naming both components: a silent fallback would surface as a mangled layout in
  production, while a throw surfaces in the first test render. This is tested.
- **`columns` is the `grid-cols-[...]` literal only**; the primitive prepends
  `grid`. The literal stays in the consumer file, which Tailwind v4's static scan
  requires, but it is declared once instead of being applied to two elements.
- **No `role="rowgroup"`.** ARIA permits `row` children directly under `table`. A
  rowgroup would force the primitive to wrap the caller's rows in an element it
  owns, which conflicts with the caller composing rows freely, and it changes
  nothing any existing or planned assertion can observe.
- **No `aria-rowcount` / `aria-colcount`.** Cursor pagination means the total is
  often unknown, and a wrong count is worse than none.
- **The primitive renders no frame.** It emits a bare semantic container. The
  caller keeps its existing wrapper exactly as it is today, which (a) makes the
  migration provably visually neutral across four different frames, (b) keeps the
  `jobs-table` / `schedules-table` `data-testid` and its asserted classes on the
  same element, and (c) resolves finding 3: the footer, the error banner and the
  `ConfirmDialog` stay inside the frame but outside the `role="table"` subtree,
  which is the only valid arrangement. An extra unstyled block-level wrapper is
  layout-neutral, because each row is its own grid rather than a subgrid of the
  container.
- **`TableCell` exists rather than leaving callers to write `<span role="cell">`.**
  It is what makes the next table correct by construction, which is the whole
  argument of the backlog item.

Rejected inside the design: having `Table` validate that `headers.length` equals
the number of grid tracks. It holds in all eight today and a mismatch is a real
bug, but enforcing it means parsing an opaque Tailwind class string at runtime.
The assertion belongs in each table's test as a columnheader count, which is where
this spec puts it.

## What stays with the caller

Explicitly not owned by the primitive, so no engineer proposes moving it later:

- Every cell's content and classes.
- The frame element, its classes, and any `data-testid` on it.
- Empty states. All four shapes differ (a centered `max-w-md` panel that still
  renders the footer, a plain panel, an inline "No workspaces." line gated on
  `!isLoading`), and rendering no table at all when there is nothing to show is
  correct.
- Loading states.
- Footers, error banners, dialogs. These are siblings of `<Table>`, never
  children.
- Sort state and the fetch it drives. `onSort` is a callback; the primitive holds
  no state and runs no effects.

## Migration rules

1. **Behavior-preserving for the five.** The only permitted diff is structural:
   helpers deleted, header JSX replaced by config, `role=`/`aria-sort` attributes
   removed from the caller because the primitive now emits them. Same roles, same
   accessible names, same classes on the same elements.
2. **The caller's `className` lands on the row element itself**, not on a wrapper.
   `ReservationsTable.test.tsx:88-91` indexes `getAllByRole('row')` and reads
   `.className` off `rows[3]`, and `UsersTable.test.tsx:96` selects on the
   `opacity-[0.55]` class. Both must keep working untouched.
3. **Rest props pass through `TableRow` to the DOM element.** Required by
   `data-testid={`job-row-${j.id}`}` and by `TasksTable`'s
   `as="button" type="button" aria-selected onClick`.
4. **Non-row content moves out of the table subtree** in `JobsTable`,
   `SchedulesTable` and `WorkspacesPanel`. The existing
   `expect(surface).toContainElement(footer)` tests still pass, because `surface`
   is the frame, not the table.
5. **No visual change anywhere.** Not the frames, not the padding, not the
   tracking. The header spacing inconsistency across the eight is real and is
   deliberately left alone; see Follow-ups.
6. **`aria-label`s for the three newly-roled tables**: `Jobs`, `Schedules`,
   `Source workspaces` (matching the visible `Panel` title that wraps it).
7. **`web/src/components/holo/index.ts` and `index.test.ts` are appended to, not
   rewritten.** The existing barrel test enumerates the shipped primitives and the
   "does not export the deferred Spark primitive" test stays as it is.

## Testing strategy

**The primitive** (`Table.test.tsx`), covering: table role and accessible name;
header row and columnheader roles; the grid template appearing on both the header
row and a `TableRow`; `aria-sort` present and correct on sortable headers in all
three states; `aria-sort` absent on non-sortable headers; `onSort` fires with the
field; the caret follows the active sort; `TableRow` renders as an alternate
element and forwards arbitrary props; `TableCell` emits `role="cell"`;
`TableRow` outside a `Table` throws. `ariaSort` and `sortCaret` are also tested
directly as pure functions, including the descending prefix and the
not-the-active-field case.

**The refactor half, five tables: the gate is a zero-line test diff.**
`WorkersTable.test.tsx`, `UsersTable.test.tsx`, `EnrollmentsTable.test.tsx`,
`ReservationsTable.test.tsx` and `TasksTable.test.tsx` must be byte-identical
before and after, verified with `git diff --stat` on the PR. A migration that
needs to adjust an assertion has changed behavior, and that is the finding, not
the fix. `WorkersTable.test.tsx:47-56` is the strongest of these and is the reason
`WorkersTable` is the reference migration: it asserts the table role with its
name, exactly 2 rows, exactly 6 columnheaders and exactly 6 cells.

**The correctness half, three tables: RED-proven, count-based assertions.** For
each of `JobsTable`, `SchedulesTable` and `WorkspacesPanel`, a new test in the
existing test file (appended, not rewritten):

```tsx
test('exposes table, row, columnheader, and cell roles', () => {
  renderTable(jobs)                                   // two rows in the fixture
  expect(screen.getByRole('table', { name: 'Jobs' })).toBeInTheDocument()
  expect(screen.getAllByRole('row')).toHaveLength(3)  // 1 header + 2 data
  expect(screen.getAllByRole('columnheader')).toHaveLength(7)
  expect(screen.getAllByRole('cell')).toHaveLength(14)
})
```

Expected counts, each cross-checked against the column count and the grid track
count, which agree in all eight tables: Jobs 7 columns, Schedules 9, Workspaces 6.

The counts are load-bearing, not decoration. `getByRole('table')` alone passes on
a partial migration where the wrapper got a role and the rows did not; the
columnheader and cell counts are what catch it.

**RED proof is required evidence, not a claim.** For each of the three, run the
new test against the unmigrated component and paste the failure into the PR. The
expected failure is `Unable to find an accessible element with the role "table"`.
A test written after the migration and never seen failing proves nothing about
what it would have caught, which is a standing lesson in this project's retros.

**Suite-level:** the full web suite must be green, and the count must rise by
exactly the number of tests added. Production build (`tsc -b && vite build`) green,
with `git checkout -- web/dist/` before the change set is assembled.

## Sequencing

Eight migrations plus a new primitive is too large for one reviewable PR, and the
two halves have different review criteria. Four slices, each independently
green and mergeable:

1. **Primitive + `WorkersTable`.** `Table.tsx`, `Table.test.tsx`, the barrel and
   its test, and the reference migration. `WorkersTable` goes first because it has
   the richest existing semantic assertions, so slice 1 proves the primitive is
   behavior-preserving before anything else consumes it. Its frame is also the odd
   one out, which proves the caller-owns-frame decision on the hardest case.
2. **`JobsTable` + `SchedulesTable`.** The correctness half on the two
   highest-traffic pages, RED-first. Front-loaded ahead of the remaining refactors
   because this is the user-visible value in the item.
3. **`UsersTable` + `EnrollmentsTable` + `ReservationsTable`.** Three mechanical
   admin migrations of near-identical shape, repetitive but easy to review
   together. `ReservationsTable`'s local `SortHeader` is deleted here.
4. **`TasksTable` + `WorkspacesPanel`.** The two special cases: the row-as-button
   with `aria-selected`, and the last un-roled table (admin-gated, lowest traffic,
   so it goes last as the item suggests). This slice also carries the backlog
   close: the `git mv` to `docs/backlog/closed/` via `/backlog close`, which is
   required scope and not optional cleanup.

The backlog item stays open until slice 4 merges, because its acceptance requires
all eight to consume the primitive.

## Risks

- **Slice 1 sets the API for seven later consumers.** If slice 3 or 4 needs an API
  change, that is a signal the primitive was shaped on too little evidence. Cheap
  mitigation: slice 1's PR description lists the specific requirement each later
  table places on the API (row-as-button, rest props, no-sort, no-frame,
  right-aligned header, opacity class on the row element), all of which are
  enumerated in the variance table above and are already checked against the code.
- **Concurrent work in these files.** Eight consumers across five feature
  directories is a wide surface for merge conflicts. Slices are ordered so each
  touches a disjoint set of files; do not run two slices concurrently, and do not
  start this alongside other work in `web/src/jobs/` or `web/src/admin/`.
- **A migration that quietly relaxes semantics.** The zero-line test diff gate is
  the specific defense. A reviewer should check `git diff --stat` for the five test
  files before reading anything else.
- **Silent visual drift.** Nothing in the suite asserts most of the row padding.
  The defense is the rule that the primitive's base contains only byte-identical
  utilities, plus a reviewer diffing the class strings on the header and row
  elements before and after.

## Non-goals

- No virtualization, memoization, or other render-cost work. All eight render
  every row of a cursor-paginated page (about 50 rows), and this change adds one
  wrapper `div` per table and one context read per row. If a table ever needs
  virtualization that is its own item, with a measurement first.
- No keyboard navigation model, roving tabindex, or `role="grid"` upgrade.
- No visual harmonization of frames, padding or tracking.
- No column resizing, reordering, persistence, or client-side sorting.
- No changes to sort wire formats or to any API.

## Security, scalability, invariants

- **Threat model: presentational only.** No new network call, no new data flow, no
  new user input path, no change to what any endpoint returns or who may call it.
  The one rule worth stating because the API permits it: `TableRow` and `TableCell`
  spread rest props onto a DOM element, and neither the primitive nor any caller
  may use that to pass `dangerouslySetInnerHTML`. The primitive never sets it. No
  runtime filter, which would be theater given the caller controls both sides.
- **Load and failure modes.** Render cost is unchanged to within one wrapper
  element per table. The context value is a stable string, so it introduces no
  re-render churn. The one new failure mode is the deliberate throw when
  `TableRow` is used outside a `Table`, which is unconditional and therefore
  cannot appear only in production.
- **Invariants.** None of the six backend invariants is in play: no Go, no gRPC
  stream, no lock, no epoch, no JSON entry point. The generation-ordering
  invariant, which is the one that has bitten `web/` before, has nothing to bite
  on here by construction: the primitive holds no state, runs no effect, opens no
  subscription and owns no `AbortController`. Any future proposal to give it
  internal async state should be read against that sentence.

## Acceptance criteria

Sharpened from the item's "Acceptance / Done When":

1. `web/src/components/holo/Table.tsx` exists, exports `Table`, `TableRow`,
   `TableCell`, `ariaSort` and `sortCaret`, and is re-exported from
   `web/src/components/holo/index.ts` with the barrel test appended.
2. All eight tables consume it: `WorkersTable`, `UsersTable`, `EnrollmentsTable`,
   `ReservationsTable`, `TasksTable`, `JobsTable`, `SchedulesTable`,
   `WorkspacesPanel`.
3. `rg 'function ariaSort|function caret' web/src` returns exactly one file,
   `web/src/components/holo/Table.tsx`. Restated from the item to cover `caret`,
   which is duplicated the same four times and which the item does not mention.
4. `rg 'role="table"|role="row"|role="columnheader"|role="cell"' web/src` returns
   matches only in `web/src/components/holo/Table.tsx`. This replaces the item's
   count-based framing with an exhaustive one: 70 hand-written attributes go to
   zero outside the primitive.
5. No table applies a grid-template class to more than one element. Each consumer
   declares its `grid-cols-[...]` literal exactly once and passes it as `columns`.
   The item's "no file declares a `COLS` constant" is unsatisfiable as literally
   written: the template is per-table and must remain a literal in the consumer
   file for Tailwind v4's static scan to emit the class. What the primitive owns is
   the single declaration site and its application to both header and rows.
6. The five role-layered tables' test files are unchanged, verified by a zero-line
   `git diff --stat` on `WorkersTable.test.tsx`, `UsersTable.test.tsx`,
   `EnrollmentsTable.test.tsx`, `ReservationsTable.test.tsx` and
   `TasksTable.test.tsx`.
7. `JobsTable`, `SchedulesTable` and `WorkspacesPanel` each have a new test
   asserting the table role with its accessible name plus exact row, columnheader
   and cell counts, and each was demonstrated RED against the unmigrated component
   with the failure output recorded in its PR.
8. No footer, error banner or dialog is a descendant of any `role="table"` element.
9. Full web suite green with the test count up by exactly the number added;
   `tsc -b && vite build` green; no `web/dist` churn in any change set.
10. The backlog item is closed with `/backlog close` in slice 4, including the
    `git mv` into `docs/backlog/closed/`.

## Follow-ups proposed, not filed

Human accept required before any of these become backlog items.

1. **`TasksTable`'s `aria-selected` is inert.** Its rows carry `aria-selected`
   under `role="table"`, where assistive technology ignores it, because selection
   is only meaningful in `grid`/`treegrid`. So the selected task is visually
   marked and not announced. Pre-existing, deliberately unchanged by this work
   (the refactor half must be behavior-preserving), and a real fix means
   `role="grid"` plus a keyboard navigation model. Worth its own item.
2. **Four of eight tables hand-roll the glass frame instead of using
   `GlassPanel`.** The three admin tables inline its base minus the shadow;
   `WorkersTable` uses a pre-upgrade flat `bg-white/5`. The same
   primitive-exists-but-was-not-used disease this item is about, one layer out.
   Excluded here because adopting `GlassPanel` in `WorkersTable` is a visible
   change (it gains the gradient and shadow) and would spoil the
   behavior-preserving proof.
3. **Header and row spacing is inconsistent across the eight** (`px-4 py-3`,
   `px-4 py-2`, `px-[18px] py-2.5`, and two tracking values). Now that the
   structure is shared, harmonizing is a small deliberate design decision rather
   than an eight-place edit. Needs a design call, not an engineering one.
