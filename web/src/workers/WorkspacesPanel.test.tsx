import { screen } from '@testing-library/react'
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
