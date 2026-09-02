import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, expect, test, vi } from 'vitest'
import { server } from '../../test/setup-helpers'
import { ReservationsTab } from './ReservationsTab'
import type { Reservation } from './api'

const W1 = 'aaaa1111-1111-1111-1111-111111111111'
const WORKER = { id: W1, name: 'render-01', status: 'online' }

function row(over: Partial<Reservation> = {}): Reservation {
  return {
    id: 'r1',
    name: 'gpu-farm-hold',
    selector: null,
    worker_ids: [W1],
    user_id: 'u1',
    created_at: '2026-08-09T09:30:00Z',
    ...over,
  }
}

// ReservationsTab does not use useAuth, so no AuthProvider and no /v1/users/me
// handler. It DOES render react-router Links, so MemoryRouter is required.
function renderTab() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return {
    client,
    ...render(
      <QueryClientProvider client={client}>
        <MemoryRouter>
          <ReservationsTab />
        </MemoryRouter>
      </QueryClientProvider>,
    ),
  }
}

function listHandler(
  seen: URLSearchParams[],
  envelope: (p: URLSearchParams) => { items: Reservation[]; next_cursor: string; total: number },
) {
  return http.get('/v1/reservations', ({ request }) => {
    const params = new URL(request.url).searchParams
    seen.push(params)
    return HttpResponse.json(envelope(params))
  })
}

const workersHandler = http.get('/v1/workers', () =>
  HttpResponse.json({ items: [WORKER], next_cursor: '', total: 1 }),
)

afterEach(() => vi.useRealTimers())

test('renders rows, the endpoint caption, the default sort, and the footer', async () => {
  const seen: URLSearchParams[] = []
  server.use(listHandler(seen, () => ({ items: [row()], next_cursor: '', total: 1 })))
  renderTab()
  expect(await screen.findByText('gpu-farm-hold')).toBeInTheDocument()
  expect(screen.getByText('GET /v1/reservations')).toBeInTheDocument()
  expect(seen[0].get('sort')).toBe('-created_at')
  expect(seen[0].get('limit')).toBe('50')
  expect(screen.getByText('1-1 of 1')).toBeInTheDocument()
})

