import { act, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, test, vi } from 'vitest'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { TaskRow } from './specBuilder'

// Captures the dispatcher SpecBuilderForm hands to every row (one stable
// function, per updateTask's own design) and the task each row currently
// renders. A real click or keystroke cannot reach two dispatches inside one
// batching window today - React flushes each discrete DOM input event
// synchronously before the next one starts, which a fireEvent-per-row
// reproduction confirmed does not exhibit the hazard below. Calling the
// captured dispatcher directly, twice, inside one act() is what puts both
// calls in the same window without going through any DOM event at all.
let capturedOnChange: ((id: string, next: TaskRow) => void) | undefined
let capturedTasks: TaskRow[] = []

vi.mock('./TaskRowFields', async (importOriginal) => {
  const actual = await importOriginal<typeof import('./TaskRowFields')>()
  return {
    ...actual,
    TaskRowFields: vi.fn((props: Parameters<typeof actual.TaskRowFields>[0]) => {
      capturedOnChange = props.onChange
      capturedTasks[props.index] = props.task
      return actual.TaskRowFields(props)
    }),
  }
})

import { NewJobPage } from './NewJobPage'

function renderBuilder() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={['/jobs/new']}>
        <Routes>
          <Route path="/jobs/new" element={<NewJobPage />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

function preview(): unknown {
  return JSON.parse(screen.getByLabelText('Job spec preview').textContent ?? '')
}

test('two dispatches to two different rows in one batching window both survive', async () => {
  renderBuilder()
  await userEvent.click(screen.getByRole('button', { name: 'Add task' }))
  const task1 = capturedTasks[0]
  const task2 = capturedTasks[1]
  expect(capturedOnChange).toBeDefined()
  expect(task1).toBeDefined()
  expect(task2).toBeDefined()

  act(() => {
    capturedOnChange!(task1.id, { ...task1, name: 'A' })
    capturedOnChange!(task2.id, { ...task2, name: 'B' })
  })

  expect(preview()).toHaveProperty('tasks', [
    { name: 'A', command: ['echo', 'hello world'] },
    { name: 'B' },
  ])
})
