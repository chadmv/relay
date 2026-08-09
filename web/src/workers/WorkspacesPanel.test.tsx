import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { renderWithQuery } from '../test/renderWithQuery'
import { WorkspacesPanel } from './WorkspacesPanel'

test('renders workspace rows', async () => {
  server.use(
    http.get('/v1/workers/w1/workspaces', () =>
      HttpResponse.json([
        {
          source_type: 'perforce',
          source_key: '//depot/x/main',
          short_id: 'ws-a4f2',
          baseline_hash: '@CL 81234',
          last_used_at: '2026-06-05T00:00:00Z',
        },
      ]),
    ),
  )
  renderWithQuery(<WorkspacesPanel workerId="w1" />)
  expect(await screen.findByText('ws-a4f2')).toBeInTheDocument()
  expect(screen.getByText('//depot/x/main')).toBeInTheDocument()
})

test('shows the empty state when there are no workspaces', async () => {
  server.use(http.get('/v1/workers/w1/workspaces', () => HttpResponse.json([])))
  renderWithQuery(<WorkspacesPanel workerId="w1" />)
  expect(await screen.findByText('No workspaces.')).toBeInTheDocument()
})

test('clicking Evict opens a confirm dialog; confirm POSTs the evict path', async () => {
  let hits = 0
  server.use(
    http.get('/v1/workers/w1/workspaces', () =>
      HttpResponse.json([
        { source_type: 'perforce', source_key: '//depot/x', short_id: 'ws-a4f2', baseline_hash: '@1', last_used_at: '2026-06-05T00:00:00Z' },
      ]),
    ),
  )
  server.use(
    http.post('/v1/workers/w1/workspaces/ws-a4f2/evict', () => {
      hits++
      return new HttpResponse(null, { status: 202 })
    }),
  )
  renderWithQuery(<WorkspacesPanel workerId="w1" />)
  await screen.findByText('ws-a4f2')
  await userEvent.click(screen.getByRole('button', { name: /evict/i }))
  const dialog = screen.getByRole('dialog')
  await userEvent.click(within(dialog).getByRole('button', { name: 'Evict' }))
  await waitFor(() => expect(hits).toBe(1))
})

test('does not render its own SOURCE WORKSPACES heading (the page Panel supplies it)', async () => {
  server.use(http.get('/v1/workers/w1/workspaces', () => HttpResponse.json([])))
  renderWithQuery(<WorkspacesPanel workerId="w1" />)
  await screen.findByText('No workspaces.')
  expect(screen.queryByText('SOURCE WORKSPACES')).not.toBeInTheDocument()
})

test('cancelling the evict confirm fires no request', async () => {
  let hits = 0
  server.use(
    http.get('/v1/workers/w1/workspaces', () =>
      HttpResponse.json([
        { source_type: 'perforce', source_key: '//depot/x', short_id: 'ws-a4f2', baseline_hash: '@1', last_used_at: '2026-06-05T00:00:00Z' },
      ]),
    ),
  )
  server.use(http.post('/v1/workers/w1/workspaces/ws-a4f2/evict', () => { hits++; return new HttpResponse(null, { status: 202 }) }))
  renderWithQuery(<WorkspacesPanel workerId="w1" />)
  await screen.findByText('ws-a4f2')
  await userEvent.click(screen.getByRole('button', { name: /evict/i }))
  await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
  await new Promise((r) => setTimeout(r, 20))
  expect(hits).toBe(0)
})

test('exposes table, row, columnheader, and cell roles', async () => {
  server.use(
    http.get('/v1/workers/w1/workspaces', () =>
      HttpResponse.json([
        { source_type: 'perforce', source_key: '//depot/x', short_id: 'ws-a4f2', baseline_hash: '@1', last_used_at: '2026-06-05T00:00:00Z' },
        { source_type: 'perforce', source_key: '//depot/y', short_id: 'ws-b7c9', baseline_hash: '@2', last_used_at: '2026-06-05T00:00:00Z' },
      ]),
    ),
  )
  renderWithQuery(<WorkspacesPanel workerId="w1" />)
  expect(await screen.findByText('ws-b7c9')).toBeInTheDocument()
  // The accessible name matches the visible title on the page Panel that wraps this.
  expect(screen.getByRole('table', { name: 'Source workspaces' })).toBeInTheDocument()
  // 1 header row + 2 data rows.
  expect(screen.getAllByRole('row')).toHaveLength(3)
  // SHORT ID, TYPE, SOURCE KEY, BASELINE, LAST USED, ACTIONS.
  expect(screen.getAllByRole('columnheader')).toHaveLength(6)
  // 6 columns x 2 rows.
  expect(screen.getAllByRole('cell')).toHaveLength(12)
  // Structural, not just a page-global count: pins the first data row's cells to
  // that row specifically, which a cell rendered outside the role="table" subtree
  // (the exact invalid-child hazard the primitive exists to prevent) would not
  // satisfy even if the page-global counts above still summed correctly.
  const firstDataRow = screen.getAllByRole('row')[1]
  expect(within(firstDataRow).getAllByRole('cell')).toHaveLength(6)
})
