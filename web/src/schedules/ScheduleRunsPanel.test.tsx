import { render, screen, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { expect, test } from 'vitest'
import { ScheduleRunsPanel } from './ScheduleRunsPanel'
import type { Job } from '../jobs/api'

function job(over: Partial<Job> = {}): Job {
  return {
    id: 'aaaaaaaa-1111-2222-3333-444444444444',
    name: 'nightly-build',
    priority: 'normal',
    status: 'done',
    labels: null,
    created_at: '2026-06-05T02:00:00Z',
    updated_at: '2026-06-05T02:04:00Z',
    started_at: '2026-06-05T02:00:00Z',
    finished_at: '2026-06-05T02:04:00Z',
    submitted_by_email: 'dev@studio.com',
    ...over,
  }
}

// The panel renders react-router Links, so a router is required.
function renderPanel(runs: Job[], total: number) {
  return render(
    <MemoryRouter>
      <ScheduleRunsPanel runs={runs} total={total} />
    </MemoryRouter>,
  )
}

test('renders the five columns and one row per run', () => {
  renderPanel([job(), job({ id: 'bbbbbbbb-1111-2222-3333-444444444444', status: 'failed' })], 2)
  const table = screen.getByRole('table', { name: 'Recent runs' })
  const headers = within(table).getAllByRole('columnheader').map((h) => h.textContent)
  expect(headers).toEqual(['STARTED', 'DUR', 'STATUS', 'JOB ID', 'OWNER'])
  // 1 header row + 2 data rows.
  expect(within(table).getAllByRole('row')).toHaveLength(3)
  expect(within(table).getAllByRole('cell')).toHaveLength(10)
})

test('the job id links to the job detail page', () => {
  renderPanel([job()], 1)
  const link = screen.getByRole('link', { name: 'aaaaaaaa' })
  expect(link).toHaveAttribute('href', '/jobs/aaaaaaaa-1111-2222-3333-444444444444')
})

test('a run that never started renders hyphens, not blanks or NaN', () => {
  // started_at / finished_at KEYS ARE ABSENT when the job has no started or finished
  // task (applyJobEnrichment, internal/api/jobs.go:119-137), so this is the real
  // wire shape for a pending run, not a contrived one.
  renderPanel([job({ started_at: undefined, finished_at: undefined, status: 'pending' })], 1)
  const table = screen.getByRole('table', { name: 'Recent runs' })
  const cells = within(table).getAllByRole('cell').map((c) => c.textContent)
  expect(cells[0]).toBe('-')
  expect(cells[1]).toBe('-')
  expect(cells.join(' ')).not.toMatch(/NaN|Invalid/)
})

test('a run with no submitter email renders a hyphen', () => {
  renderPanel([job({ submitted_by_email: undefined })], 1)
  const table = screen.getByRole('table', { name: 'Recent runs' })
  const cells = within(table).getAllByRole('cell')
  expect(cells[4]).toHaveTextContent('-')
})

test('the footer states the window honestly as latest N of total', () => {
  renderPanel([job(), job({ id: 'bbbbbbbb-1111-2222-3333-444444444444' })], 37)
  // "latest 2 of 37", not "1-2 of 37": this is a fixed window with no pager, and the
  // footer must not imply one exists.
  expect(screen.getByText('latest 2 of 37')).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: /next|prev/i })).toBeNull()
})

test('the footer is OUTSIDE the role="table" subtree', () => {
  renderPanel([job()], 1)
  const table = screen.getByRole('table', { name: 'Recent runs' })
  // A footer is not a valid child of role="table". Same rule as JobsTable.tsx:77-79.
  expect(table).not.toContainElement(screen.getByText('latest 1 of 1'))
})

test('a schedule that never fired renders an empty state and no table', () => {
  renderPanel([], 0)
  expect(screen.getByText('this schedule has never fired')).toBeInTheDocument()
  expect(screen.queryByRole('table')).toBeNull()
})
