import { screen, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { renderWithQuery } from '../test/renderWithQuery'
import { formatDateTime } from '../lib/time'
import { WorkerReservationsPanel } from './WorkerReservationsPanel'

// Fixed, injected, and inside the ACTIVE row's window: a clock read from the
// runner would make the status assertions drift into ENDED over time.
const NOW = new Date('2026-05-14T12:00:00Z')

// Hand-written literals carrying every key the server sends. ACTIVE_ROW has all
// three optional keys PRESENT; OPEN_ROW has all three ABSENT, which is the real
// wire shape for a NULL column (pointer + omitempty omits the key entirely, so a
// consumer that reads them as null is wrong in a way a null-carrying fixture
// could never show). Not built from the app's Reservation type.
const ACTIVE_ROW = {
  id: 'r-active',
  name: 'vfx-sprint',
  selector: { rack: 'gpu-farm' },
  worker_ids: ['w-1'],
  user_id: 'u-1',
  created_at: '2026-05-01T00:00:00Z',
  project: 'film-x',
  starts_at: '2026-05-01T09:00:00Z',
  ends_at: '2026-05-20T18:00:00Z',
}

const OPEN_ROW = {
  id: 'r-open',
  name: 'indefinite-hold',
  selector: null,
  worker_ids: ['w-1'],
  user_id: 'u-1',
  created_at: '2026-04-01T00:00:00Z',
}

const ENDED_ROW = {
  id: 'r-ended',
  name: 'last-week',
  selector: {},
  worker_ids: ['w-1'],
  user_id: 'u-1',
  created_at: '2026-05-01T00:00:00Z',
  ends_at: '2026-05-02T00:00:00Z',
}

const SCHEDULED_ROW = {
  id: 'r-scheduled',
  name: 'next-month',
  selector: null,
  worker_ids: ['w-1'],
  user_id: 'u-1',
  created_at: '2026-05-01T00:00:00Z',
  starts_at: '2026-06-01T00:00:00Z',
}

function page(items: unknown[], over: Record<string, unknown> = {}) {
  return { items, next_cursor: '', total: items.length, ...over }
}

function renderPanel(body: Record<string, unknown>) {
  server.use(http.get('/v1/reservations', () => HttpResponse.json(body)))
  return renderWithQuery(<WorkerReservationsPanel workerId="w-1" now={NOW} />)
}

// Kills: dropping the status column; reading ends_at as null rather than absent
// (OPEN_ROW would then render the string 'null' or crash on the missing key).
test('renders a row per reservation', async () => {
  renderPanel(page([ACTIVE_ROW, OPEN_ROW]))
  expect(await screen.findByText('vfx-sprint')).toBeInTheDocument()
  expect(screen.getByText('indefinite-hold')).toBeInTheDocument()
  expect(screen.getByText('film-x')).toBeInTheDocument()
  // Both rows are inside their window, so both derive ACTIVE.
  expect(screen.getAllByText('ACTIVE')).toHaveLength(2)
})

// Kills: swapping ends_at for starts_at in the ENDS cell. ACTIVE_ROW carries
// both, formatted through the same formatDateTime the cell itself uses (never a
// hardcoded string, since the cell renders in the runner's local time and a
// literal would drift with the runner's timezone) - and the two are asserted as
// DISTINCT strings first, or a row where they happened to format identically
// would pass on either source.
test('the ENDS cell renders ends_at, not starts_at', async () => {
  const endsText = formatDateTime(ACTIVE_ROW.ends_at)
  const startsText = formatDateTime(ACTIVE_ROW.starts_at)
  expect(endsText).not.toBe(startsText)
  renderPanel(page([ACTIVE_ROW]))
  expect(await screen.findByText(endsText)).toBeInTheDocument()
  expect(screen.queryByText(startsText)).not.toBeInTheDocument()
})

// Kills: rendering the admin table's absent-value hyphen. The absence IS the
// fact here - an open-ended reservation excludes this worker indefinitely - and
// a hyphen reads as a missing value.
test('an open-ended reservation says so', async () => {
  renderPanel(page([OPEN_ROW]))
  expect(await screen.findByText('no end')).toBeInTheDocument()
  expect(screen.queryByText('undefined')).not.toBeInTheDocument()
  // The PROJECT cell legitimately renders the admin table's absent-value hyphen
  // for OPEN_ROW's absent project - that convention is correct there. Scoped to a
  // count of one so it cannot be confused with the ENDS cell also rendering one.
  expect(screen.getAllByText('-')).toHaveLength(1)
})

// Kills: rendering a placeholder row. The header row survives every state, so a
// fabricated row shows up as a second row.
test('an empty result says no reservation targets this worker', async () => {
  renderPanel(page([]))
  expect(await screen.findByText('No reservation targets this worker.')).toBeInTheDocument()
  expect(screen.getAllByRole('row')).toHaveLength(1)
})

// Kills: comparing next_cursor only, and dropping the footer. `total` is the
// worker's whole reservation count while `items` is one page of it, so a table
// that silently stops at the page limit reads as the full set.
test('a short page says so', async () => {
  renderPanel(page([ACTIVE_ROW], { total: 7 }))
  expect(await screen.findByText('showing 1 of 7')).toBeInTheDocument()
})

test('a complete page states no count', async () => {
  renderPanel(page([ACTIVE_ROW]))
  expect(await screen.findByText('vfx-sprint')).toBeInTheDocument()
  expect(screen.queryByText(/^showing /)).not.toBeInTheDocument()
})

// Kills: rendering the line unconditionally. The negative case uses an ENDED and
// a SCHEDULED row rather than an empty page, so the line's absence is about
// derived status and not about there being no rows.
test('an active reservation states the dispatch consequence', async () => {
  renderPanel(page([ACTIVE_ROW]))
  expect(await screen.findByText(/scheduler skips this worker/)).toBeInTheDocument()
})

test('no active reservation issues no dispatch claim', async () => {
  renderPanel(page([ENDED_ROW, SCHEDULED_ROW]))
  expect(await screen.findByText('last-week')).toBeInTheDocument()
  expect(screen.getByText('ENDED')).toBeInTheDocument()
  expect(screen.getByText('SCHEDULED')).toBeInTheDocument()
  expect(screen.queryByText(/scheduler skips this worker/)).not.toBeInTheDocument()
})

// Kills: moving the footnote inside the rows branch. It is the panel's
// correctness statement - the filter matches worker_ids containment alone, so a
// selector-only reservation is absent from this list and the footnote is what
// tells the reader the absence is correct. The empty state needs it MOST.
test('the selector footnote is present in the empty state', async () => {
  renderPanel(page([]))
  await waitFor(() =>
    expect(screen.getByText(/^selectors are informational in v1/)).toBeInTheDocument(),
  )
})

// Kills: dropping the retry affordance, and rendering the error INSIDE the table
// subtree where it is not a valid child.
test('a failed read shows the message and a Retry', async () => {
  server.use(
    http.get('/v1/reservations', () =>
      HttpResponse.json({ error: 'list reservations failed' }, { status: 500 }),
    ),
  )
  renderWithQuery(<WorkerReservationsPanel workerId="w-1" now={NOW} />)
  expect(await screen.findByText(/list reservations failed/)).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Retry' })).toBeInTheDocument()
  expect(screen.getByRole('table', { name: 'Reservations' })).toBeInTheDocument()
})
