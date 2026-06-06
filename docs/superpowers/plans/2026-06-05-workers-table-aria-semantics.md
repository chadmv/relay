# Workers Table ARIA Semantics Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give the workers pseudo-table real ARIA table semantics so `aria-sort` lands on `columnheader` elements and screen readers read the grid as a table.

**Architecture:** Layer ARIA roles (`table`/`row`/`columnheader`/`cell`) onto the existing CSS-grid `<div>` markup in `WorkersTable.tsx`. The Tailwind/`grid-cols` layout is untouched (zero visual change). The `aria-sort` attribute moves from the sortable `<button>`s onto new `<div role="columnheader">` wrappers; the buttons stay inside as the operable sort controls.

**Tech Stack:** React 18 + TypeScript, Vitest + @testing-library/react + @testing-library/user-event. All commands run from the `web/` directory.

**Spec:** [docs/superpowers/specs/2026-06-05-workers-table-aria-semantics-design.md](../specs/2026-06-05-workers-table-aria-semantics-design.md)

---

## Background for the implementer

`web/src/workers/WorkersTable.tsx` renders a list of workers as a CSS-grid pseudo-table, NOT a real HTML `<table>`. Structure today:

- An outer container `<div>` (card styling).
- A header-row `<div>` with the `COLS` grid class. Its direct children are the column headers: three sortable `<button>`s (NAME, STATUS, LAST SEEN) each carrying `aria-sort`, and three plain `<span>`s (SLOTS, SPEC, LABELS).
- One data-row `<div>` per worker (also `COLS` grid), whose direct children are `<span>`s for each cell.

The problem: `aria-sort` is only formally exposed by assistive tech when it sits on an element with role `columnheader`/`rowheader`. On a plain `<button>` it may be a no-op. We fix this by adding ARIA roles and relocating `aria-sort` to `columnheader` wrappers.

Do NOT change: the component's props (`workers`, `sort`, `onSort`), the `COLS` constant, or the `caret()` / `ariaSort()` helper functions.

---

## Task 1: Update tests to assert table semantics (TDD - tests first)

**Files:**
- Test: `web/src/workers/WorkersTable.test.tsx`

The existing file has four tests. Two of them assert `aria-sort` on `button` elements; those assertions must move to `columnheader`. We also add one new test for the overall table structure. The onSort-click test and the caret test stay exactly as they are.

- [ ] **Step 1: Update the two `aria-sort` assertions and add a structure test**

Replace the test named `'exposes aria-sort on the active sortable header and "none" on the rest'` and the test named `'reports ascending aria-sort when the active sort is ascending'` so they query `columnheader` instead of `button`, and add a new structure test after them. The full replacement for those tests (lines 28-38 of the current file) is:

```tsx
test('exposes aria-sort on the active sortable header and "none" on the rest', () => {
  render(<WorkersTable workers={[worker({})]} sort="-last_seen_at" onSort={() => {}} />)
  expect(screen.getByRole('columnheader', { name: /last seen/i })).toHaveAttribute('aria-sort', 'descending')
  expect(screen.getByRole('columnheader', { name: /name/i })).toHaveAttribute('aria-sort', 'none')
  expect(screen.getByRole('columnheader', { name: /status/i })).toHaveAttribute('aria-sort', 'none')
})

test('reports ascending aria-sort when the active sort is ascending', () => {
  render(<WorkersTable workers={[worker({})]} sort="name" onSort={() => {}} />)
  expect(screen.getByRole('columnheader', { name: /name/i })).toHaveAttribute('aria-sort', 'ascending')
})

test('exposes table, row, columnheader, and cell roles', () => {
  render(<WorkersTable workers={[worker({})]} sort="-created_at" onSort={() => {}} />)
  expect(screen.getByRole('table', { name: 'Workers' })).toBeInTheDocument()
  // 1 header row + 1 data row
  expect(screen.getAllByRole('row')).toHaveLength(2)
  // NAME, STATUS, SLOTS, SPEC, LABELS, LAST SEEN
  expect(screen.getAllByRole('columnheader')).toHaveLength(6)
  // one per column in the single data row
  expect(screen.getAllByRole('cell')).toHaveLength(6)
})
```

Leave the first two tests (`'renders a row and calls onSort...'` and `'shows a descending caret...'`) unchanged - they correctly query `button`.

- [ ] **Step 2: Run the tests to verify the new expectations fail**

Run: `npm test -- src/workers/WorkersTable.test.tsx`
Expected: FAIL. The `columnheader`/`table`/`cell` role queries fail with "Unable to find an accessible element with the role ..." because the markup has no roles yet. The two unchanged `button` tests still pass.

- [ ] **Step 3: Commit the failing tests**

```bash
git add web/src/workers/WorkersTable.test.tsx
git commit -m "test(web): assert ARIA table semantics on WorkersTable"
```

