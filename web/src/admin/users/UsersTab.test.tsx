import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { afterEach, expect, test } from 'vitest'
import { server } from '../../test/setup-helpers'
import { AuthProvider } from '../../auth/AuthProvider'
import { clearToken, setToken } from '../../lib/token'
import { UsersTab } from './UsersTab'
import type { AdminUser } from './api'

const ME = { id: 'me', email: 'me@studio.dev', name: 'Me', is_admin: true }

function user(over: Partial<AdminUser> = {}): AdminUser {
  return {
    id: 'u1',
    email: 'ada@studio.dev',
    name: 'Ada',
    is_admin: false,
    created_at: '2026-08-01T12:00:00Z',
    archived_at: null,
    ...over,
  }
}

// Records every GET /v1/users request so tests can assert on query params, and
// serves `pages` in order (each entry is one response envelope).
function listHandler(
  seen: URLSearchParams[],
  envelope: (params: URLSearchParams) => { items: AdminUser[]; next_cursor: string; total: number },
) {
  return http.get('/v1/users', ({ request }) => {
    const params = new URL(request.url).searchParams
    seen.push(params)
    return HttpResponse.json(envelope(params))
  })
}

function renderTab() {
  setToken('tok')
  server.use(http.get('/v1/users/me', () => HttpResponse.json(ME)))
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <AuthProvider>
        <UsersTab debounceMs={10} />
      </AuthProvider>
    </QueryClientProvider>,
  )
}

afterEach(() => clearToken())

test('renders rows from the envelope and the endpoint hint', async () => {
  const seen: URLSearchParams[] = []
  server.use(listHandler(seen, () => ({ items: [user()], next_cursor: '', total: 1 })))
  renderTab()
  expect(await screen.findByText('ada@studio.dev')).toBeInTheDocument()
  expect(screen.getByText('GET /v1/users')).toBeInTheDocument()
  expect(seen[0].get('sort')).toBe('-created_at')
  expect(seen[0].has('include_archived')).toBe(false)
})

test('shows the loading skeleton, then the rows', async () => {
  const seen: URLSearchParams[] = []
  server.use(listHandler(seen, () => ({ items: [user()], next_cursor: '', total: 1 })))
  const { container } = renderTab()
  expect(container.querySelectorAll('.h-9').length).toBeGreaterThan(0)
  expect(await screen.findByText('ada@studio.dev')).toBeInTheDocument()
})

