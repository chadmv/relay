import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, test, vi } from 'vitest'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

// Spies on the RAW TaskRowFields export while still calling through to the
// real implementation. SpecBuilderForm applies React.memo at its own import
// site (`memo(TaskRowFields)`), and that call happens against WHATEVER this
// mock resolves to - so wrapping the export here, before SpecBuilderForm's
// module runs, is what lets the spy sit inside the memo boundary rather than
// outside it. A spy taken after the fact (e.g. via a plain vi.spyOn once both
// modules have already loaded) would count calls to a reference memo() never
// actually wraps.
vi.mock('./TaskRowFields', async (importOriginal) => {
  const actual = await importOriginal<typeof import('./TaskRowFields')>()
  return { ...actual, TaskRowFields: vi.fn(actual.TaskRowFields) }
})

import { NewJobPage } from './NewJobPage'
import { TaskRowFields } from './TaskRowFields'

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

const renderCalls = vi.mocked(TaskRowFields)

// Populated through the JSON editor and one mode switch, not 49 "Add task"
// clicks: Testing Library's getByRole re-scans the whole tree on every call,
// which against a GROWING 50-row tree is its own O(n^2) cost, unrelated to
// anything under test here. fromSpec models the whole array in one state
// transition instead.
async function populate50Tasks() {
  renderBuilder()
  await userEvent.click(screen.getByRole('button', { name: 'JSON' }))
  const editor = screen.getByRole('textbox', { name: 'Job spec JSON' })
  await userEvent.clear(editor)
  const spec = {
    name: 'perf',
    tasks: Array.from({ length: 50 }, (_, i) => ({ name: `t${i + 1}`, command: ['echo'] })),
  }
  await userEvent.paste(JSON.stringify(spec))
  await userEvent.click(screen.getByRole('button', { name: 'Form' }))
}

test(
  'a keystroke in task 50 renders only task 50, not the other 49',
  async () => {
    await populate50Tasks()
    const row50 = screen.getByRole('group', { name: 'Task 50: t50' })
    renderCalls.mockClear()

    await userEvent.type(within(row50).getByRole('textbox', { name: 'Retries' }), '1')

    // One keystroke, one row's worth of render calls - not fifty. Before the
    // fix (no memo, a fresh onChange/onRemove closure and a fresh `allTasks`
    // array handed to every row on every keystroke) this was 50.
    expect(renderCalls).toHaveBeenCalledTimes(1)
  },
  30_000,
)

test(
  'a keystroke in the job name renders no task row at all',
  async () => {
    await populate50Tasks()
    renderCalls.mockClear()

    await userEvent.type(screen.getByRole('textbox', { name: 'Job name' }), 'x')

    expect(renderCalls).not.toHaveBeenCalled()
  },
  30_000,
)
