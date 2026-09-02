import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { afterEach, beforeEach, expect, test } from 'vitest'
import { MemoryRouter } from 'react-router-dom'
import { server } from '../test/setup-helpers'
import { renderWithQuery } from '../test/renderWithQuery'
import { JobsPage } from './JobsPage'

// Sibling to JobsPage.test.tsx, which is gate-frozen apart from one narrowed
// assertion, following the JobsPage.pager.test.tsx precedent.

function renderPage() {
  return renderWithQuery(
    <MemoryRouter>
      <JobsPage />
    </MemoryRouter>,
  )
}

const stats = { running: 3, queued: 1, done_24h: 487, failed_24h: 12 }

// Hand-written wire bodies, never marshalled through the api types.
function jobRow(id: string, name: string, status: string) {
  return {
    id,
    name,
    priority: 'normal',
    status,
    submitted_by_email: 'a@x.dev',
    labels: null,
    created_at: '2026-06-05T10:00:00Z',
    updated_at: '2026-06-05T10:00:00Z',
    total_tasks: 4,
    done_tasks: 2,
  }
}

let seen: URLSearchParams[] = []

// Serves both views from one handler: lane requests carry status + limit=10, the
// table's carry limit=50. `seen` is what every request assertion below reads.
function jobsHandler(opts: { failStatus?: string } = {}) {
  return http.get('/v1/jobs', ({ request }) => {
    const p = new URL(request.url).searchParams
    seen.push(p)
    const status = p.get('status')
    if (status === null) return HttpResponse.json({ items: [], next_cursor: '', total: 0 })
    if (status === opts.failStatus) {
      return HttpResponse.json({ error: 'list jobs failed' }, { status: 500 })
    }
    return HttpResponse.json({
      items: [jobRow(`ID-${status}`, `job-${status}`, status)],
      next_cursor: '',
      total: 3,
    })
  })
}

beforeEach(() => {
  seen = []
  server.use(http.get('/v1/jobs/stats', () => HttpResponse.json(stats)), jobsHandler())
})

afterEach(() => localStorage.clear())

test('the view switch persists the choice to localStorage and a remount restores it', async () => {
  const first = renderPage()
  await screen.findByRole('button', { name: 'Lanes' })
  expect(screen.getByRole('button', { name: 'Table' })).toHaveAttribute('aria-pressed', 'true')
  expect(screen.getByRole('button', { name: 'Lanes' })).toHaveAttribute('aria-pressed', 'false')

  await userEvent.click(screen.getByRole('button', { name: 'Lanes' }))
  expect(localStorage.getItem('relay.jobs.view')).toBe('lanes')
  expect(screen.getByRole('button', { name: 'Lanes' })).toHaveAttribute('aria-pressed', 'true')

  first.unmount()
  renderPage()
  expect(await screen.findByRole('button', { name: 'Lanes' })).toHaveAttribute('aria-pressed', 'true')
})

test('a stored value that is not the literal lanes falls back to the table view', async () => {
  localStorage.setItem('relay.jobs.view', 'timeline')
  renderPage()
  expect(await screen.findByRole('button', { name: 'Table' })).toHaveAttribute('aria-pressed', 'true')
})
