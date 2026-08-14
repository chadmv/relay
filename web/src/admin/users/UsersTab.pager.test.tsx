import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { afterEach, expect, test, vi } from 'vitest'
import { server } from '../../test/setup-helpers'
import { AuthProvider } from '../../auth/AuthProvider'
import { clearToken, setToken } from '../../lib/token'
import { UsersTab } from './UsersTab'
import type { AdminUser } from './api'

// Sibling to UsersTab.test.tsx, whose file is gate-frozen (a byte-for-byte diff to
// origin/main is what licensed the useCursorPager migration). This file covers the
// one wiring the gate cannot: `pickEmail` calls `pager.resetPaging()`. Deleting
// that call leaves UsersTab.test.tsx's existing 23 tests green, because none of
// them page forward and THEN type into the email filter in the same test.

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
afterEach(() => vi.useRealTimers())

test('typing in the email filter after paging forward resets the cursor', async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true })
  try {
    const typist = userEvent.setup({ advanceTimers: vi.advanceTimersByTime })
    const seen: URLSearchParams[] = []
    server.use(
      http.get('/v1/users', ({ request }) => {
        const p = new URL(request.url).searchParams
        seen.push(p)
        return HttpResponse.json({
          items: p.has('email') ? [] : [user()],
          next_cursor: p.get('cursor') ? '' : 'CUR2',
          total: 2,
        })
      }),
    )
    renderTab()
    await screen.findByText('ada@studio.dev')

    await userEvent.click(screen.getByRole('button', { name: /next 50/ }))
    await waitFor(() => expect(seen.at(-1)?.get('cursor')).toBe('CUR2'))

    await typist.type(screen.getByLabelText('Filter by email'), 'a')
    await act(async () => {
      await vi.advanceTimersByTimeAsync(10)
    })
    await waitFor(() => expect(seen.at(-1)?.has('email')).toBe(true))
    // The server 400s a cursor issued under the pre-filter page walk, and the
    // ?email= branch does not paginate at all - the filtered request must not
    // carry the stale cursor.
    expect(seen.at(-1)?.has('cursor')).toBe(false)
  } finally {
    vi.useRealTimers()
  }
})
