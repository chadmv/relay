# Shared Accessible-Table Primitive Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extract one `Table` / `TableRow` / `TableCell` primitive into `web/src/components/holo/` and migrate all eight CSS-grid pseudo-tables onto it, deleting eight duplicated helper definitions and giving screen-reader table semantics to the three tables that have none.

**Architecture:** The primitive owns the ARIA roles, the `aria-label`, `aria-sort`, the sort button and its caret, and the grid template (published on a React context so the header row and the body rows cannot drift apart). It renders **no frame**: every caller keeps its existing wrapper element exactly as it is, which makes the migration provably visually neutral across four different frame styles and keeps footers, error banners and dialogs inside the visual surface but outside the `role="table"` subtree. Cells, empty states, loading states, footers and sort state all stay with the caller.

**Tech Stack:** React 18 + TypeScript (strict, `noUnusedLocals`), Tailwind v4, Vitest + @testing-library/react + jest-dom, msw. No new dependency.

**Source spec:** `docs/superpowers/specs/2026-08-09-shared-accessible-table-primitive.md` (authoritative).
**Backlog item:** `docs/backlog/idea-2026-06-05-shared-accessible-table-primitive.md`.

---

## Slice independence declaration

**FE/BE split: 100% frontend. Zero Go, zero SQL, zero proto, zero `make generate`.** No endpoint, no schema, no migration, no change to what any handler returns or who may call it. `make test` (Go) is not part of the gate for this work; `cd web && npm test` is.

**Independence: SEQUENTIAL. The four slices must NOT be run in parallel.**

