import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, test, vi } from 'vitest'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

// Spies on the raw KeyValueRepeater export, before whichever import site
// wraps it in memo. KeyValueRepeater backs three surfaces on this page
// (labels, one task's env, that same task's requires); the assertion below
// is that editing a field none of the three depends on calls none of them.
vi.mock('./KeyValueRepeater', async (importOriginal) => {
  const actual = await importOriginal<typeof import('./KeyValueRepeater')>()
  return { ...actual, KeyValueRepeater: vi.fn(actual.KeyValueRepeater) }
})

import { NewJobPage } from './NewJobPage'
import { KeyValueRepeater } from './KeyValueRepeater'

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

const renderCalls = vi.mocked(KeyValueRepeater)

test('a keystroke in Retries calls no KeyValueRepeater body at all', async () => {
  renderBuilder()
  renderCalls.mockClear()

  await userEvent.type(screen.getByRole('textbox', { name: 'Retries' }), '1')

  expect(renderCalls).not.toHaveBeenCalled()
})
