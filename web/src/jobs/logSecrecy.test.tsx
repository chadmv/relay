import { renderHook, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { expect, test, vi } from 'vitest'
import { server } from '../test/setup-helpers'
import { fakeSseServer } from '../test/sseStream'
import { useTaskLogStream } from './useTaskLogStream'

// Log content is raw subprocess stdout/stderr: P4 paths, hostnames, env-derived
// values, and anything a user's own script echoed, including credentials. Browser
// consoles are captured by extensions and screen-shared, so no console method may
// ever receive it. Error paths log nothing at all.
test('no console method ever receives log content, across mount-stream-drop-unmount', async () => {
  const SECRET = 'P4PASSWD=hunter2-never-log-me'
  const methods = ['log', 'info', 'warn', 'error', 'debug', 'trace'] as const
  const spies = methods.map((m) => vi.spyOn(console, m).mockImplementation(() => {}))

  const fake = fakeSseServer()
  server.use(
    http.get('/v1/tasks/t1/logs', () => HttpResponse.json({ items: [], next_seq: 0, total: 0 })),
  )
  const { result, unmount } = renderHook(() =>
    useTaskLogStream('t1', { live: true, enabled: true, fetchImpl: fake.fetchImpl }),
  )
  await waitFor(() => expect(result.current.status).toBe('live'))

  const conn = fake.latest()
  conn.emit('task_log', {
    task_id: 't1',
    job_id: 'j1',
    seq: 1,
    stream: 'stdout',
    content: `${SECRET}\n`,
    created_at: '2026-08-09T00:00:00Z',
  })
  // Positive control: the content really did flow through the code path under
  // test. Without this, a broken transport would make the absence assertion pass.
  await waitFor(() => expect(result.current.rows.some((r) => r.text === SECRET)).toBe(true))

  conn.emit('dropped', { reason: 'slow_consumer' })
  conn.close()
  await waitFor(() => expect(result.current.dropped).toBe(true))
  unmount()

  for (const spy of spies) {
    for (const call of spy.mock.calls) {
      expect(JSON.stringify(call)).not.toContain('hunter2')
    }
  }
  spies.forEach((s) => s.mockRestore())
})
