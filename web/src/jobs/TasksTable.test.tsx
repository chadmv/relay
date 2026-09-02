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

test("the selected task's control is marked aria-current and no row carries aria-selected", () => {
  const { container } = render(<TasksTable tasks={tasks} selectedTaskId="t2" onSelect={() => {}} />)
  // aria-selected is not surfaced under role="table": it advertised a selection
  // model the container does not have. aria-current is valid on any element and is
  // not conditional on the container role.
  const current = container.querySelectorAll('[aria-current="true"]')
  expect(current).toHaveLength(1)
  expect(current[0]).toHaveAccessibleName('denoise')
  expect(container.querySelectorAll('[aria-selected]')).toHaveLength(0)
})

test('the name-cell button carries a negative-offset focus ring, not the browser default', () => {
  render(<TasksTable tasks={tasks} selectedTaskId="t1" onSelect={() => {}} />)
  const button = screen.getByRole('button', { name: 'frame-001' })
  // The button fills its TableCell exactly (w-full) and both carry `truncate`
  // (overflow: hidden), so a ring drawn OUTSIDE the border box is clipped by the
  // ancestor to zero visible pixels. A negative outline-offset draws it INSIDE
  // instead, which that clip cannot reach - proved in a real browser (not jsdom,
  // which does no layout) by the job-detail keyboard describe in
  // web/e2e/keyboard.spec.ts, reading getComputedStyle on the focused element.
  expect(button).toHaveClass('focus-visible:outline-offset-[-2px]')
})

test('each task row exposes a button named for the task, and one activation selects once', async () => {
  const onSelect = vi.fn()
  render(<TasksTable tasks={tasks} selectedTaskId="t1" onSelect={onSelect} />)
  expect(screen.getByRole('button', { name: 'frame-001' })).toBeInTheDocument()
  const denoise = screen.getByRole('button', { name: 'denoise' })
  await userEvent.click(denoise)
  // ONE handler. The row owns onClick; the button owns none, and the button's click
  // bubbles to it. Giving the button its own handler makes this two.
  expect(onSelect).toHaveBeenCalledTimes(1)
  expect(onSelect).toHaveBeenCalledWith('t2')
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