test('the tab never claims worker affinity the scheduler does not implement', async () => {
  // THE central honesty requirement. A reservation unions worker_ids into reservedIDs
  // and the dispatcher SKIPS those workers for every task
  // (internal/scheduler/dispatch.go:185-191, :221-223). Nothing routes the owner's
  // work to them, so none of the hi-fi's "reserve for X" framing may survive.
  const seen: URLSearchParams[] = []
  server.use(listHandler(seen, () => ({ items: [row()], next_cursor: '', total: 1 })))
  const { container } = renderTab()
  await screen.findByText('gpu-farm-hold')
  // L4 (review 2026-08-09): innerHTML, not textContent. textContent excludes
  // attribute values entirely, so a regression adding e.g.
  // aria-label="Reserved for ada@studio.dev" to some element would pass every
  // negative matcher below while textContent-based sweeping could never see it -
  // that is the representation the real failure would take, not a hypothetical one.
  const html = container.innerHTML

  for (const claim of [
    /reserved for/i,
    /dedicated/i,
    /priority/i,
    /exclusive/i,
    /assigned to/i,
    /only .{0,24} can use/i,
  ]) {
    expect(html).not.toMatch(claim)
  }

  // Paired positive controls on the SAME instrument, so the absences above are about
  // the copy and not about a matcher that can never match.
  expect(html).toMatch(/removes its worker_ids from the general dispatch pool/i)
  expect(html).toMatch(/including the owner's own jobs/i)
  expect(html).toMatch(/never read by the scheduler/i)
  expect(html).toMatch(/next dispatch cycle/i)
  expect(html).toMatch(/never preempt/i)
  // And the column header is the neutral one.
  expect(screen.getByText('WORKERS')).toBeInTheDocument()
})

// L4 (review 2026-08-09): the sweep above never covers the confirm dialog, which is
// new copy of its own (M2) and was never checked for an affinity claim before.
test('the confirm dialog also carries no affinity claim when open', async () => {
  const seen: URLSearchParams[] = []
  server.use(listHandler(seen, () => ({ items: [row()], next_cursor: '', total: 1 })))
  renderTab()
  await screen.findByText('gpu-farm-hold')
  await userEvent.click(screen.getByRole('button', { name: 'Delete reservation gpu-farm-hold' }))
  await screen.findByRole('dialog')
  // document.body, not container: the dialog is portaled to a layer under <body>
  // (web/src/components/dialog/dialogStack.ts), so a container-scoped sweep no
  // longer sees it and every negative assertion below would be vacuous. The
  // test's stated intent has always been "the confirm dialog carries no affinity
  // claim"; `container` was only ever a proxy for "what the user sees", and the
  // assertion's scope was narrower than its intent. Line 84's sweep, in the test
  // where no dialog is open, is unaffected and deliberately untouched.
  const html = document.body.innerHTML

  for (const claim of [
    /reserved for/i,
    /dedicated/i,
    /priority/i,
    /exclusive/i,
    /assigned to/i,
    /only .{0,24} can use/i,
  ]) {
    expect(html).not.toMatch(claim)
  }
  // Positive control on the same instrument, on a phrase carried only by the ACTIVE
  // branch of confirmDeleteBody in ReservationsTab.tsx. A control phrase must not
  // also appear in the tab's own explanatory footnote: one that does stays green
  // under exactly the scope error this control exists to catch.
  expect(html).toMatch(/tasks already running on them are unaffected/i)
})

test('shows the loading skeleton, then rows', async () => {
  const seen: URLSearchParams[] = []
  server.use(listHandler(seen, () => ({ items: [row()], next_cursor: '', total: 1 })))
  const { container } = renderTab()
  expect(container.querySelectorAll('.h-9').length).toBeGreaterThan(0)
  expect(await screen.findByText('gpu-farm-hold')).toBeInTheDocument()
})

test('shows an error card whose Retry refetches, and an empty card', async () => {
  let calls = 0
  server.use(
    http.get('/v1/reservations', () => {
      calls++
      if (calls === 1) return HttpResponse.json({ error: 'boom' }, { status: 500 })
      return HttpResponse.json({ items: [], next_cursor: '', total: 0 })
    }),
  )
  renderTab()
  expect(await screen.findByText('500 boom')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Retry' }))
  expect(await screen.findByText('No reservations.')).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: /prev/ })).not.toBeInTheDocument()
})

test('header clicks issue all EIGHT exact sort values and reset the cursor', async () => {
  const seen: URLSearchParams[] = []
  server.use(listHandler(seen, () => ({ items: [row()], next_cursor: 'c2', total: 9 })))
  renderTab()
  await screen.findByText('gpu-farm-hold')

  await userEvent.click(screen.getByRole('button', { name: /next 50/ }))
  await waitFor(() => expect(seen.at(-1)?.get('cursor')).toBe('c2'))

  for (const [label, first, second] of [
    ['NAME', 'name', '-name'],
    ['STARTS', 'starts_at', '-starts_at'],
    ['ENDS', 'ends_at', '-ends_at'],
    ['CREATED', 'created_at', '-created_at'],
  ] as const) {
    await userEvent.click(screen.getByRole('button', { name: new RegExp(`^${label}`) }))
    await waitFor(() => expect(seen.at(-1)?.get('sort')).toBe(first))
    // A cursor issued under one sort is rejected by the server
    // (internal/api/pagination.go:272-286), so paging must reset.
    expect(seen.at(-1)?.has('cursor')).toBe(false)
    await userEvent.click(screen.getByRole('button', { name: new RegExp(`^${label}`) }))
    await waitFor(() => expect(seen.at(-1)?.get('sort')).toBe(second))
  }
})

