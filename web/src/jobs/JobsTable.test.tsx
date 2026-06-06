import { render, screen } from '@testing-library/react'
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

test('renders job rows with name, owner, and progress percent', () => {
  render(<JobsTable jobs={jobs} />)
  expect(screen.getByText('film-x / shot-042 render')).toBeInTheDocument()
  expect(screen.getByText('mira@studio.dev')).toBeInTheDocument()
  expect(screen.getByText('75%')).toBeInTheDocument()
  expect(screen.getByText('100%')).toBeInTheDocument()
})

test('renders the schedule chip only when scheduled_job_name is present', () => {
  render(<JobsTable jobs={jobs} />)
  expect(screen.getByText(/nightly-etl/)).toBeInTheDocument()
})

test('renders the empty state when there are no jobs', () => {
  render(<JobsTable jobs={[]} />)
  expect(screen.getByText(/no jobs/i)).toBeInTheDocument()
})
