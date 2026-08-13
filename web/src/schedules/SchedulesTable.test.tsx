import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { render } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { expect, test, vi } from 'vitest'
import { SchedulesTable } from './SchedulesTable'
import type { Schedule } from './api'

function renderTable(ui: React.ReactElement) {
  return render(<MemoryRouter>{ui}</MemoryRouter>)
}

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
  renderTable(<SchedulesTable schedules={[sched()]} pendingId={null} onRunNow={() => {}} onToggleEnabled={() => {}} />)
  expect(screen.getByText('nightly-build')).toBeInTheDocument()
  expect(screen.getByText('0 2 * * *')).toBeInTheDocument()
  expect(screen.getByText('dev@studio.com')).toBeInTheDocument()
  expect(screen.getByText('abcdef12')).toBeInTheDocument() // short last_job_id
})

test('enabled row shows Run now + Disable', () => {
  renderTable(<SchedulesTable schedules={[sched({ enabled: true })]} pendingId={null} onRunNow={() => {}} onToggleEnabled={() => {}} />)
  expect(screen.getByRole('button', { name: 'Run now' })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Disable' })).toBeInTheDocument()
})

test('disabled row shows Run now + Enable', () => {
  renderTable(<SchedulesTable schedules={[sched({ enabled: false })]} pendingId={null} onRunNow={() => {}} onToggleEnabled={() => {}} />)
  expect(screen.getByRole('button', { name: 'Run now' })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Enable' })).toBeInTheDocument()
})

test('clicking Run now and Disable fires callbacks with the id and next-enabled', async () => {
  const onRunNow = vi.fn()
  const onToggleEnabled = vi.fn()
  renderTable(<SchedulesTable schedules={[sched({ enabled: true })]} pendingId={null} onRunNow={onRunNow} onToggleEnabled={onToggleEnabled} />)
  await userEvent.click(screen.getByRole('button', { name: 'Run now' }))
  await userEvent.click(screen.getByRole('button', { name: 'Disable' }))
  expect(onRunNow).toHaveBeenCalledWith('s1')
  expect(onToggleEnabled).toHaveBeenCalledWith('s1', false)
})

test('pending row disables its action buttons', () => {
  renderTable(<SchedulesTable schedules={[sched()]} pendingId={'s1'} onRunNow={() => {}} onToggleEnabled={() => {}} />)
  expect(screen.getByRole('button', { name: 'Run now' })).toBeDisabled()
  expect(screen.getByRole('button', { name: 'Disable' })).toBeDisabled()
})

test('missing last_job_id renders a dash', () => {
  renderTable(<SchedulesTable schedules={[sched({ last_job_id: undefined })]} pendingId={null} onRunNow={() => {}} onToggleEnabled={() => {}} />)
  // last run cell and last job cell both could be '-'; assert the LAST JOB short id is absent
  expect(screen.queryByText('abcdef12')).not.toBeInTheDocument()
})

test('wraps the table in a GlassPanel surface', () => {
  renderTable(<SchedulesTable schedules={[sched()]} pendingId={null} onRunNow={() => {}} onToggleEnabled={() => {}} />)
  // The GlassPanel base classes carry the gradient glass fidelity upgrade.
  const surface = screen.getByTestId('schedules-table')
  expect(surface).toHaveClass('rounded-card', 'border', 'border-border', 'backdrop-blur-[8px]')
})

test('renders a footer slot inside the table surface when rows are present', () => {
  renderTable(
    <SchedulesTable
      schedules={[sched()]}
      pendingId={null}
      onRunNow={() => {}}
      onToggleEnabled={() => {}}
      footer={<span>FOOTER-MARKER</span>}
    />,
  )
  const surface = screen.getByTestId('schedules-table')
  const footer = screen.getByText('FOOTER-MARKER')
  expect(surface).toContainElement(footer)
})

test('renders the empty state and still shows the footer slot when there are no rows', () => {
  renderTable(
    <SchedulesTable
      schedules={[]}
      pendingId={null}
      onRunNow={() => {}}
      onToggleEnabled={() => {}}
      footer={<span>FOOTER-MARKER</span>}
    />,
  )
  expect(screen.getByText('No schedules yet.')).toBeInTheDocument()
  expect(screen.getByText('FOOTER-MARKER')).toBeInTheDocument()
})

test('exposes table, row, columnheader, and cell roles', () => {
  renderTable(
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
  // Structural, not just a page-global count: pins the first data row's cells to
  // that row specifically, which a cell rendered outside the role="table" subtree
  // would not satisfy even if the page-global counts above still summed correctly.
  const firstDataRow = screen.getAllByRole('row')[1]
  expect(within(firstDataRow).getAllByRole('cell')).toHaveLength(9)
})

test('the NAME cell links to the schedule detail page', () => {
  renderTable(
    <SchedulesTable schedules={[sched()]} pendingId={null} onRunNow={() => {}} onToggleEnabled={() => {}} />,
  )
  expect(screen.getByRole('link', { name: 'nightly-build' })).toHaveAttribute('href', '/schedules/s1')
})

test('ACTIONS carries an Edit link to the same place, named by row', () => {
  renderTable(
    <SchedulesTable schedules={[sched()]} pendingId={null} onRunNow={() => {}} onToggleEnabled={() => {}} />,
  )
  // Row identity in the accessible name, matching UsersTable.tsx:169-199, so a
  // multi-row table does not present several identically-named controls.
  const edit = screen.getByRole('link', { name: 'Edit nightly-build' })
  expect(edit).toHaveAttribute('href', '/schedules/s1')
  expect(edit).toHaveTextContent('Edit')
})

test('both entry points target the same href and there are exactly two per row', () => {
  renderTable(
    <SchedulesTable
      schedules={[sched(), sched({ id: 's2', name: 'weekly-clean' })]}
      pendingId={null}
      onRunNow={() => {}}
      onToggleEnabled={() => {}}
    />,
  )
  const toS1 = screen.getAllByRole('link').filter((a) => a.getAttribute('href') === '/schedules/s1')
  expect(toS1).toHaveLength(2)
  expect(screen.getByRole('link', { name: 'weekly-clean' })).toHaveAttribute('href', '/schedules/s2')
})

test('clicking Edit does NOT fire Run now or the enable toggle', async () => {
  const onRunNow = vi.fn()
  const onToggleEnabled = vi.fn()
  renderTable(
    <SchedulesTable
      schedules={[sched()]}
      pendingId={null}
      onRunNow={onRunNow}
      onToggleEnabled={onToggleEnabled}
    />,
  )
  await userEvent.click(screen.getByRole('link', { name: 'Edit nightly-build' }))
  expect(onRunNow).not.toHaveBeenCalled()
  expect(onToggleEnabled).not.toHaveBeenCalled()
})

test('Edit is a link, not a button, so middle-click and open-in-new-tab work', () => {
  renderTable(
    <SchedulesTable schedules={[sched()]} pendingId={null} onRunNow={() => {}} onToggleEnabled={() => {}} />,
  )
  // A useNavigate handler on a <button> would satisfy a naive "clicking Edit goes to
  // the page" test while silently breaking both affordances.
  expect(screen.queryByRole('button', { name: /^Edit/ })).toBeNull()
})

test('the ACTIONS cell still holds exactly nine cells per row after the Edit link', () => {
  renderTable(
    <SchedulesTable
      schedules={[sched(), sched({ id: 's2', name: 'weekly-clean' })]}
      pendingId={null}
      onRunNow={() => {}}
      onToggleEnabled={() => {}}
    />,
  )
  // The Edit link joins the existing ACTIONS cell; it must not become a tenth column,
  // which would desynchronise the grid template from the header row.
  expect(screen.getAllByRole('columnheader')).toHaveLength(9)
  const firstDataRow = screen.getAllByRole('row')[1]
  expect(within(firstDataRow).getAllByRole('cell')).toHaveLength(9)
})
