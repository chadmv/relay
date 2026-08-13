import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, test, vi } from 'vitest'
import { ApiError } from '../../lib/api'
import { CreateInviteForm } from './CreateInviteForm'

function renderForm(over: Partial<Parameters<typeof CreateInviteForm>[0]> = {}) {
  const props = {
    pending: false,
    error: null as Error | null,
    onSubmit: vi.fn(),
    onCancel: vi.fn(),
    ...over,
  }
  return { props, ...render(<CreateInviteForm {...props} />) }
}

test('submitting with a blank email sends ONLY an explicit expires_in', async () => {
  const { props } = renderForm()
  await userEvent.click(screen.getByRole('button', { name: 'Create invite' }))
  // No email key at all (not ''), and the 72h default as a literal. A body is
  // ALWAYS sent because readJSON runs unconditionally (internal/api/invites.go:27).
  expect(props.onSubmit).toHaveBeenCalledWith({ expires_in: '72h' })
})

test('an email is trimmed and the chosen preset is sent as its WIRE value', async () => {
  const { props } = renderForm()
  await userEvent.type(screen.getByLabelText('Email'), '  invitee@studio.dev  ')
  await userEvent.click(screen.getByRole('button', { name: '7d' }))
  await userEvent.click(screen.getByRole('button', { name: 'Create invite' }))
  // "7d" is the LABEL. Go's time.ParseDuration has no day unit, so the wire value
  // is 168h - sending "7d" would be a 400.
  expect(props.onSubmit).toHaveBeenCalledWith({
    email: 'invitee@studio.dev',
    expires_in: '168h',
  })
})

test('exactly four presets, with 72h preselected', async () => {
  renderForm()
  for (const label of ['24h', '72h', '7d', '30d']) {
    expect(screen.getByRole('button', { name: label })).toBeInTheDocument()
  }
  expect(screen.getByRole('button', { name: '72h' })).toHaveAttribute('aria-pressed', 'true')
  expect(screen.getByRole('button', { name: '30d' })).toHaveAttribute('aria-pressed', 'false')

  await userEvent.click(screen.getByRole('button', { name: '30d' }))
  expect(screen.getByRole('button', { name: '30d' })).toHaveAttribute('aria-pressed', 'true')
  expect(screen.getByRole('button', { name: '72h' })).toHaveAttribute('aria-pressed', 'false')
})

test('every preset submits an hour-denominated literal the server can parse', async () => {
  const cases: [string, string][] = [
    ['24h', '24h'],
    ['72h', '72h'],
    ['7d', '168h'],
    ['30d', '720h'],
  ]
  for (const [label, wire] of cases) {
    const { props, unmount } = renderForm()
    await userEvent.click(screen.getByRole('button', { name: label }))
    await userEvent.click(screen.getByRole('button', { name: 'Create invite' }))
    expect(props.onSubmit).toHaveBeenCalledWith({ expires_in: wire })
    expect(wire).toMatch(/^\d+h$/)
    unmount()
  }
})

test('there is no free-form duration field, so the 0 < d <= 720h bound is unreachable', () => {
  renderForm()
  expect(screen.queryByLabelText(/expires_in/i)).not.toBeInTheDocument()
  expect(screen.queryByRole('spinbutton')).not.toBeInTheDocument()
  // Exactly one text-entry control: the email. Nothing else can carry a duration.
  expect(screen.getAllByRole('textbox')).toHaveLength(1)
})

test('the email input is type=email so the browser gives a first pass', () => {
  renderForm()
  // The client does NOT reimplement mail.ParseAddress (internal/api/invites.go:66).
  // Two parsers disagreeing is worse than one round trip; the server's 400 renders
  // in this form's own error slot.
  expect(screen.getByLabelText('Email')).toHaveAttribute('type', 'email')
})

test('states up front that the raw token is returned once', () => {
  renderForm()
  expect(screen.getByText(/returned once/i)).toBeInTheDocument()
  expect(screen.getByText(/cannot be retrieved again/i)).toBeInTheDocument()
})

test('pending disables submit', () => {
  renderForm({ pending: true })
  expect(screen.getByRole('button', { name: 'Create invite' })).toBeDisabled()
})

test('a server error renders inside the panel and the form keeps its state', async () => {
  const { rerender } = renderForm()
  await userEvent.type(screen.getByLabelText('Email'), 'invitee@studio.dev')
  rerender(
    <CreateInviteForm
      pending={false}
      error={new ApiError(400, 'invalid email address', '400 invalid email address')}
      onSubmit={vi.fn()}
      onCancel={vi.fn()}
    />,
  )
  // The form owns its own error surface; nothing routes to a page-level box, which
  // would render behind an overlay if one were open.
  expect(screen.getByText('400 invalid email address')).toBeInTheDocument()
  expect(screen.getByLabelText('Email')).toHaveValue('invitee@studio.dev')
})

test('Cancel calls onCancel and does not submit', async () => {
  const { props } = renderForm()
  await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
  expect(props.onCancel).toHaveBeenCalledTimes(1)
  expect(props.onSubmit).not.toHaveBeenCalled()
})
