import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import { server } from '../test/setup-helpers'
import { renderWithQuery } from '../test/renderWithQuery'
import { SchedulesPage } from './SchedulesPage'

// Belt-and-suspenders even where a test's own try/finally restores real timers: a
// thrown assertion before that finally would leak fake timers into every later
// test in this file.
afterEach(() => vi.useRealTimers())

// SchedulesPage issues a second query. MSW matches paths exactly, so the
// /v1/scheduled-jobs handler each test registers does not answer
// /v1/scheduled-jobs/stats, and setup.ts runs with onUnhandledRequest: 'error'.
// See the fuller note on the sibling SchedulesPage.test.tsx.
//
// Hand-written, with no type annotation naming ScheduleStats. All five keys,
// because the response carries no omitempty.
beforeEach(() => {
  server.use(
    http.get('/v1/scheduled-jobs/stats', () =>
      HttpResponse.json({ enabled: 7, paused: 2, total: 9, failed_runs_24h: 1, failing: 1 }),
    ),
  )
})

// Hand-written, with no type annotation naming Schedule. Every key the server
// sends is present. last_job_id and last_job_status are BOTH absent here, which
// is the pairing the server guarantees for a schedule that has never fired.
function row(over: Record<string, unknown> = {}) {
  return {
    id: 's1',
    name: 'nightly-build',
    owner_id: 'o1',
    owner_email: 'dev@studio.com',
    cron_expr: '0 2 * * *',
    timezone: 'UTC',
    job_spec: {},
    overlap_policy: 'skip',
    enabled: true,
    next_run_at: '2099-01-01T00:00:00Z',
    last_run_at: '2026-06-05T11:00:00Z',
    created_at: '2026-06-01T00:00:00Z',
    updated_at: '2026-06-05T11:00:00Z',
    ...over,
  }
}

// Records every list request's parameters. The assertions in this file are about
// what went ON THE WIRE, which is the only thing a filter is.
function listHandler(seen: URLSearchParams[], envelope?: (p: URLSearchParams) => Record<string, unknown>) {
  return http.get('/v1/scheduled-jobs', ({ request }) => {
    const p = new URL(request.url).searchParams
    seen.push(p)
    return HttpResponse.json(
      envelope ? envelope(p) : { items: [row()], next_cursor: '', total: 1 },
    )
  })
}

function renderPage(debounceMs = 10) {
  return renderWithQuery(
    <MemoryRouter>
      <SchedulesPage debounceMs={debounceMs} />
    </MemoryRouter>,
  )
}

// DISABLED IS ASSERTED BEFORE ALL. If enabledParam('disabled') collapses to an
// omitted parameter the Disabled chip becomes a second All chip, and an assertion
// ordered after the All case would be satisfied by the mutant's own output.
test('each chip sends its own enabled value', async () => {
  const seen: URLSearchParams[] = []
  server.use(listHandler(seen))
  renderPage()
  await screen.findByText('nightly-build')
  expect(seen[0].has('enabled')).toBe(false)

  await userEvent.click(screen.getByRole('button', { name: 'Disabled' }))
  await waitFor(() => expect(seen.at(-1)?.get('enabled')).toBe('false'))

  await userEvent.click(screen.getByRole('button', { name: 'Enabled' }))
  await waitFor(() => expect(seen.at(-1)?.get('enabled')).toBe('true'))

  await userEvent.click(screen.getByRole('button', { name: 'All' }))
  await waitFor(() => expect(seen.at(-1)?.has('enabled')).toBe(false))
})

// IT PAGES FORWARD FIRST, and that is the whole test. With the cursor still empty
// the reset is a no-op, the mutant behaves identically, and a neighbouring test
// would appear to kill a mutation nothing had exercised.
test('filtering after paging forward drops the cursor', async () => {
  const seen: URLSearchParams[] = []
  server.use(
    listHandler(seen, (p) => ({
      items: [row()],
      next_cursor: p.get('cursor') ? '' : 'CUR2',
      total: 2,
    })),
  )
  renderPage()
  await screen.findByText('nightly-build')

  await userEvent.click(screen.getByRole('button', { name: /next 50/ }))
  await waitFor(() => expect(seen.at(-1)?.get('cursor')).toBe('CUR2'))

  await userEvent.click(screen.getByRole('button', { name: 'Enabled' }))
  await waitFor(() => expect(seen.at(-1)?.get('enabled')).toBe('true'))
  // A cursor carries no record of the filters that were active and the server
  // does not reject a mismatched one, so dropping it is the client's job.
  expect(seen.at(-1)?.has('cursor')).toBe(false)
})

