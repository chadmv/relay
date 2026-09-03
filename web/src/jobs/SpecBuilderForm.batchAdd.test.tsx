import { act, fireEvent, render, screen } from '@testing-library/react'
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

test('two adds in one batching window announce the correct ordinal for the second', () => {
  renderBuilder()
  const addButton = screen.getByRole('button', { name: 'Add task' })

  act(() => {
    fireEvent.click(addButton)
    fireEvent.click(addButton)
  })

  const preview = JSON.parse(screen.getByLabelText('Job spec preview').textContent ?? '')
  expect(preview.tasks).toHaveLength(3)
  expect(screen.getByRole('status')).toHaveTextContent('Task 3 added')
})
