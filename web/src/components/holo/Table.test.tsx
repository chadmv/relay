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
  expect(sortCaret('name', 'name')).toBe(' ▲')
  expect(sortCaret('name', '-name')).toBe(' ▼')
  expect(sortCaret('name', 'created_at')).toBe('')
  expect(sortCaret('name', '')).toBe('')
})

test('ariaSort and sortCaret anchor the descending prefix, not any interior hyphen', () => {
  // Field names are generic (F extends string), so a field containing a hyphen is
  // representable even though none of today's eight consumers use one. An
  // unanchored `sort.replace('-', '')` strips the FIRST hyphen anywhere in the
  // string, which for an ascending sort on a hyphenated field removes the field's
  // own interior hyphen and makes the comparison fail.
  expect(ariaSort('a-b', 'a-b')).toBe('ascending')
  expect(ariaSort('a-b', '-a-b')).toBe('descending')
  expect(sortCaret('a-b', 'a-b')).toBe(' ▲')
  expect(sortCaret('a-b', '-a-b')).toBe(' ▼')
})

// PLACEHOLDER_MIN_W: minWidth is a required prop, so every render in this file
// must pass one; tests below this point that are not exercising minWidth's own
// behaviour use this dummy value, which is intentionally too narrow to be
// mistaken for a real consumer's sizing.
const PLACEHOLDER_MIN_W = 'min-w-[1px]'

test('renders a table role whose accessible name is the label', () => {
  render(<Table label="Widgets" columns="grid-cols-[1fr]" minWidth={PLACEHOLDER_MIN_W} headers={[{ label: 'A' }]} />)
  expect(screen.getByRole('table', { name: 'Widgets' })).toBeInTheDocument()
})

test('renders a header row with one columnheader per configured column', () => {
  render(
    <Table
      label="W"
      columns="grid-cols-[1fr_1fr_1fr]"
      minWidth={PLACEHOLDER_MIN_W}
      headers={[{ label: 'A' }, { label: 'B' }, { label: 'C' }]}
    />,
  )
  expect(screen.getAllByRole('row')).toHaveLength(1)
  expect(screen.getAllByRole('columnheader')).toHaveLength(3)
})

test('applies the grid template to the header row and to every TableRow', () => {
  render(
    <Table label="W" columns="grid-cols-[1fr_80px]" minWidth={PLACEHOLDER_MIN_W} headers={[{ label: 'A' }, { label: 'B' }]}>
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
    <Table
      label="W"
      columns="grid-cols-[1fr_1fr_1fr]"
      minWidth={PLACEHOLDER_MIN_W}
      headers={headers}
      sort="-created_at"
      onSort={() => {}}
    />,
  )
  expect(screen.getByRole('columnheader', { name: /^CREATED/ })).toHaveAttribute('aria-sort', 'descending')
  expect(screen.getByRole('columnheader', { name: /^NAME/ })).toHaveAttribute('aria-sort', 'none')
  // A static header must NOT advertise a sort affordance it does not have.
  expect(screen.getByRole('columnheader', { name: 'STATIC' })).not.toHaveAttribute('aria-sort')

  rerender(
    <Table
      label="W"
      columns="grid-cols-[1fr_1fr_1fr]"
      minWidth={PLACEHOLDER_MIN_W}
      headers={headers}
      sort="created_at"
      onSort={() => {}}
    />,
  )
  expect(screen.getByRole('columnheader', { name: /^CREATED/ })).toHaveAttribute('aria-sort', 'ascending')
})

test("the sort caret is hidden from the header's accessible name", () => {
  // The glyph announces as "black down-pointing triangle" and is not a name. Direction
  // travels on aria-sort, which `emits aria-sort only on sortable headers` pins.
  // Both queries are EXACT: with the glyph still in the name they resolve nothing.
  const headers = [{ label: 'NAME', field: 'name' as const }]
  render(
    <Table label="W" columns="grid-cols-[1fr]" minWidth={PLACEHOLDER_MIN_W} headers={headers} sort="-name" onSort={() => {}} />,
  )
  expect(screen.getByRole('button', { name: 'NAME' })).toBeInTheDocument()
  expect(screen.getByRole('columnheader', { name: 'NAME' })).toBeInTheDocument()
})

