import { render, screen } from '@testing-library/react'
import { expect, test } from 'vitest'
import { WorkersGrid } from './WorkersGrid'
import type { Worker } from './api'

function worker(over: Partial<Worker>): Worker {
  return {
    id: 'w1', name: 'render-01', hostname: 'h', cpu_cores: 16, ram_gb: 128,
    gpu_count: 1, gpu_model: 'RTX 4090', os: 'linux', max_slots: 4,
    labels: { pool: 'render' }, status: 'online',
    last_seen_at: '2026-06-03T12:00:00Z', ...over,
  }
}

test('renders a card with name, status, spec, slots, and label chip', () => {
  render(<WorkersGrid workers={[worker({})]} />)
  expect(screen.getByText('render-01')).toBeInTheDocument()
  expect(screen.getByText('ONLINE')).toBeInTheDocument()
  expect(screen.getByText('RTX 4090')).toBeInTheDocument()
  expect(screen.getByText('4 slots')).toBeInTheDocument()
  expect(screen.getByText('pool=render')).toBeInTheDocument()
})

test('dims offline workers', () => {
  const { container } = render(<WorkersGrid workers={[worker({ id: 'o', name: 'off-01', status: 'offline' })]} />)
  expect(container.querySelector('.opacity-\\[0\\.55\\]')).not.toBeNull()
})
