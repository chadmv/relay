import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { beforeEach, expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { renderWithQuery } from '../test/renderWithQuery'
import { JobsPage } from './JobsPage'

const page = {
  items: [
    {
      id: '9F4E1C', name: 'film-x render', priority: 'high', status: 'running',
      submitted_by_email: 'mira@studio.dev', labels: null,
      created_at: '2026-06-05T14:22:00Z', updated_at: '2026-06-05T14:30:00Z',
      total_tasks: 64, done_tasks: 48, started_at: '2026-06-05T14:22:00Z',
    },
  ],
  next_cursor: '',
  total: 1,
}

const stats = { running: 3, queued: 1, done_24h: 487, failed_24h: 12 }

beforeEach(() => {
  server.use(http.get('/v1/jobs/stats', () => HttpResponse.json(stats)))
})

test('renders jobs and the KPI strip', async () => {
  server.use(http.get('/v1/jobs', () => HttpResponse.json(page)))
  renderWithQuery(<JobsPage />)
  expect(await screen.findByText('film-x render')).toBeInTheDocument()
  // KPI numbers come from the separately-polled stats query; await them.
  expect(await screen.findByText('487')).toBeInTheDocument() // done_24h
  expect(await screen.findByText('12')).toBeInTheDocument() // failed_24h
})

test('selecting a status chip re-requests with status and disables sort', async () => {
  const requests: URLSearchParams[] = []
  server.use(
    http.get('/v1/jobs', ({ request }) => {
      requests.push(new URL(request.url).searchParams)
      return HttpResponse.json(page)
    }),
  )
  renderWithQuery(<JobsPage />)
  await screen.findByText('film-x render')
  await userEvent.click(screen.getByRole('button', { name: 'Running' }))
  await waitFor(() => expect(requests.some((q) => q.get('status') === 'running')).toBe(true))
  // The status-filtered request must NOT carry a sort param.
  const filtered = requests.find((q) => q.get('status') === 'running')
  expect(filtered?.get('sort')).toBeNull()
  expect(screen.getByLabelText('Sort jobs')).toBeDisabled()
})

test('shows the error banner with retry, then recovers', async () => {
  server.use(http.get('/v1/jobs', () => HttpResponse.json({ error: 'boom' }, { status: 500 })))
  renderWithQuery(<JobsPage />)
  expect(await screen.findByRole('button', { name: /retry/i })).toBeInTheDocument()
  server.use(http.get('/v1/jobs', () => HttpResponse.json(page)))
  await userEvent.click(screen.getByRole('button', { name: /retry/i }))
  expect(await screen.findByText('film-x render')).toBeInTheDocument()
})

test('paginates forward and back via the cursor stack', async () => {
  const pages: Record<string, { items: unknown[]; next_cursor: string; total: number }> = {
    '': {
      items: [{
        id: 'AAAAAA', name: 'job-A', priority: 'normal', status: 'running',
        submitted_by_email: 'a@x.dev', labels: null,
        created_at: '2026-06-05T10:00:00Z', updated_at: '2026-06-05T10:00:00Z',
        total_tasks: 1, done_tasks: 0,
      }],
      next_cursor: 'CUR1', total: 2,
    },
    CUR1: {
      items: [{
        id: 'BBBBBB', name: 'job-B', priority: 'normal', status: 'done',
        submitted_by_email: 'b@x.dev', labels: null,
        created_at: '2026-06-05T09:00:00Z', updated_at: '2026-06-05T09:00:00Z',
        total_tasks: 1, done_tasks: 1,
      }],
      next_cursor: '', total: 2,
    },
  }
  server.use(
    http.get('/v1/jobs', ({ request }) => {
      const cur = new URL(request.url).searchParams.get('cursor') ?? ''
      return HttpResponse.json(pages[cur])
    }),
  )
  renderWithQuery(<JobsPage />)

  // Page 1: job-A, prev disabled, next enabled.
  expect(await screen.findByText('job-A')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /prev/i })).toBeDisabled()
  expect(screen.getByRole('button', { name: /next/i })).toBeEnabled()

  // Forward to page 2: job-B, next now disabled (no next_cursor), prev enabled.
  await userEvent.click(screen.getByRole('button', { name: /next/i }))
  expect(await screen.findByText('job-B')).toBeInTheDocument()
  await waitFor(() => expect(screen.getByRole('button', { name: /next/i })).toBeDisabled())
  expect(screen.getByRole('button', { name: /prev/i })).toBeEnabled()

  // Back to page 1.
  await userEvent.click(screen.getByRole('button', { name: /prev/i }))
  expect(await screen.findByText('job-A')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /prev/i })).toBeDisabled()
})
