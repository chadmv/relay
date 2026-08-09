import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { expect, test } from 'vitest'
import { server } from '../../test/setup-helpers'
import { ServerTab } from './ServerTab'

// Distinct values per field, so a swapped-field regression (e.g. `stale` rendered
// under OFFLINE) fails instead of passing on a coincidence.
const JOB_STATS = { running: 11, queued: 22, done_24h: 33, failed_24h: 44 }
const WORKER_STATS = { online: 55, stale: 66, offline: 77, disabled: 88, total: 99 }

function handlers({ allowSelfRegister = false }: { allowSelfRegister?: boolean } = {}) {
  return [
    http.get('/v1/jobs/stats', () => HttpResponse.json(JOB_STATS)),
    http.get('/v1/workers/stats', () => HttpResponse.json(WORKER_STATS)),
    http.get('/v1/config', () => HttpResponse.json({ allow_self_register: allowSelfRegister })),
    http.get('/v1/health', () => HttpResponse.json({ status: 'ok' })),
  ]
}

// ServerTab uses no auth context and renders no router Link, so neither
// AuthProvider nor MemoryRouter is needed - only the query client.
function renderTab() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return {
    client,
    ...render(
      <QueryClientProvider client={client}>
        <ServerTab />
      </QueryClientProvider>,
    ),
  }
}

test('renders all nine counts against their exact fields', async () => {
  server.use(...handlers())
  renderTab()
  expect(await screen.findByText('11')).toBeInTheDocument()
  for (const [label, value] of [
    ['RUNNING', '11'],
    ['QUEUED', '22'],
    ['DONE · 24H', '33'],
    ['FAILED OR CANCELLED · 24H', '44'],
    ['ONLINE', '55'],
    ['STALE', '66'],
    ['OFFLINE', '77'],
    ['DISABLED', '88'],
    ['TOTAL', '99'],
  ] as const) {
    // getAllByText, not getByText: DISABLED is also the self-registration chip's
    // text with the default (false) handler in this suite's handlers(), so the
    // label alone is not unique. The numeric value is still asserted uniquely.
    expect(screen.getAllByText(label).length).toBeGreaterThan(0)
    expect(screen.getByText(value)).toBeInTheDocument()
  }
})

test('the failed bucket is labelled failed OR cancelled', async () => {
  // JobStatusCounts filters status IN ('failed','cancelled')
  // (internal/store/query/jobs.sql:282-292), so "Failed" alone would be wrong.
  server.use(...handlers())
  renderTab()
  expect(await screen.findByText('FAILED OR CANCELLED · 24H')).toBeInTheDocument()
  expect(screen.queryByText('FAILED · 24H')).not.toBeInTheDocument()
})

test('TOTAL carries the revoked-exclusion sub-line and QUEUED says status = pending', async () => {
  server.use(...handlers())
  renderTab()
  expect(await screen.findByText('revoked workers excluded')).toBeInTheDocument()
  expect(screen.getByText('status = pending')).toBeInTheDocument()
})

test('the pill reads HEALTHY when /v1/health says ok', async () => {
  server.use(...handlers())
  renderTab()
  expect(await screen.findByText('HEALTHY')).toBeInTheDocument()
})

test('the self-registration chip reads DISABLED for allow_self_register: false', async () => {
  server.use(...handlers({ allowSelfRegister: false }))
  renderTab()
  // The FLEET section's DISABLED KPI label renders immediately (before any query
  // settles), so waiting on 'DISABLED' alone resolves too early and races the
  // config query. Wait on the prose line instead, which only exists once
  // config.data has actually landed.
  expect(
    await screen.findByText('POST /v1/auth/register requires an invite_token.'),
  ).toBeInTheDocument()
  expect(screen.getByText('Self-registration')).toBeInTheDocument()
  // Two 'DISABLED' matches are expected here: the FLEET KPI label and the chip.
  // Distinguish the chip by tag - Chip renders a <span>, KpiStat's Eyebrow a <div>.
  const disabledEls = screen.getAllByText('DISABLED')
  expect(disabledEls.some((el) => el.tagName === 'SPAN')).toBe(true)
})

