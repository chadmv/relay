import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { afterEach, expect, test } from 'vitest'
import { MemoryRouter } from 'react-router-dom'
import { server } from '../test/setup-helpers'
import { renderWithQuery } from '../test/renderWithQuery'
import { WorkersPage } from './WorkersPage'

// Sibling to WorkersPage.test.tsx, whose file is gate-frozen (a byte-for-byte diff
// to origin/main is what licensed the useCursorPager migration). That file only
// ever asserts a `1-1 of 1` footer for the decommissioned section, so it cannot
// catch a wrong `pageSize` argument to `revokedPager.next`. This file pages a
// partial last page (50 then 13) and asserts the absolute footer range - the exact
// bug class `docs/backlog/closed/bug-2026-06-05-jobs-pagination-footer-absolute-range.md`
// and `docs/backlog/closed/bug-2026-06-21-schedules-pagination-footer-absolute-range.md`
// already shipped twice.

function renderPage() {
  return renderWithQuery(
    <MemoryRouter>
      <WorkersPage />
    </MemoryRouter>,
  )
}

afterEach(() => localStorage.clear())

const page = {
  items: [
    { id: 'w1', name: 'render-01', hostname: 'h', cpu_cores: 16, ram_gb: 128, gpu_count: 1, gpu_model: 'RTX 4090', os: 'linux', max_slots: 4, labels: null, status: 'online', last_seen_at: '2026-06-03T12:00:00Z' },
  ],
  next_cursor: '',
  total: 1,
}

function revokedWorker(id: string, name: string) {
  return {
    id,
    name,
    hostname: `${name}-host`,
    cpu_cores: 4,
    ram_gb: 16,
    gpu_count: 0,
    gpu_model: '',
    os: 'linux',
    max_slots: 1,
    labels: null,
    status: 'revoked',
    revoked_at: '2026-01-02T03:04:05Z',
  }
}

function makeRevoked(count: number, startId = 0) {
  return Array.from({ length: count }, (_, i) => revokedWorker(`rw${startId + i}`, `gone-${startId + i}`))
}

const stats = { online: 1, stale: 0, offline: 0, disabled: 0, total: 1 }

test('decommissioned pagination footer shows the correct absolute range on a partial last page', async () => {
  server.use(http.get('/v1/workers', () => HttpResponse.json(page)))
  server.use(http.get('/v1/workers/stats', () => HttpResponse.json(stats)))
  server.use(
    http.get('/v1/workers/revoked', ({ request }) => {
      const cur = new URL(request.url).searchParams.get('cursor') ?? ''
      return HttpResponse.json(
        cur
          ? { items: makeRevoked(13, 50), next_cursor: '', total: 63 }
          : { items: makeRevoked(50, 0), next_cursor: 'CUR1', total: 63 },
      )
    }),
  )
  renderPage()
  await screen.findByText('render-01')
  await userEvent.click(screen.getByRole('button', { name: 'Decommissioned' }))

  await screen.findByText('gone-0')
  expect(await screen.findByText(/1-50 of 63/i)).toBeInTheDocument()

  await userEvent.click(screen.getByRole('button', { name: /next/i }))
  await screen.findByText('gone-50')
  expect(await screen.findByText(/51-63 of 63/i)).toBeInTheDocument()

  await userEvent.click(screen.getByRole('button', { name: /prev/i }))
  await screen.findByText('gone-0')
  expect(await screen.findByText(/1-50 of 63/i)).toBeInTheDocument()
})
