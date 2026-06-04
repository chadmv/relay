import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, test, vi } from 'vitest'
import { WorkersTable } from './WorkersTable'
import type { Worker } from './api'

function worker(over: Partial<Worker>): Worker {
  return {
    id: 'w1', name: 'render-01', hostname: 'h', cpu_cores: 16, ram_gb: 128,
    gpu_count: 0, gpu_model: '', os: 'linux', max_slots: 4,
    labels: null, status: 'online', last_seen_at: '2026-06-03T12:00:00Z', ...over,
  }
}

test('renders a row and calls onSort when a sortable header is clicked', async () => {
  const onSort = vi.fn()
  render(<WorkersTable workers={[worker({})]} sort="-created_at" onSort={onSort} />)
  expect(screen.getByText('render-01')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: /name/i }))
  expect(onSort).toHaveBeenCalledWith('name')
})

test('shows a descending caret on the active sort column', () => {
  render(<WorkersTable workers={[worker({})]} sort="-name" onSort={() => {}} />)
  expect(screen.getByRole('button', { name: /name ▼/i })).toBeInTheDocument()
})
