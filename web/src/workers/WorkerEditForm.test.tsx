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

test('pre-fills current name, max_slots, and labels', () => {
  render(<WorkerEditForm worker={WORKER} pending={false} onSubmit={() => {}} onCancel={() => {}} />)
  expect(screen.getByLabelText(/name/i)).toHaveValue('rig-A')
  expect(screen.getByLabelText(/max slots/i)).toHaveValue(2)
  expect(screen.getByDisplayValue('rack')).toBeInTheDocument()
  expect(screen.getByDisplayValue('gold')).toBeInTheDocument()
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

test('editing labels submits the full edited map (add + remove a key)', async () => {
  const onSubmit = vi.fn()
  render(<WorkerEditForm worker={WORKER} pending={false} onSubmit={onSubmit} onCancel={() => {}} />)
  // Remove the "tier" row.
  await userEvent.click(screen.getByRole('button', { name: 'Remove tier' }))
  // Add a new "zone=east" row.
  await userEvent.click(screen.getByRole('button', { name: /add label/i }))
  const keyInputs = screen.getAllByPlaceholderText('key')
  const valInputs = screen.getAllByPlaceholderText('value')
  await userEvent.type(keyInputs[keyInputs.length - 1], 'zone')
  await userEvent.type(valInputs[valInputs.length - 1], 'east')
  await userEvent.click(screen.getByRole('button', { name: /save/i }))
  expect(onSubmit).toHaveBeenCalledWith({ labels: { rack: 'A', zone: 'east' } })
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

test('two mounts with distinct idPrefix values produce unique, correctly-associated ids', () => {
  render(
    <>
      <WorkerEditForm
        worker={WORKER}
        pending={false}
        onSubmit={() => {}}
        onCancel={() => {}}
        idPrefix="header"
      />
      <WorkerEditForm
        worker={WORKER}
        pending={false}
        onSubmit={() => {}}
        onCancel={() => {}}
        idPrefix="labels"
      />
    </>,
  )
  const nameInputs = screen.getAllByLabelText(/^name$/i)
  expect(nameInputs).toHaveLength(2)
  const nameIds = nameInputs.map((el) => el.id)
  expect(new Set(nameIds).size).toBe(2)

  const slotsInputs = screen.getAllByLabelText(/max slots/i)
  expect(slotsInputs).toHaveLength(2)
  const slotsIds = slotsInputs.map((el) => el.id)
  expect(new Set(slotsIds).size).toBe(2)
})
