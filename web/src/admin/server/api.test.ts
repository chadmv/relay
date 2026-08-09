import { http, HttpResponse } from 'msw'
import { expect, test } from 'vitest'
import { server } from '../../test/setup-helpers'
import { ApiError } from '../../lib/api'
import { getHealth, getServerConfig } from './api'

test('getHealth requests /v1/health and returns the status string', async () => {
  let path: string | undefined
  server.use(
    http.get('/v1/health', ({ request }) => {
      path = new URL(request.url).pathname
      return HttpResponse.json({ status: 'ok' })
    }),
  )
  const res = await getHealth()
  expect(path).toBe('/v1/health')
  expect(res.status).toBe('ok')
})

test('getHealth passes through a non-ok status rather than normalizing it', async () => {
  // handleHealth writes map[string]string, so the value is NOT a closed union.
  // A future "degraded" must reach the pill instead of being coerced to ok.
  server.use(http.get('/v1/health', () => HttpResponse.json({ status: 'degraded' })))
  expect((await getHealth()).status).toBe('degraded')
})

test('getHealth surfaces a 500 as an ApiError', async () => {
  server.use(http.get('/v1/health', () => HttpResponse.json({ error: 'boom' }, { status: 500 })))
  const err = await getHealth().catch((e) => e)
  expect(err).toBeInstanceOf(ApiError)
  expect(err).toMatchObject({ status: 500, code: 'boom' })
})

test('getServerConfig requests /v1/config and returns allow_self_register', async () => {
  let path: string | undefined
  server.use(
    http.get('/v1/config', ({ request }) => {
      path = new URL(request.url).pathname
      return HttpResponse.json({ allow_self_register: true })
    }),
  )
  const res = await getServerConfig()
  expect(path).toBe('/v1/config')
  expect(res.allow_self_register).toBe(true)
})

test('getServerConfig carries false through, not undefined', async () => {
  server.use(http.get('/v1/config', () => HttpResponse.json({ allow_self_register: false })))
  const res = await getServerConfig()
  expect(res.allow_self_register).toBe(false)
})

test('getServerConfig surfaces a 500 as an ApiError', async () => {
  server.use(http.get('/v1/config', () => HttpResponse.json({ error: 'boom' }, { status: 500 })))
  await expect(getServerConfig()).rejects.toBeInstanceOf(ApiError)
})
