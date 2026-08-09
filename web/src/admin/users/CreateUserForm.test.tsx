import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, test, vi } from 'vitest'
import { ApiError } from '../../lib/api'
import { CreateUserForm } from './CreateUserForm'

function renderForm(over: Partial<Parameters<typeof CreateUserForm>[0]> = {}) {
  const props = {
    pending: false,
    error: null as Error | null,
    onSubmit: vi.fn(),
    onCancel: vi.fn(),
    ...over,
  }
  return { props, ...render(<CreateUserForm {...props} />) }
}

test('submits email, trimmed name, password, and is_admin', async () => {
  const { props } = renderForm()
  await userEvent.type(screen.getByLabelText('Email'), 'new@studio.dev')
  await userEvent.type(screen.getByLabelText('Name'), '  New Person  ')
  await userEvent.type(screen.getByLabelText('Password'), 'password1')
  await userEvent.click(screen.getByLabelText(/^Admin/))
  await userEvent.click(screen.getByRole('button', { name: 'Create' }))
  expect(props.onSubmit).toHaveBeenCalledWith({
    email: 'new@studio.dev',
    name: 'New Person',
    password: 'password1',
    is_admin: true,
  })
})

test('is_admin defaults to false', async () => {
  const { props } = renderForm()
  await userEvent.type(screen.getByLabelText('Email'), 'new@studio.dev')
  await userEvent.type(screen.getByLabelText('Password'), 'password1')
  await userEvent.click(screen.getByRole('button', { name: 'Create' }))
  expect(props.onSubmit).toHaveBeenCalledWith({
    email: 'new@studio.dev',
    name: '',
    password: 'password1',
    is_admin: false,
  })
})

test('rejects a blank email without submitting', async () => {
  const { props } = renderForm()
  await userEvent.type(screen.getByLabelText('Password'), 'password1')
  await userEvent.click(screen.getByRole('button', { name: 'Create' }))
  expect(screen.getByText('Email is required.')).toBeInTheDocument()
  expect(props.onSubmit).not.toHaveBeenCalled()
})

test('rejects a password under 8 characters without submitting', async () => {
  const { props } = renderForm()
  await userEvent.type(screen.getByLabelText('Email'), 'new@studio.dev')
  await userEvent.type(screen.getByLabelText('Password'), 'short')
  await userEvent.click(screen.getByRole('button', { name: 'Create' }))
  expect(screen.getByText('Password must be at least 8 characters.')).toBeInTheDocument()
  expect(props.onSubmit).not.toHaveBeenCalled()
})

test('a 409 renders as a duplicate-email field error and preserves form state', async () => {
  renderForm({ error: new ApiError(409, 'email already registered', '409 email already registered') })
  await userEvent.type(screen.getByLabelText('Email'), 'dupe@studio.dev')
  expect(screen.getByText('That email is already registered.')).toBeInTheDocument()
  expect(screen.getByLabelText('Email')).toHaveValue('dupe@studio.dev')
})

test('a non-409 error renders as a form-level message', () => {
  renderForm({ error: new ApiError(400, 'invalid email address', '400 invalid email address') })
  expect(screen.getByText('400 invalid email address')).toBeInTheDocument()
  expect(screen.queryByText('That email is already registered.')).not.toBeInTheDocument()
})

test('pending disables the Create button', () => {
  renderForm({ pending: true })
  expect(screen.getByRole('button', { name: 'Create' })).toBeDisabled()
})

test('Cancel calls onCancel', async () => {
  const { props } = renderForm()
  await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
  expect(props.onCancel).toHaveBeenCalled()
})
