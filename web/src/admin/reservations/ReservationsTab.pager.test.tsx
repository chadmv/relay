import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { MemoryRouter } from 'react-router-dom'
import { expect, test } from 'vitest'
import { server } from '../../test/setup-helpers'
import { ReservationsTab } from './ReservationsTab'
import type { Reservation } from './api'

// Sibling to ReservationsTab.test.tsx, whose file is gate-frozen (a byte-for-byte
// diff to origin/main is what licensed the useCursorPager migration). That file
// only ever asserts a `1-1 of 1` footer, so it cannot catch a wrong `pageSize`
// argument to `pager.next`. This file pages a partial last page (50 then 13) and
// asserts the absolute footer range - the exact bug class
// `docs/backlog/closed/bug-2026-06-05-jobs-pagination-footer-absolute-range.md`
// and `docs/backlog/closed/bug-2026-06-21-schedules-pagination-footer-absolute-range.md`
// already shipped twice.

const W1 = 'aaaa1111-1111-1111-1111-111111111111'

function reservation(id: string, name: string): Reservation {
  return {
    id,
    name,
    selector: null,
    worker_ids: [W1],
    user_id: 'u1',
    created_at: '2026-08-09T09:30:00Z',
  }
}

function makeReservations(count: number, startId = 0) {
  return Array.from({ length: count }, (_, i) => reservation(`r${startId + i}`, `res-${startId + i}`))
}

function renderTab() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <ReservationsTab />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

test('pagination footer shows the correct absolute range on a partial last page', async () => {
  server.use(
    http.get('/v1/reservations', ({ request }) => {
      const cur = new URL(request.url).searchParams.get('cursor') ?? ''
      return HttpResponse.json(
        cur
          ? { items: makeReservations(13, 50), next_cursor: '', total: 63 }
          : { items: makeReservations(50, 0), next_cursor: 'CUR1', total: 63 },
      )
    }),
  )
  renderTab()
  await screen.findByText('res-0')
  expect(await screen.findByText(/1-50 of 63/i)).toBeInTheDocument()

  await userEvent.click(screen.getByRole('button', { name: /next 50/ }))
  await screen.findByText('res-50')
  expect(await screen.findByText(/51-63 of 63/i)).toBeInTheDocument()

  await userEvent.click(screen.getByRole('button', { name: /prev/ }))
  await screen.findByText('res-0')
  expect(await screen.findByText(/1-50 of 63/i)).toBeInTheDocument()
})
