import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, test, vi } from 'vitest'
import { ApiError } from '../../lib/api'
import { CreateEnrollmentForm } from './CreateEnrollmentForm'

function renderForm(over: Partial<Parameters<typeof CreateEnrollmentForm>[0]> = {}) {
  const props = {
    pending: false,
    error: null as Error | null,
    onSubmit: vi.fn(),
    onCancel: vi.fn(),
    ...over,
  }
  return { props, ...render(<CreateEnrollmentForm {...props} />) }
}

test('submitting with a blank hint sends ONLY an explicit ttl_seconds', async () => {
  const { props } = renderForm()
  await userEvent.click(screen.getByRole('button', { name: 'Enroll' }))
  // No hostname_hint key at all (not ''), and the 24h default as a literal - never
  // 0 and never omitted, so the request states its own TTL.
  expect(props.onSubmit).toHaveBeenCalledWith({ ttl_seconds: 86400 })
})

test('a hint is trimmed and a chosen preset is sent verbatim', async () => {
  const { props } = renderForm()
  await userEvent.type(screen.getByLabelText('Hostname hint'), '  farm-west-13  ')
  await userEvent.click(screen.getByRole('button', { name: '1h' }))
  await userEvent.click(screen.getByRole('button', { name: 'Enroll' }))
  expect(props.onSubmit).toHaveBeenCalledWith({ hostname_hint: 'farm-west-13', ttl_seconds: 3600 })
})

test('exactly four TTL presets, with 24h preselected', async () => {
  renderForm()
  for (const label of ['1h', '24h', '3d', '7d']) {
    expect(screen.getByRole('button', { name: label })).toBeInTheDocument()
  }
  expect(screen.getByRole('button', { name: '24h' })).toHaveAttribute('aria-pressed', 'true')
  expect(screen.getByRole('button', { name: '7d' })).toHaveAttribute('aria-pressed', 'false')

  await userEvent.click(screen.getByRole('button', { name: '7d' }))
  expect(screen.getByRole('button', { name: '7d' })).toHaveAttribute('aria-pressed', 'true')
  expect(screen.getByRole('button', { name: '24h' })).toHaveAttribute('aria-pressed', 'false')
})

test('every preset submits its exact server-legal literal', async () => {
  const cases: [string, number][] = [
    ['1h', 3600],
    ['24h', 86400],
    ['3d', 259200],
    ['7d', 604800],
  ]
  for (const [label, seconds] of cases) {
    const { props, unmount } = renderForm()
    await userEvent.click(screen.getByRole('button', { name: label }))
    await userEvent.click(screen.getByRole('button', { name: 'Enroll' }))
    expect(props.onSubmit).toHaveBeenCalledWith({ ttl_seconds: seconds })
    unmount()
  }
})

test('there is no free-form TTL field, so the 60s/7d bounds are unreachable from the UI', () => {
  renderForm()
  expect(screen.queryByLabelText(/ttl/i)).not.toBeInTheDocument()
  expect(screen.queryByLabelText(/seconds/i)).not.toBeInTheDocument()
  expect(screen.queryByRole('spinbutton')).not.toBeInTheDocument()
})

test('states up front that the token is shown once', () => {
  renderForm()
  expect(screen.getByText(/returned once/i)).toBeInTheDocument()
  // The hi-fi's copy points at a success toast that does not exist
  // (hifi3-holo-pages.jsx:2390); this form points at the reveal dialog instead.
  expect(screen.queryByText(/toast/i)).not.toBeInTheDocument()
})

test('pending disables submit', () => {
  renderForm({ pending: true })
  expect(screen.getByRole('button', { name: 'Enroll' })).toBeDisabled()
})

test('a server error renders inside the panel and the form keeps its state', async () => {
  const { rerender } = renderForm()
  await userEvent.type(screen.getByLabelText('Hostname hint'), 'farm-west-13')
  rerender(
    <CreateEnrollmentForm
      pending={false}
      error={new ApiError(500, 'failed to create enrollment', '500 failed to create enrollment')}
      onSubmit={vi.fn()}
      onCancel={vi.fn()}
    />,
  )
  expect(screen.getByText('500 failed to create enrollment')).toBeInTheDocument()
  expect(screen.getByLabelText('Hostname hint')).toHaveValue('farm-west-13')
})

test('Cancel calls onCancel and does not submit', async () => {
  const { props } = renderForm()
  await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
  expect(props.onCancel).toHaveBeenCalledTimes(1)
  expect(props.onSubmit).not.toHaveBeenCalled()
})
