import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { expect, test } from 'vitest'
import { TaskWorkerLink } from './TaskWorkerLink'

function renderLink(task: { worker_id?: string; worker_name?: string }) {
  return render(
    <MemoryRouter>
      <TaskWorkerLink task={task} />
    </MemoryRouter>,
  )
}

test('no worker renders a dash and no link', () => {
  renderLink({})
  expect(screen.getByText('-')).toBeInTheDocument()
  expect(screen.queryByRole('link')).not.toBeInTheDocument()
})

test('a named worker renders its name as a link to the worker page', () => {
  renderLink({ worker_id: 'w9abc123-0000', worker_name: 'render-node-07' })
  const link = screen.getByRole('link', { name: 'render-node-07' })
  expect(link).toHaveAttribute('href', '/workers/w9abc123-0000')
})

test('a worker with no resolved name falls back to the short id, still linked', () => {
  renderLink({ worker_id: 'w9abc123-0000' })
  const link = screen.getByRole('link', { name: 'w9abc1' })
  expect(link).toHaveAttribute('href', '/workers/w9abc123-0000')
})