// The three buttons are ONE control. Without a group name the three aria-pressed
// states are announced with nothing saying what they switch.
test('the chips are a named group with exactly one pressed', async () => {
  const seen: URLSearchParams[] = []
  server.use(listHandler(seen))
  renderPage()
  await screen.findByText('nightly-build')

  const group = screen.getByRole('group', { name: 'Schedule status filter' })
  expect(within(group).getAllByRole('button')).toHaveLength(3)
  expect(within(group).getAllByRole('button', { pressed: true })).toHaveLength(1)
  expect(screen.getByRole('button', { name: 'All' })).toHaveAttribute('aria-pressed', 'true')

  await userEvent.click(screen.getByRole('button', { name: 'Disabled' }))
  expect(within(group).getAllByRole('button', { pressed: true })).toHaveLength(1)
  expect(screen.getByRole('button', { name: 'Disabled' })).toHaveAttribute('aria-pressed', 'true')
})

// THE RENDER SIDE of the same property the wire assertions cover: with the filter
// missing from the query key, a chip click changes state, re-renders, hashes to
// the same key and never refetches - so the rows never change and the user sees
// the previous filter's list under the new chip.
test('the filter is in the query key, so each chip shows its own rows', async () => {
  const seen: URLSearchParams[] = []
  server.use(
    listHandler(seen, (p) =>
      p.get('enabled') === 'false'
        ? { items: [row({ id: 's2', name: 'weekly-clean', enabled: false })], next_cursor: '', total: 1 }
        : { items: [row()], next_cursor: '', total: 1 },
    ),
  )
  renderPage()
  await screen.findByText('nightly-build')

  await userEvent.click(screen.getByRole('button', { name: 'Disabled' }))
  expect(await screen.findByText('weekly-clean')).toBeInTheDocument()
  expect(screen.queryByText('nightly-build')).toBeNull()

  await userEvent.click(screen.getByRole('button', { name: 'All' }))
  expect(await screen.findByText('nightly-build')).toBeInTheDocument()
})

test('the search box sends the trimmed q, and omits it entirely when empty', async () => {
  const seen: URLSearchParams[] = []
  server.use(listHandler(seen))
  renderPage()
  await screen.findByText('nightly-build')
  // An empty box must not send `q=`. The server reads an empty value as absent
  // either way, so this is hygiene - but it is also what makes the query key and
  // the request agree about what "no filter" is.
  expect(seen[0].has('q')).toBe(false)

  await userEvent.type(screen.getByRole('searchbox', { name: 'Search schedules' }), '  nightly  ')
  await waitFor(() => expect(seen.at(-1)?.get('q')).toBe('nightly'))
})

// COUNTS REQUESTS, so it needs the fake-timer scaffolding UsersTab.test.tsx uses:
// a real setTimeout races userEvent.type's per-keystroke gaps, and on a loaded
// machine those gaps genuinely exceed a 10ms window and produce two or three
// requests. shouldAdvanceTime keeps Testing Library's own setInterval-based
// polling alive; advanceTimersByTimeAsync crosses the window in one jump inside
// act. This NARROWS the race rather than fencing it, and it still kills the
// mutation it exists for: an identity-function debounce produces one request per
// keystroke.
//
// It does NOT page forward first, deliberately. With the cursor already empty the
// raw handler's resetPaging is a no-op that mints no key and issues no request, so
// the count stays clean.
test('a burst of keystrokes issues one request', async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true })
  try {
    const typist = userEvent.setup({ advanceTimers: vi.advanceTimersByTime })
    const seen: URLSearchParams[] = []
    server.use(listHandler(seen))
    renderPage()
    await screen.findByText('nightly-build')

    await typist.type(screen.getByRole('searchbox', { name: 'Search schedules' }), 'nightly')
    await act(async () => {
      await vi.advanceTimersByTimeAsync(10)
    })
    await waitFor(() => expect(seen.some((p) => p.get('q') === 'nightly')).toBe(true))
    expect(seen.filter((p) => p.has('q')).length).toBe(1)
  } finally {
    vi.useRealTimers()
  }
})

// THE LOAD-BEARING HALF of the debounce design. The property is over EVERY request
// the run made, not the last one: an effect keyed on the debounced value runs after
// the render that already issued a query, so exactly ONE request escapes carrying
// the new q and a cursor minted under the old filters, and a last-request-only
// assertion cannot see it.
//
// Real timers are fine here because nothing counts requests - the assertion
// tolerates however many the typing produced.
test('no request carries a new q with an old cursor', async () => {
  const seen: URLSearchParams[] = []
  server.use(
    listHandler(seen, (p) => ({
      items: [row()],
      next_cursor: p.get('cursor') ? '' : 'CUR2',
      total: 2,
    })),
  )
  renderPage()
  await screen.findByText('nightly-build')

  await userEvent.click(screen.getByRole('button', { name: /next 50/ }))
  await waitFor(() => expect(seen.at(-1)?.get('cursor')).toBe('CUR2'))

  await userEvent.type(screen.getByRole('searchbox', { name: 'Search schedules' }), 'nightly')
  await waitFor(() => expect(seen.some((p) => p.get('q') === 'nightly')).toBe(true))
  expect(seen.filter((p) => p.has('q') && p.has('cursor'))).toHaveLength(0)
})

