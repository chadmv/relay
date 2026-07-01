import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, test, vi } from 'vitest'
import { ConfirmDialog } from './ConfirmDialog'

test('renders title, body, confirm and cancel; is a labelled dialog', () => {
  render(
    <ConfirmDialog
      title="Disable render-rig-A?"
      body="It will stop receiving new tasks."
      confirmLabel="Disable"
      onConfirm={() => {}}
      onCancel={() => {}}
    />,
  )
  const dialog = screen.getByRole('dialog')
  expect(dialog).toHaveAccessibleName('Disable render-rig-A?')
  expect(screen.getByText('It will stop receiving new tasks.')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Disable' })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Cancel' })).toBeInTheDocument()
})

test('Cancel invokes onCancel and not onConfirm', async () => {
  const onConfirm = vi.fn()
  const onCancel = vi.fn()
  render(
    <ConfirmDialog title="t" body="b" confirmLabel="Go" onConfirm={onConfirm} onCancel={onCancel} />,
  )
  await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
  expect(onCancel).toHaveBeenCalledOnce()
  expect(onConfirm).not.toHaveBeenCalled()
})

test('Escape invokes onCancel', async () => {
  const onCancel = vi.fn()
  render(
    <ConfirmDialog title="t" body="b" confirmLabel="Go" onConfirm={() => {}} onCancel={onCancel} />,
  )
  await userEvent.keyboard('{Escape}')
  expect(onCancel).toHaveBeenCalledOnce()
})

test('Confirm invokes onConfirm', async () => {
  const onConfirm = vi.fn()
  render(
    <ConfirmDialog title="t" body="b" confirmLabel="Go" onConfirm={onConfirm} onCancel={() => {}} />,
  )
  await userEvent.click(screen.getByRole('button', { name: 'Go' }))
  expect(onConfirm).toHaveBeenCalledOnce()
})

test('destructive variant still renders the confirm button', () => {
  render(
    <ConfirmDialog
      title="t"
      body="b"
      confirmLabel="Revoke"
      destructive
      onConfirm={() => {}}
      onCancel={() => {}}
    />,
  )
  expect(screen.getByRole('button', { name: 'Revoke' })).toBeInTheDocument()
})
