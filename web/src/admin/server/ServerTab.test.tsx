import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
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
