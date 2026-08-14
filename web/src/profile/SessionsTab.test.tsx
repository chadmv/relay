import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { AuthProvider } from '../auth/AuthProvider'
import { clearToken, setToken } from '../lib/token'
import { SessionsTab } from './SessionsTab'

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
      <MemoryRouter initialEntries={['/profile/sessions']}>
        <AuthProvider>
          <SessionsTab />
        </AuthProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  )
  return { ...utils, client }
}

test('renders NO session list and issues no request for one', async () => {
  let listCalls = 0
  server.use(
    // Registered so a call is COUNTED. Without a handler, MSW's
    // onUnhandledRequest:'error' (web/src/test/setup.ts:5) would also fail the
    // test, but a counter names the route.
    http.get('/v1/auth/tokens', () => {
      listCalls++
      return HttpResponse.json({ items: [] })
    }),
  )
  renderTab()
  // GET /v1/auth/tokens exists (internal/api/server.go:103) but this tab holds no
  // query against it - the list UI is simply not built here yet, not blocked on a
  // missing endpoint. api_tokens also still has no last_used_at, agent, IP or
  // location column (internal/store/migrations/000001_initial.up.sql:13-19), so
  // even a built list would be minimal. Mirrors EnrollmentsTable.test.tsx:74-78.
  expect(listCalls).toBe(0)
  expect(screen.queryByRole('table')).toBeNull()
  expect(screen.queryByRole('columnheader')).toBeNull()
  expect(screen.queryByRole('button', { name: /revoke/i })).toBeNull()
  // Paired positive: the action IS present, so none of the four assertions above
  // can be passing against an empty component.
  expect(screen.getByRole('button', { name: 'Sign out everywhere' })).toBeInTheDocument()
})

test('the button is labelled "Sign out everywhere" and never says "else"', async () => {
  const { container } = renderTab()
  expect(screen.getByRole('button', { name: 'Sign out everywhere' })).toBeInTheDocument()
  // The hi-fi's label is "Sign out everywhere else" (hifi3-holo-pages.jsx:3049)
  // and describes an endpoint that does not exist: DeleteTokensForUser has no
  // `id <> $2` (internal/store/query/tokens.sql:40-41). Anyone implementing from
  // the mockup rather than the spec ships it. Assert over the whole subtree, not
  // just the accessible name, so it cannot hide in the copy either.
  expect(container.textContent).not.toMatch(/everywhere else/i)
  expect(screen.queryByRole('button', { name: /everywhere else/i })).toBeNull()
})

test('the page copy states that this browser is included', async () => {
  renderTab()
  expect(screen.getByTestId('sessions-blast-radius')).toHaveTextContent(
    /this browser|signed out here|including this/i,
  )
})

test('the omission footnote names the endpoint and states the list is not built', async () => {
  // GET /v1/auth/tokens shipped in PR #125 (internal/api/server.go:103), so the
  // note must no longer claim there is no endpoint - only that this tab does not
  // render the list it could supply.
  renderTab()
  const note = screen.getByTestId('sessions-omission-note')
  expect(note).toHaveTextContent('GET /v1/auth/tokens')
  expect(note).not.toHaveTextContent(/no endpoint|not registered/i)
})

test('the confirm dialog states the blast radius and the CLI consequence', async () => {
  renderTab()
  await userEvent.click(screen.getByRole('button', { name: 'Sign out everywhere' }))
  const dialog = await screen.findByRole('dialog')
  expect(dialog).toHaveTextContent(/this browser/i)
  expect(dialog).toHaveTextContent(/relay login/i)
})

test('Cancel in the dialog issues ZERO requests', async () => {
  let deletes = 0
  server.use(
    http.delete('/v1/auth/tokens', () => {
      deletes++
      return new HttpResponse(null, { status: 204 })
    }),
  )
  renderTab()
  await userEvent.click(screen.getByRole('button', { name: 'Sign out everywhere' }))
  await screen.findByRole('dialog')
  await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
  expect(deletes).toBe(0)
  expect(screen.queryByRole('dialog')).toBeNull()
})