test('shows an error card whose Retry refetches', async () => {
  let calls = 0
  server.use(
    http.get('/v1/users', () => {
      calls++
      if (calls === 1) return HttpResponse.json({ error: 'boom' }, { status: 500 })
      return HttpResponse.json({ items: [user()], next_cursor: '', total: 1 })
    }),
  )
  renderTab()
  expect(await screen.findByText('500 boom')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Retry' }))
  expect(await screen.findByText('ada@studio.dev')).toBeInTheDocument()
})

test('toggling include archived sets include_archived=true and resets the cursor', async () => {
  const seen: URLSearchParams[] = []
  server.use(
    listHandler(seen, (params) => ({
      items: [user({ archived_at: params.get('include_archived') ? '2026-08-02T00:00:00Z' : null })],
      next_cursor: 'c2',
      total: 3,
    })),
  )
  renderTab()
  await screen.findByText('ada@studio.dev')
  await userEvent.click(screen.getByRole('button', { name: /next 50/ }))
  await waitFor(() => expect(seen.at(-1)?.get('cursor')).toBe('c2'))

  await userEvent.click(screen.getByLabelText(/include archived/i))
  await waitFor(() => expect(seen.at(-1)?.get('include_archived')).toBe('true'))
  expect(seen.at(-1)?.has('cursor')).toBe(false)
  expect(await screen.findByRole('button', { name: 'Unarchive ada@studio.dev' })).toBeInTheDocument()
})

test('typing in the email filter issues exactly one ?email= request and hides the pager', async () => {
  const seen: URLSearchParams[] = []
  server.use(listHandler(seen, () => ({ items: [user()], next_cursor: 'c2', total: 9 })))
  renderTab()
  await screen.findByText('ada@studio.dev')
  expect(screen.getByRole('button', { name: /next 50/ })).toBeInTheDocument()

  await userEvent.type(screen.getByLabelText('Filter by email'), 'ada@studio.dev')
  // Wait on the full debounced value landing - that is the actual proof the
  // debounce collapsed the keystroke burst into one request. Asserting the raw
  // count with toBe(1) is timing-fragile on a loaded machine (a real 10ms
  // debounce can occasionally observe 2), and since the count only grows,
  // waitFor could never recover from an early over-count; toBeLessThanOrEqual
  // keeps the count check honest without that flakiness.
  await waitFor(() =>
    expect(seen.some((p) => p.get('email') === 'ada@studio.dev')).toBe(true),
  )
  expect(seen.filter((p) => p.has('email')).length).toBeLessThanOrEqual(1)

  // The server returns before parsePage on the ?email= branch, so the pager would
  // claim a page that does not exist.
  await waitFor(() => expect(screen.queryByRole('button', { name: /next 50/ })).not.toBeInTheDocument())
})

test('a filter with no match shows the filtered empty card', async () => {
  const seen: URLSearchParams[] = []
  server.use(
    listHandler(seen, (params) =>
      params.has('email')
        ? { items: [], next_cursor: '', total: 0 }
        : { items: [user()], next_cursor: '', total: 1 },
    ),
  )
  renderTab()
  await screen.findByText('ada@studio.dev')
  await userEvent.type(screen.getByLabelText('Filter by email'), 'nobody@studio.dev')
  expect(await screen.findByText('No users match that email.')).toBeInTheDocument()
})

test('a header click issues the expected sort and resets the cursor', async () => {
  const seen: URLSearchParams[] = []
  server.use(listHandler(seen, () => ({ items: [user()], next_cursor: 'c2', total: 3 })))
  renderTab()
  await screen.findByText('ada@studio.dev')
  await userEvent.click(screen.getByRole('button', { name: /next 50/ }))
  await waitFor(() => expect(seen.at(-1)?.get('cursor')).toBe('c2'))

  await userEvent.click(screen.getByRole('button', { name: /^EMAIL/ }))
  await waitFor(() => expect(seen.at(-1)?.get('sort')).toBe('email'))
  expect(seen.at(-1)?.has('cursor')).toBe(false)

  await userEvent.click(screen.getByRole('button', { name: /^EMAIL/ }))
  await waitFor(() => expect(seen.at(-1)?.get('sort')).toBe('-email'))
})

test('pagination walks the cursor stack and reports the computed range', async () => {
  const seen: URLSearchParams[] = []
  server.use(
    listHandler(seen, (params) =>
      params.get('cursor') === 'c2'
        ? { items: [user({ id: 'u2', email: 'bob@studio.dev' })], next_cursor: '', total: 2 }
        : { items: [user()], next_cursor: 'c2', total: 2 },
    ),
  )
  renderTab()
  expect(await screen.findByText('1-1 of 2')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /prev/ })).toBeDisabled()

  await userEvent.click(screen.getByRole('button', { name: /next 50/ }))
  expect(await screen.findByText('bob@studio.dev')).toBeInTheDocument()
  expect(await screen.findByText('2-2 of 2')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /next 50/ })).toBeDisabled()

  await userEvent.click(screen.getByRole('button', { name: /prev/ }))
  expect(await screen.findByText('ada@studio.dev')).toBeInTheDocument()
  expect(await screen.findByText('1-1 of 2')).toBeInTheDocument()
})

test('creating a user POSTs, closes the form, and refreshes the table', async () => {
  const seen: URLSearchParams[] = []
  let created = false
  let body: unknown
  server.use(
    listHandler(seen, () =>
      created
        ? { items: [user(), user({ id: 'u2', email: 'new@studio.dev', name: 'New' })], next_cursor: '', total: 2 }
        : { items: [user()], next_cursor: '', total: 1 },
    ),
    http.post('/v1/users', async ({ request }) => {
      body = await request.json()
      created = true
      return HttpResponse.json(user({ id: 'u2', email: 'new@studio.dev' }), { status: 201 })
    }),
  )
  renderTab()
  await screen.findByText('ada@studio.dev')
  await userEvent.click(screen.getByRole('button', { name: '+ Create user' }))
  await userEvent.type(screen.getByLabelText('Email'), 'new@studio.dev')
  await userEvent.type(screen.getByLabelText('Password'), 'password1')
  await userEvent.click(screen.getByRole('button', { name: 'Create' }))

  await waitFor(() =>
    expect(body).toEqual({ email: 'new@studio.dev', name: '', password: 'password1', is_admin: false }),
  )
  expect(await screen.findByText('new@studio.dev')).toBeInTheDocument()
  await waitFor(() => expect(screen.queryByLabelText('Password')).not.toBeInTheDocument())
})

