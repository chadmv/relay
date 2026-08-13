import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { afterEach, expect, test, vi } from 'vitest'
import { server } from '../../test/setup-helpers'
import { InvitesTab } from './InvitesTab'
import type { Invite } from './api'

const TOKEN = 'f00dcafe'.repeat(8)

function row(over: Partial<Invite> = {}): Invite {
  return {
    id: 'i1',
    created_at: '2026-08-01T09:00:00Z',
    expires_at: '2026-08-10T09:00:00Z',
    created_by: 'u1',
    created_by_email: 'admin@studio.dev',
    email: 'invitee@studio.dev',
    ...over,
  }
}

// InvitesTab does not use useAuth, so no AuthProvider and no /v1/users/me handler
// are needed - unlike the UsersTab tests.
function renderTab() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return {
    client,
    ...render(
      <QueryClientProvider client={client}>
        <InvitesTab />
      </QueryClientProvider>,
    ),
  }
}

function listHandler(
  seen: URLSearchParams[],
  envelope: (p: URLSearchParams) => { items: Invite[]; next_cursor: string; total: number },
) {
  return http.get('/v1/invites', ({ request }) => {
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
  expect(await screen.findByText('invitee@studio.dev')).toBeInTheDocument()
  expect(screen.getByText('GET /v1/invites')).toBeInTheDocument()
  expect(seen[0].get('sort')).toBe('-created_at')
  expect(seen[0].get('limit')).toBe('50')
})

test('shows the loading skeleton, then rows', async () => {
  const seen: URLSearchParams[] = []
  server.use(listHandler(seen, () => ({ items: [row()], next_cursor: '', total: 1 })))
  const { container } = renderTab()
  expect(container.querySelectorAll('.h-9').length).toBeGreaterThan(0)
  expect(await screen.findByText('invitee@studio.dev')).toBeInTheDocument()
})

test('shows an error card whose Retry refetches', async () => {
  let calls = 0
  server.use(
    http.get('/v1/invites', () => {
      calls++
      if (calls === 1) return HttpResponse.json({ error: 'boom' }, { status: 500 })
      return HttpResponse.json({ items: [row()], next_cursor: '', total: 1 })
    }),
  )
  renderTab()
  expect(await screen.findByText('500 boom')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Retry' }))
  expect(await screen.findByText('invitee@studio.dev')).toBeInTheDocument()
})

test('shows the empty card when there are no invites, with no prev hatch', async () => {
  const seen: URLSearchParams[] = []
  server.use(listHandler(seen, () => ({ items: [], next_cursor: '', total: 0 })))
  renderTab()
  expect(await screen.findByText('No invites yet.')).toBeInTheDocument()
  // Unlike EnrollmentsTab, this list is UNFILTERED and nothing deletes or reaps an
  // invite, so a non-first page landing on zero rows is unreachable. Shipping an
  // untestable escape hatch would be dead code (spec decision 17).
  expect(screen.queryByRole('button', { name: /prev/ })).not.toBeInTheDocument()
})

test('sort header clicks issue the four exact server sort values and reset the cursor', async () => {
  const seen: URLSearchParams[] = []
  server.use(listHandler(seen, () => ({ items: [row()], next_cursor: 'c2', total: 9 })))
  renderTab()
  await screen.findByText('invitee@studio.dev')

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
      items: [
        row({
          id: p.get('cursor') ? 'i2' : 'i1',
          email: p.get('cursor') ? 'page-two@studio.dev' : 'page-one@studio.dev',
        }),
      ],
      next_cursor: p.get('cursor') ? '' : 'c2',
      total: 2,
    })),
  )
  renderTab()
  await screen.findByText('page-one@studio.dev')
  expect(screen.getByText('1-1 of 2')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /prev/ })).toBeDisabled()

  await userEvent.click(screen.getByRole('button', { name: /next 50/ }))
  expect(await screen.findByText('page-two@studio.dev')).toBeInTheDocument()
  expect(screen.getByText('2-2 of 2')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /next 50/ })).toBeDisabled()

  await userEvent.click(screen.getByRole('button', { name: /prev/ }))
  expect(await screen.findByText('page-one@studio.dev')).toBeInTheDocument()
  expect(screen.getByText('1-1 of 2')).toBeInTheDocument()
})

