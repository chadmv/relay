import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { expect, test, vi } from 'vitest'
import { WorkersTable, type SortField } from './WorkersTable'
import type { Worker, WorkerSort } from './api'

function worker(over: Partial<Worker>): Worker {
  return {
    id: 'w1', name: 'render-01', hostname: 'h', cpu_cores: 16, ram_gb: 128,
    gpu_count: 0, gpu_model: '', os: 'linux', max_slots: 4,
    labels: null, status: 'online', last_seen_at: '2026-06-03T12:00:00Z', ...over,
  }
}

function renderTable(
  workers: Worker[],
  sort: WorkerSort = '-created_at',
  onSort: (f: SortField) => void = () => {},
) {
  return render(
    <MemoryRouter>
      <WorkersTable workers={workers} sort={sort} onSort={onSort} />
    </MemoryRouter>,
  )
}

test('renders a row and calls onSort when a sortable header is clicked', async () => {
  const onSort = vi.fn()
  renderTable([worker({})], '-created_at', onSort)
  expect(screen.getByText('render-01')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: /name/i }))
  expect(onSort).toHaveBeenCalledWith('name')
})

test('shows a descending caret on the active sort column', () => {
  renderTable([worker({})], '-name')
  expect(screen.getByRole('button', { name: /name ▼/i })).toBeInTheDocument()
})

test('exposes aria-sort on the active sortable header and "none" on the rest', () => {
  renderTable([worker({})], '-last_seen_at')
  expect(screen.getByRole('button', { name: /last seen/i })).toHaveAttribute('aria-sort', 'descending')
  expect(screen.getByRole('button', { name: /name/i })).toHaveAttribute('aria-sort', 'none')
  expect(screen.getByRole('button', { name: /status/i })).toHaveAttribute('aria-sort', 'none')
})

test('reports ascending aria-sort when the active sort is ascending', () => {
  renderTable([worker({})], 'name')
  expect(screen.getByRole('button', { name: /name/i })).toHaveAttribute('aria-sort', 'ascending')
})

test('each row links to the worker detail page', () => {
  renderTable([worker({ id: 'w9', name: 'render-09' })])
  expect(screen.getByRole('link', { name: /render-09/ })).toHaveAttribute('href', '/workers/w9')
})
