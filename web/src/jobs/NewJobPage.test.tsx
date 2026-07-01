import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse, delay } from 'msw'
import { expect, test } from 'vitest'
import { MemoryRouter, Route, Routes, useParams } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { server } from '../test/setup-helpers'
import { NewJobPage } from './NewJobPage'
import { AppRoutes } from '../app/router'
import { AuthProvider } from '../auth/AuthProvider'
import { setToken, clearToken } from '../lib/token'

// Stub detail route component: calling useParams() inside a real component's
// render (not inline in the JSX expression passed to `element`) keeps the hook
// call valid - JSX for `element` is constructed eagerly by renderNew(), so an
// inline useParams() call there would run outside of any component's render.
function DetailStub() {
  const { id } = useParams()
  return <div>detail for {id ?? ''}</div>
}

// Renders NewJobPage at /jobs/new. A stub /jobs/:id route lets us assert
// navigation lands on the detail page for a real id (and prove /jobs/new does
// NOT match :id).
function renderNew() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={['/jobs/new']}>
        <Routes>
          <Route path="/jobs/new" element={<NewJobPage />} />
          <Route path="/jobs/:id" element={<DetailStub />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

function editor() {
  return screen.getByRole('textbox') as HTMLTextAreaElement
}

test('renders the editor prefilled with the starter template', () => {
  renderNew()
  expect(editor().value).toMatch(/"name": "my-job"/)
  expect(editor().value).toMatch(/"hello world"/)
})

test('submitting the unedited template POSTs that body', async () => {
  let body: unknown = null
  server.use(
    http.post('/v1/jobs', async ({ request }) => {
      body = await request.json()
      return HttpResponse.json({ id: 'job-1' }, { status: 201 })
    }),
  )
  renderNew()
  await userEvent.click(screen.getByRole('button', { name: /create job/i }))
  await waitFor(() => expect(body).toMatchObject({ name: 'my-job' }))
  expect((body as { tasks: unknown[] }).tasks.length).toBe(1)
})

test('happy path: POST body, 201, navigation to /jobs/:id', async () => {
  let body: unknown = null
  server.use(
    http.post('/v1/jobs', async ({ request }) => {
      body = await request.json()
      return HttpResponse.json({ id: 'job-123' }, { status: 201 })
    }),
  )
  renderNew()
  const ta = editor()
  await userEvent.clear(ta)
  await userEvent.type(ta, '{{"name":"nj","tasks":[[{{"name":"t","command":[["echo"]}]}')
  await userEvent.click(screen.getByRole('button', { name: /create job/i }))

  expect(await screen.findByText('detail for job-123')).toBeInTheDocument()
  expect(body).toEqual({ name: 'nj', tasks: [{ name: 't', command: ['echo'] }] })
})

test('local parse error shows a banner and makes NO POST', async () => {
  let posted = false
  server.use(http.post('/v1/jobs', () => { posted = true; return HttpResponse.json({ id: 'x' }, { status: 201 }) }))
  renderNew()
  const ta = editor()
  await userEvent.clear(ta)
  await userEvent.type(ta, '{{ not json }')
  await userEvent.click(screen.getByRole('button', { name: /create job/i }))

  expect(await screen.findByText(/Invalid JSON/)).toBeInTheDocument()
  expect(posted).toBe(false)
})

test('local shape error - missing name - banner and NO POST', async () => {
  let posted = false
  server.use(http.post('/v1/jobs', () => { posted = true; return HttpResponse.json({ id: 'x' }, { status: 201 }) }))
  renderNew()
  const ta = editor()
  await userEvent.clear(ta)
  await userEvent.type(ta, '{{"tasks":[[{{"name":"t"}]}')
  await userEvent.click(screen.getByRole('button', { name: /create job/i }))

  // Scope to the alert banner: the page's helper hint paragraph also mentions
  // "name", so an unscoped text match is ambiguous.
  expect(await screen.findByRole('alert')).toHaveTextContent(/name/i)
  expect(posted).toBe(false)
})

test('local shape error - empty tasks - banner and NO POST', async () => {
  let posted = false
  server.use(http.post('/v1/jobs', () => { posted = true; return HttpResponse.json({ id: 'x' }, { status: 201 }) }))
  renderNew()
  const ta = editor()
  await userEvent.clear(ta)
  await userEvent.type(ta, '{{"name":"x","tasks":[[]}')
  await userEvent.click(screen.getByRole('button', { name: /create job/i }))

  // Scope to the alert banner: the helper hint paragraph also mentions "task".
  expect(await screen.findByRole('alert')).toHaveTextContent(/task/i)
  expect(posted).toBe(false)
})

test('server 400 surfaces inline, no navigation, text preserved', async () => {
  server.use(
    http.post('/v1/jobs', () =>
      HttpResponse.json({ error: 'duplicate task name: build' }, { status: 400 }),
    ),
  )
  renderNew()
  const ta = editor()
  await userEvent.clear(ta)
  await userEvent.type(ta, '{{"name":"nj","tasks":[[{{"name":"t","command":[["echo"]}]}')
  await userEvent.click(screen.getByRole('button', { name: /create job/i }))

  // ApiError.message is "400 duplicate task name: build" - assert the substring.
  expect(await screen.findByText(/duplicate task name: build/)).toBeInTheDocument()
  expect(screen.queryByText(/^detail for/)).not.toBeInTheDocument()
  expect(editor().value).toContain('"name":"nj"')
})

test('413 oversize surfaces inline (same banner path)', async () => {
  server.use(
    http.post('/v1/jobs', () =>
      HttpResponse.json({ error: 'request body too large' }, { status: 413 }),
    ),
  )
  renderNew()
  await userEvent.click(screen.getByRole('button', { name: /create job/i }))
  expect(await screen.findByText(/request body too large/)).toBeInTheDocument()
})

test('submit button is disabled while the create is pending', async () => {
  server.use(
    http.post('/v1/jobs', async () => {
      await delay(50)
      return HttpResponse.json({ id: 'job-1' }, { status: 201 })
    }),
  )
  renderNew()
  const btn = screen.getByRole('button', { name: /create job/i })
  await userEvent.click(btn)
  await waitFor(() => expect(btn).toBeDisabled())
})

test('a stale server error clears on the next submit', async () => {
  let call = 0
  server.use(
    http.post('/v1/jobs', () => {
      call++
      if (call === 1) return HttpResponse.json({ error: 'duplicate task name: build' }, { status: 400 })
      return HttpResponse.json({ id: 'job-9' }, { status: 201 })
    }),
  )
  renderNew()
  const ta = editor()
  await userEvent.clear(ta)
  await userEvent.type(ta, '{{"name":"nj","tasks":[[{{"name":"t","command":[["echo"]}]}')
  await userEvent.click(screen.getByRole('button', { name: /create job/i }))
  expect(await screen.findByText(/duplicate task name: build/)).toBeInTheDocument()

  // Resubmit (same valid text); the second POST 201s.
  await userEvent.click(screen.getByRole('button', { name: /create job/i }))
  expect(await screen.findByText('detail for job-9')).toBeInTheDocument()
  expect(screen.queryByText(/duplicate task name: build/)).not.toBeInTheDocument()
})

test('the /jobs/new route renders the form and makes NO GET /v1/jobs/new', async () => {
  setToken('test-token')
  let detailFetched = false
  server.use(
    http.get('/v1/users/me', () =>
      HttpResponse.json({ id: 'u1', email: 'a@b.co', name: 'A', is_admin: false }),
    ),
    // If JobDetailPage wrongly matched, it would GET /v1/jobs/new. Record it.
    http.get('/v1/jobs/new', () => {
      detailFetched = true
      return HttpResponse.json({ id: 'new' })
    }),
  )
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={['/jobs/new']}>
        <AuthProvider>
          <AppRoutes />
        </AuthProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  )

  // The editor renders (proves the form matched, not the detail page).
  expect(await screen.findByRole('textbox')).toBeInTheDocument()
  expect(detailFetched).toBe(false)
  clearToken()
})
