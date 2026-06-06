import { http, HttpResponse } from 'msw'
import { expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { ApiError } from '../lib/api'
import { listWorkers, getWorkerStats, listRevokedWorkers, type WorkersPage } from './api'

const emptyPage: WorkersPage = { items: [], next_cursor: '', total: 0 }

test('requests the first page with the given sort and limit=50', async () => {
  let captured: URLSearchParams | undefined
  server.use(
    http.get('/v1/workers', ({ request }) => {
      captured = new URL(request.url).searchParams
      return HttpResponse.json(emptyPage)
    }),
  )
  await listWorkers('name')
  expect(captured?.get('sort')).toBe('name')
  expect(captured?.get('limit')).toBe('50')
  expect(captured?.get('cursor')).toBeNull()
})

test('parses the page payload', async () => {
  server.use(
    http.get('/v1/workers', () =>
      HttpResponse.json({
        items: [{ id: 'w1', name: 'render-01', status: 'online' }],
        next_cursor: 'abc',
        total: 1,
      }),
    ),
  )
  const page = await listWorkers('-created_at')
  expect(page.total).toBe(1)
  expect(page.items[0].name).toBe('render-01')
})

test('throws ApiError on the error envelope', async () => {
  server.use(
    http.get('/v1/workers', () =>
      HttpResponse.json({ error: 'boom' }, { status: 500 }),
    ),
  )
  await expect(listWorkers('-created_at')).rejects.toBeInstanceOf(ApiError)
})

test('getWorkerStats fetches /workers/stats', async () => {
  let captured: string | undefined
  server.use(
    http.get('/v1/workers/stats', ({ request }) => {
      captured = new URL(request.url).pathname
      return HttpResponse.json({ online: 3, stale: 1, offline: 2, disabled: 1, total: 7 })
    }),
  )
  const stats = await getWorkerStats()
  expect(captured).toBe('/v1/workers/stats')
  expect(stats.total).toBe(7)
  expect(stats.online).toBe(3)
})

test('getWorkerStats throws ApiError on the error envelope', async () => {
  server.use(
    http.get('/v1/workers/stats', () => HttpResponse.json({ error: 'boom' }, { status: 500 })),
  )
  await expect(getWorkerStats()).rejects.toBeInstanceOf(ApiError)
})

test('listRevokedWorkers fetches /workers/revoked with limit=50', async () => {
  let captured: string | undefined
  server.use(
    http.get('/v1/workers/revoked', ({ request }) => {
      captured = new URL(request.url).pathname
      return HttpResponse.json({
        items: [{ id: 'w1', name: 'gone-1', status: 'revoked', revoked_at: '2026-01-02T03:04:05Z' }],
        next_cursor: '',
        total: 1,
      })
    }),
  )
  const page = await listRevokedWorkers()
  expect(captured).toBe('/v1/workers/revoked')
  expect(page.items[0].revoked_at).toBe('2026-01-02T03:04:05Z')
})
