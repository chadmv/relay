import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { afterEach, expect, test, vi } from 'vitest'
import { server } from '../../test/setup-helpers'
import { EnrollmentsTab } from './EnrollmentsTab'
import type { AgentEnrollment } from './api'

const TOKEN = 'f00dcafe'.repeat(8)

function row(over: Partial<AgentEnrollment> = {}): AgentEnrollment {
  return {
    id: 'e1',
    created_at: '2026-08-09T09:30:00Z',
    expires_at: '2026-08-10T09:42:00Z',
    created_by: 'u1',
    hostname_hint: 'farm-west-13',
    ...over,
  }
}

// EnrollmentsTab does not use useAuth, so no AuthProvider and no /v1/users/me
// handler are needed - unlike the UsersTab tests.
function renderTab() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return {
    client,
    ...render(
      <QueryClientProvider client={client}>
        <EnrollmentsTab />
      </QueryClientProvider>,
    ),
  }
}

function listHandler(
  seen: URLSearchParams[],
  envelope: (p: URLSearchParams) => { items: AgentEnrollment[]; next_cursor: string; total: number },
) {
  return http.get('/v1/agent-enrollments', ({ request }) => {
    const params = new URL(request.url).searchParams
    seen.push(params)
    return HttpResponse.json(envelope(params))
  })
}

afterEach(() => vi.useRealTimers())

test('renders rows, the endpoint hint, and the default sort', async () => {
  const seen: URLSearchParams[] = []
  server.use(listHandler(seen, () => ({ items: [row()], next_cursor: '', total: 1 })))
  renderTab()
  expect(await screen.findByText('farm-west-13')).toBeInTheDocument()
  expect(screen.getByText('GET /v1/agent-enrollments')).toBeInTheDocument()
  expect(seen[0].get('sort')).toBe('-created_at')
  expect(seen[0].get('limit')).toBe('50')
})

test('shows the loading skeleton, then rows', async () => {
  const seen: URLSearchParams[] = []
  server.use(listHandler(seen, () => ({ items: [row()], next_cursor: '', total: 1 })))
  const { container } = renderTab()
  expect(container.querySelectorAll('.h-9').length).toBeGreaterThan(0)
  expect(await screen.findByText('farm-west-13')).toBeInTheDocument()
})

test('shows an error card whose Retry refetches', async () => {
  let calls = 0
  server.use(
    http.get('/v1/agent-enrollments', () => {
      calls++
      if (calls === 1) return HttpResponse.json({ error: 'boom' }, { status: 500 })
      return HttpResponse.json({ items: [row()], next_cursor: '', total: 1 })
    }),
  )
  renderTab()
  expect(await screen.findByText('500 boom')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Retry' }))
  expect(await screen.findByText('farm-west-13')).toBeInTheDocument()
})

test('shows the empty card when there are no active enrollments', async () => {
  const seen: URLSearchParams[] = []
  server.use(listHandler(seen, () => ({ items: [], next_cursor: '', total: 0 })))
  renderTab()
  expect(await screen.findByText('No active enrollments.')).toBeInTheDocument()
})

test('sort header clicks issue the four exact server sort values and reset the cursor', async () => {
  const seen: URLSearchParams[] = []
  server.use(listHandler(seen, () => ({ items: [row()], next_cursor: 'c2', total: 9 })))
  renderTab()
  await screen.findByText('farm-west-13')

  await userEvent.click(screen.getByRole('button', { name: /next 50/ }))
  await waitFor(() => expect(seen.at(-1)?.get('cursor')).toBe('c2'))

  await userEvent.click(screen.getByRole('button', { name: /^CREATED/ }))
  await waitFor(() => expect(seen.at(-1)?.get('sort')).toBe('created_at'))
  // A cursor issued under one sort is rejected by the server
  // (internal/api/pagination.go:272-286), so paging must reset.
  expect(seen.at(-1)?.has('cursor')).toBe(false)

  await userEvent.click(screen.getByRole('button', { name: /^EXPIRES/ }))
  await waitFor(() => expect(seen.at(-1)?.get('sort')).toBe('expires_at'))
  await userEvent.click(screen.getByRole('button', { name: /^EXPIRES/ }))
  await waitFor(() => expect(seen.at(-1)?.get('sort')).toBe('-expires_at'))
})

test('the pager walks the cursor stack and the footer range tracks the offset', async () => {
  const seen: URLSearchParams[] = []
  server.use(
    listHandler(seen, (p) => ({
      items: [row({ id: p.get('cursor') ? 'e2' : 'e1', hostname_hint: p.get('cursor') ? 'page-two' : 'page-one' })],
      next_cursor: p.get('cursor') ? '' : 'c2',
      total: 2,
    })),
  )
  renderTab()
  await screen.findByText('page-one')
  expect(screen.getByText('1-1 of 2')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /prev/ })).toBeDisabled()

  await userEvent.click(screen.getByRole('button', { name: /next 50/ }))
  expect(await screen.findByText('page-two')).toBeInTheDocument()
  expect(screen.getByText('2-2 of 2')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /next 50/ })).toBeDisabled()

  await userEvent.click(screen.getByRole('button', { name: /prev/ }))
  expect(await screen.findByText('page-one')).toBeInTheDocument()
  expect(screen.getByText('1-1 of 2')).toBeInTheDocument()
})

