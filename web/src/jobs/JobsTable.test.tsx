import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { expect, test } from 'vitest'
import { JobsTable } from './JobsTable'
import type { Job } from './api'

const jobs: Job[] = [
  {
    id: '9F4E1C', name: 'film-x / shot-042 render', priority: 'high', status: 'running',
    submitted_by_email: 'mira@studio.dev', labels: null,
    created_at: '2026-06-05T14:22:00Z', updated_at: '2026-06-05T14:30:00Z',
    total_tasks: 64, done_tasks: 48, started_at: '2026-06-05T14:22:00Z',
    scheduled_job_name: 'nightly-etl',
  },
  {
    id: 'C41A02', name: 'ci build', priority: 'low', status: 'done',
    submitted_by_email: 'ci@studio.dev', labels: null,
    created_at: '2026-06-05T14:30:00Z', updated_at: '2026-06-05T14:34:00Z',
    total_tasks: 12, done_tasks: 12,
  },
]

function renderTable(rows: Job[]) {
  return render(
    <MemoryRouter>
      <JobsTable jobs={rows} />
    </MemoryRouter>,
  )
}

test('renders job rows with name, owner, and progress percent', () => {
  renderTable(jobs)
  expect(screen.getByText('film-x / shot-042 render')).toBeInTheDocument()
  expect(screen.getByText('mira@studio.dev')).toBeInTheDocument()
  expect(screen.getByText('75%')).toBeInTheDocument()
  expect(screen.getByText('100%')).toBeInTheDocument()
})

test('renders the schedule chip only when scheduled_job_name is present', () => {
  renderTable(jobs)
  expect(screen.getByText(/nightly-etl/)).toBeInTheDocument()
})

test('renders the empty state when there are no jobs', () => {
  renderTable([])
  expect(screen.getByText(/no jobs/i)).toBeInTheDocument()
})

test('the job name links to the job detail page', () => {
  renderTable(jobs)
  const link = screen.getByRole('link', { name: 'film-x / shot-042 render' })
  expect(link).toHaveAttribute('href', '/jobs/9F4E1C')
})

test('wraps the table in a GlassPanel surface', () => {
  renderTable(jobs)
  // The GlassPanel base classes carry the gradient glass fidelity upgrade.
  const surface = screen.getByTestId('jobs-table')
  expect(surface).toHaveClass('rounded-card', 'border', 'border-border', 'backdrop-blur-[8px]')
})

test('tints the running row with a subtle accent background', () => {
  renderTable(jobs)
  // film-x / shot-042 render is status:running; ci build is status:done.
  const runningRow = screen.getByTestId('job-row-9F4E1C')
  const doneRow = screen.getByTestId('job-row-C41A02')
  expect(runningRow).toHaveClass('bg-accent/[0.04]')
  expect(doneRow).not.toHaveClass('bg-accent/[0.04]')
})

test('renders a footer slot inside the table surface when provided', () => {
  render(
    <MemoryRouter>
      <JobsTable jobs={jobs} footer={<span>FOOTER-MARKER</span>} />
    </MemoryRouter>,
  )
  const surface = screen.getByTestId('jobs-table')
  const footer = screen.getByText('FOOTER-MARKER')
  expect(surface).toContainElement(footer)
})
