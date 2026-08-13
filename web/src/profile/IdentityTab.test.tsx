import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { AuthProvider, useAuth } from '../auth/AuthProvider'
import { clearToken, setToken } from '../lib/token'
import { IdentityTab } from './IdentityTab'

afterEach(() => clearToken())

const ME = {
  id: 'u1',
  email: 'mira@studio.dev',
  name: 'Mira Sato',
  is_admin: true,
  created_at: '2025-04-02T09:15:00Z',
  archived_at: null,
}

// Reads the user straight off the context, so it proves applyUser ran rather
// than proving the form set its own local state. This is the same instrument
// ProfilePage's <h1> uses; ProfilePage.test.tsx repeats the assertion against the
// real header once that file exists.
function UserProbe() {
  const { user } = useAuth()
  return <span data-testid="probe-name">{user?.name ?? 'none'}</span>
}

async function renderTab(me: Record<string, unknown> = ME) {
  setToken('tok')
  server.use(http.get('/v1/users/me', () => HttpResponse.json(me)))
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  const utils = render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={['/profile/identity']}>
        <AuthProvider>
          <UserProbe />
          <IdentityTab />
        </AuthProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  )
  // Wait for hydration before touching the form: the draft is derived lazily from
  // the live user row until the first keystroke, so a test that types before
  // hydration would be editing a field seeded from null.
  await waitFor(() => expect(screen.getByTestId('probe-name')).toHaveTextContent('Mira Sato'))
  return { ...utils, client }
}

test('Save with an untouched name issues ZERO requests', async () => {
  let patches = 0
  server.use(
    http.patch('/v1/users/me', () => {
      patches++
      return HttpResponse.json(ME)
    }),
  )
  await renderTab()
  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))
  expect(patches).toBe(0)
})

test('a changed name issues exactly one PATCH whose body is the TRIMMED name (positive control)', async () => {
  // Without this, the test above passes against a form whose Save does nothing.
  let patches = 0
  let body: Record<string, unknown> | undefined
  server.use(
    http.patch('/v1/users/me', async ({ request }) => {
      patches++
      body = (await request.json()) as Record<string, unknown>
      return HttpResponse.json({ ...ME, name: 'Mira Renamed' })
    }),
  )
  await renderTab()
  const input = screen.getByLabelText('Display name')
  await userEvent.clear(input)
  await userEvent.type(input, '  Mira Renamed  ')
  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))

  await waitFor(() => expect(patches).toBe(1))
  // toEqual on the whole object: an extra key is the failure mode a property
  // check cannot see, and `email` is the specific extra key somebody will add.
  expect(body).toEqual({ name: 'Mira Renamed' })
})

test('a whitespace-only edit is NOT a change and issues zero requests', async () => {
  // The server trims before storing (internal/api/users.go:61), so "Mira Sato  "
  // and "Mira Sato" are the same row. A dirtiness flag instead of a value
  // comparison would fail here and would write on every visit to the field.
  let patches = 0
  server.use(
    http.patch('/v1/users/me', () => {
      patches++
      return HttpResponse.json(ME)
    }),
  )
  await renderTab()
  const input = screen.getByLabelText('Display name')
  await userEvent.type(input, '   ')
  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))
  expect(patches).toBe(0)
})

test('typing a new name and typing it back issues zero requests', async () => {
  let patches = 0
  server.use(
    http.patch('/v1/users/me', () => {
      patches++
      return HttpResponse.json(ME)
    }),
  )
  await renderTab()
  const input = screen.getByLabelText('Display name')
  await userEvent.clear(input)
  await userEvent.type(input, 'Someone Else')
  await userEvent.clear(input)
  await userEvent.type(input, 'Mira Sato')
  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))
  expect(patches).toBe(0)
})

test('on 200 the AuthProvider user is replaced, not just the local input', async () => {
  server.use(
    http.patch('/v1/users/me', () => HttpResponse.json({ ...ME, name: 'Mira Renamed' })),
  )
  await renderTab()
  const input = screen.getByLabelText('Display name')
  await userEvent.clear(input)
  await userEvent.type(input, 'Mira Renamed')
  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))

  // Reading the PROBE, not the input. A component that only sets its own state
  // passes an input-reading test while leaving every other consumer of `user`
  // stale for the rest of the session.
  await waitFor(() => expect(screen.getByTestId('probe-name')).toHaveTextContent('Mira Renamed'))
  expect(await screen.findByRole('status')).toHaveTextContent('Display name updated.')
})

test('a 400 renders the server sentence inline and leaves the user row unchanged', async () => {
  server.use(
    http.patch('/v1/users/me', () =>
      HttpResponse.json({ error: 'name is required' }, { status: 400 }),
    ),
  )
  await renderTab()
  const input = screen.getByLabelText('Display name')
  await userEvent.clear(input)
  await userEvent.type(input, 'x')
  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))

  expect(await screen.findByRole('alert')).toHaveTextContent('400 name is required')
  expect(screen.getByTestId('probe-name')).toHaveTextContent('Mira Sato')
})