---

## Task 2: Add ARIA roles to the markup

**Files:**
- Modify: `web/src/workers/WorkersTable.tsx`

- [ ] **Step 1: Replace the `WorkersTable` component body with the role-annotated markup**

Replace the entire `return (...)` block of the `WorkersTable` function (lines 28-69 of the current file) with:

```tsx
  return (
    <div role="table" aria-label="Workers" className="rounded-card border border-border bg-white/5 backdrop-blur">
      <div role="row" className={`${COLS} border-b border-border px-4 py-3 font-mono text-[10px] tracking-wider text-fg-mute`}>
        <div role="columnheader" aria-sort={ariaSort('name', sort)}>
          <button type="button" className="text-left" onClick={() => onSort('name')}>
            NAME{caret('name', sort)}
          </button>
        </div>
        <div role="columnheader" aria-sort={ariaSort('status', sort)}>
          <button type="button" className="text-left" onClick={() => onSort('status')}>
            STATUS{caret('status', sort)}
          </button>
        </div>
        <span role="columnheader">SLOTS</span>
        <span role="columnheader">SPEC</span>
        <span role="columnheader">LABELS</span>
        <div role="columnheader" aria-sort={ariaSort('last_seen_at', sort)}>
          <button type="button" className="text-left" onClick={() => onSort('last_seen_at')}>
            LAST SEEN{caret('last_seen_at', sort)}
          </button>
        </div>
      </div>
      {workers.map((w) => (
        <div
          key={w.id}
          role="row"
          className={`${COLS} items-center border-b border-border/40 px-4 py-2 font-mono text-[11.5px] ${livenessView(w.status).dimClass}`}
        >
          <span role="cell" className="text-fg">{w.name}</span>
          <span role="cell"><StatusDot status={w.status} /></span>
          <span role="cell" className="text-fg-mute">{w.max_slots}</span>
          <span role="cell" className="text-[10.5px] text-fg-mute">{specLine(w)}</span>
          <span role="cell" className="flex flex-wrap gap-1">
            {labelChips(w.labels).map((c) => (
              <span
                key={c}
                className="rounded-full border border-accent/40 bg-accent/10 px-1.5 py-0.5 text-[9.5px] text-accent"
              >
                {c}
              </span>
            ))}
          </span>
          <span role="cell" className="text-fg-mute">
            {w.last_seen_at ? formatRelativeTime(w.last_seen_at) : '-'}
          </span>
        </div>
      ))}
    </div>
  )
```

Notes:
- The `aria-sort` attribute is now on the `<div role="columnheader">` wrappers, not the buttons.
- The sortable `<button>`s keep `type="button"`, `className="text-left"`, and their `onClick` handlers, so click/keyboard sorting is unchanged.
- The chip `<span>`s inside the LABELS cell are decorative content and intentionally get no role.
- Everything above the `return` (imports, `COLS`, `caret`, `ariaSort`, the function signature) is unchanged.

- [ ] **Step 2: Run the WorkersTable tests to verify they pass**

Run: `npm test -- src/workers/WorkersTable.test.tsx`
Expected: PASS, all tests in the file (the four original + the new structure test) green.

- [ ] **Step 3: Run the type check / build to confirm no TS errors**

Run: `npx tsc -b`
Expected: completes with no errors.

- [ ] **Step 4: Commit**

```bash
git add web/src/workers/WorkersTable.tsx
git commit -m "fix(web): expose ARIA table semantics so aria-sort is announced on WorkersTable"
```

---

## Task 3: Run the full web suite and close the backlog item

**Files:**
- Move: `docs/backlog/idea-2026-06-05-workers-table-aria-semantics.md` -> `docs/backlog/closed/`

- [ ] **Step 1: Run the entire web test suite**

Run: `npm test`
Expected: PASS, all test files green (confirms no regression in `WorkersPage.test.tsx` or elsewhere).

- [ ] **Step 2: Close the backlog item**

Update the front-matter `status: open` -> `status: closed` and add a `closed: 2026-06-05` line in `docs/backlog/idea-2026-06-05-workers-table-aria-semantics.md`, then move it:

```bash
git mv docs/backlog/idea-2026-06-05-workers-table-aria-semantics.md docs/backlog/closed/
```

- [ ] **Step 3: Commit**

```bash
git add docs/backlog/
git commit -m "chore(backlog): close workers-table-aria-semantics"
```

---

## Self-review notes

- **Spec coverage:** Every "Done When" row in the spec maps to a task - columnheader+aria-sort (Task 2), table/row/cell roles (Task 2), keyboard-operable buttons retained (Task 2), tests updated and passing (Tasks 1-3).
- **No placeholders:** all code blocks are complete and copy-paste ready.
- **Type consistency:** no new types or signatures introduced; `caret()`/`ariaSort()`/`COLS` and the component props are reused unchanged.
