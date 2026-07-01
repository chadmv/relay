import { render, screen } from '@testing-library/react'
import { expect, test } from 'vitest'
import { TaskDag } from './TaskDag'
import type { TaskDetail } from './api'

function task(name: string, deps: string[] = [], status: TaskDetail['status'] = 'pending'): TaskDetail {
  return {
    id: name, name, status, commands: [], env: {}, requires: {},
    timeout_seconds: null, retries: 0, retry_count: 0,
    depends_on: deps.length ? deps : undefined,
  }
}

test('renders an accessible image labelled with node and edge counts', () => {
  render(<TaskDag tasks={[task('a'), task('b', ['a']), task('c', ['a'])]} />)
  const img = screen.getByRole('img', { name: /task dependency graph/i })
  expect(img).toBeInTheDocument()
  expect(img.getAttribute('aria-label')).toMatch(/3 tasks/)
  expect(img.getAttribute('aria-label')).toMatch(/2 dependency edges/)
})

test('renders each task name as a node label', () => {
  render(<TaskDag tasks={[task('frame-001'), task('denoise', ['frame-001'])]} />)
  expect(screen.getByText('frame-001')).toBeInTheDocument()
  expect(screen.getByText('denoise')).toBeInTheDocument()
})

test('renders an empty-state note when there are no tasks', () => {
  render(<TaskDag tasks={[]} />)
  expect(screen.getByText(/no tasks/i)).toBeInTheDocument()
})
