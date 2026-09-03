import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { afterEach, expect, test } from 'vitest'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { server } from '../test/setup-helpers'
import { AuthProvider } from '../auth/AuthProvider'
import { clearToken, setToken } from '../lib/token'
import { SPLIT_DEFAULT, SPLIT_MAX, SPLIT_MIN, SPLIT_STORAGE_KEY } from './splitWidth'
import { JobDetailPage } from './JobDetailPage'

const ID = 'j1'

// Hand-written literal, not the app's Job type.
const JOB = {
  id: ID,
  name: 'shot-042 render',
  priority: 'high',
  status: 'running',
  submitted_by: 'u1',
  submitted_by_email: 'mira@studio.dev',
  labels: {},
  created_at: '2026-07-01T00:00:00Z',
  updated_at: '2026-07-01T00:01:00Z',
  tasks: [
    {
      id: 't1', name: 'frame-001', status: 'done',
      commands: [['blender', '-b']], env: {}, requires: {},
      timeout_seconds: 3600, retries: 2, retry_count: 0,
    },
  ],
}

const SEPARATOR = 'Resize the pipeline and task detail panes'

function renderDetail() {
  setToken('test-token')
  server.use(http.get('/v1/users/me', () =>
    HttpResponse.json({ id: 'u1', email: 'a@b.co', name: 'A', is_admin: false }),
  ))
  server.use(http.get(`/v1/jobs/${ID}`, () => HttpResponse.json(JOB)))
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[`/jobs/${ID}`]}>
        <AuthProvider>
          <Routes>
            <Route path="/jobs/:id" element={<JobDetailPage />} />
          </Routes>
        </AuthProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

afterEach(() => {
  clearToken()
  window.localStorage.clear()
})

// Kills: omitting aria-orientation. `separator`'s implicit orientation is
// HORIZONTAL, and this one runs vertically between two horizontally-arranged
// panes, so an omitted attribute announces the wrong axis rather than nothing.
// Kills: dropping the tab stop, which makes the whole key surface unreachable.
test('the separator exposes its range and orientation', async () => {
  renderDetail()
  const sep = await screen.findByRole('separator', { name: SEPARATOR })
  expect(sep).toHaveAttribute('aria-orientation', 'vertical')
  expect(sep).toHaveAttribute('tabindex', '0')
  expect(sep).toHaveAttribute('aria-valuenow', String(SPLIT_DEFAULT))
  expect(sep).toHaveAttribute('aria-valuemin', String(SPLIT_MIN))
  expect(sep).toHaveAttribute('aria-valuemax', String(SPLIT_MAX))
  // A bare number announces nothing about WHICH side grows.
  expect(sep).toHaveAttribute('aria-valuetext', 'pipeline 55%, task detail 45%')
  expect(sep.parentElement?.getAttribute('style')).toContain('--relay-split: 55%')
})

// Kills: dropping the clamp on the key path. Pressed well past the bound, so a
// clamp applied at only one end is caught.
test('the arrow keys move the value by the step and clamp at both ends', async () => {
  renderDetail()
  const sep = await screen.findByRole('separator', { name: SEPARATOR })
  sep.focus()
  await userEvent.keyboard('{ArrowRight}')
  expect(sep).toHaveAttribute('aria-valuenow', '57')
  await userEvent.keyboard('{ArrowLeft}{ArrowLeft}')
  expect(sep).toHaveAttribute('aria-valuenow', '53')
  for (let i = 0; i < 20; i++) await userEvent.keyboard('{ArrowRight}')
  expect(sep).toHaveAttribute('aria-valuenow', String(SPLIT_MAX))
  for (let i = 0; i < 30; i++) await userEvent.keyboard('{ArrowLeft}')
  expect(sep).toHaveAttribute('aria-valuenow', String(SPLIT_MIN))
})

test('Home and End jump to the bounds', async () => {
  renderDetail()
  const sep = await screen.findByRole('separator', { name: SEPARATOR })
  sep.focus()
  await userEvent.keyboard('{Home}')
  expect(sep).toHaveAttribute('aria-valuenow', String(SPLIT_MIN))
  await userEvent.keyboard('{End}')
  expect(sep).toHaveAttribute('aria-valuenow', String(SPLIT_MAX))
})

// Kills: binding the cross-axis keys. This separator is vertical and says so, so
// an Up or Down binding would make the announced orientation a lie.
//
// Asserted after EACH key, not only at the end: ArrowUp bound to the same +step
// as ArrowRight and ArrowDown bound to the same -step as ArrowLeft is a
// symmetric mutation, so a sequence ending back at the default (up then down)
// cannot discriminate it - the two presses cancel and the final read matches
// either implementation.
test('the cross-axis keys move nothing', async () => {
  renderDetail()
  const sep = await screen.findByRole('separator', { name: SEPARATOR })
  sep.focus()
  await userEvent.keyboard('{ArrowUp}')
  expect(sep).toHaveAttribute('aria-valuenow', String(SPLIT_DEFAULT))
  await userEvent.keyboard('{ArrowDown}')
  expect(sep).toHaveAttribute('aria-valuenow', String(SPLIT_DEFAULT))
  await userEvent.keyboard('{PageUp}')
  expect(sep).toHaveAttribute('aria-valuenow', String(SPLIT_DEFAULT))
})

// Kills: dropping the write, and writing more than once per press.
test('a key press persists the new value once', async () => {
  renderDetail()
  const sep = await screen.findByRole('separator', { name: SEPARATOR })
  sep.focus()
  await userEvent.keyboard('{ArrowRight}')
  expect(window.localStorage.getItem(SPLIT_STORAGE_KEY)).toBe('57')
})

// Kills: reading the preference but never applying it, which the value/attribute
// pair catches from the ARIA side. The pixel half is the browser lane's.
test('a stored preference is restored into the announced value', async () => {
  window.localStorage.setItem(SPLIT_STORAGE_KEY, '40')
  renderDetail()
  const sep = await screen.findByRole('separator', { name: SEPARATOR })
  expect(sep).toHaveAttribute('aria-valuenow', '40')
  expect(sep).toHaveAttribute('aria-valuetext', 'pipeline 40%, task detail 60%')
})