test('the pager walks the cursor stack and an empty page 2 still offers a way back', async () => {
  const seen: URLSearchParams[] = []
  server.use(
    listHandler(seen, (p) =>
      p.get('cursor')
        ? { items: [], next_cursor: '', total: 1 }
        : { items: [row()], next_cursor: 'c2', total: 1 },
    ),
  )
  renderTab()
  await screen.findByText('gpu-farm-hold')
  expect(screen.getByRole('button', { name: /prev/ })).toBeDisabled()

  await userEvent.click(screen.getByRole('button', { name: /next 50/ }))
  expect(await screen.findByText('No reservations.')).toBeInTheDocument()
  const prevButton = screen.getByRole('button', { name: /prev/ })
  expect(prevButton).toBeEnabled()
  await userEvent.click(prevButton)
  expect(await screen.findByText('gpu-farm-hold')).toBeInTheDocument()
})

test('creating posts the exact body, closes the panel, and refreshes the list', async () => {
  const seen: URLSearchParams[] = []
  let body: unknown
  server.use(
    listHandler(seen, () => ({ items: [row()], next_cursor: '', total: 1 })),
    workersHandler,
    http.post('/v1/reservations', async ({ request }) => {
      body = await request.json()
      return HttpResponse.json({ ...row(), id: 'r9' }, { status: 201 })
    }),
  )
  renderTab()
  await screen.findByText('gpu-farm-hold')
  const before = seen.length

  await userEvent.click(screen.getByRole('button', { name: '+ Reserve workers' }))
  await userEvent.type(await screen.findByLabelText('Name'), 'sim-drain')
  await userEvent.click(await screen.findByRole('checkbox', { name: /render-01/ }))
  await userEvent.click(screen.getByRole('button', { name: 'Reserve' }))

  await waitFor(() => expect(body).toEqual({ name: 'sim-drain', worker_ids: [W1] }))
  // The panel closes and the bare-prefix invalidation refetched the mounted list.
  await waitFor(() => expect(screen.queryByLabelText('Name')).not.toBeInTheDocument())
  await waitFor(() => expect(seen.length).toBeGreaterThan(before))
  // Nothing is revealed: this is not a credential-bearing create.
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
})

test('a create error renders in the panel, and reopening clears it', async () => {
  const seen: URLSearchParams[] = []
  server.use(
    listHandler(seen, () => ({ items: [row()], next_cursor: '', total: 1 })),
    workersHandler,
    http.post('/v1/reservations', () =>
      HttpResponse.json({ error: 'create reservation failed' }, { status: 500 }),
    ),
  )
  renderTab()
  await screen.findByText('gpu-farm-hold')
  await userEvent.click(screen.getByRole('button', { name: '+ Reserve workers' }))
  await userEvent.type(await screen.findByLabelText('Name'), 'sim-drain')
  await userEvent.click(await screen.findByRole('checkbox', { name: /render-01/ }))
  await userEvent.click(screen.getByRole('button', { name: 'Reserve' }))

  expect(await screen.findByText('500 create reservation failed')).toBeInTheDocument()
  expect(screen.getByText('gpu-farm-hold')).toBeInTheDocument()

  await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
  await userEvent.click(screen.getByRole('button', { name: '+ Reserve workers' }))
  expect(screen.queryByText('500 create reservation failed')).not.toBeInTheDocument()
})

