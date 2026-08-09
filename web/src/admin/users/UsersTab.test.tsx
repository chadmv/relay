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

// ---------------------------------------------------------------------------
// Dialog hardening (docs/superpowers/specs/2026-08-09-dialog-hardening.md).
// This tab is the only page in the app that can mount two dialogs at once
// through a purely ordinary path - onResetPassword sets `resetting` and
// onArchive sets `confirm`, and neither clears the other - so it is where the
// modal guarantees are measured.
// ---------------------------------------------------------------------------

test('focus is trapped in the reset dialog: ten tabs each way never reach a background control', async () => {
  const seen: URLSearchParams[] = []
  server.use(listHandler(seen, () => ({ items: [user()], next_cursor: '', total: 1 })))
  renderTab()
  await screen.findByText('ada@studio.dev')
  // Captured BEFORE the dialog opens. Once it is open the background carries
  // aria-hidden="true", and @testing-library/dom's role query filters on exactly
  // that (role-helpers.js:33-45), so getByRole could no longer find this button.
  const archive = screen.getByRole('button', { name: 'Archive ada@studio.dev' })

  await userEvent.click(
    screen.getByRole('button', { name: 'Reset password for ada@studio.dev' }),
  )
  const dialog = await screen.findByRole('dialog')
  expect(dialog.contains(document.activeElement)).toBe(true)

  // Ten is more than the dialog's four focusables, so this proves the ring WRAPS
  // rather than merely not having escaped yet.
  for (let i = 0; i < 10; i++) {
    await userEvent.tab()
    expect(dialog.contains(document.activeElement)).toBe(true)
    expect(archive).not.toHaveFocus()
  }
  for (let i = 0; i < 10; i++) {
    await userEvent.tab({ shift: true })
    expect(dialog.contains(document.activeElement)).toBe(true)
    expect(archive).not.toHaveFocus()
  }
})

test('with two dialogs open, one Escape closes exactly one - the topmost', async () => {
  const seen: URLSearchParams[] = []
  server.use(listHandler(seen, () => ({ items: [user()], next_cursor: '', total: 1 })))
  renderTab()
  await screen.findByText('ada@studio.dev')

  // Dialog 1, through the item's real path.
  await userEvent.click(
    screen.getByRole('button', { name: 'Reset password for ada@studio.dev' }),
  )
  await screen.findByRole('dialog')

  // Dialog 2, through the BACKGROUND trigger, with a click and not a tab
  // sequence. Once the trap lands the tab route to this button is unreachable BY
  // DESIGN, so a test that reached it by tabbing could only ever be red. The
  // click route is legitimate rather than synthetic: jsdom performs no
  // hit-testing so the scrim occludes nothing here, and on WorkerDetailPage the
  // scrim genuinely fails to cover the page (its ConfirmDialog sits inside a
  // backdrop-filter ancestor, which becomes the containing block for its
  // position:fixed descendants). It also models every non-tab route to a second
  // dialog: a click landing before paint, an AT activation, a dialog opened by an
  // async result.
  //
  // hidden: true because the open dialog marks the background aria-hidden, which
  // getByRole's accessibility filter honours. The flag makes this query behave
  // identically before and after the fix, so this test measures Escape scoping
  // and nothing else.
  await userEvent.click(
    screen.getByRole('button', { name: 'Archive ada@studio.dev', hidden: true }),
  )
  expect(screen.getAllByRole('dialog')).toHaveLength(2)

  await userEvent.keyboard('{Escape}')

  const survivors = screen.getAllByRole('dialog')
  expect(survivors).toHaveLength(1)
  // By accessible name, NEVER by array index: the DOM order of the two dialogs is
  // ConfirmDialog then ResetPasswordDialog (the JSX order at UsersTab.tsx:278 and
  // :297) regardless of which opened first, so an index assertion tests the wrong
  // property and would pass for the wrong reason.
  expect(survivors[0]).toHaveAccessibleName('Reset password for ada@studio.dev?')
  expect(screen.getByLabelText('New password')).toBeInTheDocument()
})

test('closing a dialog returns focus to the control that opened it', async () => {
  const seen: URLSearchParams[] = []
  server.use(listHandler(seen, () => ({ items: [user()], next_cursor: '', total: 1 })))
  renderTab()
  await screen.findByText('ada@studio.dev')

  const archive = screen.getByRole('button', { name: 'Archive ada@studio.dev' })
  await userEvent.click(archive)
  await screen.findByRole('dialog')
  // The dialog took focus, so the assertion below is about restoration and not
  // about focus that simply never moved.
  expect(archive).not.toHaveFocus()

  await userEvent.keyboard('{Escape}')
  await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())

  expect(archive).toHaveFocus()
})