test('a non-first page that comes back empty still offers a way back (active-only rows can vanish mid-page)', async () => {
  // This is the reachable case UsersTab does not have as sharply: the list is
  // active-only, so a row can be consumed or expire between paging forward and
  // the next fetch, landing the admin on an empty page-2 with no rows to page
  // from. Without a visible prev control there, a reload is the only way out.
  const seen: URLSearchParams[] = []
  server.use(
    listHandler(seen, (p) =>
      p.get('cursor')
        ? { items: [], next_cursor: '', total: 1 }
        : { items: [row()], next_cursor: 'c2', total: 1 },
    ),
  )
  renderTab()
  await screen.findByText('farm-west-13')

  await userEvent.click(screen.getByRole('button', { name: /next 50/ }))
  expect(await screen.findByText('No active enrollments.')).toBeInTheDocument()

  const prevButton = screen.getByRole('button', { name: /prev/ })
  expect(prevButton).toBeEnabled()
  await userEvent.click(prevButton)
  expect(await screen.findByText('farm-west-13')).toBeInTheDocument()
})

test('the empty card on the FIRST page (no rows ever) shows no prev control', async () => {
  const seen: URLSearchParams[] = []
  server.use(listHandler(seen, () => ({ items: [], next_cursor: '', total: 0 })))
  renderTab()
  expect(await screen.findByText('No active enrollments.')).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: /prev/ })).not.toBeInTheDocument()
})

test('creating posts the exact body, opens the reveal dialog, and refreshes the list', async () => {
  const seen: URLSearchParams[] = []
  let body: unknown
  server.use(
    listHandler(seen, () => ({ items: [row()], next_cursor: '', total: 1 })),
    http.post('/v1/agent-enrollments', async ({ request }) => {
      body = await request.json()
      return HttpResponse.json({ id: 'e9', token: TOKEN, expires_at: row().expires_at }, { status: 201 })
    }),
  )
  renderTab()
  await screen.findByText('farm-west-13')
  const listCallsBefore = seen.length

  await userEvent.click(screen.getByRole('button', { name: '+ Enroll agent' }))
  await userEvent.type(screen.getByLabelText('Hostname hint'), 'farm-east-01')
  await userEvent.click(screen.getByRole('button', { name: 'Enroll' }))

  const dialog = await screen.findByRole('dialog')
  expect(body).toEqual({ hostname_hint: 'farm-east-01', ttl_seconds: 86400 })
  expect(screen.getByLabelText('Token')).toHaveValue(TOKEN)
  expect(dialog).toHaveTextContent(/cannot be retrieved again/i)
  // The 201's expires_at (row().expires_at here) is threaded through to the
  // reveal dialog so the admin knows when the credential stops working.
  expect(dialog).toHaveTextContent(/expires/i)
  // The inline panel closes behind the dialog.
  expect(screen.queryByLabelText('Hostname hint')).not.toBeInTheDocument()
  // The bare-prefix invalidation refetched the mounted list.
  await waitFor(() => expect(seen.length).toBeGreaterThan(listCallsBefore))

  await userEvent.click(screen.getByRole('button', { name: /I have copied it/ }))
  await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
})

