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
  expect(screen.getByRole('button', { name: 'NAME ▼' })).toBeInTheDocument()
  rerender(<Table label="W" columns="grid-cols-[1fr]" headers={headers} sort="name" onSort={() => {}} />)
  expect(screen.getByRole('button', { name: 'NAME ▲' })).toBeInTheDocument()
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

test('a right-aligned header carries text-right, and a plain one carries no class attribute', () => {
  render(
    <Table
      label="W"
      columns="grid-cols-[1fr_1fr]"
      headers={[{ label: 'A' }, { label: 'ACT', align: 'right' }]}
    />,
  )
  expect(screen.getByRole('columnheader', { name: 'ACT' })).toHaveClass('text-right')
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

test('a caller-passed role does not override the role the primitive owns', () => {
  // Typed as a loose bag so the spread compiles even though TableRowProps and
  // TableCellProps no longer include `role` in their type: the hazard this guards
  // is a `role` slipping through at runtime (a JS caller, a stale .d.ts, a cast),
  // not just what today's TypeScript call sites can express.
  const rowProps = { role: 'presentation' } as Record<string, string>
  const cellProps = { role: 'rowheader' } as Record<string, string>
  render(
    <Table label="W" columns="grid-cols-[1fr]" headers={[{ label: 'A' }]}>
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
    render(<Table label="W" columns="grid-cols-[1fr]" headers={[{ label: 'NAME', field: 'name' as const }]} />),
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
    <Table label="W" columns="grid-cols-[1fr_1fr]" headers={[{ label: 'DUP' }, { label: 'DUP' }]} />,
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
