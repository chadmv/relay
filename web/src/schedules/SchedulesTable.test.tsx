import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { render } from '@testing-library/react'
import { expect, test, vi } from 'vitest'
import { SchedulesTable } from './SchedulesTable'
import type { Schedule } from './api'

function sched(over: Partial<Schedule> = {}): Schedule {
  return {
    id: 's1',
    name: 'nightly-build',
    owner_id: 'o1',
    owner_email: 'dev@studio.com',
    cron_expr: '0 2 * * *',
    timezone: 'UTC',
    job_spec: {},
    overlap_policy: 'skip',
    enabled: true,
    next_run_at: '2099-01-01T00:00:00Z',
    last_run_at: '2026-06-05T11:00:00Z',
    last_job_id: 'abcdef12-3456-7890-abcd-ef1234567890',
    created_at: '2026-06-01T00:00:00Z',
    updated_at: '2026-06-05T11:00:00Z',
    ...over,
  }
}

test('renders core columns', () => {
  render(<SchedulesTable schedules={[sched()]} pendingId={null} onRunNow={() => {}} onToggleEnabled={() => {}} />)
  expect(screen.getByText('nightly-build')).toBeInTheDocument()
  expect(screen.getByText('0 2 * * *')).toBeInTheDocument()
  expect(screen.getByText('dev@studio.com')).toBeInTheDocument()
  expect(screen.getByText('abcdef12')).toBeInTheDocument() // short last_job_id
})

test('enabled row shows Run now + Disable', () => {
  render(<SchedulesTable schedules={[sched({ enabled: true })]} pendingId={null} onRunNow={() => {}} onToggleEnabled={() => {}} />)
  expect(screen.getByRole('button', { name: 'Run now' })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Disable' })).toBeInTheDocument()
})

test('disabled row shows Run now + Enable', () => {
  render(<SchedulesTable schedules={[sched({ enabled: false })]} pendingId={null} onRunNow={() => {}} onToggleEnabled={() => {}} />)
  expect(screen.getByRole('button', { name: 'Run now' })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Enable' })).toBeInTheDocument()
})

test('clicking Run now and Disable fires callbacks with the id and next-enabled', async () => {
  const onRunNow = vi.fn()
  const onToggleEnabled = vi.fn()
  render(<SchedulesTable schedules={[sched({ enabled: true })]} pendingId={null} onRunNow={onRunNow} onToggleEnabled={onToggleEnabled} />)
  await userEvent.click(screen.getByRole('button', { name: 'Run now' }))
  await userEvent.click(screen.getByRole('button', { name: 'Disable' }))
  expect(onRunNow).toHaveBeenCalledWith('s1')
  expect(onToggleEnabled).toHaveBeenCalledWith('s1', false)
})

test('pending row disables its action buttons', () => {
  render(<SchedulesTable schedules={[sched()]} pendingId={'s1'} onRunNow={() => {}} onToggleEnabled={() => {}} />)
  expect(screen.getByRole('button', { name: 'Run now' })).toBeDisabled()
  expect(screen.getByRole('button', { name: 'Disable' })).toBeDisabled()
})

test('missing last_job_id renders a dash', () => {
  render(<SchedulesTable schedules={[sched({ last_job_id: undefined })]} pendingId={null} onRunNow={() => {}} onToggleEnabled={() => {}} />)
  // last run cell and last job cell both could be '-'; assert the LAST JOB short id is absent
  expect(screen.queryByText('abcdef12')).not.toBeInTheDocument()
})
