import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, test, vi } from 'vitest'
import { TasksTable } from './TasksTable'
import type { TaskDetail } from './api'

function task(over: Partial<TaskDetail>): TaskDetail {
  return {
    id: 't1', name: 'frame-001', status: 'done', commands: [], env: {}, requires: {},
    timeout_seconds: null, retries: 2, retry_count: 0, ...over,
  }
}

const tasks: TaskDetail[] = [
  task({ id: 't1', name: 'frame-001', status: 'done' }),
  task({ id: 't2', name: 'denoise', status: 'running', depends_on: ['frame-001'], worker_id: 'w9abc123' }),
]

test('renders each task name and status', () => {
  render(<TasksTable tasks={tasks} selectedTaskId="t1" onSelect={() => {}} />)
  // 'frame-001' appears twice: as the first row's name cell and as the second
  // row's deps cell (denoise depends_on ['frame-001']).
  expect(screen.getAllByText('frame-001')).toHaveLength(2)
  expect(screen.getByText('denoise')).toBeInTheDocument()
  expect(screen.getByText('running')).toBeInTheDocument()
})

test('marks the selected row with aria-selected', () => {
  render(<TasksTable tasks={tasks} selectedTaskId="t2" onSelect={() => {}} />)
  const rows = screen.getAllByRole('row')
  const selected = rows.filter((r) => r.getAttribute('aria-selected') === 'true')
  expect(selected).toHaveLength(1)
  expect(selected[0]).toHaveTextContent('denoise')
})

test('clicking a row calls onSelect with its id (selection, not navigation)', async () => {
  const onSelect = vi.fn()
  render(<TasksTable tasks={tasks} selectedTaskId="t1" onSelect={onSelect} />)
  await userEvent.click(screen.getByText('denoise'))
  expect(onSelect).toHaveBeenCalledWith('t2')
  // Rows are buttons/selectable, never anchors.
  expect(screen.queryByRole('link')).not.toBeInTheDocument()
})

test('shows an empty state when there are no tasks', () => {
  render(<TasksTable tasks={[]} selectedTaskId="" onSelect={() => {}} />)
  expect(screen.getByText(/no tasks/i)).toBeInTheDocument()
})
