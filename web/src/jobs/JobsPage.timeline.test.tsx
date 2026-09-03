import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { afterEach, beforeEach, expect, test } from 'vitest'
import { MemoryRouter } from 'react-router-dom'
import { server } from '../test/setup-helpers'
import { renderWithQuery } from '../test/renderWithQuery'
import { JobsPage } from './JobsPage'

function renderPage() {
  return renderWithQuery(
    <MemoryRouter>
      <JobsPage debounceMs={10} />
    </MemoryRouter>,
  )
}

function jobRow(id: string, name: string) {
  return {
    id,
    name,
    priority: 'normal',
    status: 'pending',
    submitted_by_email: 'a@x.dev',
    labels: null,
    created_at: '2026-09-02T10:00:00Z',
    updated_at: '2026-09-02T10:00:00Z',
    total_tasks: 1,
    done_tasks: 0,
  }
}

const stats = { running: 0, queued: 1, done_24h: 0, failed_24h: 0 }

let seen: URLSearchParams[] = []

beforeEach(() => {
  seen = []
  server.use(
    http.get('/v1/jobs/stats', () => HttpResponse.json(stats)),
    http.get('/v1/jobs', ({ request }) => {
      seen.push(new URL(request.url).searchParams)
      return HttpResponse.json({ items: [jobRow('AAAAAA', 'job-A')], next_cursor: '', total: 1 })
    }),
  )
})

afterEach(() => localStorage.clear())

test('the view switch persists timeline and a remount restores it', async () => {
  const first = renderPage()
  await screen.findByRole('button', { name: 'Timeline' })
  const group = screen.getByRole('group', { name: 'Jobs view' })
  expect(within(group).getAllByRole('button')).toHaveLength(3)

  await userEvent.click(screen.getByRole('button', { name: 'Timeline' }))
  expect(localStorage.getItem('relay.jobs.view')).toBe('timeline')
  expect(screen.getByRole('button', { name: 'Timeline' })).toHaveAttribute('aria-pressed', 'true')

  first.unmount()
  renderPage()
  expect(await screen.findByRole('button', { name: 'Timeline' })).toHaveAttribute(
    'aria-pressed',
    'true',
  )
})

test('the window picker is a named group and persists its choice', async () => {
  localStorage.setItem('relay.jobs.view', 'timeline')
  const first = renderPage()
  const picker = await screen.findByRole('group', { name: 'Timeline window' })
  expect(within(picker).getAllByRole('button')).toHaveLength(3)
  expect(within(picker).getByRole('button', { name: '24h' })).toHaveAttribute(
    'aria-pressed',
    'true',
  )

  await userEvent.click(within(picker).getByRole('button', { name: '7d' }))
  expect(localStorage.getItem('relay.jobs.timeline.window')).toBe('7d')

  first.unmount()
  renderPage()
  const picker2 = await screen.findByRole('group', { name: 'Timeline window' })
  expect(within(picker2).getByRole('button', { name: '7d' })).toHaveAttribute(
    'aria-pressed',
    'true',
  )
})

test('the timeline view issues no table or lane request', async () => {
  localStorage.setItem('relay.jobs.view', 'timeline')
  renderPage()
  await screen.findByRole('region', { name: 'Jobs timeline' })
  await waitFor(() => expect(seen.length).toBeGreaterThan(0))
  // A bounded wait for an absence, longer than the table query's own 3000ms
  // interval would need to fire once - that interval is what would produce a
  // request if the enabled gate leaked.
  await new Promise((r) => setTimeout(r, 120))

  // limit=50 is the table's enriched page and limit=10 is a lane's. Nobody is
  // looking at either in this view.
  expect(seen.some((p) => p.get('limit') === '50')).toBe(false)
  expect(seen.some((p) => p.get('limit') === '10')).toBe(false)
  expect(seen.every((p) => p.get('limit') === '200')).toBe(true)
})

test('the live indicator tracks the timeline query', async () => {
  localStorage.setItem('relay.jobs.view', 'timeline')
  let release!: () => void
  const gate = new Promise<void>((r) => {
    release = r
  })
  server.use(
    http.get('/v1/jobs', async ({ request }) => {
      seen.push(new URL(request.url).searchParams)
      await gate
      return HttpResponse.json({ items: [], next_cursor: '', total: 0 })
    }),
  )
  renderPage()

  // Through a data attribute, never a class name. Forgetting the timeline branch
  // of the polling expression leaves this dot permanently dark beside text
  // claiming the page auto-refreshes.
  const dot = await screen.findByTestId('live-dot')
  await waitFor(() => expect(dot).toHaveAttribute('data-live', 'on'))
  release()
  await waitFor(() => expect(dot).toHaveAttribute('data-live', 'off'))
})