test('Cancel restores the loaded name and clears the error, so a following Save issues nothing', async () => {
  let patches = 0
  server.use(
    http.patch('/v1/users/me', () => {
      patches++
      return HttpResponse.json({ error: 'name is required' }, { status: 400 })
    }),
  )
  await renderTab()
  const input = screen.getByLabelText('Display name')
  await userEvent.clear(input)
  await userEvent.type(input, 'x')
  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))
  expect(await screen.findByRole('alert')).toBeInTheDocument()

  await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
  expect(input).toHaveValue('Mira Sato')
  expect(screen.queryByRole('alert')).toBeNull()

  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))
  expect(patches).toBe(1)
})

test('the email field is present, disabled, and hinted - and no control can mutate it', async () => {
  await renderTab()
  const email = screen.getByLabelText('Email')
  expect(email).toHaveValue('mira@studio.dev')
  expect(email).toBeDisabled()
  expect(screen.getByText('identity - contact your admin to change')).toBeInTheDocument()
  // Both directions: the form has exactly ONE editable text control, so there is
  // no second input somebody could wire to an email PATCH the API does not have.
  const editable = screen
    .getAllByRole('textbox')
    .filter((el) => !(el as HTMLInputElement).disabled)
  expect(editable).toHaveLength(1)
  expect(editable[0]).toBe(screen.getByLabelText('Display name'))
})

test('the role note shows ADMIN for an admin and USER for a non-admin', async () => {
  await renderTab()
  expect(screen.getByText('ADMIN')).toBeInTheDocument()
  expect(screen.queryByText('USER')).toBeNull()
  expect(
    screen.getByText(/Role is server-side only/),
  ).toBeInTheDocument()
})

test('the role note shows USER for a non-admin (paired control)', async () => {
  await renderTab({ ...ME, is_admin: false })
  expect(screen.getByText('USER')).toBeInTheDocument()
  expect(screen.queryByText('ADMIN')).toBeNull()
})

test('a no-op Save after a failed save clears the stale error banner', async () => {
  server.use(
    http.patch('/v1/users/me', () =>
      HttpResponse.json({ error: 'name is required' }, { status: 400 }),
    ),
  )
  await renderTab()
  const input = screen.getByLabelText('Display name')
  await userEvent.clear(input)
  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))
  expect(await screen.findByRole('alert')).toHaveTextContent('400 name is required')

  // Retype the original name: this is a no-op save (the early return fires, no
  // request is sent) but the form is valid again, so the stale error must go.
  await userEvent.type(input, 'Mira Sato')
  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))
  expect(screen.queryByRole('alert')).toBeNull()
})

test('a no-op Save after a successful save clears the stale success banner', async () => {
  server.use(
    http.patch('/v1/users/me', () => HttpResponse.json({ ...ME, name: 'Mira Renamed' })),
  )
  await renderTab()
  const input = screen.getByLabelText('Display name')
  await userEvent.clear(input)
  await userEvent.type(input, 'Mira Renamed')
  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))
  expect(await screen.findByRole('status')).toHaveTextContent('Display name updated.')

  // A following no-op save (retyping the now-current name) must clear the
  // success banner rather than leaving it displayed against a fresh submit.
  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))
  expect(screen.queryByRole('status')).toBeNull()
})

test('an edit typed while a save is in flight survives the save settling', async () => {
  // Reproduces the review probe: Save "Alpha" while the PATCH is still pending,
  // type "Beta" into the field, then let the PATCH resolve. The settled
  // mutation's onSuccess must not clobber the newer, unsent edit by re-deriving
  // the draft from the (now stale) server response.
  let releaseResponse: (() => void) | undefined
  const gate = new Promise<void>((resolve) => {
    releaseResponse = resolve
  })
  server.use(
    http.patch('/v1/users/me', async () => {
      await gate
      return HttpResponse.json({ ...ME, name: 'Alpha' })
    }),
  )
  await renderTab()
  const input = screen.getByLabelText('Display name')
  await userEvent.clear(input)
  await userEvent.type(input, 'Alpha')
  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))

  // The PATCH is now pending. Type a newer edit before it settles.
  await waitFor(() => expect(screen.getByRole('button', { name: 'Save changes' })).toBeDisabled())
  await userEvent.clear(input)
  await userEvent.type(input, 'Beta')
  expect(input).toHaveValue('Beta')

  // Let the in-flight save resolve.
  releaseResponse!()
  await waitFor(() => expect(screen.getByRole('button', { name: 'Save changes' })).not.toBeDisabled())

  // The newer, unsent edit must survive - not be clobbered by the settled save.
  expect(input).toHaveValue('Beta')
})
