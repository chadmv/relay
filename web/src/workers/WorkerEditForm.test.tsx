import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, test, vi } from 'vitest'
import { WorkerEditForm } from './WorkerEditForm'
import type { Worker } from './api'

const WORKER: Worker = {
  id: 'w1',
  name: 'rig-A',
  hostname: 'h',
  cpu_cores: 8,
  ram_gb: 32,
  gpu_count: 0,
  gpu_model: '',
  os: 'linux',
  max_slots: 2,
  labels: { rack: 'A', tier: 'gold' },
  status: 'online',
}

test('pre-fills current name and max_slots', () => {
  render(<WorkerEditForm worker={WORKER} pending={false} onSubmit={() => {}} onCancel={() => {}} />)
  expect(screen.getByLabelText(/name/i)).toHaveValue('rig-A')
  expect(screen.getByLabelText(/max slots/i)).toHaveValue(2)
})

test('does not render any label editing controls', () => {
  render(<WorkerEditForm worker={WORKER} pending={false} onSubmit={() => {}} onCancel={() => {}} />)
  expect(screen.queryByText(/labels/i)).not.toBeInTheDocument()
  expect(screen.queryByPlaceholderText('key')).not.toBeInTheDocument()
  expect(screen.queryByPlaceholderText('value')).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: /add label/i })).not.toBeInTheDocument()
})

test('submits only the changed name field', async () => {
  const onSubmit = vi.fn()
  render(<WorkerEditForm worker={WORKER} pending={false} onSubmit={onSubmit} onCancel={() => {}} />)
  const name = screen.getByLabelText(/name/i)
  await userEvent.clear(name)
  await userEvent.type(name, 'rig-B')
  await userEvent.click(screen.getByRole('button', { name: /save/i }))
  expect(onSubmit).toHaveBeenCalledWith({ name: 'rig-B' })
})

test('submits only the changed max_slots field', async () => {
  const onSubmit = vi.fn()
  render(<WorkerEditForm worker={WORKER} pending={false} onSubmit={onSubmit} onCancel={() => {}} />)
  const slots = screen.getByLabelText(/max slots/i)
  await userEvent.clear(slots)
  await userEvent.type(slots, '5')
  await userEvent.click(screen.getByRole('button', { name: /save/i }))
  expect(onSubmit).toHaveBeenCalledWith({ max_slots: 5 })
})

test('submitting with no changes sends an empty patch', async () => {
  const onSubmit = vi.fn()
  render(<WorkerEditForm worker={WORKER} pending={false} onSubmit={onSubmit} onCancel={() => {}} />)
  await userEvent.click(screen.getByRole('button', { name: /save/i }))
  expect(onSubmit).toHaveBeenCalledWith({})
})

test('clearing max slots blocks save and does not send max_slots: 0', async () => {
  const onSubmit = vi.fn()
  render(<WorkerEditForm worker={WORKER} pending={false} onSubmit={onSubmit} onCancel={() => {}} />)
  const slots = screen.getByLabelText(/max slots/i)
  await userEvent.clear(slots)
  await userEvent.click(screen.getByRole('button', { name: /save/i }))
  expect(onSubmit).not.toHaveBeenCalled()
  expect(screen.getByText(/must be a whole number/i)).toBeInTheDocument()
})

test.each(['2.5', '-1', '0'])('rejects max slots value %s', async (value) => {
  const onSubmit = vi.fn()
  render(<WorkerEditForm worker={WORKER} pending={false} onSubmit={onSubmit} onCancel={() => {}} />)
  const slots = screen.getByLabelText(/max slots/i)
  await userEvent.clear(slots)
  await userEvent.type(slots, value)
  await userEvent.click(screen.getByRole('button', { name: /save/i }))
  expect(onSubmit).not.toHaveBeenCalled()
  expect(screen.getByText(/must be a whole number/i)).toBeInTheDocument()
})

test('a valid max slots change still sends max_slots', async () => {
  const onSubmit = vi.fn()
  render(<WorkerEditForm worker={WORKER} pending={false} onSubmit={onSubmit} onCancel={() => {}} />)
  const slots = screen.getByLabelText(/max slots/i)
  await userEvent.clear(slots)
  await userEvent.type(slots, '4')
  await userEvent.click(screen.getByRole('button', { name: /save/i }))
  expect(onSubmit).toHaveBeenCalledWith({ max_slots: 4 })
})

test('clearing name blocks save and does not send an empty name', async () => {
  const onSubmit = vi.fn()
  render(<WorkerEditForm worker={WORKER} pending={false} onSubmit={onSubmit} onCancel={() => {}} />)
  const name = screen.getByLabelText(/name/i)
  await userEvent.clear(name)
  await userEvent.click(screen.getByRole('button', { name: /save/i }))
  expect(onSubmit).not.toHaveBeenCalled()
  expect(screen.getByText(/name is required/i)).toBeInTheDocument()
})

test('a whitespace-only name blocks save', async () => {
  const onSubmit = vi.fn()
  render(<WorkerEditForm worker={WORKER} pending={false} onSubmit={onSubmit} onCancel={() => {}} />)
  const name = screen.getByLabelText(/name/i)
  await userEvent.clear(name)
  await userEvent.type(name, '   ')
  await userEvent.click(screen.getByRole('button', { name: /save/i }))
  expect(onSubmit).not.toHaveBeenCalled()
  expect(screen.getByText(/name is required/i)).toBeInTheDocument()
})
