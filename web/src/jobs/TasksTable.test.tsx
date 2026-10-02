import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { ReactElement } from 'react'
import { MemoryRouter } from 'react-router-dom'
import { expect, test, vi } from 'vitest'
import { TasksTable } from './TasksTable'
import type { TaskDetail } from './api'

function renderTable(ui: ReactElement) {
  return render(<MemoryRouter>{ui}</MemoryRouter>)
}

function task(over: Partial<TaskDetail>): TaskDetail {
  return {
    id: 't1', name: 'frame-001', status: 'done', commands: [], env: {}, requires: {},
    timeout_seconds: null, retries: 2, retry_count: 0, ...over,
  }
}

const tasks: TaskDetail[] = [
  task({ id: 't1', name: 'frame-001', status: 'done' }),
  task({ id: 't2', name: 'denoise', status: 'running', depends_on: ['frame-001'], worker_id: 'w9abc123', worker_name: 'render-node-07' }),
]

test('renders each task name and status', () => {
  renderTable(<TasksTable tasks={tasks} selectedTaskId="t1" onSelect={() => {}} />)
  // 'frame-001' appears twice: as the first row's name cell and as the second
  // row's deps cell (denoise depends_on ['frame-001']).
  expect(screen.getAllByText('frame-001')).toHaveLength(2)
  expect(screen.getByText('denoise')).toBeInTheDocument()
  expect(screen.getByText('running')).toBeInTheDocument()
})

test("the selected task's control is marked aria-current and no row carries aria-selected", () => {
  const { container } = renderTable(<TasksTable tasks={tasks} selectedTaskId="t2" onSelect={() => {}} />)
  // See TasksTable.tsx for why: no aria-selected, no interactive row.
  // aria-current is valid on any element and is not conditional on the
  // container role.
  const current = container.querySelectorAll('[aria-current="true"]')
  expect(current).toHaveLength(1)
  expect(current[0]).toHaveAccessibleName('denoise')
  expect(container.querySelectorAll('[aria-selected]')).toHaveLength(0)
})

test('the name-cell button carries a negative-offset focus ring, not the browser default', () => {
  renderTable(<TasksTable tasks={tasks} selectedTaskId="t1" onSelect={() => {}} />)
  const button = screen.getByRole('button', { name: 'frame-001' })
  // The button fills its TableCell exactly (w-full) and both carry `truncate`
  // (overflow: hidden), so a ring drawn OUTSIDE the border box is clipped by the
  // ancestor to zero visible pixels. A negative outline offset draws it INSIDE
  // instead, which that clip cannot reach - proved in a real browser (not jsdom,
  // which does no layout) by the job-detail keyboard describe in
  // web/e2e/keyboard.spec.ts, reading getComputedStyle on the focused element.
  //
  // The value is interpolated INSIDE the brackets rather than spelled as a
  // literal class-shaped substring: Tailwind v4 scans this file too, and a
  // literal match here would keep the CSS rule alive even if the component
  // stopped emitting the class.
  const OFFSET = '-2px'
  expect(button).toHaveClass(`focus-visible:outline-offset-[${OFFSET}]`)
})

test('each task row exposes a button named for the task, and one activation selects once', async () => {
  const onSelect = vi.fn()
  renderTable(<TasksTable tasks={tasks} selectedTaskId="t1" onSelect={onSelect} />)
  expect(screen.getByRole('button', { name: 'frame-001' })).toBeInTheDocument()
  const denoise = screen.getByRole('button', { name: 'denoise' })
  await userEvent.click(denoise)
  // ONE handler. The row owns onClick; the button owns none, and the button's click
  // bubbles to it. Giving the button its own handler makes this two.
  expect(onSelect).toHaveBeenCalledTimes(1)
  expect(onSelect).toHaveBeenCalledWith('t2')
})

test('clicking a non-button cell in a row calls onSelect with its id (row-level handler, not just the button)', async () => {
  const onSelect = vi.fn()
  renderTable(<TasksTable tasks={tasks} selectedTaskId="t1" onSelect={onSelect} />)
  // 'running' is denoise's STATUS cell, a plain text node with no button
  // ancestor - unlike 'denoise' itself, which resolves inside the name-cell
  // button and so cannot tell a row handler apart from a button-only one.
  await userEvent.click(screen.getByText('running'))
  expect(onSelect).toHaveBeenCalledWith('t2')
  // Task selection is a click or key action, never navigation: the only link in
  // the table is the worker link, which leaves for the worker page.
  const links = screen.getAllByRole('link')
  expect(links).toHaveLength(1)
  expect(links[0]).toHaveAttribute('href', '/workers/w9abc123')
})

test('the worker cell shows the worker name as a link to the worker page', () => {
  renderTable(<TasksTable tasks={tasks} selectedTaskId="t1" onSelect={() => {}} />)
  expect(screen.getByRole('link', { name: 'render-node-07' })).toHaveAttribute('href', '/workers/w9abc123')
  expect(screen.queryByText('w9abc1')).not.toBeInTheDocument()
})

test('following the worker link does not select the row', async () => {
  const onSelect = vi.fn()
  renderTable(<TasksTable tasks={tasks} selectedTaskId="t1" onSelect={onSelect} />)
  await userEvent.click(screen.getByRole('link', { name: 'render-node-07' }))
  expect(onSelect).not.toHaveBeenCalled()
})

test('shows an empty state when there are no tasks', () => {
  renderTable(<TasksTable tasks={[]} selectedTaskId="" onSelect={() => {}} />)
  expect(screen.getByText(/no tasks/i)).toBeInTheDocument()
})
