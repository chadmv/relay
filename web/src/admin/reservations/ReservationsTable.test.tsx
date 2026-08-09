import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { expect, test, vi } from 'vitest'
import { ReservationsTable } from './ReservationsTable'
import type { Reservation } from './api'

const NOW = new Date('2026-08-09T12:00:00Z')
const W1 = 'aaaa1111-1111-1111-1111-111111111111'
const W2 = 'bbbb2222-2222-2222-2222-222222222222'

function row(over: Partial<Reservation> = {}): Reservation {
  return {
    id: 'r1',
    name: 'gpu-farm-hold',
    selector: null,
    worker_ids: [W1],
    user_id: 'u1',
    created_at: '2026-08-09T09:30:00Z',
    ...over,
  }
}

function renderTable(reservations: Reservation[], over: Record<string, unknown> = {}) {
  const onSort = vi.fn()
  const onDelete = vi.fn()
  const view = render(
    <MemoryRouter>
      <ReservationsTable
        reservations={reservations}
        sort="-created_at"
        onSort={onSort}
        now={NOW}
        busy={false}
        onDelete={onDelete}
        {...over}
      />
    </MemoryRouter>,
  )
  return { onSort, onDelete, ...view }
}

test('renders the agreed columns and no owner column', () => {
  renderTable([row()])
  for (const h of ['NAME', 'PROJECT', 'WORKERS', 'STARTS', 'ENDS', 'STATUS', 'CREATED']) {
    expect(screen.getByText(new RegExp(`^${h}`))).toBeInTheDocument()
  }
  // user_id is a bare UUID with no join to `users`, so there is nothing honest to put
  // in an owner column (internal/api/reservations.go:18, :47).
  for (const h of ['OWNER', 'USER', 'RESERVED FOR', 'SELECTOR']) {
    expect(screen.queryByText(h)).not.toBeInTheDocument()
  }
})

test('absent project / starts_at / ends_at render a plain ASCII hyphen, never null or undefined', () => {
  const { container } = renderTable([row()])
  const text = container.textContent ?? ''
  expect(text).not.toMatch(/null/)
  expect(text).not.toMatch(/undefined/)
  expect(text).not.toMatch(/—|–/) // em dash and en dash are banned house-wide
  // Exactly three absent cells on this row.
  expect(screen.getAllByText('-')).toHaveLength(3)
})

test('positive control: present project and window render their values', () => {
  renderTable([
    row({ project: 'atlas', starts_at: '2026-08-09T10:00:00Z', ends_at: '2026-08-11T10:00:00Z' }),
  ])
  expect(screen.getByText('atlas')).toBeInTheDocument()
  // Local rendering, so build the expectation the same way formatDateTime does
  // rather than hardcoding a UTC string.
  const d = new Date('2026-08-09T10:00:00Z')
  const p = (n: number) => String(n).padStart(2, '0')
  const expected = `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`
  expect(screen.getByText(expected)).toBeInTheDocument()
  expect(screen.queryByText('-')).not.toBeInTheDocument()
})

test('the three derived statuses render with the right rows dimmed', () => {
  renderTable([
    row({ id: 'a', name: 'active-now' }),
    row({ id: 's', name: 'later', starts_at: '2026-08-20T00:00:00Z' }),
    row({ id: 'e', name: 'over', ends_at: '2026-08-01T00:00:00Z' }),
  ])
  expect(screen.getByText('ACTIVE')).toBeInTheDocument()
  expect(screen.getByText('SCHEDULED')).toBeInTheDocument()
  expect(screen.getByText('ENDED')).toBeInTheDocument()
  const rows = screen.getAllByRole('row')
  // rows[0] is the header. Only the ENDED row is dimmed.
  expect(rows[3].className).toContain('opacity-[0.55]')
  expect(rows[1].className).not.toContain('opacity-[0.55]')
})