test('the create toggle and Cancel are disabled while a create is pending, so an in-flight request cannot be abandoned', async () => {
  const seen: URLSearchParams[] = []
  let resolvePost!: (value: Response) => void
  const postGate = new Promise<Response>((resolve) => {
    resolvePost = resolve
  })
  let posts = 0
  server.use(
    listHandler(seen, () => ({ items: [row()], next_cursor: '', total: 1 })),
    http.post('/v1/agent-enrollments', async () => {
      posts++
      return postGate
    }),
  )
  renderTab()
  await screen.findByText('farm-west-13')

  await userEvent.click(screen.getByRole('button', { name: '+ Enroll agent' }))
  await userEvent.click(screen.getByRole('button', { name: 'Enroll' }))

  // Same gap as the invites tab this pattern was copied from: neither the outer
  // toggle nor the panel's Cancel was disabled while pending, so a click on
  // either called create.reset() and detached the mutation observer before the
  // response landed - MutationObserver.reset() only detaches, it does not cancel
  // the in-flight Mutation.execute - stranding a permanently unusable enrollment
  // token the admin never saw.
  await waitFor(() =>
    expect(screen.getByRole('button', { name: '+ Enroll agent' })).toBeDisabled(),
  )
  expect(screen.getByRole('button', { name: 'Cancel' })).toBeDisabled()

  resolvePost(
    HttpResponse.json({ id: 'e9', token: TOKEN, expires_at: row().expires_at }, { status: 201 }),
  )

  await screen.findByRole('dialog')
  expect(posts).toBe(1)
  expect(screen.getByLabelText('Token')).toHaveValue(TOKEN)

  await userEvent.click(screen.getByRole('button', { name: /I have copied it/ }))
  await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
})

test('a create error renders inside the panel and leaves the table mounted', async () => {
  const seen: URLSearchParams[] = []
  server.use(
    listHandler(seen, () => ({ items: [row()], next_cursor: '', total: 1 })),
    http.post('/v1/agent-enrollments', () =>
      HttpResponse.json({ error: 'failed to create enrollment' }, { status: 500 }),
    ),
  )
  renderTab()
  await screen.findByText('farm-west-13')
  await userEvent.click(screen.getByRole('button', { name: '+ Enroll agent' }))
  await userEvent.click(screen.getByRole('button', { name: 'Enroll' }))

  expect(await screen.findByText('500 failed to create enrollment')).toBeInTheDocument()
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  expect(screen.getByText('farm-west-13')).toBeInTheDocument()

  // Reopening the panel clears the stale error - the reset()-before-reopen
  // convention from UsersTab.tsx:238-245.
  await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
  await userEvent.click(screen.getByRole('button', { name: '+ Enroll agent' }))
  expect(screen.queryByText('500 failed to create enrollment')).not.toBeInTheDocument()
})

test('the 60s tick flips EXPIRING to EXPIRED with ZERO extra requests', async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true })
  vi.setSystemTime(new Date('2026-08-09T12:00:00Z'))
  const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime })

  const seen: URLSearchParams[] = []
  server.use(
    listHandler(seen, () => ({
      // 30 minutes of life left at the fake now.
      items: [row({ expires_at: '2026-08-09T12:30:00Z' })],
      next_cursor: '',
      total: 1,
    })),
  )
  renderTab()
  expect(await screen.findByText('EXPIRING')).toBeInTheDocument()
  const callsAfterLoad = seen.length

  // 31 fake minutes: five useNow ticks past the expiry.
  act(() => {
    vi.advanceTimersByTime(31 * 60_000)
  })
  expect(await screen.findByText('EXPIRED')).toBeInTheDocument()
  // The tick is a local clock, not a refetch.
  expect(seen.length).toBe(callsAfterLoad)

  // Positive control on the SAME counter, inside this same test: it can move, so
  // the equality above is about the tick and not about a dead instrument.
  await user.click(screen.getByRole('button', { name: /^EXPIRES/ }))
  await waitFor(() => expect(seen.length).toBeGreaterThan(callsAfterLoad))
})
