import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { AuthProvider } from '../auth/AuthProvider'
import { clearToken, setToken } from '../lib/token'
import { HoloShell } from './HoloShell'

function renderShell(isAdmin: boolean) {
  setToken('tok')
  server.use(
    http.get('/v1/users/me', () =>
      HttpResponse.json({ id: 'me', email: 'me@studio.dev', name: 'Me', is_admin: isAdmin }),
    ),
  )
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={['/jobs']}>
        <AuthProvider>
          <HoloShell>
            <div>page body</div>
          </HoloShell>
        </AuthProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

afterEach(() => clearToken())

test('always shows the non-admin nav entries', async () => {
  renderShell(false)
  await waitFor(() => expect(screen.getByText('page body')).toBeInTheDocument())
  for (const label of ['Jobs', 'Workers', 'Schedules']) {
    expect(screen.getByRole('link', { name: label })).toBeInTheDocument()
  }
})

test('hides the Admin nav entry from non-admins', async () => {
  renderShell(false)
  await waitFor(() => expect(screen.getByRole('link', { name: 'Jobs' })).toBeInTheDocument())
  expect(screen.queryByRole('link', { name: 'Admin' })).not.toBeInTheDocument()
})

test('shows the Admin nav entry to admins', async () => {
  renderShell(true)
  expect(await screen.findByRole('link', { name: 'Admin' })).toHaveAttribute('href', '/admin')
})

// The header owns the profile dropdown, which hangs down over <main>. <main> is
// a later sibling and its panels create stacking contexts of their own (every
// GlassPanel carries backdrop-blur), so with both at z-auto the page content
// paints over an open dropdown. A z-index on the dropdown alone cannot fix that:
// the header's own backdrop-blur makes it a stacking context, which confines any
// z-index inside it. The order has to be declared out here, between the siblings.
//
// jsdom does no layout and no hit-testing, so this can only pin the classes;
// the ordering they produce was measured in Chrome by hit-testing 275 points
// across the open dropdown (see HoloShell.tsx for the numbers). z-0 on <main>
// is asserted for the same reason it is written: it is the guard that keeps a
// future page-level z-index from climbing back over the header.
test('stacks the header above the page content', async () => {
  renderShell(false)
  await waitFor(() => expect(screen.getByText('page body')).toBeInTheDocument())
  expect(screen.getByRole('banner')).toHaveClass('relative', 'z-10')
  expect(screen.getByRole('main')).toHaveClass('relative', 'z-0')
})
