import { act, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import { MemoryRouter } from 'react-router-dom'
import { server } from '../test/setup-helpers'
import { renderWithQuery } from '../test/renderWithQuery'
import { JobsPage } from './JobsPage'

// debounceMs is shrunk rather than faked: real timers keep userEvent's own
// internal waits working, which is how UsersTab.test.tsx drives the same hook.
function renderPage() {
  return renderWithQuery(
    <MemoryRouter>
      <JobsPage debounceMs={10} />
    </MemoryRouter>,
  )
}

// Hand-written wire bodies, never marshalled through the api types: a fixture
// built from the production interface agrees with the decoder by construction
// and cannot detect drift in either direction.
function jobRow(id: string, name: string) {
  return {
    id,
    name,
    priority: 'normal',
    status: 'running',
    submitted_by_email: 'a@x.dev',
    labels: null,
    created_at: '2026-09-02T10:00:00Z',
    updated_at: '2026-09-02T10:00:00Z',
    total_tasks: 4,
    done_tasks: 2,
  }
}

const stats = { running: 1, queued: 0, done_24h: 0, failed_24h: 0 }

let seen: URLSearchParams[] = []

function onePage() {
  return http.get('/v1/jobs', ({ request }) => {
    const p = new URL(request.url).searchParams
    seen.push(p)
    return HttpResponse.json({ items: [jobRow('AAAAAA', 'job-A')], next_cursor: '', total: 1 })
  })
}

// Two pages, so a test can put a cursor in play before asserting it is dropped.
function twoPages() {
  const bodies: Record<string, { items: unknown[]; next_cursor: string; total: number }> = {
    '': { items: [jobRow('AAAAAA', 'job-A')], next_cursor: 'CUR1', total: 2 },
    CUR1: { items: [jobRow('BBBBBB', 'job-B')], next_cursor: '', total: 2 },
  }
  return http.get('/v1/jobs', ({ request }) => {
    const p = new URL(request.url).searchParams
    seen.push(p)
    return HttpResponse.json(bodies[p.get('cursor') ?? ''])
  })
}

beforeEach(() => {
  seen = []
  server.use(http.get('/v1/jobs/stats', () => HttpResponse.json(stats)), onePage())
})

afterEach(() => localStorage.clear())
afterEach(() => vi.useRealTimers())

test('the search box sends q on the table request', async () => {
  renderPage()
  await screen.findByText('job-A')
  await userEvent.type(screen.getByRole('searchbox'), '  nightly  ')
  await waitFor(() => expect(seen.some((p) => p.get('q') === 'nightly')).toBe(true))
  // Trimmed before it reaches the key and the wire. Untrimmed padding would make
  // two spellings of one search into two cache entries, and the server reads a
  // whitespace-only value as absent anyway.
  expect(seen.some((p) => p.get('q') === '  nightly  ')).toBe(false)
})

test('a burst of keystrokes issues one request', async () => {
  renderPage()
  await screen.findByText('job-A')
  await userEvent.type(screen.getByRole('searchbox'), 'etl')
  await waitFor(() => expect(seen.some((p) => p.get('q') === 'etl')).toBe(true))

  // Asserted as the SET of non-empty q values rather than a request count: the
  // table polls on its own interval, so a repeat of the same value is expected
  // and a raw count would be timing-dependent. An identity-function debounce
  // puts 'e' and 'et' on the wire, which this set cannot contain.
  const values = new Set(seen.map((p) => p.get('q') ?? '').filter((v) => v !== ''))
  expect([...values]).toEqual(['etl'])
})

test('searching after paging forward drops the cursor', async () => {
  server.use(twoPages())
  renderPage()
  await screen.findByText('job-A')

  await userEvent.click(screen.getByRole('button', { name: /next/i }))
  // THE CURSOR MUST BE IN PLAY BEFORE THE RESET CAN BE OBSERVED. Without paging
  // forward first the cursor is already empty, removing the reset changes
  // nothing, and this test would pass on the broken code while looking like it
  // covered it.
  await waitFor(() => expect(seen.some((p) => p.get('cursor') === 'CUR1')).toBe(true))

  await userEvent.type(screen.getByRole('searchbox'), 'etl')
  await waitFor(() => expect(seen.some((p) => p.get('q') === 'etl')).toBe(true))
  for (const p of seen.filter((x) => x.get('q') === 'etl')) {
    expect(p.has('cursor')).toBe(false)
  }
})

test('a click inside the debounce window cannot mint a cursor the landing search carries', async () => {
  // The unfiltered page 1 (q='') has a next cursor; the filtered result (q='e')
  // never does. If a click inside the debounce window could still mint CUR1,
  // the search that lands afterward would carry it straight into a q=e&cursor=
  // CUR1 request - a cursor from one filter combined with a different one, and
  // the server does not reject the mismatch.
  server.use(
    http.get('/v1/jobs', ({ request }) => {
      const p = new URL(request.url).searchParams
      seen.push(p)
      if (p.get('q')) return HttpResponse.json({ items: [], next_cursor: '', total: 0 })
      return HttpResponse.json({ items: [jobRow('AAAAAA', 'job-A')], next_cursor: 'CUR1', total: 2 })
    }),
  )
  vi.useFakeTimers({ shouldAdvanceTime: true })
  try {
    const typist = userEvent.setup({ advanceTimers: vi.advanceTimersByTime })
    // A wider debounce than this file's usual 10ms: userEvent's own internal
    // per-keystroke delay, replayed through advanceTimers, can otherwise land
    // the debounce before this test ever gets to observe the vulnerable
    // window between the keystroke and it landing.
    renderWithQuery(
      <MemoryRouter>
        <JobsPage debounceMs={300} />
      </MemoryRouter>,
    )
    await screen.findByText('job-A')

    await typist.type(screen.getByRole('searchbox'), 'e')
    // The raw input has outrun the debounced q (still '' until the timer
    // fires), which is exactly the vulnerable window: this is the assertion
    // that pins the fix itself, not just its absence of an effect.
    const next = screen.getByRole('button', { name: /next/i })
    expect(next).toBeDisabled()
    await typist.click(next)

    await act(async () => {
      await vi.advanceTimersByTimeAsync(300)
    })
    await waitFor(() => expect(seen.some((p) => p.get('q') === 'e')).toBe(true))
    for (const p of seen.filter((x) => x.get('q') === 'e')) {
      expect(p.has('cursor')).toBe(false)
    }
  } finally {
    vi.useRealTimers()
  }
})

test('a trailing space in the raw input does not leave the pager disabled forever', async () => {
  // JobsPage debounces qInput and then TRIMS it into q; the guard must compare
  // like for like. Comparing the raw, untrimmed qInput against the trimmed q
  // means a trailing space never resolves to equal, even after the debounce
  // has long since settled - the pager would stay disabled until the user
  // deleted the space themselves.
  server.use(
    http.get('/v1/jobs', ({ request }) => {
      const p = new URL(request.url).searchParams
      seen.push(p)
      if (p.get('q') === 'etl') {
        return HttpResponse.json({ items: [jobRow('AAAAAA', 'job-A')], next_cursor: 'CUR1', total: 2 })
      }
      return HttpResponse.json({ items: [jobRow('AAAAAA', 'job-A')], next_cursor: '', total: 1 })
    }),
  )
  renderPage()
  await screen.findByText('job-A')

  await userEvent.type(screen.getByRole('searchbox'), 'etl ')
  await waitFor(() => expect(seen.some((p) => p.get('q') === 'etl')).toBe(true))

  // The debounce has landed (q='etl' went out) and there IS a next page, so
  // the pager must be usable - not stuck disabled by a raw/trimmed mismatch
  // that never resolves.
  await waitFor(() => expect(screen.getByRole('button', { name: /next/i })).toBeEnabled())
})

test('My jobs sends mine=true and drops the cursor', async () => {
  server.use(twoPages())
  renderPage()
  await screen.findByText('job-A')
  expect(seen.every((p) => !p.has('mine'))).toBe(true)

  await userEvent.click(screen.getByRole('button', { name: /next/i }))
  // Same paging-first requirement as the search case above.
  await waitFor(() => expect(seen.some((p) => p.get('cursor') === 'CUR1')).toBe(true))

  const toggle = screen.getByRole('button', { name: 'My jobs' })
  expect(toggle).toHaveAttribute('aria-pressed', 'false')
  await userEvent.click(toggle)
  expect(toggle).toHaveAttribute('aria-pressed', 'true')

  await waitFor(() => expect(seen.some((p) => p.get('mine') === 'true')).toBe(true))
  for (const p of seen.filter((x) => x.get('mine') === 'true')) {
    expect(p.has('cursor')).toBe(false)
  }

  await userEvent.click(toggle)
  await waitFor(() => expect(seen[seen.length - 1].has('mine')).toBe(false))
})

test('every view request carries the active filters', async () => {
  renderPage()
  await screen.findByText('job-A')
  await userEvent.type(screen.getByRole('searchbox'), 'etl')
  await userEvent.click(screen.getByRole('button', { name: 'My jobs' }))
  await waitFor(() =>
    expect(seen.some((p) => p.get('q') === 'etl' && p.get('mine') === 'true')).toBe(true),
  )

  seen = []
  await userEvent.click(screen.getByRole('button', { name: 'Lanes' }))
  // limit=10 discriminates a lane request from the table's 50.
  await waitFor(() => expect(seen.filter((p) => p.get('limit') === '10').length).toBe(5))
  for (const p of seen.filter((x) => x.get('limit') === '10')) {
    expect(p.get('q')).toBe('etl')
    expect(p.get('mine')).toBe('true')
  }

  seen = []
  await userEvent.click(screen.getByRole('button', { name: 'Timeline' }))
  // limit=200 discriminates a timeline page from the table's 50 and a lane's 10.
  await waitFor(() => expect(seen.some((p) => p.get('limit') === '200')).toBe(true))
  for (const p of seen.filter((x) => x.get('limit') === '200')) {
    expect(p.get('q')).toBe('etl')
    expect(p.get('mine')).toBe('true')
    expect(p.has('since')).toBe(true)
    expect(p.has('until')).toBe(true)
  }
})

test('an empty filtered table says no jobs match', async () => {
  server.use(
    http.get('/v1/jobs', ({ request }) => {
      const p = new URL(request.url).searchParams
      seen.push(p)
      if (p.get('q')) return HttpResponse.json({ items: [], next_cursor: '', total: 0 })
      return HttpResponse.json({ items: [jobRow('AAAAAA', 'job-A')], next_cursor: '', total: 1 })
    }),
  )
  renderPage()
  await screen.findByText('job-A')
  await userEvent.type(screen.getByRole('searchbox'), 'zzz')
  expect(await screen.findByText('No jobs match those filters.')).toBeInTheDocument()
  expect(screen.queryByText('No jobs yet.')).toBeNull()
})

test('the search box is named, is a searchbox, and is length-capped', async () => {
  renderPage()
  const box = await screen.findByRole('searchbox', { name: 'Search jobs' })
  // A STRUCTURAL PIN, not a claim about what a browser does with the attribute.
  // The attribute counts UTF-16 code units and the server counts runes, so this
  // bound sits at or below the server's and can never produce its 400 - it can
  // only cut an astral-plane string shorter than the server would.
  expect(box).toHaveAttribute('maxlength', '200')
  const placeholder = box.getAttribute('placeholder') ?? ''
  expect(placeholder).toMatch(/name/i)
  expect(placeholder).toMatch(/owner/i)
  // The server matches the job name or the submitter's email and nothing else,
  // so a placeholder promising id search is a wrong-prose defect on arrival.
  expect(placeholder).not.toMatch(/id/i)
})