test('renaming a row PATCHes and refreshes without a confirm dialog', async () => {
  const seen: URLSearchParams[] = []
  let renamed = false
  server.use(
    listHandler(seen, () => ({ items: [user({ name: renamed ? 'Ada L' : 'Ada' })], next_cursor: '', total: 1 })),
    http.patch('/v1/users/u1', () => {
      renamed = true
      return HttpResponse.json(user({ name: 'Ada L' }))
    }),
  )
  renderTab()
  await screen.findByText('Ada')
  await userEvent.click(screen.getByRole('button', { name: 'Rename ada@studio.dev' }))
  const input = screen.getByLabelText('Name for ada@studio.dev')
  await userEvent.clear(input)
  await userEvent.type(input, 'Ada L')
  await userEvent.click(screen.getByRole('button', { name: 'Save' }))
  expect(await screen.findByText('Ada L')).toBeInTheDocument()
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
})

test('Archive is behind a confirm dialog: Cancel fires no request, Confirm fires one', async () => {
  const seen: URLSearchParams[] = []
  let archiveCalls = 0
  server.use(
    listHandler(seen, () => ({ items: [user()], next_cursor: '', total: 1 })),
    http.post('/v1/users/u1/archive', () => {
      archiveCalls++
      return HttpResponse.json(user({ archived_at: '2026-08-02T00:00:00Z' }))
    }),
  )
  renderTab()
  await screen.findByText('ada@studio.dev')

  await userEvent.click(screen.getByRole('button', { name: 'Archive ada@studio.dev' }))
  expect(screen.getByRole('dialog')).toBeInTheDocument()
  expect(screen.getByText(/revokes all of their API tokens/i)).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
  expect(archiveCalls).toBe(0)

  await userEvent.click(screen.getByRole('button', { name: 'Archive ada@studio.dev' }))
  // The row button is now labelled "Archive ada@studio.dev" (M4) and the dialog's
  // confirm button is plain "Archive", so this no longer needs
  // getAllByRole(...).at(-1) DOM-order trickery to disambiguate them.
  await userEvent.click(screen.getByRole('button', { name: 'Archive' }))
  await waitFor(() => expect(archiveCalls).toBe(1))
})

test('Unarchive is behind a confirm dialog', async () => {
  const seen: URLSearchParams[] = []
  let unarchiveCalls = 0
  server.use(
    listHandler(seen, () => ({
      items: [user({ archived_at: '2026-08-02T00:00:00Z' })],
      next_cursor: '',
      total: 1,
    })),
    http.post('/v1/users/u1/unarchive', () => {
      unarchiveCalls++
      return HttpResponse.json(user())
    }),
  )
  renderTab()
  await userEvent.click(await screen.findByLabelText(/include archived/i))
  await userEvent.click(await screen.findByRole('button', { name: 'Unarchive ada@studio.dev' }))
  expect(screen.getByRole('dialog')).toBeInTheDocument()
  // Row button is "Unarchive ada@studio.dev"; the dialog's confirm button is
  // plain "Unarchive" - unambiguous without DOM-order trickery.
  await userEvent.click(screen.getByRole('button', { name: 'Unarchive' }))
  await waitFor(() => expect(unarchiveCalls).toBe(1))
})

test('Reset pw opens the dialog and POSTs email + new_password', async () => {
  const seen: URLSearchParams[] = []
  let body: unknown
  server.use(
    listHandler(seen, () => ({ items: [user()], next_cursor: '', total: 1 })),
    http.post('/v1/users/password-reset', async ({ request }) => {
      body = await request.json()
      return new HttpResponse(null, { status: 204 })
    }),
  )
  renderTab()
  await screen.findByText('ada@studio.dev')
  await userEvent.click(screen.getByRole('button', { name: 'Reset password for ada@studio.dev' }))
  await userEvent.type(screen.getByLabelText('New password'), 'password1')
  await userEvent.type(screen.getByLabelText('Confirm password'), 'password1')
  await userEvent.click(screen.getByRole('button', { name: 'Reset password' }))

  await waitFor(() =>
    expect(body).toEqual({ email: 'ada@studio.dev', new_password: 'password1' }),
  )
  await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
})

