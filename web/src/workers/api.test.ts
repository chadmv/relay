import { http, HttpResponse } from 'msw'
import { expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { ApiError } from '../lib/api'
import {
  listWorkers,
  getWorkerStats,
  getWorker,
  getWorkerMetrics,
  listWorkerWorkspaces,
  type WorkersPage,
} from './api'

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

test('getWorker fetches /workers/{id}', async () => {
  let path: string | undefined
  server.use(
    http.get('/v1/workers/w1', ({ request }) => {
      path = new URL(request.url).pathname
      return HttpResponse.json({ id: 'w1', name: 'render-01', status: 'online' })
    }),
  )
  const w = await getWorker('w1')
  expect(path).toBe('/v1/workers/w1')
  expect(w.name).toBe('render-01')
})

test('getWorkerMetrics fetches /workers/{id}/metrics', async () => {
  server.use(
    http.get('/v1/workers/w1/metrics', () =>
      HttpResponse.json({
        worker_id: 'w1',
        sample_interval_seconds: 10,
        samples: [
          {
            t: '2026-06-05T00:00:00Z',
            cpu_pct: 12.5,
            mem_used: 1,
            mem_total: 2,
            gpu: false,
            gpu_util_pct: 0,
            gpu_mem_used: 0,
            gpu_mem_total: 0,
          },
        ],
      }),
    ),
  )
  const m = await getWorkerMetrics('w1')
  expect(m.sample_interval_seconds).toBe(10)
  expect(m.samples[0].cpu_pct).toBe(12.5)
})

test('listWorkerWorkspaces fetches /workers/{id}/workspaces', async () => {
  server.use(
    http.get('/v1/workers/w1/workspaces', () =>
      HttpResponse.json([
        {
          source_type: 'perforce',
          source_key: '//depot/x',
          short_id: 'ws-1',
          baseline_hash: '@1',
          last_used_at: '2026-06-05T00:00:00Z',
        },
      ]),
    ),
  )
  const ws = await listWorkerWorkspaces('w1')
  expect(ws).toHaveLength(1)
  expect(ws[0].short_id).toBe('ws-1')
})

test('getWorker throws ApiError on the error envelope', async () => {
  server.use(http.get('/v1/workers/w1', () => HttpResponse.json({ error: 'worker not found' }, { status: 404 })))
  await expect(getWorker('w1')).rejects.toBeInstanceOf(ApiError)
})

test('getWorkerMetrics throws ApiError on the error envelope', async () => {
  server.use(http.get('/v1/workers/w1/metrics', () => HttpResponse.json({ error: 'boom' }, { status: 500 })))
  await expect(getWorkerMetrics('w1')).rejects.toBeInstanceOf(ApiError)
})

test('listWorkerWorkspaces throws ApiError on the error envelope', async () => {
  server.use(http.get('/v1/workers/w1/workspaces', () => HttpResponse.json({ error: 'boom' }, { status: 500 })))
  await expect(listWorkerWorkspaces('w1')).rejects.toBeInstanceOf(ApiError)
})
