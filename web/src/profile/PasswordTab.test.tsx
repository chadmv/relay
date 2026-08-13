import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { AuthProvider } from '../auth/AuthProvider'
import { clearToken, setToken } from '../lib/token'
import { PasswordTab } from './PasswordTab'

afterEach(() => clearToken())

const ME = {
  id: 'u1',
  email: 'mira@studio.dev',
  name: 'Mira Sato',
  is_admin: true,
  created_at: '2025-04-02T09:15:00Z',
  archived_at: null,
}

function renderTab() {
  setToken('tok')
  server.use(http.get('/v1/users/me', () => HttpResponse.json(ME)))
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  const utils = render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={['/profile/password']}>
        <AuthProvider>
          <PasswordTab />
        </AuthProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  )
  return { ...utils, client }
}

async function fill(current: string, next: string, confirm: string) {
  await userEvent.type(screen.getByLabelText('Current password'), current)
  await userEvent.type(screen.getByLabelText('New password'), next)
  await userEvent.type(screen.getByLabelText('Confirm new password'), confirm)
}

// The nearest ancestor <div> of a labelled control is that control's Field
// wrapper (Field.tsx renders label + control + hint + error inside one
// "mb-3" div, and Input renders a bare <input> with no wrapping div of its
// own), so this scopes an assertion to "this field's group" without adding a
// testid that does not exist in the shipped markup.
function fieldGroup(labelText: string): HTMLElement {
  return screen.getByLabelText(labelText).closest('div') as HTMLElement
}

function countingHandler(counter: { n: number; body?: Record<string, unknown> }) {
  return http.put('/v1/users/me/password', async ({ request }) => {
    counter.n++
    counter.body = (await request.json()) as Record<string, unknown>
    return new HttpResponse(null, { status: 204 })
  })
}

test('a valid submit sends EXACTLY current_password and new_password', async () => {
  const c = { n: 0 } as { n: number; body?: Record<string, unknown> }
  server.use(countingHandler(c))
  renderTab()
  await fill('old-secret', 'new-secret-123', 'new-secret-123')
  await userEvent.click(screen.getByRole('button', { name: 'Update password' }))

  await waitFor(() => expect(c.n).toBe(1))
  expect(c.body).toEqual({ current_password: 'old-secret', new_password: 'new-secret-123' })
  // The form has three fields and the API takes two. Assert the key set so a
  // stray confirm_password cannot pass.
  expect(Object.keys(c.body!).sort()).toEqual(['current_password', 'new_password'])
})

test('a 204 clears all three inputs and shows a success line', async () => {
  const c = { n: 0 } as { n: number; body?: Record<string, unknown> }
  server.use(countingHandler(c))
  renderTab()
  await fill('old-secret', 'new-secret-123', 'new-secret-123')
  await userEvent.click(screen.getByRole('button', { name: 'Update password' }))

  await waitFor(() => expect(screen.getByLabelText('Current password')).toHaveValue(''))
  expect(screen.getByLabelText('New password')).toHaveValue('')
  expect(screen.getByLabelText('Confirm new password')).toHaveValue('')
  expect(screen.getByRole('status')).toHaveTextContent(
    'Password updated. Your other sessions have been signed out.',
  )
})

test('a confirm mismatch blocks the request and is announced on the Confirm field', async () => {
  const c = { n: 0 } as { n: number; body?: Record<string, unknown> }
  server.use(countingHandler(c))
  renderTab()
  await fill('old-secret', 'new-secret-123', 'new-secret-124')
  await userEvent.click(screen.getByRole('button', { name: 'Update password' }))

  // The mismatch is genuinely about Confirm, so it belongs there - pinned with
  // within() so a future regression that moves it to New password fails here
  // instead of passing a bare getByText.
  expect(
    within(fieldGroup('Confirm new password')).getByText('The two passwords do not match.'),
  ).toBeInTheDocument()
  expect(
    within(fieldGroup('New password')).queryByText('The two passwords do not match.'),
  ).toBeNull()
  const alert = screen.getByRole('alert')
  expect(alert).toHaveTextContent('The two passwords do not match.')
  expect(screen.getByLabelText('Confirm new password')).toHaveAttribute(
    'aria-describedby',
    alert.id,
  )
  expect(c.n).toBe(0)
})