test('a server-guard rejection renders inline and leaves the table mounted', async () => {
  const seen: URLSearchParams[] = []
  server.use(
    listHandler(seen, () => ({ items: [user()], next_cursor: '', total: 1 })),
    http.post('/v1/users/u1/archive', () =>
      HttpResponse.json({ error: 'cannot archive the last active admin' }, { status: 400 }),
    ),
  )
  renderTab()
  await screen.findByText('ada@studio.dev')
  await userEvent.click(screen.getByRole('button', { name: 'Archive ada@studio.dev' }))
  await userEvent.click(screen.getByRole('button', { name: 'Archive' }))

  expect(await screen.findByText('400 cannot archive the last active admin')).toBeInTheDocument()
  expect(screen.getByText('ada@studio.dev')).toBeInTheDocument()
})

test('the acting admin own row has no Archive control', async () => {
  const seen: URLSearchParams[] = []
  server.use(
    listHandler(seen, () => ({
      items: [user({ id: 'me', email: 'me@studio.dev', name: 'Me', is_admin: true })],
      next_cursor: '',
      total: 1,
    })),
  )
  renderTab()
  expect(await screen.findByText('me@studio.dev')).toBeInTheDocument()
  // Wait via polling rather than a single-shot assertion right after the row
  // text appears: /v1/users and /v1/users/me resolve independently, so if the
  // row list settles before AuthProvider's user does, currentUserId is
  // momentarily '' and Archive could transiently render. Polling absence gives
  // the auth fetch a chance to land before this is treated as a failure (L8).
  await waitFor(() =>
    expect(screen.queryByRole('button', { name: 'Archive me@studio.dev' })).not.toBeInTheDocument(),
  )
})

test('reopening the create form after a duplicate-email error clears the stale error', async () => {
  const seen: URLSearchParams[] = []
  server.use(
    listHandler(seen, () => ({ items: [user()], next_cursor: '', total: 1 })),
    http.post('/v1/users', () =>
      HttpResponse.json({ error: 'email already registered' }, { status: 409 }),
    ),
  )
  renderTab()
  await screen.findByText('ada@studio.dev')

  await userEvent.click(screen.getByRole('button', { name: '+ Create user' }))
  await userEvent.type(screen.getByLabelText('Email'), 'dupe@studio.dev')
  await userEvent.type(screen.getByLabelText('Password'), 'password1')
  await userEvent.click(screen.getByRole('button', { name: 'Create' }))
  expect(await screen.findByText('That email is already registered.')).toBeInTheDocument()

  await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
  await userEvent.click(screen.getByRole('button', { name: '+ Create user' }))

  expect(screen.queryByText('That email is already registered.')).not.toBeInTheDocument()
})

test('reopening the reset-password dialog after a failed reset clears the stale error', async () => {
  const seen: URLSearchParams[] = []
  server.use(
    listHandler(seen, () => ({ items: [user()], next_cursor: '', total: 1 })),
    http.post('/v1/users/password-reset', () =>
      HttpResponse.json({ error: 'internal error' }, { status: 500 }),
    ),
  )
  renderTab()
  await screen.findByText('ada@studio.dev')

  await userEvent.click(screen.getByRole('button', { name: 'Reset password for ada@studio.dev' }))
  await userEvent.type(screen.getByLabelText('New password'), 'password1')
  await userEvent.type(screen.getByLabelText('Confirm password'), 'password1')
  await userEvent.click(screen.getByRole('button', { name: 'Reset password' }))
  expect(await screen.findByText('500 internal error')).toBeInTheDocument()

  await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
  await userEvent.click(screen.getByRole('button', { name: 'Reset password for ada@studio.dev' }))

  expect(screen.queryByText('500 internal error')).not.toBeInTheDocument()
})

test('the footnote explains the archive and reset side effects', async () => {
  const seen: URLSearchParams[] = []
  server.use(listHandler(seen, () => ({ items: [user()], next_cursor: '', total: 1 })))
  renderTab()
  await screen.findByText('ada@studio.dev')
  expect(screen.getByText(/Server guards prevent archiving yourself or the last active admin/i)).toBeInTheDocument()
})