test('the self-registration chip reads ENABLED for allow_self_register: true', async () => {
  server.use(...handlers({ allowSelfRegister: true }))
  renderTab()
  expect(await screen.findByText('ENABLED')).toBeInTheDocument()
  expect(
    screen.getByText('POST /v1/auth/register creates a non-admin account without an invite.'),
  ).toBeInTheDocument()
})

test('renders no version, build, uptime, database or environment content', async () => {
  server.use(...handlers())
  const { container } = renderTab()
  await screen.findByText('11')
  // innerHTML, not textContent: an env row rendered into a title or aria-label
  // would be invisible to textContent.
  const html = container.innerHTML
  for (const forbidden of [
    'VERSION',
    'BUILD',
    'UPTIME',
    'RELAY_',
    'postgres://',
    'go1.',
  ]) {
    expect(html).not.toContain(forbidden)
  }
})

test('the footnote states the 24h proxy and that the pill is not a database probe', async () => {
  server.use(...handlers())
  renderTab()
  await screen.findByText('11')
  const footnote = screen.getByTestId('server-footnote').textContent ?? ''
  expect(footnote).toContain('updated_at')
  expect(footnote).toContain('does not check the database')
})

const fail = (path: string) =>
  http.get(path, () => HttpResponse.json({ error: 'boom' }, { status: 500 }))