// THE REVERSE ORDER of the test above, and the one resetPaging() in the raw
// keystroke handler does NOT cover. Typing resets the cursor at keystroke time,
// but a click on next INSIDE the still-open debounce window reads `data` from the
// query that is still keyed on the OLD q - so it mints a cursor from a page the
// new filter never produced. That cursor then rides along into the request that
// finally carries the new q, once the debounce settles.
//
// The fix closes the ACQUISITION window rather than catching a stale cursor up
// after the fact: next and prev are disabled for as long as qInput has outrun q,
// so there is no click for the stale `data` to answer.
// shouldAdvanceTime keeps MSW's own async resolution and Testing Library's
// polling alive (both need real ticks), but the keystroke and the click are
// still fireEvent, not userEvent: userEvent's built-in per-action delays are
// real time and could let the 10ms debounce settle on its own between the two -
// a different, legitimate case this test must not credit as a pass. Two
// synchronous fireEvent calls back to back, with no await between them, spend no
// measurable real time.
test('the pager cannot mint a cursor while a debounced search is still pending', async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true })
  try {
    const seen: URLSearchParams[] = []
    server.use(
      listHandler(seen, (p) => ({
        items: [row()],
        next_cursor: p.get('cursor') ? '' : 'CUR2',
        total: 2,
      })),
    )
    renderPage()
    await screen.findByText('nightly-build')

    fireEvent.change(screen.getByRole('searchbox', { name: 'Search schedules' }), {
      target: { value: 'n' },
    })
    // STILL inside the debounce window: qInput ('n') has outrun q ('').
    expect(screen.getByRole('button', { name: /next 50/ })).toBeDisabled()
    fireEvent.click(screen.getByRole('button', { name: /next 50/ }))

    await act(async () => {
      await vi.advanceTimersByTimeAsync(10)
    })
    await waitFor(() => expect(seen.some((p) => p.get('q') === 'n')).toBe(true))
    // No request may ever carry BOTH the new q and a cursor: with next() unable to
    // fire while q was pending, no cursor was ever minted from the stale page.
    expect(seen.filter((p) => p.has('q') && p.has('cursor'))).toHaveLength(0)
  } finally {
    vi.useRealTimers()
  }
})

// A STRUCTURAL PIN ON THE ATTRIBUTE, not a claim about what a browser does with
// it. jsdom performs no native form-control enforcement worth relying on here, so
// this establishes that the cap is declared and nothing more. It matters because
// the browser counts UTF-16 code units and the server counts runes: a 200-unit cap
// is at or below the server's 200-rune bound for every string, so it can never
// produce the 400, and for astral-plane text it truncates earlier, which is the
// safe direction.
test('the search box is named, is a searchbox, and is length-capped', async () => {
  const seen: URLSearchParams[] = []
  server.use(listHandler(seen))
  renderPage()
  await screen.findByText('nightly-build')

  const box = screen.getByRole('searchbox', { name: 'Search schedules' })
  expect(box).toHaveAttribute('maxlength', '200')
  const placeholder = box.getAttribute('placeholder') ?? ''
  expect(placeholder).toMatch(/name/i)
  expect(placeholder).toMatch(/owner/i)
  expect(placeholder).toMatch(/cron/i)
})

// Two totals will appear on one screen once the strip lands - one fleet-wide, one
// filtered. Unlabelled they read as a bug the first time a filter is active. This
// pins the footer half; the strip's caption is pinned in SchedulesPage.stats.test.tsx,
// and both are driven by one derived boolean so they cannot disagree.
test('the footer says MATCHING exactly when a filter is active', async () => {
  const seen: URLSearchParams[] = []
  server.use(listHandler(seen, () => ({ items: [row()], next_cursor: '', total: 7 })))
  renderPage()
  await screen.findByText('nightly-build')
  expect(await screen.findByText('1-1 of 7')).toBeInTheDocument()

  await userEvent.click(screen.getByRole('button', { name: 'Enabled' }))
  expect(await screen.findByText('1-1 of 7 MATCHING')).toBeInTheDocument()
})

// The zero-row branch gets the same treatment, and the empty card's sentence
// changes with it: "No schedules yet." is false while a filter is narrowing the
// set, and a user who cannot tell "none" from "none matching" files a bug.
test('an empty filtered table says no schedules match, and an unfiltered one says none exist', async () => {
  const seen: URLSearchParams[] = []
  server.use(
    listHandler(seen, (p) =>
      p.has('enabled')
        ? { items: [], next_cursor: '', total: 0 }
        : { items: [row()], next_cursor: '', total: 1 },
    ),
  )
  renderPage()
  await screen.findByText('nightly-build')

  await userEvent.click(screen.getByRole('button', { name: 'Disabled' }))
  expect(await screen.findByText('No schedules match these filters.')).toBeInTheDocument()
  expect(screen.queryByText('No schedules yet.')).toBeNull()
  expect(await screen.findByText('0 of 0 MATCHING')).toBeInTheDocument()

  await userEvent.click(screen.getByRole('button', { name: 'All' }))
  expect(await screen.findByText('nightly-build')).toBeInTheDocument()
})
