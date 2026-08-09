import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, test, vi } from 'vitest'
import { ResetPasswordDialog } from './ResetPasswordDialog'

function renderDialog(over: Partial<Parameters<typeof ResetPasswordDialog>[0]> = {}) {
  const props = {
    email: 'ada@studio.dev',
    pending: false,
    onSubmit: vi.fn(),
    onCancel: vi.fn(),
    ...over,
  }
  return { props, ...render(<ResetPasswordDialog {...props} />) }
}

test('is a labelled modal dialog naming the target and focuses the first field', () => {
  renderDialog()
  const dialog = screen.getByRole('dialog')
  expect(dialog).toHaveAttribute('aria-modal', 'true')
  expect(screen.getByText('Reset password for ada@studio.dev?')).toBeInTheDocument()
  expect(dialog).toHaveAccessibleName('Reset password for ada@studio.dev?')
  expect(screen.getByLabelText('New password')).toHaveFocus()
})

test('warns that every session of the target is revoked and that self-reset signs you out', () => {
  renderDialog()
  expect(
    screen.getByText(/revokes every session belonging to that user/i),
  ).toBeInTheDocument()
  expect(screen.getByText(/if that user is you, you will be signed out immediately/i)).toBeInTheDocument()
})

test('rejects a password under 8 characters without submitting', async () => {
  const { props } = renderDialog()
  await userEvent.type(screen.getByLabelText('New password'), 'short')
  await userEvent.type(screen.getByLabelText('Confirm password'), 'short')
  await userEvent.click(screen.getByRole('button', { name: 'Reset password' }))
  expect(screen.getByText('Password must be at least 8 characters.')).toBeInTheDocument()
  expect(props.onSubmit).not.toHaveBeenCalled()
})

test('rejects a mismatched confirmation without submitting', async () => {
  const { props } = renderDialog()
  await userEvent.type(screen.getByLabelText('New password'), 'password1')
  await userEvent.type(screen.getByLabelText('Confirm password'), 'password2')
  await userEvent.click(screen.getByRole('button', { name: 'Reset password' }))
  expect(screen.getByText('The two passwords do not match.')).toBeInTheDocument()
  expect(props.onSubmit).not.toHaveBeenCalled()
})

test('submits the new password when both fields agree', async () => {
  const { props } = renderDialog()
  await userEvent.type(screen.getByLabelText('New password'), 'password1')
  await userEvent.type(screen.getByLabelText('Confirm password'), 'password1')
  await userEvent.click(screen.getByRole('button', { name: 'Reset password' }))
  expect(props.onSubmit).toHaveBeenCalledWith('password1')
})

test('Escape and Cancel both dismiss', async () => {
  const { props } = renderDialog()
  await userEvent.keyboard('{Escape}')
  expect(props.onCancel).toHaveBeenCalledTimes(1)
  await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
  expect(props.onCancel).toHaveBeenCalledTimes(2)
})

test('pending disables the submit button', () => {
  renderDialog({ pending: true })
  expect(screen.getByRole('button', { name: 'Reset password' })).toBeDisabled()
})
