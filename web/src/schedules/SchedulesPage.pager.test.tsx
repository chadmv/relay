import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { renderWithQuery } from '../test/renderWithQuery'
import { SchedulesPage } from './SchedulesPage'

// Sibling to SchedulesPage.test.tsx, whose file is gate-frozen (a byte-for-byte
// diff to origin/main is what licensed the useCursorPager migration). This file
// covers the one wiring the gate cannot: that `chooseSort` calls
// `pager.resetPaging()`. Deleting that call leaves SchedulesPage.test.tsx's
// existing 11 tests green, because none of them page forward and THEN change
// sort in the same test.

// See the sibling file's fuller note: SchedulesPage issues a second query, and
// MSW's exact path matching means the /v1/scheduled-jobs handler below does not
// answer /v1/scheduled-jobs/stats.
beforeEach(() => {
  server.use(
    http.get('/v1/scheduled-jobs/stats', () =>
      HttpResponse.json({ enabled: 7, paused: 2, total: 9, failed_runs_24h: 1, failing: 1 }),
    ),
  )
})

function makeSchedules(count: number, startId = 0) {
  return Array.from({ length: count }, (_, i) => ({
    id: `s${startId + i}`,
    name: `sched-${startId + i}`,
    owner_id: 'o1',
    owner_email: 'dev@studio.dev',
    cron_expr: '0 2 * * *',
    timezone: 'UTC',
    job_spec: {},
    overlap_policy: 'skip',
    enabled: true,
    next_run_at: '2099-01-01T00:00:00Z',
    last_run_at: '2026-06-05T11:00:00Z',
    last_job_id: null,
    created_at: '2026-06-01T00:00:00Z',
    updated_at: '2026-06-05T11:00:00Z',
  }))
}

test('changing the sort after paging forward resets the cursor', async () => {
  const seen: URLSearchParams[] = []
  server.use(
    http.get('/v1/scheduled-jobs', ({ request }) => {
      const p = new URL(request.url).searchParams
      seen.push(p)
      return HttpResponse.json({
        items: makeSchedules(1),
        next_cursor: p.get('cursor') ? '' : 'CUR2',
        total: 2,
      })
    }),
  )
  renderWithQuery(
    <MemoryRouter>
      <SchedulesPage />
    </MemoryRouter>,
  )
  await screen.findByText('sched-0')

  await userEvent.click(screen.getByRole('button', { name: /next/i }))
  await waitFor(() => expect(seen.at(-1)?.get('cursor')).toBe('CUR2'))

  await userEvent.selectOptions(screen.getByLabelText('Sort'), 'name')
  await waitFor(() => expect(seen.at(-1)?.get('sort')).toBe('name'))
  // A cursor minted under the old sort is rejected by the server
  // ("cursor sort key does not match requested sort", internal/api/pagination.go),
  // so the sort change must NOT carry the stale cursor.
  expect(seen.at(-1)?.has('cursor')).toBe(false)
})