test('worker ids render as truncated chips linking to the worker detail page', () => {
  renderTable([row({ worker_ids: [W1, W2] })])
  const links = screen.getAllByRole('link')
  expect(links).toHaveLength(2)
  expect(links[0]).toHaveAttribute('href', `/workers/${W1}`)
  expect(links[0]).toHaveTextContent(W1.slice(0, 8))
  // Full UUID recoverable without a backend enricher.
  expect(links[0]).toHaveAttribute('title', W1)
})

test('worker_ids: [] renders no chips, says none, and does not crash', () => {
  renderTable([row({ worker_ids: [] })])
  expect(screen.queryAllByRole('link')).toHaveLength(0)
  // 'none' rather than a hyphen: an empty reservation reserves nothing, and the word
  // also keeps the absent-key hyphen count unambiguous.
  expect(screen.getByText('none')).toBeInTheDocument()
})

test('a non-empty selector renders a sel chip; null and {} render none', () => {
  // L7 (review 2026-08-09): unmount between renders in the SAME test. Without it
  // this passed only because the two later renders happen to add zero "sel" chips
  // of their own - not because the test proves isolation between the three roots.
  const withSel = renderTable([row({ id: 'p', name: 'with-sel', selector: { tier: 'gpu', site: 'west' } })])
  const chip = screen.getByText('sel')
  expect(chip).toBeInTheDocument()
  expect(chip).toHaveAttribute('title', 'tier=gpu site=west')
  withSel.unmount()

  const withNull = renderTable([row({ id: 'n', selector: null })])
  expect(screen.queryByText('sel')).not.toBeInTheDocument()
  withNull.unmount()

  renderTable([row({ id: 'z', selector: {} })])
  expect(screen.queryByText('sel')).not.toBeInTheDocument()
})

test('the four sortable headers call onSort with the server field names', async () => {
  const { onSort } = renderTable([row()])
  for (const [label, field] of [
    ['NAME', 'name'],
    ['STARTS', 'starts_at'],
    ['ENDS', 'ends_at'],
    ['CREATED', 'created_at'],
  ] as const) {
    await userEvent.click(screen.getByRole('button', { name: new RegExp(`^${label}`) }))
    expect(onSort).toHaveBeenLastCalledWith(field)
  }
  // PROJECT, WORKERS and STATUS are NOT sortable: the server has no sort arm for
  // them, and a header that silently does nothing is a dead affordance.
  expect(screen.queryByRole('button', { name: /^PROJECT/ })).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: /^STATUS/ })).not.toBeInTheDocument()
})

test('aria-sort and the caret follow the active sort', () => {
  renderTable([row()], { sort: 'name' })
  const headers = screen.getAllByRole('columnheader')
  const nameHeader = headers.find((h) => h.textContent?.startsWith('NAME'))
  expect(nameHeader).toHaveAttribute('aria-sort', 'ascending')
  expect(nameHeader?.textContent).toContain('▲')
  const createdHeader = headers.find((h) => h.textContent?.startsWith('CREATED'))
  expect(createdHeader).toHaveAttribute('aria-sort', 'none')
})

test('every Delete button names its own row, and clicking one passes that row', async () => {
  // A 50-row page of buttons all named "Delete" was a real finding on the Users tab:
  // it makes both the a11y tree and the tests unable to tell rows apart.
  const { onDelete } = renderTable([
    row({ id: 'r1', name: 'gpu-farm-hold' }),
    row({ id: 'r2', name: 'sim-drain' }),
  ])
  const first = screen.getByRole('button', { name: 'Delete reservation gpu-farm-hold' })
  const second = screen.getByRole('button', { name: 'Delete reservation sim-drain' })
  expect(first).not.toBe(second)

  await userEvent.click(second)
  expect(onDelete).toHaveBeenCalledTimes(1)
  expect(onDelete.mock.calls[0][0]).toMatchObject({ id: 'r2', name: 'sim-drain' })
})

test('busy disables every Delete button', () => {
  renderTable([row()], { busy: true })
  expect(screen.getByRole('button', { name: 'Delete reservation gpu-farm-hold' })).toBeDisabled()
})
