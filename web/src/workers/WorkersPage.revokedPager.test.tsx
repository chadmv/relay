import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { afterEach, expect, test } from 'vitest'
import { MemoryRouter } from 'react-router-dom'
import { server } from '../test/setup-helpers'
import { renderWithQuery } from '../test/renderWithQuery'
import { WorkersPage } from './WorkersPage'

// The decommissioned pager must advance on the REVOKED query's page. Both queries on
// this surface return the same page shape, so handing it the active workers page
// instead typechecks. The active fixture therefore carries its own non-empty
// next_cursor: with an empty one the substitution would be caught only by becoming a
// silent no-op, which is a property of the fixture rather than of the wiring. The
// assertion is on the cursor the revoked endpoint actually receives.

const activeWorker = {
  id: 'w1',
  name: 'render-01',
  hostname: 'h',
  cpu_cores: 16,
  ram_gb: 128,
  gpu_count: 1,
  gpu_model: 'RTX 4090',
  os: 'linux',
  max_slots: 4,
  labels: null,
  status: 'online',
  last_seen_at: '2026-06-03T12:00:00Z',
}

function revokedWorker(id: string, name: string) {
  return {
    id,
    name,
    hostname: `${name}-host`,
    cpu_cores: 4,
    ram_gb: 16,
    gpu_count: 0,
    gpu_model: '',
    os: 'linux',
    max_slots: 1,
    labels: null,
    status: 'revoked',
    revoked_at: '2026-01-02T03:04:05Z',
  }
}

function makeRevoked(count: number, startId = 0) {
  return Array.from({ length: count }, (_, i) => revokedWorker(`rw${startId + i}`, `gone-${startId + i}`))
}

afterEach(() => localStorage.clear())

test('the decommissioned next button advances the revoked cursor, not the active workers cursor', async () => {
  const cursors: string[] = []
  server.use(
    http.get('/v1/workers', () =>
      HttpResponse.json({ items: [activeWorker], next_cursor: 'ACTIVE_CUR', total: 1 }),
    ),
  )
  server.use(
    http.get('/v1/workers/stats', () =>
      HttpResponse.json({ online: 1, stale: 0, offline: 0, disabled: 0, total: 1 }),
    ),
  )
  server.use(
    http.get('/v1/workers/revoked', ({ request }) => {
      const cur = new URL(request.url).searchParams.get('cursor') ?? ''
      cursors.push(cur)
      return HttpResponse.json(
        cur === 'REVOKED_CUR'
          ? { items: makeRevoked(13, 50), next_cursor: '', total: 63 }
          : { items: makeRevoked(50, 0), next_cursor: 'REVOKED_CUR', total: 63 },
      )
    }),
  )

  renderWithQuery(
    <MemoryRouter>
      <WorkersPage />
    </MemoryRouter>,
  )
  await screen.findByText('render-01')
  await userEvent.click(screen.getByRole('button', { name: 'Decommissioned' }))
  await screen.findByText('gone-0')

  await userEvent.click(screen.getByRole('button', { name: /next/i }))

  // The cursor is asserted BEFORE the row text. Asserting the rows first makes a
  // wrong-page substitution surface as a findBy timeout with a DOM dump, and the
  // cursor assertion below never runs - so the failure would not name its own cause.
  await waitFor(() => expect(cursors).toHaveLength(2))
  expect(cursors[1]).toBe('REVOKED_CUR')
  expect(cursors).not.toContain('ACTIVE_CUR')

  await screen.findByText('gone-50')
  expect(await screen.findByText(/51-63 of 63/i)).toBeInTheDocument()
})