test('Delete is gated: Cancel sends NO request, Confirm sends exactly one', async () => {
  const seen: URLSearchParams[] = []
  let deletes = 0
  server.use(
    listHandler(seen, () => ({ items: [row()], next_cursor: '', total: 1 })),
    http.delete('/v1/reservations/:id', () => {
      deletes++
      return new HttpResponse(null, { status: 204 })
    }),
  )
  renderTab()
  await screen.findByText('gpu-farm-hold')

  await userEvent.click(screen.getByRole('button', { name: 'Delete reservation gpu-farm-hold' }))
  const dialog = await screen.findByRole('dialog')
  expect(dialog).toHaveAttribute('aria-modal', 'true')
  expect(dialog).toHaveAccessibleName('Delete reservation "gpu-farm-hold"?')
  // The dialog body states the real effect and its latency.
  const dialogText = (dialog.textContent ?? '').replace(/\s+/g, ' ')
  expect(dialogText).toMatch(/returns its 1 worker/i)
  expect(dialogText).toMatch(/general dispatch pool/i)
  expect(dialogText).toMatch(/next dispatch cycle/i)
  expect(dialogText).toMatch(/already running .* are unaffected/i)

  await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
  await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  expect(deletes).toBe(0)

  // Paired positive control: the same flow CAN issue a request, so the zero above is
  // about the gate and not about an inert button.
  await userEvent.click(screen.getByRole('button', { name: 'Delete reservation gpu-farm-hold' }))
  await userEvent.click(screen.getByRole('button', { name: 'Delete' }))
  await waitFor(() => expect(deletes).toBe(1))
  await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
})

// M2 (review 2026-08-09): the dialog body was unconditional - "returns its N
// worker(s) to the general dispatch pool" - but that effect only exists for a row
// the tab itself derives as ACTIVE. A SCHEDULED or ENDED row was never in
// ListActiveReservations' result, so its workers were never withheld and deleting it
// changes nothing about dispatch; the same sentence also read "returns its 0
// worker(s)" for a worker_ids: [] row. deriveStatus is already computed for the
// STATUS column, so the dialog reuses it rather than asserting an effect the row
// does not have.
test('the delete dialog for a SCHEDULED reservation says deletion has no dispatch effect', async () => {
  const seen: URLSearchParams[] = []
  server.use(
    listHandler(seen, () => ({
      items: [row({ starts_at: '2099-01-01T00:00:00Z' })], // far future -> SCHEDULED
      next_cursor: '',
      total: 1,
    })),
  )
  renderTab()
  await screen.findByText('gpu-farm-hold')
  await userEvent.click(screen.getByRole('button', { name: 'Delete reservation gpu-farm-hold' }))
  const dialog = await screen.findByRole('dialog')
  const text = (dialog.textContent ?? '').replace(/\s+/g, ' ')
  expect(text).toMatch(/not currently in force/i)
  expect(text).toMatch(/does not change dispatch/i)
  expect(text).not.toMatch(/returns its \d+ worker/i)
})

test('the delete dialog for an ENDED reservation says deletion has no dispatch effect', async () => {
  const seen: URLSearchParams[] = []
  server.use(
    listHandler(seen, () => ({
      items: [row({ ends_at: '2000-01-01T00:00:00Z' })], // far past -> ENDED
      next_cursor: '',
      total: 1,
    })),
  )
  renderTab()
  await screen.findByText('gpu-farm-hold')
  await userEvent.click(screen.getByRole('button', { name: 'Delete reservation gpu-farm-hold' }))
  const dialog = await screen.findByRole('dialog')
  const text = (dialog.textContent ?? '').replace(/\s+/g, ' ')
  expect(text).toMatch(/not currently in force/i)
  expect(text).toMatch(/does not change dispatch/i)
  expect(text).not.toMatch(/returns its \d+ worker/i)
})

test('the delete dialog for a reservation holding zero workers says so, not "returns its 0 worker(s)"', async () => {
  const seen: URLSearchParams[] = []
  server.use(
    listHandler(seen, () => ({
      items: [row({ worker_ids: [] })], // ACTIVE (open bounds) but reserves nothing
      next_cursor: '',
      total: 1,
    })),
  )
  renderTab()
  await screen.findByText('gpu-farm-hold')
  await userEvent.click(screen.getByRole('button', { name: 'Delete reservation gpu-farm-hold' }))
  const dialog = await screen.findByRole('dialog')
  const text = (dialog.textContent ?? '').replace(/\s+/g, ' ')
  expect(text).toMatch(/holds no workers/i)
  expect(text).toMatch(/does not change dispatch/i)
  expect(text).not.toMatch(/returns its \d+ worker/i)
})

