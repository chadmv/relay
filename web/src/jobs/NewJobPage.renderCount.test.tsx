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

// 20, not 50: twenty is still enough to discriminate "one row rendered" from
// "every row rendered" (a stale-loop mutation cannot pass by coincidence at
// either count), and it is one render shared by both assertions below, not
// two - populated through the JSON editor and one mode switch, not 19 "Add
// task" clicks, since Testing Library's getByRole re-scans the whole tree on
// every call, an O(n^2) cost against a GROWING tree unrelated to anything
// under test here.
async function populate20Tasks() {
  renderBuilder()
  await userEvent.click(screen.getByRole('button', { name: 'JSON' }))
  const editor = screen.getByRole('textbox', { name: 'Job spec JSON' })
  await userEvent.clear(editor)
  const spec = {
    name: 'perf',
    tasks: Array.from({ length: 20 }, (_, i) => ({ name: `t${i + 1}`, command: ['echo'] })),
  }
  await userEvent.paste(JSON.stringify(spec))
  await userEvent.click(screen.getByRole('button', { name: 'Form' }))
}

test(
  'memoization holds for both an unrelated edit and a same-row edit',
  async () => {
    await populate20Tasks()

    // An edit outside the task list renders no task row at all.
    renderCalls.mockClear()
    await userEvent.type(screen.getByRole('textbox', { name: 'Job name' }), 'x')
    expect(renderCalls).not.toHaveBeenCalled()

    // A keystroke in one row renders only that row, not the other nineteen.
    // Before the fix (no memo, a fresh onChange/onRemove closure and a fresh
    // `allTasks` array handed to every row on every keystroke) this was 20.
    const row20 = screen.getByRole('group', { name: 'Task 20: t20' })
    renderCalls.mockClear()
    await userEvent.type(within(row20).getByRole('textbox', { name: 'Retries' }), '1')
    expect(renderCalls).toHaveBeenCalledTimes(1)
  },
  30_000,
)
