import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { expect, test, vi } from 'vitest'
import { server } from '../../test/setup-helpers'
import { CreateReservationForm } from './CreateReservationForm'

const A = { id: 'aaaa1111-1111-1111-1111-111111111111', name: 'render-01', status: 'online' }
const B = { id: 'bbbb2222-2222-2222-2222-222222222222', name: 'render-02', status: 'online' }

function renderForm(over: Record<string, unknown> = {}) {
  server.use(
    http.get('/v1/workers', () => HttpResponse.json({ items: [A, B], next_cursor: '', total: 2 })),
  )
  const onSubmit = vi.fn()
  const onCancel = vi.fn()
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const view = render(
    <QueryClientProvider client={client}>
      <CreateReservationForm
        pending={false}
        error={null}
        onSubmit={onSubmit}
        onCancel={onCancel}
        {...over}
      />
    </QueryClientProvider>,
  )
  return { onSubmit, onCancel, ...view }
}

const submitButton = () => screen.getByRole('button', { name: 'Reserve' })

test('an empty name blocks submit, and filling it unblocks (paired positive)', async () => {
  renderForm()
  await userEvent.click(await screen.findByRole('checkbox', { name: /render-01/ }))
  expect(screen.getByText('Name is required.')).toBeInTheDocument()
  expect(submitButton()).toBeDisabled()

  await userEvent.type(screen.getByLabelText('Name'), 'gpu-farm-hold')
  expect(screen.queryByText('Name is required.')).not.toBeInTheDocument()
  expect(submitButton()).toBeEnabled()
})

test('whitespace alone is not a name', async () => {
  renderForm()
  await userEvent.click(await screen.findByRole('checkbox', { name: /render-01/ }))
  await userEvent.type(screen.getByLabelText('Name'), '   ')
  expect(screen.getByText('Name is required.')).toBeInTheDocument()
  expect(submitButton()).toBeDisabled()
})

test('zero workers blocks submit, and selecting one unblocks (paired positive)', async () => {
  renderForm()
  await userEvent.type(await screen.findByLabelText('Name'), 'gpu-farm-hold')
  // The server accepts worker_ids: [] and stores a reservation that reserves nothing,
  // because reservedIDs is built only from that array
  // (internal/scheduler/dispatch.go:186-191). Submitting one is always a mistake.
  expect(screen.getByText('Select at least one worker.')).toBeInTheDocument()
  expect(submitButton()).toBeDisabled()

  await userEvent.click(screen.getByRole('checkbox', { name: /render-01/ }))
  expect(screen.queryByText('Select at least one worker.')).not.toBeInTheDocument()
  expect(submitButton()).toBeEnabled()
})

test('an inverted or zero-length window blocks submit, and fixing it unblocks', async () => {
  renderForm()
  await userEvent.type(await screen.findByLabelText('Name'), 'gpu-farm-hold')
  await userEvent.click(screen.getByRole('checkbox', { name: /render-01/ }))

  // datetime-local: assign through fireEvent.change. jsdom implements no segmented
  // editing UI, so userEvent.type on this input type is unreliable.
  const starts = screen.getByLabelText('Starts') as HTMLInputElement
  const ends = screen.getByLabelText('Ends') as HTMLInputElement
  fireEvent.change(starts, { target: { value: '2026-08-20T09:00' } })
  fireEvent.change(ends, { target: { value: '2026-08-10T09:00' } })
  // Such a row can never satisfy ListActiveReservations, and the server persists it
  // happily (internal/store/query/reservations.sql:21-22).
  expect(screen.getByText('Ends must be after starts.')).toBeInTheDocument()
  expect(submitButton()).toBeDisabled()

  // Equal bounds are also empty, not merely inverted.
  fireEvent.change(ends, { target: { value: '2026-08-20T09:00' } })
  expect(screen.getByText('Ends must be after starts.')).toBeInTheDocument()

  fireEvent.change(ends, { target: { value: '2026-08-21T09:00' } })
  expect(screen.queryByText('Ends must be after starts.')).not.toBeInTheDocument()
  expect(submitButton()).toBeEnabled()
})

test('a window entirely in the past is allowed (a legitimate historical record)', async () => {
  const { onSubmit } = renderForm()
  await userEvent.type(await screen.findByLabelText('Name'), 'old-hold')
  await userEvent.click(screen.getByRole('checkbox', { name: /render-01/ }))
  fireEvent.change(screen.getByLabelText('Starts'), { target: { value: '2020-01-01T09:00' } })
  fireEvent.change(screen.getByLabelText('Ends'), { target: { value: '2020-01-02T09:00' } })
  expect(submitButton()).toBeEnabled()
  await userEvent.click(submitButton())
  expect(onSubmit).toHaveBeenCalledTimes(1)
})

test('submits the minimal body and omits every blank optional', async () => {
  const { onSubmit } = renderForm()
  await userEvent.type(await screen.findByLabelText('Name'), '  gpu-farm-hold  ')
  await userEvent.click(screen.getByRole('checkbox', { name: /render-02/ }))
  await userEvent.click(submitButton())

  expect(onSubmit).toHaveBeenCalledTimes(1)
  const body = onSubmit.mock.calls[0][0]
  expect(body).toEqual({ name: 'gpu-farm-hold', worker_ids: [B.id] })
  for (const key of ['project', 'starts_at', 'ends_at', 'selector', 'user_id']) {
    expect(key in body).toBe(false)
  }
})

test('dates are sent as RFC3339 with an offset, not the raw datetime-local value', async () => {
  const { onSubmit } = renderForm()
  await userEvent.type(await screen.findByLabelText('Name'), 'gpu-farm-hold')
  await userEvent.click(screen.getByRole('checkbox', { name: /render-01/ }))
  fireEvent.change(screen.getByLabelText('Starts'), { target: { value: '2026-08-10T09:00' } })
  await userEvent.type(screen.getByLabelText('Project'), 'atlas')
  await userEvent.click(submitButton())

  const body = onSubmit.mock.calls[0][0]
  // A datetime-local value is zone-less and Go's time.Time decoder rejects it. The
  // expectation is computed the same way the component computes it, from the same
  // local string, so this is TZ-independent - a hardcoded 'Z' literal would only pass
  // in a UTC runner.
  expect(body.starts_at).toBe(new Date('2026-08-10T09:00').toISOString())
  expect(body.starts_at).toMatch(/Z$/)
  expect(body.starts_at).not.toBe('2026-08-10T09:00')
  expect(body.project).toBe('atlas')
  expect('ends_at' in body).toBe(false)
})

test('the panel states the exclusion effect and claims no affinity', () => {
  const { container } = renderForm()
  const text = (container.textContent ?? '').replace(/\s+/g, ' ')
  expect(text).toMatch(/removes these workers from the dispatch pool for every job/i)
  // A reservation does not route the owner's work anywhere
  // (internal/scheduler/dispatch.go:221-223).
  for (const claim of [/reserved for/i, /dedicated/i, /priority/i, /exclusive/i, /assigned to/i]) {
    expect(text).not.toMatch(claim)
  }
})

test('a create error renders inline and Cancel is wired', async () => {
  const { onCancel } = renderForm({ error: new Error('500 create reservation failed') })
  expect(screen.getByText('500 create reservation failed')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
  expect(onCancel).toHaveBeenCalledTimes(1)
})

test('pending disables submit even when the form is valid', async () => {
  renderForm({ pending: true })
  await userEvent.type(await screen.findByLabelText('Name'), 'gpu-farm-hold')
  await userEvent.click(screen.getByRole('checkbox', { name: /render-01/ }))
  expect(submitButton()).toBeDisabled()
})