// Positive control on the same instrument: an ACTIVE row with workers still gets the
// real-effect sentence - the branch above must not have swallowed this case too.
test('the delete dialog for an ACTIVE reservation still states the real dispatch effect', async () => {
  const seen: URLSearchParams[] = []
  server.use(listHandler(seen, () => ({ items: [row()], next_cursor: '', total: 1 })))
  renderTab()
  await screen.findByText('gpu-farm-hold')
  await userEvent.click(screen.getByRole('button', { name: 'Delete reservation gpu-farm-hold' }))
  const dialog = await screen.findByRole('dialog')
  const text = (dialog.textContent ?? '').replace(/\s+/g, ' ')
  expect(text).toMatch(/returns its 1 worker/i)
  expect(text).not.toMatch(/not currently in force/i)
})

test('deleting the SECOND row deletes that row, not the first', async () => {
  const seen: URLSearchParams[] = []
  const deleted: string[] = []
  server.use(
    listHandler(seen, () => ({
      items: [row({ id: 'r1', name: 'gpu-farm-hold' }), row({ id: 'r2', name: 'sim-drain' })],
      next_cursor: '',
      total: 2,
    })),
    http.delete('/v1/reservations/:id', ({ params }) => {
      deleted.push(String(params.id))
      return new HttpResponse(null, { status: 204 })
    }),
  )
  renderTab()
  await screen.findByText('sim-drain')

  await userEvent.click(screen.getByRole('button', { name: 'Delete reservation sim-drain' }))
  expect(await screen.findByRole('dialog')).toHaveAccessibleName('Delete reservation "sim-drain"?')
  await userEvent.click(screen.getByRole('button', { name: 'Delete' }))
  await waitFor(() => expect(deleted).toEqual(['r2']))
})

test('a delete 404 renders in the action-error box and the list still refetches', async () => {
  const seen: URLSearchParams[] = []
  server.use(
    listHandler(seen, () => ({ items: [row()], next_cursor: '', total: 1 })),
    http.delete('/v1/reservations/:id', () =>
      HttpResponse.json({ error: 'reservation not found' }, { status: 404 }),
    ),
  )
  renderTab()
  await screen.findByText('gpu-farm-hold')
  const before = seen.length

  await userEvent.click(screen.getByRole('button', { name: 'Delete reservation gpu-farm-hold' }))
  await userEvent.click(screen.getByRole('button', { name: 'Delete' }))

  expect(await screen.findByText('404 reservation not found')).toBeInTheDocument()
  // onSettled invalidation: the stale row leaves on the refetch, so the message is
  // informational rather than a dead end.
  await waitFor(() => expect(seen.length).toBeGreaterThan(before))
})

test('the 60s tick flips SCHEDULED to ACTIVE with ZERO extra requests', async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true })
  vi.setSystemTime(new Date('2026-08-09T12:00:00Z'))
  const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime })

  const seen: URLSearchParams[] = []
  server.use(
    listHandler(seen, () => ({
      // Starts 5 fake minutes from now.
      items: [row({ starts_at: '2026-08-09T12:05:00Z' })],
      next_cursor: '',
      total: 1,
    })),
  )
  renderTab()
  expect(await screen.findByText('SCHEDULED')).toBeInTheDocument()
  const callsAfterLoad = seen.length

  act(() => {
    vi.advanceTimersByTime(6 * 60_000)
  })
  expect(await screen.findByText('ACTIVE')).toBeInTheDocument()
  // The tick is a local clock, not a refetch.
  expect(seen.length).toBe(callsAfterLoad)

  // Positive control on the SAME counter, in this same test.
  await user.click(screen.getByRole('button', { name: /^NAME/ }))
  await waitFor(() => expect(seen.length).toBeGreaterThan(callsAfterLoad))
})
