import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { beforeEach, expect, test } from 'vitest'
import { MemoryRouter } from 'react-router-dom'
import { server } from '../test/setup-helpers'
import { renderWithQuery } from '../test/renderWithQuery'
import { JobsPage } from './JobsPage'

// Sibling to JobsPage.test.tsx, whose file is gate-frozen (a byte-for-byte diff to
// origin/main is what licensed the useCursorPager migration). This file covers the
// two wirings the gate cannot: `pickFilter` and `pickSort` both call
// `pager.resetPaging()`. Deleting either leaves JobsPage.test.tsx's existing 13
// tests green, because none of them page forward and THEN change sort or filter in
// the same test.

function renderPage() {
  return renderWithQuery(
    <MemoryRouter>
      <JobsPage />
    </MemoryRouter>,
  )
}

const stats = { running: 3, queued: 1, done_24h: 487, failed_24h: 12 }

beforeEach(() => {
  server.use(http.get('/v1/jobs/stats', () => HttpResponse.json(stats)))
})

function makeItems(count: number, startId = 0) {
  return Array.from({ length: count }, (_, i) => ({
    id: `ID${String(startId + i).padStart(4, '0')}`,
    name: `job-${startId + i}`,
    priority: 'normal',
    status: 'done',
    submitted_by_email: 'a@x.dev',
    labels: null,
    created_at: '2026-06-05T10:00:00Z',
    updated_at: '2026-06-05T10:00:00Z',
    total_tasks: 1,
    done_tasks: 1,
  }))
}

test('changing the sort after paging forward resets the cursor', async () => {
  const seen: URLSearchParams[] = []
  server.use(
    http.get('/v1/jobs', ({ request }) => {
      const p = new URL(request.url).searchParams
      seen.push(p)
      return HttpResponse.json({
        items: makeItems(1),
        next_cursor: p.get('cursor') ? '' : 'CUR2',
        total: 2,
      })
    }),
  )
  renderPage()
  await screen.findByText('job-0')

  await userEvent.click(screen.getByRole('button', { name: /next/i }))
  await waitFor(() => expect(seen.at(-1)?.get('cursor')).toBe('CUR2'))

  await userEvent.selectOptions(screen.getByLabelText('Sort jobs'), 'name')
  await waitFor(() => expect(seen.at(-1)?.get('sort')).toBe('name'))
  // A cursor minted under the old sort is rejected by the server
  // ("cursor sort key does not match requested sort", internal/api/pagination.go),
  // so the sort change must NOT carry the stale cursor.
  expect(seen.at(-1)?.has('cursor')).toBe(false)
})

test('selecting a status filter chip after paging forward resets the cursor', async () => {
  const seen: URLSearchParams[] = []
  server.use(
    http.get('/v1/jobs', ({ request }) => {
      const p = new URL(request.url).searchParams
      seen.push(p)
      return HttpResponse.json({
        items: makeItems(1),
        next_cursor: p.get('cursor') ? '' : 'CUR2',
        total: 2,
      })
    }),
  )
  renderPage()
  await screen.findByText('job-0')

  await userEvent.click(screen.getByRole('button', { name: /next/i }))
  await waitFor(() => expect(seen.at(-1)?.get('cursor')).toBe('CUR2'))

  await userEvent.click(screen.getByRole('button', { name: 'Running' }))
  await waitFor(() => expect(seen.at(-1)?.get('status')).toBe('running'))
  expect(seen.at(-1)?.has('cursor')).toBe(false)
})
