import { act, fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, test } from 'vitest'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
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

test('removing both rows in one batching window focuses a surviving control, not the body', async () => {
  renderBuilder()
  await userEvent.click(screen.getByRole('button', { name: 'Add task' }))

  const removeHello = screen.getByRole('button', { name: /^Remove task \d+: hello$/ })
  const removeTask2 = screen.getByRole('button', { name: 'Remove task 2' })

  act(() => {
    fireEvent.click(removeHello)
    fireEvent.click(removeTask2)
  })

  expect(screen.getByLabelText('Job spec preview').textContent).toContain('"tasks": []')
  expect(document.activeElement).toBe(screen.getByRole('button', { name: 'Add task' }))
})
