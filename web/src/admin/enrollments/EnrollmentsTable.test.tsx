import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, test, vi } from 'vitest'
import { EnrollmentsTable } from './EnrollmentsTable'
import type { AgentEnrollment, EnrollmentSort } from './api'

const NOW = new Date('2026-08-09T12:00:00Z')

function row(over: Partial<AgentEnrollment> = {}): AgentEnrollment {
  return {
    id: 'e1',
    created_at: '2026-08-09T09:30:00Z',
    expires_at: '2026-08-10T09:42:00Z',
    created_by: '11111111-2222-3333-4444-555555555555',
    hostname_hint: 'farm-west-13',
    ...over,
  }
}

function renderTable(over: Partial<Parameters<typeof EnrollmentsTable>[0]> = {}) {
  const props = {
    enrollments: [row()],
    sort: '-created_at' as EnrollmentSort,
    onSort: vi.fn(),
    now: NOW,
    ...over,
  }
  return { props, ...render(<EnrollmentsTable {...props} />) }
}

test('renders hostname hint, created date, relative expiry, and the note', () => {
  renderTable()
  expect(screen.getByText('farm-west-13')).toBeInTheDocument()
  expect(screen.getByText('2026-08-09')).toBeInTheDocument()
  expect(screen.getByText('in 21h')).toBeInTheDocument()
  expect(screen.getByText(/consumed on first agent connect/i)).toBeInTheDocument()
})

test('a row whose hostname_hint key is ABSENT renders a plain hyphen, not undefined', () => {
  const { created_at, expires_at, created_by, id } = row()
  renderTable({ enrollments: [{ id, created_at, expires_at, created_by }] })
  expect(screen.getByText('-')).toBeInTheDocument()
  expect(screen.queryByText(/undefined/)).not.toBeInTheDocument()
  expect(screen.queryByText(/null/)).not.toBeInTheDocument()
  // House rule: an ASCII hyphen, never the em dash the hi-fi uses.
  expect(screen.queryByText('—')).not.toBeInTheDocument()
})

test('the status pill renders exactly the three derivable states', () => {
  renderTable({
    enrollments: [
      row({ id: 'a', expires_at: '2026-08-10T12:00:00Z' }), // 24h left
      row({ id: 'b', expires_at: '2026-08-09T12:30:00Z' }), // 30m left
      row({ id: 'c', expires_at: '2026-08-09T11:00:00Z' }), // past
    ],
  })
  expect(screen.getByText('ACTIVE')).toBeInTheDocument()
  expect(screen.getByText('EXPIRING')).toBeInTheDocument()
  expect(screen.getByText('EXPIRED')).toBeInTheDocument()
  // Consumption is unobservable through this endpoint, so there is no such state.
  expect(screen.queryByText('CONSUMED')).not.toBeInTheDocument()
})

test('renders no TOKEN PREFIX and no CREATED BY column, and never the raw creator UUID', () => {
  renderTable()
  expect(screen.queryByText('TOKEN PREFIX')).not.toBeInTheDocument()
  expect(screen.queryByText('CREATED BY')).not.toBeInTheDocument()
  expect(screen.queryByText('11111111-2222-3333-4444-555555555555')).not.toBeInTheDocument()
})

test('the last column is headed NOTE, and no row carries any control', () => {
  renderTable({ enrollments: [row(), row({ id: 'e2' })] })
  expect(screen.getByText('NOTE')).toBeInTheDocument()
  expect(screen.queryByText('ACTIONS')).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: /revoke|delete|remove/i })).not.toBeInTheDocument()
  // The only buttons in the table are the two sortable headers - two rows of data
  // add none. There is no DELETE /v1/agent-enrollments/{id} to serve one.
  expect(screen.getAllByRole('button')).toHaveLength(2)
})

test('clicking a sortable header calls onSort with that field', async () => {
  const { props } = renderTable()
  await userEvent.click(screen.getByRole('button', { name: /^CREATED/ }))
  expect(props.onSort).toHaveBeenCalledWith('created_at')
  await userEvent.click(screen.getByRole('button', { name: /^EXPIRES/ }))
  expect(props.onSort).toHaveBeenCalledWith('expires_at')
})

test('aria-sort marks the active column and the button is named by its label alone', () => {
  renderTable({ sort: '-created_at' })
  expect(screen.getByRole('columnheader', { name: /CREATED/ })).toHaveAttribute('aria-sort', 'descending')
  expect(screen.getByRole('columnheader', { name: /EXPIRES/ })).toHaveAttribute('aria-sort', 'none')
  expect(screen.getByRole('button', { name: 'CREATED' })).toBeInTheDocument()
})

test('an ascending sort leaves the button named by its label alone', () => {
  renderTable({ sort: 'expires_at' })
  expect(screen.getByRole('columnheader', { name: /EXPIRES/ })).toHaveAttribute('aria-sort', 'ascending')
  expect(screen.getByRole('button', { name: 'EXPIRES' })).toBeInTheDocument()
})

test('a different now re-derives the pill and the label from the same row', () => {
  const later = new Date('2026-08-10T09:41:30Z') // 30s before expiry
  renderTable({ now: later })
  expect(screen.getByText('EXPIRING')).toBeInTheDocument()
  // Not "in 30s": the table's `now` comes from a 60s clock tick (useNow), so a
  // seconds-precision label would go stale for up to 59 more real seconds after
  // the render that produced it - see enrollmentStatus.formatExpiryLabel.
  expect(screen.getByText('in <1m')).toBeInTheDocument()
})