test('a 7-character new password blocks the request, announced on New password (not Confirm)', async () => {
  const c = { n: 0 } as { n: number; body?: Record<string, unknown> }
  server.use(countingHandler(c))
  renderTab()
  await fill('old-secret', 'short12', 'short12')
  await userEvent.click(screen.getByRole('button', { name: 'Update password' }))

  // The exact string already at RegisterScreen.tsx:31-32, ResetPasswordDialog.tsx:36
  // and CreateUserForm.tsx:40. Copied to a fourth site by design (spec decision 11).
  // This message is about the NEW password, so it must render on that field's
  // group, not Confirm's - within() pins the placement, not just the text.
  expect(
    within(fieldGroup('New password')).getByText('Password must be at least 8 characters.'),
  ).toBeInTheDocument()
  expect(
    within(fieldGroup('Confirm new password')).queryByText(
      'Password must be at least 8 characters.',
    ),
  ).toBeNull()
  const alert = screen.getByRole('alert')
  expect(alert).toHaveTextContent('Password must be at least 8 characters.')
  expect(screen.getByLabelText('New password')).toHaveAttribute('aria-describedby', alert.id)
  expect(c.n).toBe(0)
})

test('an 8-character new password IS sent (positive control on the min-8 guard)', async () => {
  const c = { n: 0 } as { n: number; body?: Record<string, unknown> }
  server.use(countingHandler(c))
  renderTab()
  await fill('old-secret', 'eightchr', 'eightchr')
  await userEvent.click(screen.getByRole('button', { name: 'Update password' }))
  await waitFor(() => expect(c.n).toBe(1))
})

test('a 73-byte new password blocks the request - BYTE length, announced on New password', async () => {
  const c = { n: 0 } as { n: number; body?: Record<string, unknown> }
  server.use(countingHandler(c))
  renderTab()
  // 37 characters, 74 BYTES in UTF-8. Deliberately not 73 ASCII characters: with
  // ASCII the test cannot distinguish TextEncoder().encode(x).length from
  // x.length, and a .length guard would pass it. bcrypt rejects over 72 bytes and
  // handleChangePassword turns that into an opaque 500 (internal/api/auth.go:303-307).
  const pw = 'é'.repeat(37)
  expect(pw).toHaveLength(37)
  expect(new TextEncoder().encode(pw).length).toBe(74)
  await fill('old-secret', pw, pw)
  await userEvent.click(screen.getByRole('button', { name: 'Update password' }))

  // Byte-length is also a New-password fact, so it belongs on that field's
  // group, not Confirm's.
  expect(
    within(fieldGroup('New password')).getByText('Password must be 72 bytes or fewer.'),
  ).toBeInTheDocument()
  expect(
    within(fieldGroup('Confirm new password')).queryByText('Password must be 72 bytes or fewer.'),
  ).toBeNull()
  expect(c.n).toBe(0)
})

test('a 72-byte new password IS sent (positive control on the byte guard)', async () => {
  const c = { n: 0 } as { n: number; body?: Record<string, unknown> }
  server.use(countingHandler(c))
  renderTab()
  const pw = 'é'.repeat(36)
  expect(new TextEncoder().encode(pw).length).toBe(72)
  await fill('old-secret', pw, pw)
  await userEvent.click(screen.getByRole('button', { name: 'Update password' }))
  await waitFor(() => expect(c.n).toBe(1))
})

test('Cancel clears all three inputs and issues nothing', async () => {
  const c = { n: 0 } as { n: number; body?: Record<string, unknown> }
  server.use(countingHandler(c))
  renderTab()
  await fill('old-secret', 'new-secret-123', 'new-secret-123')
  await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))

  expect(screen.getByLabelText('Current password')).toHaveValue('')
  expect(screen.getByLabelText('New password')).toHaveValue('')
  expect(screen.getByLabelText('Confirm new password')).toHaveValue('')
  expect(c.n).toBe(0)
})

test('the warning states OTHER sessions are signed out and this browser stays signed in', async () => {
  renderTab()
  const warning = screen.getByTestId('password-session-warning')
  // Verified against DeleteOtherTokensForUser (internal/api/auth.go:325-328 ->
  // internal/store/query/tokens.sql:28-29 `AND id <> $2`). This is the ONE place
  // the hi-fi's session copy is correct (hifi3-holo-pages.jsx:3010-3012).
  expect(warning).toHaveTextContent(/other/i)
  expect(warning).toHaveTextContent(/this browser stays signed in/i)
})

test('there is NO strength meter', async () => {
  renderTab()
  // The server's only rule is len(new) >= 8 (internal/api/auth.go:284-287). A
  // meter reading "mixed case - 1 number" (hifi3-holo-pages.jsx:3003) would
  // assert a policy that does not exist anywhere in the codebase.
  expect(screen.queryByText(/strong|weak|mixed case/i)).toBeNull()
  // Paired positive on the same instrument: the honest hint IS rendered, so the
  // absence assertion is not about an empty component.
  expect(screen.getByText('min 8 characters')).toBeInTheDocument()
})