- Slice 1 creates the primitive that slices 2, 3 and 4 all import. Nothing else can start until `web/src/components/holo/Table.tsx` exists and is exported.
- Slices 2, 3 and 4 touch disjoint file sets and are technically parallelizable *once slice 1 lands*, but they are still to be run one at a time by a single engineer: the spec's risk register calls concurrent work in these files out explicitly, and slice 4 changes a shared page test (`WorkerDetailPage.test.tsx`) whose failure only appears after the WorkspacesPanel migration.
- Everything ships in **one PR** as **four commit groups** (conductor decision, overriding the spec's four-PR recommendation). The reviewability argument the spec made for four PRs is met by four clean commits, and the backlog item's acceptance requires all eight consumers before it can close.

**Halt safety:** the full web suite plus `npm run build` must be green at every slice boundary, so a halt after any commit leaves a coherent branch.

---

## Corrections and decisions made against the spec

Recorded here so a reviewer does not read them as drift. Each was verified against the tree on 2026-08-09.

1. **`WorkerDetailPage.test.tsx:118-126` will fail when `WorkspacesPanel` gains roles, and the spec does not mention it.** That test renders the page as an admin (which mounts `WorkspacesPanel` with an empty workspaces list) and asserts `screen.queryByRole('row')` and `screen.queryByRole('table')` are both absent, globally. Today that passes only because `WorkspacesPanel` has no roles at all. The assertion's *intent* is "the reservations panel fabricates no rows"; it over-reached by using a page-global query. Task 4.4 narrows it, after recording the failure. This is a sixth test file edited, outside the five protected by the zero-line-diff gate, and it is expected and justified.
2. **`ariaSort` and `sortCaret` are declared non-generic** (`(field: string, sort: string)`) rather than the spec's `<F extends string>(field: F, ...)`. A type parameter used in exactly one parameter position and nowhere in the return type is equivalent to `string` and buys no type safety. `Table` itself stays generic, where the parameter is load-bearing: it ties `headers[].field` to `onSort`'s argument type, so a typo'd field name is a compile error.
3. **`Table`'s type parameter gets a default: `Table<F extends string = string>`.** Four of the eight tables have no sortable column, so TypeScript has no inference candidate for `F` at those call sites. Without the default the call sites would not compile.
4. **Acceptance criterion 3 is satisfiable exactly as written.** `rg 'function ariaSort|function caret' web/src` will return only `web/src/components/holo/Table.tsx`, because the primitive names its caret helper `sortCaret` (which the `function caret` alternative does not match) and does declare `export function ariaSort`. No rewording needed.
5. **Class-attribute ordering changes on the header row** (the primitive appends `headerClassName` after its base, so `px-4 py-3` now trails `text-fg-mute` instead of preceding `font-mono`). The class *set* is byte-identical; Tailwind resolves by stylesheet order, and every existing assertion is `toHaveClass`, which is set-based. Called out so a reviewer diffing class strings does not flag it as drift.

---

## File structure

**Created (2 files):**

| File | Responsibility |
|---|---|
| `web/src/components/holo/Table.tsx` | The primitive: `Table`, `TableRow`, `TableCell`, `ariaSort`, `sortCaret`, `TableColumn`, `SortDirection`, and the private columns context. |
| `web/src/components/holo/Table.test.tsx` | Unit tests for the primitive, including the two pure helpers and the orphan-`TableRow` throw. |

**Modified (13 files):**

| File | Change | Slice |
|---|---|---|
| `web/src/components/holo/index.ts:1-11` | Append the `Table` value and type re-exports. | 1 |
| `web/src/components/holo/index.test.ts:4-13` | Append three assertions to the existing barrel test (do not add a test, do not touch the Spark test). | 1 |
| `web/src/workers/WorkersTable.tsx:1-82` | Migrate. Delete local `caret`/`ariaSort`. | 1 |
| `web/src/jobs/JobsTable.tsx:1-77` | Migrate (newly roled). Footer moves outside the table subtree. | 2 |
| `web/src/jobs/JobsTable.test.tsx` | Append one RED-proven roles test. | 2 |
| `web/src/schedules/SchedulesTable.tsx:1-94` | Migrate (newly roled). Footer moves outside the table subtree. | 2 |
| `web/src/schedules/SchedulesTable.test.tsx` | Append one RED-proven roles test. | 2 |
| `web/src/admin/users/UsersTable.tsx:1-208` | Migrate. Delete local `caret`/`ariaSort`. | 3 |
| `web/src/admin/enrollments/EnrollmentsTable.tsx:1-104` | Migrate. Delete local `caret`/`ariaSort`. | 3 |
| `web/src/admin/reservations/ReservationsTable.tsx:1-179` | Migrate. Delete local `caret`/`ariaSort` **and** the local `SortHeader` component (`:52-71`). | 3 |
| `web/src/jobs/TasksTable.tsx:1-70` | Migrate. Row is `as="button"` with rest-prop passthrough. | 4 |
| `web/src/workers/WorkspacesPanel.tsx:1-75` | Migrate (newly roled). Empty state, error banner and `ConfirmDialog` move outside the table subtree. | 4 |
| `web/src/workers/WorkspacesPanel.test.tsx` | Append one RED-proven roles test. | 4 |
| `web/src/workers/WorkerDetailPage.test.tsx:118-126` | Narrow two page-global role assertions. See correction 1. | 4 |

**Must NOT change (the zero-line-diff gate, acceptance criterion 6):**

`web/src/workers/WorkersTable.test.tsx`, `web/src/admin/users/UsersTable.test.tsx`, `web/src/admin/enrollments/EnrollmentsTable.test.tsx`, `web/src/admin/reservations/ReservationsTable.test.tsx`, `web/src/jobs/TasksTable.test.tsx`.

---

## Ground rules for the engineer

- **Every test body in this plan is a guess until you have seen it fail for the reason stated.** Do not copy a test in, see it green, and move on. Where a step says "run it and expect FAIL with X", if you get a *different* failure or a pass, stop and work out why before writing implementation.
- **All commands run from `D:\dev\relay\.claude\worktrees\pr-merging-session-3f03bb`.** The web commands run from the `web/` subdirectory. Never `cd D:/dev/relay` - that is a different worktree on a different branch.
- **`web/dist` is tracked but stale.** Any `npm run build` dirties it. Always `git checkout -- web/dist/` before `git add`. Never commit `web/dist` changes.
- **One commit per slice**, at the end of the slice, after the full suite and the build are green.
- Never use em dashes or en dashes in code, comments, tests or commit messages. ASCII hyphens only.
- Do not harmonize frames, padding or tracking. Do not adopt `GlassPanel` in `WorkersTable`. Do not touch `aria-selected` semantics in `TasksTable`. All three are explicit non-goals.

**Baseline to record before Task 1.1:**

```bash
cd web && npm test 2>&1 | tail -5
```

Write down the "Tests  N passed (N)" number. Expected deltas: slice 1 `+12`, slice 2 `+2`, slice 3 `+0`, slice 4 `+1`. Total `+15`.

---

# Slice 1: The primitive + WorkersTable

`WorkersTable` goes first because `WorkersTable.test.tsx:47-56` is the strongest existing semantic assertion in the repo (table role with name, exactly 2 rows, exactly 6 columnheaders, exactly 6 cells), and because its hand-written flat frame is the odd one out, which proves the caller-owns-frame decision on the hardest case.

### Task 1.1: The two pure helpers

**Files:**
- Create: `web/src/components/holo/Table.tsx`
- Create: `web/src/components/holo/Table.test.tsx`

- [ ] **Step 1: Write the failing tests**

Create `web/src/components/holo/Table.test.tsx` with exactly this content:

```tsx
import { expect, test } from 'vitest'
import { ariaSort, sortCaret } from './Table'

test('ariaSort maps the wire sort value onto the ARIA direction', () => {
  expect(ariaSort('name', 'name')).toBe('ascending')
  expect(ariaSort('name', '-name')).toBe('descending')
  expect(ariaSort('name', '-created_at')).toBe('none')
  expect(ariaSort('name', 'created_at')).toBe('none')
  expect(ariaSort('name', '')).toBe('none')
})

test('sortCaret returns a caret only for the active field', () => {
  expect(sortCaret('name', 'name')).toBe(' \u25b2')
  expect(sortCaret('name', '-name')).toBe(' \u25bc')
  expect(sortCaret('name', 'created_at')).toBe('')
  expect(sortCaret('name', '')).toBe('')
})
```

The escapes are the same glyphs the four existing helpers emit (`\u25b2` is the up caret, `\u25bc` the down caret). Using escapes in the test and literals in the source proves the source really carries those characters.

- [ ] **Step 2: Run the tests to verify they fail**

```bash
cd web && npx vitest run src/components/holo/Table.test.tsx
```

Expected: FAIL. `Error: Failed to resolve import "./Table" from "src/components/holo/Table.test.tsx"`.

- [ ] **Step 3: Write the minimal implementation**

Create `web/src/components/holo/Table.tsx`:

```tsx
export type SortDirection = 'ascending' | 'descending' | 'none'

// One definition, replacing the four duplicated pairs in WorkersTable, UsersTable,
// EnrollmentsTable and ReservationsTable. `sort` is the wire-format sort value: the
// field name, optionally '-'-prefixed for descending. Field names contain
// underscores and never hyphens, so stripping the first '-' is exactly "strip the
// descending prefix" - this is the behavior of the four helpers it replaces, kept
// byte-for-byte so the five already-roled tables are unchanged.
export function ariaSort(field: string, sort: string): SortDirection {
  if (sort.replace('-', '') !== field) return 'none'
  return sort.startsWith('-') ? 'descending' : 'ascending'
}

export function sortCaret(field: string, sort: string): string {
  if (sort.replace('-', '') !== field) return ''
  return sort.startsWith('-') ? ' ▼' : ' ▲'
}
```

- [ ] **Step 4: Run the tests to verify they pass**

```bash
cd web && npx vitest run src/components/holo/Table.test.tsx
```

Expected: PASS, 2 tests.

---

### Task 1.2: `Table`, `TableRow`, `TableCell`

**Files:**
- Modify: `web/src/components/holo/Table.tsx`
- Modify: `web/src/components/holo/Table.test.tsx`

- [ ] **Step 1: Write the failing tests**

Replace the import line at the top of `web/src/components/holo/Table.test.tsx` and append the new tests, so the file reads:

```tsx
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, test, vi } from 'vitest'
import { Table, TableCell, TableRow, ariaSort, sortCaret } from './Table'

test('ariaSort maps the wire sort value onto the ARIA direction', () => {
  expect(ariaSort('name', 'name')).toBe('ascending')
  expect(ariaSort('name', '-name')).toBe('descending')
  expect(ariaSort('name', '-created_at')).toBe('none')
  expect(ariaSort('name', 'created_at')).toBe('none')
  expect(ariaSort('name', '')).toBe('none')
})

test('sortCaret returns a caret only for the active field', () => {
  expect(sortCaret('name', 'name')).toBe(' \u25b2')
  expect(sortCaret('name', '-name')).toBe(' \u25bc')
  expect(sortCaret('name', 'created_at')).toBe('')
  expect(sortCaret('name', '')).toBe('')
})

test('renders a table role whose accessible name is the label', () => {
  render(<Table label="Widgets" columns="grid-cols-[1fr]" headers={[{ label: 'A' }]} />)
  expect(screen.getByRole('table', { name: 'Widgets' })).toBeInTheDocument()
})

test('renders a header row with one columnheader per configured column', () => {
  render(
    <Table label="W" columns="grid-cols-[1fr_1fr_1fr]" headers={[{ label: 'A' }, { label: 'B' }, { label: 'C' }]} />,
  )
  expect(screen.getAllByRole('row')).toHaveLength(1)
  expect(screen.getAllByRole('columnheader')).toHaveLength(3)
})

test('applies the grid template to the header row and to every TableRow', () => {
  render(
    <Table label="W" columns="grid-cols-[1fr_80px]" headers={[{ label: 'A' }, { label: 'B' }]}>
      <TableRow data-testid="r1">
        <TableCell>x</TableCell>
        <TableCell>y</TableCell>
      </TableRow>
    </Table>,
  )
  const header = screen.getAllByRole('row')[0]
  expect(header).toHaveClass('grid', 'grid-cols-[1fr_80px]')
  const row = screen.getByTestId('r1')
  expect(row).toHaveClass('grid', 'grid-cols-[1fr_80px]', 'items-center')
})

test('emits aria-sort only on sortable headers, and follows the active sort', () => {
  const headers = [
    { label: 'NAME', field: 'name' as const },
    { label: 'CREATED', field: 'created_at' as const },
    { label: 'STATIC' },
  ]
  const { rerender } = render(
    <Table label="W" columns="grid-cols-[1fr_1fr_1fr]" headers={headers} sort="-created_at" onSort={() => {}} />,
  )
  expect(screen.getByRole('columnheader', { name: /^CREATED/ })).toHaveAttribute('aria-sort', 'descending')
  expect(screen.getByRole('columnheader', { name: /^NAME/ })).toHaveAttribute('aria-sort', 'none')
  // A static header must NOT advertise a sort affordance it does not have.
  expect(screen.getByRole('columnheader', { name: 'STATIC' })).not.toHaveAttribute('aria-sort')

  rerender(
    <Table label="W" columns="grid-cols-[1fr_1fr_1fr]" headers={headers} sort="created_at" onSort={() => {}} />,
  )
  expect(screen.getByRole('columnheader', { name: /^CREATED/ })).toHaveAttribute('aria-sort', 'ascending')
})

test('the caret follows the active sort direction', () => {
  const headers = [{ label: 'NAME', field: 'name' as const }]
  const { rerender } = render(
    <Table label="W" columns="grid-cols-[1fr]" headers={headers} sort="-name" onSort={() => {}} />,
  )
  expect(screen.getByRole('button', { name: 'NAME \u25bc' })).toBeInTheDocument()
  rerender(<Table label="W" columns="grid-cols-[1fr]" headers={headers} sort="name" onSort={() => {}} />)
  expect(screen.getByRole('button', { name: 'NAME \u25b2' })).toBeInTheDocument()
})

test('clicking a sortable header calls onSort with that column field', async () => {
  const onSort = vi.fn()
  render(
    <Table
      label="W"
      columns="grid-cols-[1fr_1fr]"
      headers={[{ label: 'NAME', field: 'name' as const }, { label: 'STATIC' }]}
      sort="-created_at"
      onSort={onSort}
    />,
  )
  await userEvent.click(screen.getByRole('button', { name: 'NAME' }))
  expect(onSort).toHaveBeenCalledWith('name')
  // A non-sortable header renders no button, so it cannot be a dead affordance.
  expect(screen.queryByRole('button', { name: 'STATIC' })).not.toBeInTheDocument()
})

test('a right-aligned header carries text-right plus its own className, and a plain one carries no class attribute', () => {
  render(
    <Table
      label="W"
      columns="grid-cols-[1fr_1fr]"
      headers={[{ label: 'A' }, { label: 'ACT', align: 'right', className: 'pr-2' }]}
    />,
  )
  expect(screen.getByRole('columnheader', { name: 'ACT' })).toHaveClass('text-right', 'pr-2')
  expect(screen.getByRole('columnheader', { name: 'A' })).not.toHaveAttribute('class')
})

test('TableRow renders as the element named by `as` and forwards arbitrary props', async () => {
  const onClick = vi.fn()
  render(
    <Table label="W" columns="grid-cols-[1fr]" headers={[{ label: 'A' }]}>
      <TableRow as="button" type="button" aria-selected data-testid="row-btn" onClick={onClick}>
        <TableCell>x</TableCell>
      </TableRow>
    </Table>,
  )
  const row = screen.getByTestId('row-btn')
  expect(row.tagName).toBe('BUTTON')
  expect(row).toHaveAttribute('role', 'row')
  expect(row).toHaveAttribute('aria-selected', 'true')
  expect(row).toHaveAttribute('type', 'button')
  await userEvent.click(row)
  expect(onClick).toHaveBeenCalledTimes(1)
})

test('TableCell exposes role=cell and merges its className', () => {
  render(
    <Table label="W" columns="grid-cols-[1fr]" headers={[{ label: 'A' }]}>
      <TableRow>
        <TableCell className="text-fg-mute">only</TableCell>
      </TableRow>
    </Table>,
  )
  const cells = screen.getAllByRole('cell')
  expect(cells).toHaveLength(1)
  expect(cells[0]).toHaveTextContent('only')
  expect(cells[0]).toHaveClass('text-fg-mute')
})

test('TableRow rendered outside a Table throws, naming both components', () => {
  // React logs the render error to console.error before rethrowing; silence it so
  // the deliberate failure does not read as a broken suite.
  const spy = vi.spyOn(console, 'error').mockImplementation(() => {})
  expect(() => render(<TableRow>orphan</TableRow>)).toThrow(/TableRow must be rendered inside a Table/)
  spy.mockRestore()
})
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
cd web && npx vitest run src/components/holo/Table.test.tsx
```

Expected: FAIL, with the two helper tests still passing and the ten new ones failing on `SyntaxError`/`TypeError` around the missing `Table`, `TableRow`, `TableCell` exports (typically `Element type is invalid: expected a string ... but got: undefined`).

- [ ] **Step 3: Write the minimal implementation**

Rewrite `web/src/components/holo/Table.tsx` to exactly this:

```tsx
import { createContext, useContext } from 'react'
import type { ElementType, ReactNode } from 'react'

// Semantic wrapper set for the app's CSS-grid pseudo-tables. It owns the ARIA
// roles, the aria-label, aria-sort, the sort button and its caret, and the grid
// template - which travels on a context so the header row and the body rows cannot
// be put out of agreement by hand.
//
// It deliberately renders NO frame. The caller keeps its own wrapper, which makes
// the migration visually neutral across four different frame styles and keeps
// footers, error banners and dialogs inside the visual surface but OUTSIDE the
// role="table" subtree, where they would be invalid children.
//
// The base strings below contain ONLY utilities that are byte-identical across all
// eight consumers. Two competing Tailwind utilities on one element resolve by
// stylesheet order, not by class-attribute order, so a caller className cannot
// reliably override a base class: anything that varies is caller-supplied, never
// override-supplied. Class strings are literals so Tailwind v4's static scan emits
// them.
const HEADER_BASE = 'border-b border-border font-mono text-[10px] text-fg-mute'
const ROW_BASE = 'items-center'

export type SortDirection = 'ascending' | 'descending' | 'none'

// One definition, replacing the four duplicated pairs in WorkersTable, UsersTable,
// EnrollmentsTable and ReservationsTable. `sort` is the wire-format sort value: the
// field name, optionally '-'-prefixed for descending. Field names contain
// underscores and never hyphens, so stripping the first '-' is exactly "strip the
// descending prefix" - this is the behavior of the four helpers it replaces, kept
// byte-for-byte so the five already-roled tables are unchanged.
export function ariaSort(field: string, sort: string): SortDirection {
  if (sort.replace('-', '') !== field) return 'none'
  return sort.startsWith('-') ? 'descending' : 'ascending'
}

export function sortCaret(field: string, sort: string): string {
  if (sort.replace('-', '') !== field) return ''
  return sort.startsWith('-') ? ' ▼' : ' ▲'
}

export interface TableColumn<F extends string = string> {
  label: string
  // Present => sortable: renders a button, a caret and aria-sort.
  field?: F
  align?: 'right'
  className?: string
}

interface TableProps<F extends string> {
  // Required: becomes the accessible name of the table.
  label: string
  // The `grid-cols-[...]` literal only; Table prepends `grid`. The literal must stay
  // in the consumer file for Tailwind v4's static scan, but it is declared once
  // there instead of being applied to two elements by hand.
  columns: string
  headers: TableColumn<F>[]
  sort?: string
  onSort?: (field: F) => void
  // The caller's header spacing and tracking deltas.
  headerClassName?: string
  className?: string
  children?: ReactNode
}

// The value is the raw columns string, never a fresh object literal, so it is
// referentially stable across renders.
const ColumnsContext = createContext<string | null>(null)

export function Table<F extends string = string>({
  label,
  columns,
  headers,
  sort = '',
  onSort,
  headerClassName,
  className,
  children,
}: TableProps<F>) {
  return (
    <ColumnsContext.Provider value={columns}>
      <div role="table" aria-label={label} className={className}>
        <div role="row" className={`grid ${columns} ${HEADER_BASE} ${headerClassName ?? ''}`}>
          {headers.map((h) => {
            const cls = [h.align === 'right' ? 'text-right' : '', h.className ?? ''].filter(Boolean).join(' ')
            const field = h.field
            if (field === undefined) {
              // No aria-sort on a static header: it would advertise a sort
              // affordance that does not exist.
              return (
                <span key={h.label} role="columnheader" className={cls || undefined}>
                  {h.label}
                </span>
              )
            }
            return (
              <div
                key={h.label}
                role="columnheader"
                aria-sort={ariaSort(field, sort)}
                className={cls || undefined}
              >
                <button type="button" className="text-left" onClick={() => onSort?.(field)}>
                  {h.label}
                  {sortCaret(field, sort)}
                </button>
              </div>
            )
          })}
        </div>
        {/* The caller's rows. No role="rowgroup": ARIA permits row children directly
            under table, and a rowgroup would force this component to wrap rows in an
            element it owns. */}
        {children}
      </div>
    </ColumnsContext.Provider>
  )
}

interface TableRowProps {
  as?: ElementType
  className?: string
  children?: ReactNode
  [prop: string]: unknown
}

export function TableRow({ as, className, children, ...rest }: TableRowProps) {
  const columns = useContext(ColumnsContext)
  // A silent fallback would ship as a mangled layout in production. A throw is
  // unconditional, so it surfaces in the first test render instead.
  if (columns === null) throw new Error('TableRow must be rendered inside a Table')
  const Tag = as ?? 'div'
  return (
    <Tag role="row" className={`grid ${columns} ${ROW_BASE} ${className ?? ''}`} {...rest}>
      {children}
    </Tag>
  )
}

interface TableCellProps {
  className?: string
  children?: ReactNode
  [prop: string]: unknown
}

export function TableCell({ className, children, ...rest }: TableCellProps) {
  return (
    <span role="cell" className={className} {...rest}>
      {children}
    </span>
  )
}
```

- [ ] **Step 4: Run the tests to verify they pass**

```bash
cd web && npx vitest run src/components/holo/Table.test.tsx
```

Expected: PASS, 12 tests.

- [ ] **Step 5: Type-check**

```bash
cd web && npx tsc -b
```

Expected: no output (success).

---

### Task 1.3: Barrel export

**Files:**
- Modify: `web/src/components/holo/index.ts:1-11`
- Modify: `web/src/components/holo/index.test.ts:4-13`

- [ ] **Step 1: Write the failing assertions**

Append three lines inside the **existing** first test in `web/src/components/holo/index.test.ts`, so it reads:

```tsx
test('barrel re-exports the built primitives', () => {
  expect(typeof holo.GlassPanel).toBe('function')
  expect(typeof holo.Eyebrow).toBe('function')
  expect(typeof holo.ProgressBar).toBe('function')
  expect(typeof holo.Chip).toBe('function')
  expect(typeof holo.PillButton).toBe('function')
  expect(typeof holo.KpiStat).toBe('function')
  expect(typeof holo.Panel).toBe('function')
  expect(typeof holo.StatusDot).toBe('function')
  expect(typeof holo.Table).toBe('function')
  expect(typeof holo.TableRow).toBe('function')
  expect(typeof holo.TableCell).toBe('function')
})
```

Do not add a new `test(` block and do not touch the `does not export the deferred Spark primitive` test.

- [ ] **Step 2: Run it to verify it fails**

```bash
cd web && npx vitest run src/components/holo/index.test.ts
```

Expected: FAIL. `expected "undefined" to be "function"` on `holo.Table`.

- [ ] **Step 3: Write the minimal implementation**

Append to `web/src/components/holo/index.ts`:

```ts
export { Table, TableRow, TableCell, ariaSort, sortCaret } from './Table'
export type { TableColumn, SortDirection } from './Table'
```

The `export type` form is required: `tsconfig.json` sets `isolatedModules: true`.

- [ ] **Step 4: Run it to verify it passes**

```bash
cd web && npx vitest run src/components/holo/index.test.ts
```

Expected: PASS, 2 tests (unchanged count).

---

### Task 1.4: Migrate `WorkersTable`

**Files:**
- Modify: `web/src/workers/WorkersTable.tsx:1-82`
- Test (MUST NOT CHANGE): `web/src/workers/WorkersTable.test.tsx`

- [ ] **Step 1: Confirm the existing tests are green before you touch anything**

```bash
cd web && npx vitest run src/workers/WorkersTable.test.tsx
```

Expected: PASS, 6 tests. This is the "before" side of the behavior-preserving proof.

- [ ] **Step 2: Rewrite the component**

Replace the whole of `web/src/workers/WorkersTable.tsx` with:

```tsx
import { Link } from 'react-router-dom'
import { StatusDot } from '../components/holo/StatusDot'
import { Table, TableCell, TableRow, type TableColumn } from '../components/holo'
import { formatRelativeTime, labelChips, livenessView, specLine } from './liveness'
import type { Worker, WorkerSort } from './api'

export type SortField = 'name' | 'status' | 'last_seen_at'

const COLS = 'grid-cols-[1fr_120px_70px_140px_1.2fr_120px]'

const HEADERS: TableColumn<SortField>[] = [
  { label: 'NAME', field: 'name' },
  { label: 'STATUS', field: 'status' },
  { label: 'SLOTS' },
  { label: 'SPEC' },
  { label: 'LABELS' },
  { label: 'LAST SEEN', field: 'last_seen_at' },
]

export function WorkersTable({
  workers,
  sort,
  onSort,
}: {
  workers: Worker[]
  sort: WorkerSort
  onSort: (field: SortField) => void
}) {
  return (
    // The frame stays with the caller and is deliberately left as-is: adopting
    // GlassPanel here would add the gradient and shadow, which is a visible change.
    <div className="rounded-card border border-border bg-white/5 backdrop-blur">
      <Table
        label="Workers"
        columns={COLS}
        headers={HEADERS}
        sort={sort}
        onSort={onSort}
        headerClassName="px-4 py-3 tracking-wider"
      >
        {workers.map((w) => (
          <TableRow
            key={w.id}
            className={`border-b border-border/40 px-4 py-2 font-mono text-[11.5px] ${livenessView(w.status).dimClass}`}
          >
            <TableCell>
              <Link to={`/workers/${w.id}`} className="text-fg hover:text-accent">
                {w.name}
              </Link>
            </TableCell>
            <TableCell>
              <StatusDot status={w.status} />
            </TableCell>
            <TableCell className="text-fg-mute">{w.max_slots}</TableCell>
            <TableCell className="text-[10.5px] text-fg-mute">{specLine(w)}</TableCell>
            <TableCell className="flex flex-wrap gap-1">
              {labelChips(w.labels).map((c) => (
                <span
                  key={c}
                  className="rounded-full border border-accent/40 bg-accent/10 px-1.5 py-0.5 text-[9.5px] text-accent"
                >
                  {c}
                </span>
              ))}
            </TableCell>
            <TableCell className="text-fg-mute">
              {w.last_seen_at ? formatRelativeTime(w.last_seen_at) : '-'}
            </TableCell>
          </TableRow>
        ))}
      </Table>
    </div>
  )
}
```

- [ ] **Step 3: Run the existing tests to verify they still pass, unedited**

```bash
cd web && npx vitest run src/workers/WorkersTable.test.tsx
```

Expected: PASS, 6 tests. If any assertion fails, **do not edit the test**. A failure here is the finding, not the fix: work out which class, role or accessible name the primitive changed and fix the primitive or the migration.

- [ ] **Step 4: Verify the zero-line test diff gate**

```bash
git diff --stat -- web/src/workers/WorkersTable.test.tsx
```

Expected: **no output at all**. Any output is a stop.

---

### Task 1.5: Slice 1 gate and commit

- [ ] **Step 1: Full web suite**

```bash
cd web && npm test
```

Expected: PASS. Test count = baseline + 12.

- [ ] **Step 2: Production build**

```bash
cd web && npm run build
```

Expected: `tsc -b` silent, then a vite build summary with no errors.

- [ ] **Step 3: Discard the dist churn**

```bash
git checkout -- web/dist/
git status --short
```

Expected: only `web/src/components/holo/Table.tsx`, `web/src/components/holo/Table.test.tsx`, `web/src/components/holo/index.ts`, `web/src/components/holo/index.test.ts`, `web/src/workers/WorkersTable.tsx`. Nothing under `web/dist/`.

- [ ] **Step 4: Commit**

```bash
git add web/src/components/holo/Table.tsx web/src/components/holo/Table.test.tsx \
        web/src/components/holo/index.ts web/src/components/holo/index.test.ts \
        web/src/workers/WorkersTable.tsx
git commit -m "feat(web): shared accessible Table primitive; migrate WorkersTable

Table/TableRow/TableCell own the ARIA roles, aria-label, aria-sort, the sort
button and caret, and the grid template (published on a context so header and
body rows cannot drift). It renders no frame: the caller keeps its wrapper, so
non-row content stays inside the surface and outside the table subtree.

WorkersTable is the reference migration. Its test file is unchanged (zero-line
git diff --stat), which is the behavior-preserving proof."
```

---

# Slice 2: JobsTable + SchedulesTable (the correctness half)

These two have no table semantics today, so a screen reader gets undifferentiated `div`s on the two highest-traffic pages. Each new test must be seen RED against the unmigrated component, and the failure output recorded in the PR description.

### Task 2.1: RED test for `JobsTable`

**Files:**
- Modify: `web/src/jobs/JobsTable.test.tsx` (append only)

- [ ] **Step 1: Write the failing test**

Append to the end of `web/src/jobs/JobsTable.test.tsx`:

```tsx
test('exposes table, row, columnheader, and cell roles', () => {
  renderTable(jobs)
  expect(screen.getByRole('table', { name: 'Jobs' })).toBeInTheDocument()
  // 1 header row + 2 data rows.
  expect(screen.getAllByRole('row')).toHaveLength(3)
  // ID, NAME, STATUS, PROGRESS, STARTED, DUR, OWNER - one per grid track.
  expect(screen.getAllByRole('columnheader')).toHaveLength(7)
  // 7 columns x 2 rows. The count is load-bearing: getByRole('table') alone passes
  // on a partial migration where the wrapper got a role and the rows did not.
  expect(screen.getAllByRole('cell')).toHaveLength(14)
})
```

- [ ] **Step 2: Run it against the unmigrated component and RECORD the failure**

```bash
cd web && npx vitest run src/jobs/JobsTable.test.tsx 2>&1 | tee ../.red-jobstable.txt
```

Expected: FAIL with `TestingLibraryElementError: Unable to find an accessible element with the role "table"`.

Copy that failure block into the PR description under a "RED proof: JobsTable" heading. Then delete the scratch file: `rm .red-jobstable.txt` (run from the repo root). If the test passes, stop: it is asserting nothing.

---

### Task 2.2: Migrate `JobsTable`

**Files:**
- Modify: `web/src/jobs/JobsTable.tsx:1-77`

- [ ] **Step 1: Rewrite the component**

Replace the whole of `web/src/jobs/JobsTable.tsx` with:

```tsx
import type { ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { GlassPanel, Table, TableCell, TableRow, type TableColumn } from '../components/holo'
import type { Job } from './api'
import { statusColor, progressPct, formatDuration, formatStarted } from './status'

const COLS = 'grid-cols-[90px_1fr_120px_150px_120px_70px_150px]'

const HEADERS: TableColumn[] = [
  { label: 'ID' },
  { label: 'NAME' },
  { label: 'STATUS' },
  { label: 'PROGRESS' },
  { label: 'STARTED' },
  { label: 'DUR' },
  { label: 'OWNER' },
]

export function JobsTable({ jobs, footer }: { jobs: Job[]; footer?: ReactNode }) {
  if (jobs.length === 0) {
    return (
      <div className="flex flex-col gap-4">
        <GlassPanel className="mx-auto mt-10 max-w-md p-6 text-center text-[13px] text-fg-mute">
          No jobs yet.
        </GlassPanel>
        {footer && <div className="px-1">{footer}</div>}
      </div>
    )
  }
  return (
    <GlassPanel data-testid="jobs-table">
      <Table label="Jobs" columns={COLS} headers={HEADERS} headerClassName="px-4 py-3 tracking-wider">
        {jobs.map((j) => {
          const c = statusColor(j.status)
          const pct = progressPct(j.done_tasks, j.total_tasks)
          return (
            <TableRow
              key={j.id}
              data-testid={`job-row-${j.id}`}
              className={`border-b border-border/40 px-4 py-2 font-mono text-[11.5px] ${
                j.status === 'running' ? 'bg-accent/[0.04]' : ''
              }`}
            >
              <TableCell className="text-fg-mute">{j.id.slice(0, 6)}</TableCell>
              <TableCell className="flex min-w-0 items-center gap-2">
                <Link to={`/jobs/${j.id}`} className="truncate font-sans text-[13px] text-fg hover:text-accent">
                  {j.name}
                </Link>
                {j.scheduled_job_name && (
                  <span className="flex-none rounded-full border border-accent-b/40 bg-accent-b/10 px-1.5 py-0.5 text-[9.5px] text-accent-b">
                    ⟳ {j.scheduled_job_name}
                  </span>
                )}
              </TableCell>
              <TableCell className={`flex items-center gap-2 ${c.text}`}>
                <span className={`h-1.5 w-1.5 rounded-full ${c.dot}`} />
                {j.status}
              </TableCell>
              <TableCell className="grid grid-cols-[1fr_36px] items-center gap-2 pr-4">
                <span className="relative h-1 overflow-hidden rounded bg-white/10">
                  <span
                    className={`absolute inset-y-0 left-0 rounded ${
                      j.status === 'done' ? 'bg-ok' : j.status === 'failed' ? 'bg-err' : 'bg-accent'
                    }`}
                    style={{ width: `${pct}%` }}
                  />
                </span>
                <span className="text-right text-fg">{pct}%</span>
              </TableCell>
              <TableCell className="text-fg-mute">{formatStarted(j.started_at)}</TableCell>
              <TableCell className="text-fg-mute">{formatDuration(j.started_at, j.finished_at)}</TableCell>
              <TableCell className="truncate text-[11px] text-fg-mute">{j.submitted_by_email ?? '-'}</TableCell>
            </TableRow>
          )
        })}
      </Table>
      {/* Outside the table subtree: a footer is not a valid child of role="table".
          It stays inside the GlassPanel, so the surface still contains it. */}
      {footer && <div className="border-t border-border px-4 py-3">{footer}</div>}
    </GlassPanel>
  )
}
```

- [ ] **Step 2: Run the test file to verify it now passes**

```bash
cd web && npx vitest run src/jobs/JobsTable.test.tsx
```

Expected: PASS, 8 tests (7 existing + 1 new). In particular `renders a footer slot inside the table surface when provided` must still pass, because `surface` is the GlassPanel frame, not the table.

- [ ] **Step 3: Run the pages that consume it**

```bash
cd web && npx vitest run src/jobs/JobsPage.test.tsx
```

Expected: PASS.

---

### Task 2.3: RED test for `SchedulesTable`

**Files:**
- Modify: `web/src/schedules/SchedulesTable.test.tsx` (append only)

- [ ] **Step 1: Write the failing test**

Append to the end of `web/src/schedules/SchedulesTable.test.tsx`:

```tsx
test('exposes table, row, columnheader, and cell roles', () => {
  render(
    <SchedulesTable
      schedules={[sched(), sched({ id: 's2', name: 'weekly-report' })]}
      pendingId={null}
      onRunNow={() => {}}
      onToggleEnabled={() => {}}
    />,
  )
  expect(screen.getByRole('table', { name: 'Schedules' })).toBeInTheDocument()
  // 1 header row + 2 data rows.
  expect(screen.getAllByRole('row')).toHaveLength(3)
  // NAME, CRON, TZ, OVERLAP, NEXT RUN, LAST RUN, LAST JOB, OWNER, ACTIONS.
  expect(screen.getAllByRole('columnheader')).toHaveLength(9)
  // 9 columns x 2 rows.
  expect(screen.getAllByRole('cell')).toHaveLength(18)
})
```

- [ ] **Step 2: Run it against the unmigrated component and RECORD the failure**

```bash
cd web && npx vitest run src/schedules/SchedulesTable.test.tsx
```

Expected: FAIL with `Unable to find an accessible element with the role "table"`. Copy the failure block into the PR description under "RED proof: SchedulesTable".

---

### Task 2.4: Migrate `SchedulesTable`

**Files:**
- Modify: `web/src/schedules/SchedulesTable.tsx:1-94`

- [ ] **Step 1: Rewrite the component**

Replace the whole of `web/src/schedules/SchedulesTable.tsx` with:

```tsx
import type { ReactNode } from 'react'
import { GlassPanel, Table, TableCell, TableRow, type TableColumn } from '../components/holo'
import type { Schedule } from './api'
import { formatRelativeTime, nextRunDisplay, shortId } from './format'

const COLS = 'grid-cols-[1.4fr_120px_110px_90px_1fr_1fr_110px_1.3fr_150px]'

const HEADERS: TableColumn[] = [
  { label: 'NAME' },
  { label: 'CRON' },
  { label: 'TZ' },
  { label: 'OVERLAP' },
  { label: 'NEXT RUN' },
  { label: 'LAST RUN' },
  { label: 'LAST JOB' },
  { label: 'OWNER' },
  { label: 'ACTIONS', align: 'right' },
]

export function SchedulesTable({
  schedules,
  pendingId,
  onRunNow,
  onToggleEnabled,
  footer,
}: {
  schedules: Schedule[]
  pendingId: string | null
  onRunNow: (id: string) => void
  onToggleEnabled: (id: string, nextEnabled: boolean) => void
  footer?: ReactNode
}) {
  if (schedules.length === 0) {
    return (
      <div className="flex flex-col gap-4">
        <GlassPanel className="mx-auto mt-10 max-w-md p-6 text-center text-[13px] text-fg-mute">
          No schedules yet.
        </GlassPanel>
        {footer && <div className="px-1">{footer}</div>}
      </div>
    )
  }
  return (
    <GlassPanel data-testid="schedules-table">
      <Table label="Schedules" columns={COLS} headers={HEADERS} headerClassName="px-4 py-3 tracking-wider">
        {schedules.map((s) => {
          const pending = pendingId === s.id
          return (
            <TableRow
              key={s.id}
              className={`border-b border-border/40 px-4 py-2 font-mono text-[11.5px] ${s.enabled ? '' : 'opacity-[0.55]'}`}
            >
              <TableCell className="flex min-w-0 items-center gap-2">
                <span className={`h-1.5 w-1.5 shrink-0 rounded-full ${s.enabled ? 'bg-ok' : 'bg-fg-dim'}`} />
                <span className="truncate font-sans text-[13px] text-fg">{s.name}</span>
              </TableCell>
              <TableCell className="text-fg">{s.cron_expr}</TableCell>
              <TableCell className="truncate text-[10.5px] text-fg-mute">{s.timezone}</TableCell>
              <TableCell>
                <span
                  className={`rounded-full border border-border px-1.5 py-0.5 text-[9.5px] uppercase tracking-wider ${s.overlap_policy === 'allow' ? 'text-accent' : 'text-fg-mute'}`}
                >
                  {s.overlap_policy}
                </span>
              </TableCell>
              <TableCell className={s.enabled ? 'text-fg' : 'text-fg-dim'}>
                {s.enabled ? <span className="text-accent-b">&#9658;</span> : null} {nextRunDisplay(s.next_run_at)}
              </TableCell>
              <TableCell className="text-fg-mute">{s.last_run_at ? formatRelativeTime(s.last_run_at) : '-'}</TableCell>
              <TableCell className="text-[10.5px] text-fg-mute">{shortId(s.last_job_id)}</TableCell>
              <TableCell className="truncate text-[10.5px] text-fg-mute">{s.owner_email}</TableCell>
              <TableCell className="flex justify-end gap-1.5">
                <button
                  type="button"
                  disabled={pending}
                  onClick={() => onRunNow(s.id)}
                  className="rounded-md border border-accent/50 bg-accent/15 px-2.5 py-1 text-[11px] text-fg disabled:opacity-40"
                >
                  Run now
                </button>
                <button
                  type="button"
                  disabled={pending}
                  onClick={() => onToggleEnabled(s.id, !s.enabled)}
                  className="rounded-md border border-border bg-white/5 px-2.5 py-1 text-[11px] text-fg-mute disabled:opacity-40"
                >
                  {s.enabled ? 'Disable' : 'Enable'}
                </button>
              </TableCell>
            </TableRow>
          )
        })}
      </Table>
      {/* Outside the table subtree: a footer is not a valid child of role="table". */}
      {footer && <div className="border-t border-border px-4 py-3">{footer}</div>}
    </GlassPanel>
  )
}
```

- [ ] **Step 2: Run the test file to verify it now passes**

```bash
cd web && npx vitest run src/schedules/SchedulesTable.test.tsx
```

Expected: PASS, 10 tests (9 existing + 1 new).

- [ ] **Step 3: Run the consuming page**

```bash
cd web && npx vitest run src/schedules/SchedulesPage.test.tsx
```

Expected: PASS.

---

### Task 2.5: Slice 2 gate and commit

- [ ] **Step 1: Full web suite**

```bash
cd web && npm test
```

Expected: PASS. Test count = slice-1 count + 2.

- [ ] **Step 2: Build and discard dist churn**

```bash
cd web && npm run build
cd .. && git checkout -- web/dist/ && git status --short
```

Expected: only `web/src/jobs/JobsTable.tsx`, `web/src/jobs/JobsTable.test.tsx`, `web/src/schedules/SchedulesTable.tsx`, `web/src/schedules/SchedulesTable.test.tsx`.

- [ ] **Step 3: Commit**

```bash
git add web/src/jobs/JobsTable.tsx web/src/jobs/JobsTable.test.tsx \
        web/src/schedules/SchedulesTable.tsx web/src/schedules/SchedulesTable.test.tsx
git commit -m "feat(web): table semantics for the jobs and schedules lists

Both were undifferentiated divs to a screen reader. Each gains a role-count test
proven RED against the unmigrated component first. The footer slot moves outside
the role=table subtree (still inside the GlassPanel), which is the only valid
arrangement for a non-row child."
```

---

# Slice 3: UsersTable + EnrollmentsTable + ReservationsTable

Three mechanical admin migrations of near-identical shape. All three are refactor-half: their test files must end with a zero-line diff. `ReservationsTable`'s local `SortHeader` component is deleted here - it was someone reaching for exactly this abstraction, one file wide.

### Task 3.1: Migrate `UsersTable`

**Files:**
- Modify: `web/src/admin/users/UsersTable.tsx:1-208`
- Test (MUST NOT CHANGE): `web/src/admin/users/UsersTable.test.tsx`

- [ ] **Step 1: Confirm green before you touch anything**

```bash
cd web && npx vitest run src/admin/users/UsersTable.test.tsx
```

Expected: PASS, 16 tests.

- [ ] **Step 2: Edit the component**

In `web/src/admin/users/UsersTable.tsx`:

Replace the import block and the `COLS` / helper block (lines 1-27) with:

```tsx
import { useState } from 'react'
import { Chip, Table, TableCell, TableRow, type TableColumn } from '../../components/holo'
import { Input } from '../../components/Input'
import type { AdminUser, UserSort, UserSortField } from './api'

// EMAIL | NAME | ROLE | CREATED | ACTIONS. The hi-fi's SESSIONS and LAST LOGIN
// columns are omitted: no endpoint exposes a per-user token count and `users` has
// no last_login_at column. Faking either would read as real data.
const COLS = 'grid-cols-[1.6fr_1fr_110px_120px_270px]'

const HEADERS: TableColumn<UserSortField>[] = [
  { label: 'EMAIL', field: 'email' },
  { label: 'NAME', field: 'name' },
  { label: 'ROLE' },
  { label: 'CREATED', field: 'created_at' },
  { label: 'ACTIONS', align: 'right' },
]

// Row mini-actions use literal classes rather than PillButton overrides: two
// competing padding utilities on one element resolve by stylesheet order, not by
// class-attribute order, so an override is not reliable at this size.
const MINI = 'rounded-full border px-2.5 py-1 font-mono text-[10.5px] tracking-[0.04em] disabled:opacity-40'
const MINI_GHOST = `${MINI} border-border bg-white/5 text-fg-mute`
const MINI_ACCENT = `${MINI} border-accent/50 bg-accent/10 text-accent`
const MINI_DANGER = `${MINI} border-err/40 bg-err/10 text-err`
```

Then replace the JSX returned by `UsersTable` (from `return (` at line 71 to the closing `)` at line 207) with:

```tsx
  return (
    <div className="rounded-card border border-border bg-gradient-to-b from-white/[0.06] to-white/[0.02] backdrop-blur-[8px]">
      <Table
        label="Users"
        columns={COLS}
        headers={HEADERS}
        sort={sort}
        onSort={onSort}
        headerClassName="px-[18px] py-3 tracking-[0.16em]"
      >
        {users.map((u) => {
          const archived = showArchived && Boolean(u.archived_at)
          const isSelf = u.id === currentUserId
          return (
            <TableRow
              key={u.id}
              className={`border-b border-accent/[0.06] px-[18px] py-2.5 font-mono text-[11.5px] ${
                archived ? 'opacity-[0.55]' : ''
              }`}
            >
              <TableCell className="flex min-w-0 items-center gap-2.5">
                <span className="grid h-6 w-6 flex-none place-items-center rounded-md bg-gradient-to-br from-accent/45 to-accent-b/30 text-[11px] font-semibold text-white">
                  {u.email.charAt(0).toUpperCase()}
                </span>
                <span className="truncate font-sans text-[12.5px] text-fg">{u.email}</span>
              </TableCell>

              <TableCell className="min-w-0 pr-2">
                {editingId === u.id ? (
                  <span className="flex items-center gap-1.5">
                    <Input
                      aria-label={`Name for ${u.email}`}
                      value={draft}
                      onChange={(e) => setDraft(e.target.value)}
                      className="py-1 text-[12px]"
                    />
                    <button type="button" className={MINI_ACCENT} onClick={() => submitRename(u.id)}>
                      Save
                    </button>
                    <button type="button" className={MINI_GHOST} onClick={() => setEditingId(null)}>
                      Cancel
                    </button>
                  </span>
                ) : (
                  <span className="truncate font-sans text-[12px] text-fg-mute">{u.name}</span>
                )}
              </TableCell>

              <TableCell>
                {/* Two values only. Relay's model is a single is_admin boolean; the
                    hi-fi's `service` role is mock fiction. */}
                <Chip tone={u.is_admin ? 'accent' : 'muted'}>{u.is_admin ? 'ADMIN' : 'USER'}</Chip>
              </TableCell>

              <TableCell className="text-[10.5px] text-fg-mute">{u.created_at.slice(0, 10)}</TableCell>

              <TableCell className="flex justify-end gap-1.5">
                {archived ? (
                  // No Unarchive on your own archived row either: the server 400s
                  // "cannot unarchive yourself" (symmetric with the Archive guard
                  // below), so the button would be a guaranteed-failing control.
                  !isSelf && (
                    <button
                      type="button"
                      className={MINI_ACCENT}
                      disabled={busy}
                      aria-label={`Unarchive ${u.email}`}
                      onClick={() => onUnarchive(u)}
                    >
                      Unarchive
                    </button>
                  )
                ) : (
                  <>
                    <button
                      type="button"
                      className={MINI_GHOST}
                      disabled={busy}
                      aria-label={`Reset password for ${u.email}`}
                      onClick={() => onResetPassword(u)}
                    >
                      Reset pw
                    </button>
                    <button
                      type="button"
                      className={MINI_GHOST}
                      disabled={busy}
                      aria-label={`Rename ${u.email}`}
                      onClick={() => startRename(u)}
                    >
                      Rename
                    </button>
                    {/* No Archive on your own row: the server 400s "cannot archive
                        yourself", so the button would be a guaranteed-failing control. */}
                    {!isSelf && (
                      <button
                        type="button"
                        className={MINI_DANGER}
                        disabled={busy}
                        aria-label={`Archive ${u.email}`}
                        onClick={() => onArchive(u)}
                      >
                        Archive
                      </button>
                    )}
                  </>
                )}
              </TableCell>
            </TableRow>
          )
        })}
      </Table>
    </div>
  )
```

Leave the `UsersTableProps` interface, the `useState` hooks and `startRename` / `submitRename` exactly as they are.

- [ ] **Step 3: Run the tests, unedited**

```bash
cd web && npx vitest run src/admin/users/UsersTable.test.tsx
```

Expected: PASS, 16 tests. `container.querySelector('.opacity-\\[0\\.55\\]')` at line 96 must still find the dimmed row, because the caller className lands on the row element itself, not on a wrapper.

- [ ] **Step 4: Zero-line diff gate**

```bash
git diff --stat -- web/src/admin/users/UsersTable.test.tsx
```

Expected: **no output**.

---

### Task 3.2: Migrate `EnrollmentsTable`

**Files:**
- Modify: `web/src/admin/enrollments/EnrollmentsTable.tsx:1-104`
- Test (MUST NOT CHANGE): `web/src/admin/enrollments/EnrollmentsTable.test.tsx`

- [ ] **Step 1: Confirm green before you touch anything**

```bash
cd web && npx vitest run src/admin/enrollments/EnrollmentsTable.test.tsx
```

Expected: PASS, 8 tests.

- [ ] **Step 2: Rewrite the component**

Replace the whole of `web/src/admin/enrollments/EnrollmentsTable.tsx` with:

```tsx
import { Chip, Table, TableCell, TableRow, type TableColumn } from '../../components/holo'
import { deriveStatus, formatExpiryLabel, statusTone } from './enrollmentStatus'
import type { AgentEnrollment, EnrollmentSort, EnrollmentSortField } from './api'

// HOSTNAME HINT | CREATED | EXPIRES | STATUS | NOTE.
//
// Two hi-fi columns are omitted (hifi3-holo-pages.jsx:2164):
//  - TOKEN PREFIX: only tokenhash.Hash(rawHex) is stored, no prefix column exists
//    and nothing returns one.
//  - CREATED BY: created_by is a bare user UUID with no join to `users`, so the
//    cell could only show 36 opaque characters.
// The hi-fi's ACTIONS header is renamed NOTE: the cell holds prose, and a header
// promising actions while delivering a sentence is itself a dead affordance. There
// is no DELETE /v1/agent-enrollments/{id} in v1.
// CREATED is added because it is the default sort key and needs a clickable header.
const COLS = 'grid-cols-[1.6fr_130px_130px_120px_1fr]'

const HEADERS: TableColumn<EnrollmentSortField>[] = [
  { label: 'HOSTNAME HINT' },
  { label: 'CREATED', field: 'created_at' },
  { label: 'EXPIRES', field: 'expires_at' },
  { label: 'STATUS' },
  { label: 'NOTE', align: 'right' },
]

interface EnrollmentsTableProps {
  enrollments: AgentEnrollment[]
  sort: EnrollmentSort
  onSort: (field: EnrollmentSortField) => void
  // Injected so the pill and the relative label are pure functions of props. The
  // tab supplies useNow(60_000); tests supply a fixed Date.
  now: Date
}

export function EnrollmentsTable({ enrollments, sort, onSort, now }: EnrollmentsTableProps) {
  return (
    <div className="rounded-card border border-border bg-gradient-to-b from-white/[0.06] to-white/[0.02] backdrop-blur-[8px]">
      <Table
        label="Agent enrollments"
        columns={COLS}
        headers={HEADERS}
        sort={sort}
        onSort={onSort}
        headerClassName="px-[18px] py-3 tracking-[0.16em]"
      >
        {enrollments.map((e) => {
          const status = deriveStatus(e.expires_at, now)
          return (
            <TableRow
              key={e.id}
              className={`border-b border-accent/[0.06] px-[18px] py-2.5 font-mono text-[11.5px] ${
                status === 'EXPIRED' ? 'opacity-[0.55]' : ''
              }`}
            >
              {/* The key is ABSENT (not null) when unset
                  (internal/api/agent_enrollments.go:90-92), so this is a plain
                  ASCII hyphen placeholder - never an em dash. */}
              <TableCell className="truncate font-sans text-[12.5px] text-fg">
                {e.hostname_hint ?? <span className="text-fg-dim">-</span>}
              </TableCell>
              <TableCell className="text-[10.5px] text-fg-mute">{e.created_at.slice(0, 10)}</TableCell>
              <TableCell className={`text-[11px] ${status === 'ACTIVE' ? 'text-fg' : 'text-fg-mute'}`}>
                {formatExpiryLabel(e.expires_at, now)}
              </TableCell>
              <TableCell>
                <Chip tone={statusTone(status)}>{status}</Chip>
              </TableCell>
              <TableCell className="text-right text-[10.5px] tracking-[0.04em] text-fg-dim">
                consumed on first agent connect
              </TableCell>
            </TableRow>
          )
        })}
      </Table>
    </div>
  )
}
```

- [ ] **Step 3: Run the tests, unedited**

```bash
cd web && npx vitest run src/admin/enrollments/EnrollmentsTable.test.tsx
```

Expected: PASS, 8 tests. Watch line 78 in particular: `expect(screen.getAllByRole('button')).toHaveLength(2)` - the primitive must add exactly the two sortable-header buttons and nothing else.

- [ ] **Step 4: Zero-line diff gate**

```bash
git diff --stat -- web/src/admin/enrollments/EnrollmentsTable.test.tsx
```

Expected: **no output**.

---

### Task 3.3: Migrate `ReservationsTable` and delete its local `SortHeader`

**Files:**
- Modify: `web/src/admin/reservations/ReservationsTable.tsx:1-179`
- Test (MUST NOT CHANGE): `web/src/admin/reservations/ReservationsTable.test.tsx`

- [ ] **Step 1: Confirm green before you touch anything**

```bash
cd web && npx vitest run src/admin/reservations/ReservationsTable.test.tsx
```

Expected: PASS, 10 tests.

- [ ] **Step 2: Rewrite the component**

Replace the whole of `web/src/admin/reservations/ReservationsTable.tsx` with:

```tsx
import { Link } from 'react-router-dom'
import { Chip, Table, TableCell, TableRow, type TableColumn } from '../../components/holo'
import { formatDateTime } from '../../lib/time'
import { deriveStatus, statusTone } from './reservationStatus'
import type { Reservation, ReservationSort, ReservationSortField } from './api'

// NAME | PROJECT | WORKERS | STARTS | ENDS | STATUS | CREATED | ACT.
//
// Against the hi-fi (hifi3-holo-pages.jsx:2205-2278):
//  - The dedicated SELECTOR column is dropped to pay for STATUS and CREATED. A
//    selector, when present, is a `sel` chip beside the name. Every row THIS UI can
//    create has no selector, so a column for it would be permanently empty.
//  - CREATED is added because it is the default sort key and needs a clickable header.
//  - No owner column: user_id is a bare UUID with no join to `users`
//    (internal/api/reservations.go:18, :47).
// The header is WORKERS, not "RESERVED FOR": the listed workers are EXCLUDED from
// dispatch for everyone, so any possessive header would be a claim the scheduler does
// not implement (internal/scheduler/dispatch.go:185-223).
const COLS = 'grid-cols-[1.3fr_110px_1.5fr_130px_130px_110px_110px_100px]'

const HEADERS: TableColumn<ReservationSortField>[] = [
  { label: 'NAME', field: 'name' },
  { label: 'PROJECT' },
  { label: 'WORKERS' },
  { label: 'STARTS', field: 'starts_at' },
  { label: 'ENDS', field: 'ends_at' },
  { label: 'STATUS' },
  { label: 'CREATED', field: 'created_at' },
  { label: 'ACT.', align: 'right' },
]

const MINI = 'rounded-full border px-2.5 py-1 font-mono text-[10.5px] tracking-[0.04em] disabled:opacity-40'
const MINI_DANGER = `${MINI} border-err/40 bg-err/10 text-err`

// Absent KEY (not null) for project/starts_at/ends_at: plain ASCII hyphen, never an
// em dash.
const DASH = <span className="text-fg-dim">-</span>

interface ReservationsTableProps {
  reservations: Reservation[]
  sort: ReservationSort
  onSort: (field: ReservationSortField) => void
  // Injected so the status pill is a pure function of props. The tab supplies
  // useNow(60_000); tests supply a fixed Date.
  now: Date
  busy: boolean
  onDelete: (reservation: Reservation) => void
}

export function ReservationsTable({
  reservations,
  sort,
  onSort,
  now,
  busy,
  onDelete,
}: ReservationsTableProps) {
  return (
    <div className="rounded-card border border-border bg-gradient-to-b from-white/[0.06] to-white/[0.02] backdrop-blur-[8px]">
      <Table
        label="Reservations"
        columns={COLS}
        headers={HEADERS}
        sort={sort}
        onSort={onSort}
        headerClassName="px-[18px] py-3 tracking-[0.16em]"
      >
        {reservations.map((r) => {
          const status = deriveStatus(r, now)
          // `selector` can be null (a create with no selector marshals a nil map to the
          // literal `null`) or {} (column default) or pairs - all three must render
          // without null/undefined reaching the DOM.
          const pairs = r.selector ? Object.entries(r.selector) : []
          return (
            <TableRow
              key={r.id}
              className={`border-b border-accent/[0.06] px-[18px] py-2.5 font-mono text-[11.5px] ${
                status === 'ENDED' ? 'opacity-[0.55]' : ''
              }`}
            >
              <TableCell className="flex min-w-0 items-center gap-2">
                <span className="truncate font-sans text-[12.5px] text-fg">{r.name}</span>
                {pairs.length > 0 && (
                  <Chip tone="muted">
                    <span title={pairs.map(([k, v]) => `${k}=${v}`).join(' ')}>sel</span>
                  </Chip>
                )}
              </TableCell>

              <TableCell className="truncate font-sans text-[12px] text-fg-mute">{r.project ?? DASH}</TableCell>

              <TableCell className="flex flex-wrap gap-1">
                {r.worker_ids.length === 0 ? (
                  <span className="text-[11px] text-fg-dim">none</span>
                ) : (
                  // No FK on worker_ids, so a link can 404 on a deleted or revoked
                  // worker. That is the existing detail page's error state, and an
                  // unresolvable id is itself useful information. Wrapping in a Link
                  // rather than giving Chip an href keeps the shared primitive untouched.
                  r.worker_ids.map((id) => (
                    <Link key={id} to={`/workers/${id}`} title={id}>
                      <Chip tone="muted">{id.slice(0, 8)}</Chip>
                    </Link>
                  ))
                )}
              </TableCell>

              <TableCell className="text-[10.5px] text-fg-mute">
                {r.starts_at ? formatDateTime(r.starts_at) : DASH}
              </TableCell>
              <TableCell className="text-[10.5px] text-fg-mute">
                {r.ends_at ? formatDateTime(r.ends_at) : DASH}
              </TableCell>

              <TableCell>
                <Chip tone={statusTone(status)}>{status}</Chip>
              </TableCell>

              <TableCell className="text-[10.5px] text-fg-mute">{r.created_at.slice(0, 10)}</TableCell>

              <TableCell className="flex justify-end">
                {/* Row identity in the accessible name: a page of 50 buttons all named
                    "Delete" is indistinguishable to a screen reader and to a test. */}
                <button
                  type="button"
                  className={MINI_DANGER}
                  disabled={busy}
                  aria-label={`Delete reservation ${r.name}`}
                  onClick={() => onDelete(r)}
                >
                  Delete
                </button>
              </TableCell>
            </TableRow>
          )
        })}
      </Table>
    </div>
  )
}
```

The local `SortHeader` component and the local `caret` / `ariaSort` are gone.

- [ ] **Step 3: Run the tests, unedited**

```bash
cd web && npx vitest run src/admin/reservations/ReservationsTable.test.tsx
```

Expected: PASS, 10 tests. Two to watch:
- line 88-91 indexes `getAllByRole('row')` and reads `rows[3].className` - the caller className must land on the row element itself, and the header must remain `rows[0]`.
- line 43-53 uses `getByText(new RegExp('^NAME'))`, which matches an element by its *direct* text children. The sortable header's text lives on the `<button>` in both the old and the new markup, so this is unchanged.

- [ ] **Step 4: Zero-line diff gate**

```bash
git diff --stat -- web/src/admin/reservations/ReservationsTable.test.tsx
```

Expected: **no output**.

---

### Task 3.4: Slice 3 gate and commit

- [ ] **Step 1: Full web suite**

```bash
cd web && npm test
```

Expected: PASS. Test count **unchanged** from slice 2 (this slice adds no tests; it is pure refactor).

- [ ] **Step 2: Verify all three zero-line diffs at once**

```bash
git diff --stat -- web/src/admin/users/UsersTable.test.tsx \
                   web/src/admin/enrollments/EnrollmentsTable.test.tsx \
                   web/src/admin/reservations/ReservationsTable.test.tsx
```

Expected: **no output**.

- [ ] **Step 3: Build and discard dist churn**

```bash
cd web && npm run build
cd .. && git checkout -- web/dist/ && git status --short
```

Expected: only the three `.tsx` component files.

- [ ] **Step 4: Commit**

```bash
git add web/src/admin/users/UsersTable.tsx \
        web/src/admin/enrollments/EnrollmentsTable.tsx \
        web/src/admin/reservations/ReservationsTable.tsx
git commit -m "refactor(web): migrate the three admin tables onto the Table primitive

Deletes six duplicated ariaSort/caret definitions and ReservationsTable's local
SortHeader, which was the same abstraction reached for one file wide. Behavior
preserving: all three test files are unchanged (zero-line git diff --stat)."
```

---

# Slice 4: TasksTable + WorkspacesPanel + backlog close

The two special cases: the row-as-button with `aria-selected` and rest-prop passthrough, and the last un-roled table.

### Task 4.1: Migrate `TasksTable`

**Files:**
- Modify: `web/src/jobs/TasksTable.tsx:1-70`
- Test (MUST NOT CHANGE): `web/src/jobs/TasksTable.test.tsx`

- [ ] **Step 1: Confirm green before you touch anything**

```bash
cd web && npx vitest run src/jobs/TasksTable.test.tsx src/jobs/JobDetailPage.test.tsx
```

Expected: PASS. `JobDetailPage.test.tsx` matters here: lines 154-158 and 232-235 index `getAllByRole('row')` on this table.

- [ ] **Step 2: Rewrite the component**

Replace the whole of `web/src/jobs/TasksTable.tsx` with:

```tsx
import { GlassPanel, Table, TableCell, TableRow, type TableColumn } from '../components/holo'
import type { TaskDetail } from './api'
import { taskStatusColor } from './taskStatus'

const COLS = 'grid-cols-[1fr_110px_80px_120px_1fr]'

const HEADERS: TableColumn[] = [
  { label: 'NAME' },
  { label: 'STATUS' },
  { label: 'RETRY' },
  { label: 'WORKER' },
  { label: 'DEPS' },
]

// Tasks table. Rows are SELECTION controls, not navigation: clicking a row sets
// the selected task that drives the Spec/Log panes. Uses aria-selected on each
// row (role=row inside role=table). No per-task duration/percent column: the API
// returns neither per-task timing nor a percent
// (docs/backlog/feature-2026-07-01-per-task-timing.md). The worker cell stays
// plain text; a link to the worker is a deferred follow-up.
export function TasksTable({
  tasks,
  selectedTaskId,
  onSelect,
}: {
  tasks: TaskDetail[]
  selectedTaskId: string
  onSelect: (id: string) => void
}) {
  if (tasks.length === 0) {
    return <GlassPanel className="p-4 text-[12px] text-fg-mute">No tasks.</GlassPanel>
  }
  return (
    <GlassPanel as="div">
      <Table label="Tasks" columns={COLS} headers={HEADERS} headerClassName="px-4 py-2 tracking-wider">
        {tasks.map((t) => {
          const c = taskStatusColor(t.status)
          const selected = t.id === selectedTaskId
          return (
            <TableRow
              key={t.id}
              as="button"
              type="button"
              aria-selected={selected}
              onClick={() => onSelect(t.id)}
              className={`w-full border-b border-border/40 px-4 py-2 text-left font-mono text-[11.5px] ${
                selected ? 'border-l-2 border-accent bg-accent/[0.08]' : ''
              }`}
            >
              <TableCell className="truncate font-sans text-[13px] text-fg">{t.name}</TableCell>
              <TableCell className={`flex items-center gap-2 ${c.text}`}>
                <span className={`h-1.5 w-1.5 rounded-full ${c.dot}`} />
                {t.status}
              </TableCell>
              <TableCell className="text-fg-mute">
                {t.retry_count}/{t.retries}
              </TableCell>
              <TableCell className="truncate text-fg-mute">{t.worker_id ? t.worker_id.slice(0, 6) : '-'}</TableCell>
              <TableCell className="truncate text-fg-mute">
                {t.depends_on && t.depends_on.length > 0 ? t.depends_on.join(', ') : '-'}
              </TableCell>
            </TableRow>
          )
        })}
      </Table>
    </GlassPanel>
  )
}
```

`aria-selected` is preserved exactly as-is. It is inert under `role="table"` (assistive technology only honors it in `grid`/`treegrid`), which is a pre-existing finding recorded in the spec's Follow-ups and deliberately NOT fixed here: the refactor half must be behavior-preserving.

- [ ] **Step 3: Run the tests, unedited**

```bash
cd web && npx vitest run src/jobs/TasksTable.test.tsx src/jobs/JobDetailPage.test.tsx
```

Expected: PASS both files.

- [ ] **Step 4: Zero-line diff gate**

```bash
git diff --stat -- web/src/jobs/TasksTable.test.tsx
```

Expected: **no output**.

---

### Task 4.2: RED test for `WorkspacesPanel`

**Files:**
- Modify: `web/src/workers/WorkspacesPanel.test.tsx` (append only)

- [ ] **Step 1: Write the failing test**

Append to the end of `web/src/workers/WorkspacesPanel.test.tsx`:

```tsx
test('exposes table, row, columnheader, and cell roles', async () => {
  server.use(
    http.get('/v1/workers/w1/workspaces', () =>
      HttpResponse.json([
        { source_type: 'perforce', source_key: '//depot/x', short_id: 'ws-a4f2', baseline_hash: '@1', last_used_at: '2026-06-05T00:00:00Z' },
        { source_type: 'perforce', source_key: '//depot/y', short_id: 'ws-b7c9', baseline_hash: '@2', last_used_at: '2026-06-05T00:00:00Z' },
      ]),
    ),
  )
  renderWithQuery(<WorkspacesPanel workerId="w1" />)
  expect(await screen.findByText('ws-b7c9')).toBeInTheDocument()
  // The accessible name matches the visible title on the page Panel that wraps this.
  expect(screen.getByRole('table', { name: 'Source workspaces' })).toBeInTheDocument()
  // 1 header row + 2 data rows.
  expect(screen.getAllByRole('row')).toHaveLength(3)
  // SHORT ID, TYPE, SOURCE KEY, BASELINE, LAST USED, ACTIONS.
  expect(screen.getAllByRole('columnheader')).toHaveLength(6)
  // 6 columns x 2 rows.
  expect(screen.getAllByRole('cell')).toHaveLength(12)
})
```

- [ ] **Step 2: Run it against the unmigrated component and RECORD the failure**

```bash
cd web && npx vitest run src/workers/WorkspacesPanel.test.tsx
```

Expected: FAIL with `Unable to find an accessible element with the role "table"`, after `findByText('ws-b7c9')` has already resolved (so you know the fixture rendered and the failure is about semantics, not data). Copy the failure block into the PR description under "RED proof: WorkspacesPanel".

---

### Task 4.3: Migrate `WorkspacesPanel`

**Files:**
- Modify: `web/src/workers/WorkspacesPanel.tsx:1-75`

- [ ] **Step 1: Rewrite the component**

Replace the whole of `web/src/workers/WorkspacesPanel.tsx` with:

```tsx
import { useState } from 'react'
import { ConfirmDialog } from '../components/ConfirmDialog'
import { Chip, Table, TableCell, TableRow, type TableColumn } from '../components/holo'
import { formatRelativeTime } from './liveness'
import { useWorkerActions } from './useWorkerActions'
import { useWorkerWorkspaces } from './useWorkerWorkspaces'

const COLS = 'grid-cols-[120px_90px_1fr_120px_90px_90px]'

const HEADERS: TableColumn[] = [
  { label: 'SHORT ID' },
  { label: 'TYPE' },
  { label: 'SOURCE KEY' },
  { label: 'BASELINE' },
  { label: 'LAST USED' },
  { label: 'ACTIONS', align: 'right' },
]

// Admin-only source workspaces table with per-row evict. Rendered inside the
// page's Panel (which supplies the glass frame and the "Source workspaces"
// title), so this component is only the header row + data rows + confirm flow.
// Mounted by WorkerDetailPage only for admins, so no inner is_admin check is
// needed. Eviction is best-effort/async (202): the row does not vanish
// immediately; the 15s workspace poll reconciles once the agent confirms.
export function WorkspacesPanel({ workerId }: { workerId: string }) {
  const { data, isLoading } = useWorkerWorkspaces(workerId)
  const { evict } = useWorkerActions(workerId)
  const [confirmId, setConfirmId] = useState<string | null>(null)
  const rows = data ?? []

  function runEvict() {
    if (confirmId) evict.mutate(confirmId)
    setConfirmId(null)
  }

  return (
    <div className="flex flex-col">
      {/* aria-label matches the visible title on the page Panel that wraps this. */}
      <Table label="Source workspaces" columns={COLS} headers={HEADERS} headerClassName="px-4 py-2 tracking-wider">
        {rows.map((ws) => (
          <TableRow key={ws.short_id} className="border-b border-border/40 px-4 py-2 font-mono text-[11px]">
            <TableCell className="text-fg">{ws.short_id}</TableCell>
            <TableCell className="text-fg-mute">{ws.source_type}</TableCell>
            <TableCell className="truncate text-fg-mute">{ws.source_key}</TableCell>
            <TableCell className="text-fg-mute">{ws.baseline_hash}</TableCell>
            <TableCell className="text-fg-mute">{formatRelativeTime(ws.last_used_at)}</TableCell>
            <TableCell className="flex justify-end">
              <Chip tone="accent" onClick={evict.isPending ? undefined : () => setConfirmId(ws.short_id)}>
                Evict
              </Chip>
            </TableCell>
          </TableRow>
        ))}
      </Table>

      {/* The empty state, the error banner and the dialog are siblings of the table,
          never children: none of them is a valid child of role="table". The empty
          state only renders when there are no rows, so it still appears directly
          below the header row. */}
      {!isLoading && rows.length === 0 && (
        <div className="px-4 py-3 text-[12px] text-fg-mute">No workspaces.</div>
      )}

      {evict.error ? (
        <div className="mx-4 my-2 rounded-card border border-err/40 bg-err/10 px-4 py-2 text-[12px] text-err">
          {(evict.error as Error).message}
        </div>
      ) : null}

      {confirmId && (
        <ConfirmDialog
          title={`Evict workspace ${confirmId}?`}
          body="The agent removes it on next opportunity. A held workspace is refused."
          confirmLabel="Evict"
          onConfirm={runEvict}
          onCancel={() => setConfirmId(null)}
        />
      )}
    </div>
  )
}
```

- [ ] **Step 2: Run the test file to verify it now passes**

```bash
cd web && npx vitest run src/workers/WorkspacesPanel.test.tsx
```

Expected: PASS, 6 tests (5 existing + 1 new).

---

### Task 4.4: Narrow the two over-broad assertions in `WorkerDetailPage.test.tsx`

This is the sixth test file, outside the zero-line-diff gate, and the edit is expected. See "Corrections and decisions" item 1.

**Files:**
- Modify: `web/src/workers/WorkerDetailPage.test.tsx:118-126`

- [ ] **Step 1: Observe and record the failure the migration causes**

```bash
cd web && npx vitest run src/workers/WorkerDetailPage.test.tsx
```

Expected: FAIL on `the reservations panel contains no fabricated reservation rows`, with a message like `expected null not to be in the document` inverted - concretely, `queryByRole('table')` now returns the Source workspaces table. Paste this into the PR description under "Expected fallout: WorkerDetailPage".

The test at lines 109-116 (`the current-tasks panel contains no fabricated task rows`) renders as a non-admin, which does not mount `WorkspacesPanel`, so it must still pass. If it fails, stop: something is mounting the panel for non-admins.

- [ ] **Step 2: Narrow the assertions**

Replace lines 118-126 of `web/src/workers/WorkerDetailPage.test.tsx` with:

```tsx
test('the reservations panel contains no fabricated reservation rows', async () => {
  server.use(http.get(`/v1/workers/${ID}`, () => HttpResponse.json(WORKER)))
  server.use(http.get(`/v1/workers/${ID}/metrics`, () => HttpResponse.json(metrics())))
  server.use(http.get(`/v1/workers/${ID}/workspaces`, () => HttpResponse.json([])))
  renderDetail(true)
  expect(await screen.findByText('no per-worker reservation lookup yet')).toBeInTheDocument()
  // Scoped, not page-global: the admin Source workspaces table is a real table and
  // with no workspaces it contributes exactly its header row. A fabricated
  // reservations table would show up here as a second table or as extra rows.
  const tables = screen.getAllByRole('table')
  expect(tables).toHaveLength(1)
  expect(tables[0]).toHaveAccessibleName('Source workspaces')
  expect(screen.getAllByRole('row')).toHaveLength(1)
})
```

- [ ] **Step 3: Run it to verify it passes**

```bash
cd web && npx vitest run src/workers/WorkerDetailPage.test.tsx
```

Expected: PASS.

---

### Task 4.5: Acceptance sweeps

- [ ] **Step 1: One helper definition, one file**

```bash
rg 'function ariaSort|function caret' web/src
```

Expected: exactly one match, `web/src/components/holo/Table.tsx:...:export function ariaSort(...)`.

- [ ] **Step 2: Zero hand-written role attributes outside the primitive**

```bash
rg 'role="table"|role="row"|role="columnheader"|role="cell"|role="rowgroup"|role="grid"' web/src
```

Expected: matches only in `web/src/components/holo/Table.tsx` (4 attributes: `table`, `row` on the header, `columnheader` twice, `cell`, `row` on TableRow). Zero in any consumer. This replaces the 70 hand-written attributes counted on 2026-08-09.

- [ ] **Step 3: No grid template applied to two elements**

```bash
rg 'grid-cols-\[' web/src
```

Expected: 13 hits in 12 files, same as the pre-work baseline, but every table file now has its literal exactly once (in its `COLS` const) and it no longer carries the `grid ` prefix. The five non-table hits are unchanged: `WorkersGrid.tsx`, the two `repeat(auto-fill,...)` card grids in `WorkersPage.tsx` and `WorkerDetailPage.tsx`, `LogView.tsx:54`, and `JobsTable.tsx`'s nested progress-bar grid.

- [ ] **Step 4: No non-row content inside a table subtree**

Read the three migrated files and confirm by eye: in `JobsTable.tsx` and `SchedulesTable.tsx` the `{footer && ...}` block is after `</Table>`; in `WorkspacesPanel.tsx` the "No workspaces." block, the error banner and the `ConfirmDialog` are all after `</Table>`.

- [ ] **Step 5: All five protected test files still at zero-line diff**

```bash
git diff --stat -- web/src/workers/WorkersTable.test.tsx \
                   web/src/admin/users/UsersTable.test.tsx \
                   web/src/admin/enrollments/EnrollmentsTable.test.tsx \
                   web/src/admin/reservations/ReservationsTable.test.tsx \
                   web/src/jobs/TasksTable.test.tsx
```

Run this against the merge base of the whole branch (`git diff --stat origin/main -- <the five paths>`) as well, to prove no earlier slice touched them. Expected: **no output** from both.

---

### Task 4.6: Slice 4 gate, backlog close, commit

- [ ] **Step 1: Full web suite**

```bash
cd web && npm test
```

Expected: PASS. Test count = slice-3 count + 1, and = baseline + 15 overall.

- [ ] **Step 2: Build and discard dist churn**

```bash
cd web && npm run build
cd .. && git checkout -- web/dist/ && git status --short
```

Expected: only `web/src/jobs/TasksTable.tsx`, `web/src/workers/WorkspacesPanel.tsx`, `web/src/workers/WorkspacesPanel.test.tsx`, `web/src/workers/WorkerDetailPage.test.tsx`.

- [ ] **Step 3: Commit the code**

```bash
git add web/src/jobs/TasksTable.tsx web/src/workers/WorkspacesPanel.tsx \
        web/src/workers/WorkspacesPanel.test.tsx web/src/workers/WorkerDetailPage.test.tsx
git commit -m "feat(web): migrate TasksTable and WorkspacesPanel; all eight tables share the primitive

TasksTable exercises the row-as-button path (as=button plus rest-prop
passthrough for type/aria-selected/onClick) with its test file unchanged.
WorkspacesPanel gains table semantics, RED-proven first; its empty state, error
banner and ConfirmDialog move outside the role=table subtree.

WorkerDetailPage.test.tsx's two page-global 'no rows anywhere' assertions are
narrowed to the reservations placeholder: they only passed before because
WorkspacesPanel had no roles at all, which is exactly the defect this fixes."
```

- [ ] **Step 4: Close the backlog item**

Run the slash command (it does the `git mv` into `docs/backlog/closed/`, stamps the frontmatter, appends a Resolution note, and commits):

```
/backlog close shared-accessible-table-primitive
```

Do NOT hand-edit `status:` in `docs/backlog/idea-2026-06-05-shared-accessible-table-primitive.md`. Flipping the field alone leaves the file in the open directory and `/backlog list` reports it as malformed.

- [ ] **Step 5: Final tree check**

```bash
git status --short
git log --oneline origin/main..HEAD
```

Expected: a clean tree and five commits (four slices plus the backlog close).

---

## PR description checklist

The PR must carry, because they are acceptance criteria and not optional narrative:

- [ ] The three recorded RED failures (JobsTable, SchedulesTable, WorkspacesPanel), pasted verbatim.
- [ ] The recorded `WorkerDetailPage.test.tsx` failure plus the one-paragraph justification for narrowing it.
- [ ] The `git diff --stat` output (empty) for the five protected test files.
- [ ] The outputs of the two `rg` sweeps in Task 4.5.
- [ ] Baseline and final web test counts, with the `+15` delta.
- [ ] The list of requirements each later table placed on the API, so a reviewer can check slice 1 was shaped on real evidence: row-as-button with rest props (`TasksTable`), `data-testid` passthrough (`JobsTable`), no-sort tables (`JobsTable`, `SchedulesTable`, `TasksTable`, `WorkspacesPanel`), no-frame (`WorkspacesPanel`), right-aligned header (`UsersTable`, `EnrollmentsTable`, `ReservationsTable`, `SchedulesTable`, `WorkspacesPanel`), dimming class on the row element itself (`UsersTable`, `ReservationsTable`, `SchedulesTable`).
- [ ] Confirmation that no `web/dist` file is in the diff.

## Non-goals restated (do not let scope creep in)

- No virtualization or memoization.
- No keyboard navigation model, roving tabindex, or `role="grid"` upgrade - which means `TasksTable`'s inert `aria-selected` stays inert.
- No visual harmonization of frames, padding or tracking; no adopting `GlassPanel` in `WorkersTable`.
- No column resizing, reordering, persistence, or client-side sorting.
- No `aria-rowcount` / `aria-colcount` (cursor pagination means the total is often unknown, and a wrong count is worse than none).
- No `role="rowgroup"`.
- No changes to sort wire formats or to any API.
