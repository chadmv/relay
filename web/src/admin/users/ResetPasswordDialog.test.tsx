import { fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, test, vi } from 'vitest'
import { ApiError } from '../../lib/api'
import { ResetPasswordDialog } from './ResetPasswordDialog'

function renderDialog(over: Partial<Parameters<typeof ResetPasswordDialog>[0]> = {}) {
  const props = {
    email: 'ada@studio.dev',
    pending: false,
    error: null as Error | null,
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

test('rejects a password over 72 bytes without submitting', async () => {
  // bcrypt.GenerateFromPassword returns ErrPasswordTooLong above 72 bytes, which
  // handleAdminPasswordReset turns into a 500. A routine password-manager output
  // can be longer than 72 characters, so this must be caught client-side.
  const { props } = renderDialog()
  const longPw = 'x'.repeat(73)
  fireEvent.change(screen.getByLabelText('New password'), { target: { value: longPw } })
  fireEvent.change(screen.getByLabelText('Confirm password'), { target: { value: longPw } })
  await userEvent.click(screen.getByRole('button', { name: 'Reset password' }))
  expect(screen.getByText('Password must be 72 bytes or fewer.')).toBeInTheDocument()
  expect(props.onSubmit).not.toHaveBeenCalled()
})

test('a failing reset surfaces a visible error message inside the dialog', () => {
  renderDialog({ error: new ApiError(500, 'error', '500 error') })
  const dialog = screen.getByRole('dialog')
  expect(screen.getByText('500 error')).toBeInTheDocument()
  expect(dialog).toContainElement(screen.getByText('500 error'))
})