test('a jobs/stats 500 degrades ONLY the jobs section', async () => {
  // fail(...) MUST be listed before handlers(): MSW resolves handlers registered
  // in one server.use() call in order, first match wins, so a later handler for
  // the same path is dead code.
  server.use(fail('/v1/jobs/stats'), ...handlers())
  renderTab()
  // The fleet numbers, the chip and the pill all survive.
  expect(await screen.findByText('55')).toBeInTheDocument()
  expect(screen.getByText('99')).toBeInTheDocument()
  expect(screen.getAllByText('DISABLED').some((el) => el.tagName === 'SPAN')).toBe(true)
  expect(screen.getByText('HEALTHY')).toBeInTheDocument()
  // The jobs section shows the strip, and no jobs number is on screen.
  expect(screen.getByText('500 boom')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Retry' })).toBeInTheDocument()
  expect(screen.queryByText('RUNNING')).not.toBeInTheDocument()
  expect(screen.queryByText('11')).not.toBeInTheDocument()
})

test('Retry issues exactly one more jobs/stats request and restores the grid', async () => {
  let calls = 0
  server.use(
    http.get('/v1/jobs/stats', () => {
      calls++
      return calls === 1
        ? HttpResponse.json({ error: 'boom' }, { status: 500 })
        : HttpResponse.json(JOB_STATS)
    }),
    ...handlers(),
  )
  renderTab()
  await screen.findByText('500 boom')
  expect(calls).toBe(1)
  await userEvent.click(screen.getByRole('button', { name: 'Retry' }))
  expect(await screen.findByText('11')).toBeInTheDocument()
  expect(screen.getByText('RUNNING')).toBeInTheDocument()
  expect(screen.queryByText('500 boom')).not.toBeInTheDocument()
  expect(calls).toBe(2)
})

test('a workers/stats 500 degrades ONLY the fleet section', async () => {
  server.use(fail('/v1/workers/stats'), ...handlers())
  renderTab()
  expect(await screen.findByText('11')).toBeInTheDocument()
  expect(screen.getByText('44')).toBeInTheDocument()
  expect(screen.getAllByText('DISABLED').some((el) => el.tagName === 'SPAN')).toBe(true)
  expect(screen.getByText('HEALTHY')).toBeInTheDocument()
  expect(screen.getByText('500 boom')).toBeInTheDocument()
  expect(screen.queryByText('ONLINE')).not.toBeInTheDocument()
  expect(screen.queryByText('99')).not.toBeInTheDocument()
})

test('a config 500 renders NO self-registration chip in either state', async () => {
  server.use(fail('/v1/config'), ...handlers())
  renderTab()
  expect(await screen.findByText('11')).toBeInTheDocument()
  expect(screen.getByText('55')).toBeInTheDocument()
  // Absence of BOTH as a chip (span), so a fabricated default cannot pass this
  // test. 'DISABLED' still appears once as the FLEET KPI label, which is expected.
  expect(screen.queryByText('ENABLED')).not.toBeInTheDocument()
  expect(
    screen.getAllByText('DISABLED').some((el) => el.tagName === 'SPAN'),
  ).toBe(false)
  expect(screen.getByText('Access')).toBeInTheDocument()
  expect(screen.getByText('500 boom')).toBeInTheDocument()
})

test('a health 500 shows UNREACHABLE and nothing else changes', async () => {
  server.use(fail('/v1/health'), ...handlers())
  renderTab()
  expect(await screen.findByText('UNREACHABLE')).toBeInTheDocument()
  for (const v of ['11', '22', '33', '44', '55', '66', '77', '88', '99']) {
    expect(screen.getByText(v)).toBeInTheDocument()
  }
  expect(screen.getAllByText('DISABLED').some((el) => el.tagName === 'SPAN')).toBe(true)
})

test('the realistic outage: health ok while BOTH stats endpoints 500', async () => {
  // Postgres down, server up. The pill is a listener probe, not a database probe,
  // so HEALTHY here is CORRECT - and this test fails loudly if anyone later derives
  // the pill from the stat queries to make it look smarter.
  server.use(fail('/v1/jobs/stats'), fail('/v1/workers/stats'), ...handlers())
  renderTab()
  expect(await screen.findByText('HEALTHY')).toBeInTheDocument()
  expect(screen.getAllByText('500 boom')).toHaveLength(2)
  expect(screen.getAllByRole('button', { name: 'Retry' })).toHaveLength(2)
  expect(screen.queryByText('RUNNING')).not.toBeInTheDocument()
  expect(screen.queryByText('ONLINE')).not.toBeInTheDocument()
})

test('all four failing still renders the header, both captions, the panel and the footnote', async () => {
  server.use(
    fail('/v1/jobs/stats'),
    fail('/v1/workers/stats'),
    fail('/v1/config'),
    fail('/v1/health'),
  )
  renderTab()
  expect(await screen.findByText('UNREACHABLE')).toBeInTheDocument()
  expect(screen.getByText('Server overview')).toBeInTheDocument()
  expect(screen.getByText('JOBS · GET /v1/jobs/stats')).toBeInTheDocument()
  expect(screen.getByText('FLEET · GET /v1/workers/stats')).toBeInTheDocument()
  expect(screen.getByText('Access')).toBeInTheDocument()
  expect(screen.getByTestId('server-footnote')).toBeInTheDocument()
  expect(screen.getAllByRole('button', { name: 'Retry' })).toHaveLength(3)
})

test('a poll that fails AFTER a good load keeps the numbers and marks them stale', async () => {
  let calls = 0
  server.use(
    http.get('/v1/jobs/stats', () => {
      calls++
      return calls === 1
        ? HttpResponse.json(JOB_STATS)
        : HttpResponse.json({ error: 'boom' }, { status: 500 })
    }),
    ...handlers(),
  )
  const { client } = renderTab()
  expect(await screen.findByText('11')).toBeInTheDocument()

  // Drive the second fetch explicitly rather than waiting out the 10s interval.
  await client.refetchQueries({ queryKey: ['job-stats'] })

  await waitFor(() => expect(screen.getByText('stale · last update failed')).toBeInTheDocument())
  // The numbers are STILL on screen - a dropped poll must not blank good data.
  expect(screen.getByText('11')).toBeInTheDocument()
  expect(screen.getByText('44')).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Retry' })).not.toBeInTheDocument()
})