test('the footer names the endpoint and says ALL states are shown', async () => {
  const seen: URLSearchParams[] = []
  server.use(listHandler(seen, () => ({ items: [row()], next_cursor: '', total: 1 })))
  renderTab()
  await screen.findByText('invitee@studio.dev')
  // Unlike /v1/agent-enrollments, this endpoint applies NO filter
  // (internal/api/invites.go:148-250 reads no filter param), so every state is on
  // the page and `total` is the unfiltered COUNT(*) (invites.sql:55-61) - the
  // footer range can never state a number the admin cannot page to.
  expect(screen.getByText(/\/v1\/invites \(all states\)/)).toBeInTheDocument()
  expect(screen.queryByText(/active only/)).not.toBeInTheDocument()
})

test('the footnote states that expiry and redemption are the only terminal states', async () => {
  const seen: URLSearchParams[] = []
  server.use(listHandler(seen, () => ({ items: [row()], next_cursor: '', total: 1 })))
  renderTab()
  await screen.findByText('invitee@studio.dev')
  expect(screen.getByText(/one-time/i)).toBeInTheDocument()
  expect(screen.getByText(/no revoke endpoint in v1/i)).toBeInTheDocument()
  expect(screen.getByText(/only terminal states/i)).toBeInTheDocument()
})

test('the tab renders no revoke, delete or resend control at all', async () => {
  const seen: URLSearchParams[] = []
  server.use(listHandler(seen, () => ({ items: [row()], next_cursor: '', total: 1 })))
  renderTab()
  await screen.findByText('invitee@studio.dev')
  expect(screen.queryAllByRole('button', { name: /revoke|delete|resend/i })).toHaveLength(0)
})

test('creating posts the exact body, opens the reveal dialog, and refreshes the list', async () => {
  const seen: URLSearchParams[] = []
  let body: unknown
  server.use(
    listHandler(seen, () => ({ items: [row()], next_cursor: '', total: 1 })),
    http.post('/v1/invites', async ({ request }) => {
      body = await request.json()
      return HttpResponse.json(
        { id: 'i9', token: TOKEN, expires_at: row().expires_at, email: 'new@studio.dev' },
        { status: 201 },
      )
    }),
  )
  renderTab()
  await screen.findByText('invitee@studio.dev')
  const listCallsBefore = seen.length

  await userEvent.click(screen.getByRole('button', { name: '+ Create invite' }))
  await userEvent.type(screen.getByLabelText('Email'), 'new@studio.dev')
  await userEvent.click(screen.getByRole('button', { name: 'Create invite' }))

  const dialog = await screen.findByRole('dialog')
  expect(body).toEqual({ email: 'new@studio.dev', expires_in: '72h' })
  expect(screen.getByLabelText('Token')).toHaveValue(TOKEN)
  expect(dialog).toHaveTextContent(/cannot be retrieved again/i)
  expect(dialog).toHaveTextContent(/expires/i)
  // The inline panel closes behind the dialog.
  expect(screen.queryByLabelText('Email')).not.toBeInTheDocument()
  // The bare-prefix invalidation refetched the mounted list.
  await waitFor(() => expect(seen.length).toBeGreaterThan(listCallsBefore))

  await userEvent.click(screen.getByRole('button', { name: /I have copied it/ }))
  await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
})

test('a create error renders inside the panel and leaves the table mounted', async () => {
  const seen: URLSearchParams[] = []
  server.use(
    listHandler(seen, () => ({ items: [row()], next_cursor: '', total: 1 })),
    http.post('/v1/invites', () =>
      HttpResponse.json({ error: 'invalid email address' }, { status: 400 }),
    ),
  )
  renderTab()
  await screen.findByText('invitee@studio.dev')
  await userEvent.click(screen.getByRole('button', { name: '+ Create invite' }))
  await userEvent.click(screen.getByRole('button', { name: 'Create invite' }))

  expect(await screen.findByText('400 invalid email address')).toBeInTheDocument()
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  expect(screen.getByText('invitee@studio.dev')).toBeInTheDocument()

  // Reopening the panel clears the stale error - and, critically, a stale
  // create.data that would otherwise re-open the reveal dialog.
  await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
  await userEvent.click(screen.getByRole('button', { name: '+ Create invite' }))
  expect(screen.queryByText('400 invalid email address')).not.toBeInTheDocument()
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