test('the caret is still rendered for sighted users', () => {
  // The partner of the test above, and neither alone distinguishes "hidden from the
  // name" from "deleted". Located by position, NOT by accessible name, so this test
  // stays red only for a missing caret and cannot also fail for a missing aria-hidden.
  // Escapes, not raw characters: a raw non-ASCII literal in source is unverifiable by
  // eye and survives every check this repo runs.
  const headers = [{ label: 'NAME', field: 'name' as const }]
  const { rerender } = render(
    <Table label="W" columns="grid-cols-[1fr]" minWidth={PLACEHOLDER_MIN_W} headers={headers} sort="-name" onSort={() => {}} />,
  )
  expect(screen.getAllByRole('columnheader')[0].textContent).toContain('\u25BC')
  rerender(
    <Table label="W" columns="grid-cols-[1fr]" minWidth={PLACEHOLDER_MIN_W} headers={headers} sort="name" onSort={() => {}} />,
  )
  expect(screen.getAllByRole('columnheader')[0].textContent).toContain('\u25B2')
})

test('clicking a sortable header calls onSort with that column field', async () => {
  const onSort = vi.fn()
  render(
    <Table
      label="W"
      columns="grid-cols-[1fr_1fr]"
      minWidth={PLACEHOLDER_MIN_W}
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

test('a right-aligned header carries text-right, and a plain one carries no class attribute', () => {
  render(
    <Table
      label="W"
      columns="grid-cols-[1fr_1fr]"
      minWidth={PLACEHOLDER_MIN_W}
      headers={[{ label: 'A' }, { label: 'ACT', align: 'right' }]}
    />,
  )
  expect(screen.getByRole('columnheader', { name: 'ACT' })).toHaveClass('text-right')
  expect(screen.getByRole('columnheader', { name: 'A' })).not.toHaveAttribute('class')
})

test('TableRow always renders a div and cannot have its role overridden', async () => {
  const onClick = vi.fn()
  render(
    <Table label="W" columns="grid-cols-[1fr]" minWidth={PLACEHOLDER_MIN_W} headers={[{ label: 'A' }]}>
      <TableRow data-testid="row-div" onClick={onClick}>
        <TableCell>x</TableCell>
      </TableRow>
    </Table>,
  )
  const row = screen.getByTestId('row-div')
  expect(row.tagName).toBe('DIV')
  expect(row).toHaveAttribute('role', 'row')
  await userEvent.click(row)
  expect(onClick).toHaveBeenCalledTimes(1)
})

// A TYPE-LEVEL PIN, checked by `tsc -b` and not by vitest. TableRow no longer
// accepts an element-type escape hatch: `as="button"` produced a
// <button role="row">, the arrangement that made aria-selected look supported on a
// table row. Restore the prop and this directive has no error to suppress, so tsc
// fails it as unused - the pin goes red exactly when the guard is lost. Precedent:
// the SortFieldOf pin in web/src/lib/toggleSort.test.ts.
// @ts-expect-error `as` is not a prop of TableRow
void (<TableRow as="button">x</TableRow>)

test('TableCell exposes role=cell and merges its className', () => {
  render(
    <Table label="W" columns="grid-cols-[1fr]" minWidth={PLACEHOLDER_MIN_W} headers={[{ label: 'A' }]}>
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

test('a caller-passed role does not override the role the primitive owns', () => {
  // Typed as a loose bag so the spread compiles even though TableRowProps and
  // TableCellProps no longer include `role` in their type: the hazard this guards
  // is a `role` slipping through at runtime (a JS caller, a stale .d.ts, a cast),
  // not just what today's TypeScript call sites can express.
  const rowProps = { role: 'presentation' } as Record<string, string>
  const cellProps = { role: 'rowheader' } as Record<string, string>
  render(
    <Table label="W" columns="grid-cols-[1fr]" minWidth={PLACEHOLDER_MIN_W} headers={[{ label: 'A' }]}>
      <TableRow data-testid="row" {...rowProps}>
        <TableCell data-testid="cell" {...cellProps}>
          x
        </TableCell>
      </TableRow>
    </Table>,
  )
  expect(screen.getByTestId('row')).toHaveAttribute('role', 'row')
  expect(screen.getByTestId('cell')).toHaveAttribute('role', 'cell')
})

test('a sortable header without an onSort handler throws instead of rendering a dead affordance', () => {
  // Without this guard, a header with `field` set but no `onSort` passed to Table
  // renders a focusable, screen-reader-announced sort button that does nothing
  // when activated - it type-checks and looks correct but is silently broken.
  const spy = vi.spyOn(console, 'error').mockImplementation(() => {})
  expect(() =>
    render(
      <Table
        label="W"
        columns="grid-cols-[1fr]"
        minWidth={PLACEHOLDER_MIN_W}
        headers={[{ label: 'NAME', field: 'name' as const }]}
      />,
    ),
  ).toThrow(/onSort/)
  spy.mockRestore()
})

test('does not warn about duplicate React keys when two headers share a label', () => {
  // headers is a static config array that is never reordered, so keying by index
  // is safe; keying by h.label is not, because duplicate labels are
  // representable (e.g. two headers legitimately both named the same thing) and
  // would collide as React keys.
  const spy = vi.spyOn(console, 'error').mockImplementation(() => {})
  render(
    <Table
      label="W"
      columns="grid-cols-[1fr_1fr]"
      minWidth={PLACEHOLDER_MIN_W}
      headers={[{ label: 'DUP' }, { label: 'DUP' }]}
    />,
  )
  const dupKeyWarning = spy.mock.calls.some((args) => String(args[0]).includes('same key'))
  expect(dupKeyWarning).toBe(false)
  spy.mockRestore()
})

test('TableRow rendered outside a Table throws, naming both components', () => {
  // React logs the render error to console.error before rethrowing; silence it so
  // the deliberate failure does not read as a broken suite.
  const spy = vi.spyOn(console, 'error').mockImplementation(() => {})
  expect(() => render(<TableRow>orphan</TableRow>)).toThrow(/TableRow must be rendered inside a Table/)
  spy.mockRestore()
})

// Cause 2 of docs/backlog/bug-2026-08-12-web-narrow-viewport-horizontal-overflow.md.
// Every consumer's template has fixed px tracks that sum past a narrow viewport
// (SchedulesTable's nine columns total 580px of fixed track before any `fr` gets a
// pixel), and nothing wrapped them in a scroll region.
//
// The min-width is NOT decoration and it is not a substitute for the wrapper. With
// negative free space an `fr` track falls back to its CONTENT minimum, and the
// header row and the body rows are SEPARATE grid containers whose content minimums
// differ ("NAME" versus a truncating link, whose min-content is 0) - so the columns
// visibly desynchronize. A shared min-width keeps free space non-negative, at which
// point `fr` resolves identically in both. That agreement is the property this
// primitive exists to own, which is why minWidth travels on the same context string
// as columns rather than being applied by hand in each consumer.
test('minWidth lands on the header row and on every body row as ONE identical class string', () => {
  render(
    <Table label="W" columns="grid-cols-[1fr_80px]" minWidth="min-w-[640px]" headers={[{ label: 'A' }, { label: 'B' }]}>
      <TableRow data-testid="r1">
        <TableCell>x</TableCell>
        <TableCell>y</TableCell>
      </TableRow>
    </Table>,
  )
  const header = screen.getAllByRole('row')[0]
  const row = screen.getByTestId('r1')
  expect(header).toHaveClass('grid', 'grid-cols-[1fr_80px]', 'min-w-[640px]')
  expect(row).toHaveClass('grid', 'grid-cols-[1fr_80px]', 'min-w-[640px]', 'items-center')
  // The load-bearing assertion: not "both have a min-width" but "both have the
  // SAME one". A per-element implementation can satisfy the two lines above and
  // still put the two grids out of agreement.
  const gridOf = (el: HTMLElement) =>
    el.className.split(/\s+/).filter((c) => c.startsWith('grid-cols-') || c.startsWith('min-w-')).sort().join(' ')
  expect(gridOf(row)).toBe(gridOf(header))
})

test('minWidth wraps the table subtree in a scroll container, and nothing else moves', () => {
  render(
    <div data-testid="frame">
      <Table label="W" columns="grid-cols-[1fr_80px]" minWidth="min-w-[640px]" headers={[{ label: 'A' }]} />
      <div data-testid="footer">page 1 of 3</div>
    </div>,
  )
  const table = screen.getByRole('table', { name: 'W' })
  expect(table.parentElement).toHaveClass('overflow-x-auto')
  // The scroll container wraps the role="table" subtree ONLY. Footers, error
  // banners and dialogs are siblings of <Table> in every consumer, and they must
  // stay outside the scroll region or a paginator would scroll away with the rows.
  expect(screen.getByTestId('footer').parentElement).toBe(screen.getByTestId('frame'))
  expect(screen.getByTestId('footer').closest('.overflow-x-auto')).toBeNull()
  // And the wrapper is not a frame: overflow only, no border/background/padding,
  // per the no-frame contract in Table.tsx's header comment.
  expect(table.parentElement?.className).toBe('overflow-x-auto')
})

// "Without minWidth the DOM is byte-identical" was deleted here: minWidth is now
// a required prop (TableProps), so omitting it is a configuration no production
// code (or valid test code) can express any more - `tsc -b` rejects the call site
// itself, which is a stronger guarantee than a runtime DOM assertion ever gave.
// See the deleted `every Table call site opts in to a scroll min-width` test in
// responsive.guard.test.ts, which this replaces.

// EnrollmentsTable and InvitesTable have ZERO focusable elements in any row - no
// links, no buttons - so their clipped right-hand columns were reachable only via
// the scroll wrapper's own implicit scroller focusability, which Chromium grants
// and Safari does not, and which was never exercised by a real Tab press in any
// environment this slice's verification could reach. It is also an axe
// scrollable-region-focusable violation as shipped: a scrollable region with no
// other means of keyboard reaching its overflowing content needs its own tab
// stop. tabIndex={0} plus role="group" (a scroll region is not a landmark, but it
// needs SOME accessible-name-bearing role for the aria-label to attach to) fixes
// both. The label is derived from the existing `label` prop, not a second
// constant, so it can never drift from the table's own accessible name.
test('the scroll wrapper is a keyboard-reachable, labelled group', () => {
  render(<Table label="Widgets" columns="grid-cols-[1fr_80px]" minWidth="min-w-[640px]" headers={[{ label: 'A' }]} />)
  const table = screen.getByRole('table', { name: 'Widgets' })
  const wrapper = table.parentElement as HTMLElement
  // The DOM attribute is lowercase ("tabindex") regardless of the JSX prop's
  // casing - React reflects tabIndex onto the standard HTML attribute name.
  expect(wrapper).toHaveAttribute('tabindex', '0')
  expect(wrapper).toHaveAttribute('role', 'group')
  // Derived, not duplicated: a caller who renames the table via `label` cannot
  // leave this one stale by forgetting a second place to update it.
  expect(wrapper).toHaveAccessibleName(expect.stringContaining('Widgets'))
})
