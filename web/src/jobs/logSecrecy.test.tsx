import { render, renderHook, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { http, HttpResponse } from 'msw'
import { afterEach, expect, test, vi, type MockInstance } from 'vitest'
import { server } from '../test/setup-helpers'
import { fakeSseServer, openSseResponse } from '../test/sseStream'
import { clearToken, setToken } from '../lib/token'
import { AppRoutes } from '../app/router'
import { AuthProvider } from '../auth/AuthProvider'
import { LogTab } from './LogTab'
import { useTaskLogStream } from './useTaskLogStream'

// Log content is raw subprocess stdout/stderr: P4 paths, hostnames, env-derived
// values, and anything a user's own script echoed, including credentials. Browser
// consoles are captured by extensions and screen-shared, so no console method may
// ever receive it, in ANY form a future change might pass to console.* - not just
// a bare string.

const CONSOLE_METHODS = ['log', 'info', 'warn', 'error', 'debug', 'trace'] as const

// JSON.stringify on an Error yields '{}' (verified: JSON.stringify([new
// Error('secret')]) === '[{}]'), so `JSON.stringify(call)` is blind to
// `console.error(err)` where err.message or err.stack carries log content -
// exactly the property this check exists to protect. Every argument is
// stringified through its own representation instead.
function stringifyConsoleArg(a: unknown): string {
  if (a instanceof Error) return `${a.message} ${a.stack ?? ''}`
  if (typeof a === 'string') return a
  try {
    return JSON.stringify(a)
  } catch {
    return String(a)
  }
}

function findLeakedSecret(spies: MockInstance[], secret: string): string | null {
  for (const spy of spies) {
    for (const call of spy.mock.calls) {
      for (const arg of call) {
        const s = stringifyConsoleArg(arg)
        if (s.includes(secret)) return s
      }
    }
  }
  return null
}

function assertNoSecretLeaked(spies: MockInstance[], secret: string) {
  const leaked = findLeakedSecret(spies, secret)
  if (leaked !== null) throw new Error(`secret leaked to console: ${leaked}`)
}

function spyOnConsole() {
  return CONSOLE_METHODS.map((m) => vi.spyOn(console, m).mockImplementation(() => {}))
}

afterEach(() => clearToken())

// The non-vacuity proof for the check itself, kept as a permanent test rather
// than a one-off manual mutation: without this, a broken stringify or a
// broken loop would make every absence assertion below pass for the wrong
// reason.
test('the leak check itself catches a deliberate console.log of the secret (positive control)', () => {
  const SECRET = 'P4PASSWD=hunter2-positive-control'
  const spies = spyOnConsole()
  console.log(`unrelated text ${SECRET} more text`)
  expect(() => assertNoSecretLeaked(spies, SECRET)).toThrow(/secret leaked/)
  spies.forEach((s) => s.mockRestore())
})

// The specific gap found in review: JSON.stringify on an Error silently drops
// its message and stack, so a naive check would pass even though the secret
// is genuinely sitting in the mock's recorded call.
test('the leak check catches a secret carried by an Error object, not just a bare string', () => {
  const SECRET = 'P4PASSWD=hunter2-in-an-error'
  const spies = spyOnConsole()
  console.error(new Error(`stream failed: ${SECRET}`))
  expect(() => assertNoSecretLeaked(spies, SECRET)).toThrow(/secret leaked/)
  spies.forEach((s) => s.mockRestore())
})

test('no console method ever receives log content, across mount-stream-drop-unmount', async () => {
  const SECRET = 'P4PASSWD=hunter2-never-log-me'
  const spies = spyOnConsole()

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

  assertNoSecretLeaked(spies, 'hunter2')
  spies.forEach((s) => s.mockRestore())
})

// The hook-only test above never mounts LogView/LogTab/TaskLogPage, so a
// console.* call added inside any of those components would be invisible to
// it. This test drives a secret through the real hook -> LogTab -> LogView ->
// LogRowView rendering pipeline.
test('no console method receives log content rendered through LogTab', async () => {
  const SECRET = 'P4PASSWD=hunter2-via-logtab'
  const spies = spyOnConsole()

  server.use(
    http.get('/v1/tasks/t1/logs', () =>
      HttpResponse.json({
        items: [{ seq: 1, stream: 'stdout', content: `${SECRET}\n`, created_at: '2026-08-09T00:00:00Z' }],
        next_seq: 0,
        total: 1,
      }),
    ),
  )
  server.use(http.get('/v1/events', () => openSseResponse()))

  function Harness() {
    const stream = useTaskLogStream('t1', { live: true, enabled: true })
    return <LogTab jobId="j1" taskId="t1" stream={stream} />
  }

  const { findByText, unmount } = render(
    <MemoryRouter>
      <Harness />
    </MemoryRouter>,
  )
  // Positive control: the secret really did reach the DOM through the full
  // hook -> LogTab -> LogView -> LogRowView pipeline.
  expect(await findByText(SECRET)).toBeInTheDocument()
  unmount()

  assertNoSecretLeaked(spies, 'hunter2')
  spies.forEach((s) => s.mockRestore())
})

// Same coverage gap, for the OTHER surface that renders log content: the
// full-screen route. TaskLogPage adds useJob and its own breadcrumb/status
// chrome on top of the same LogView, so it is a distinct render path from
// LogTab's, not a duplicate of the test above.
test('no console method receives log content rendered through the full-screen TaskLogPage route', async () => {
  const SECRET = 'P4PASSWD=hunter2-via-tasklogpage'
  const spies = spyOnConsole()

  setToken('test-token')
  server.use(
    http.get('/v1/users/me', () =>
      HttpResponse.json({ id: 'u1', email: 'a@b.co', name: 'A', is_admin: false }),
    ),
  )
  server.use(
    http.get('/v1/jobs/j1', () =>
      HttpResponse.json({
        id: 'j1',
        name: 'shot-042 render',
        priority: 'high',
        status: 'running',
        submitted_by: 'u1',
        labels: null,
        created_at: '2026-08-09T00:00:00Z',
        updated_at: '2026-08-09T00:00:00Z',
        tasks: [
          {
            id: 't1',
            name: 'frame-001',
            status: 'running',
            commands: [['blender', '-b']],
            env: null,
            requires: null,
            timeout_seconds: null,
            retries: 1,
            retry_count: 0,
            worker_id: 'w1abcdef',
          },
        ],
      }),
    ),
  )
  server.use(
    http.get('/v1/tasks/t1/logs', () =>
      HttpResponse.json({
        items: [{ seq: 1, stream: 'stdout', content: `${SECRET}\n`, created_at: '2026-08-09T00:00:00Z' }],
        next_seq: 0,
        total: 1,
      }),
    ),
  )
  server.use(http.get('/v1/events', () => openSseResponse()))

  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const { findByText, unmount } = render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={['/jobs/j1/tasks/t1']}>
        <AuthProvider>
          <AppRoutes />
        </AuthProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  )
  // Positive control: the secret really did reach the DOM through
  // useJob -> useTaskLogStream -> TaskLogPage -> LogView -> LogRowView.
  expect(await findByText(SECRET)).toBeInTheDocument()
  unmount()

  assertNoSecretLeaked(spies, 'hunter2')
  spies.forEach((s) => s.mockRestore())
})
