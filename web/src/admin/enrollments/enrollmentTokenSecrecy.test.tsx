import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import { server } from '../../test/setup-helpers'
import {
  assertNoConsoleLeak,
  domContainsSecret,
  findConsoleLeak,
  spyOnConsole,
  storageContainsSecret,
} from '../../test/secretLeaks'
import { EnrollmentsTab } from './EnrollmentsTab'

// A distinctive 64-hex-char stand-in for the real credential.
const TOKEN = 'f00dcafe'.repeat(8)

const ROW = {
  id: 'e1',
  created_at: '2026-08-09T09:30:00Z',
  expires_at: '2026-08-10T09:42:00Z',
  created_by: 'u1',
  hostname_hint: 'farm-west-13',
}

let requestUrls: string[] = []
let restoreClipboard: (() => void) | null = null

function installClipboard(writeText: (t: string) => Promise<void>) {
  const original = Object.getOwnPropertyDescriptor(navigator, 'clipboard')
  Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
  restoreClipboard = () => {
    if (original) Object.defineProperty(navigator, 'clipboard', original)
    else delete (navigator as { clipboard?: unknown }).clipboard
    restoreClipboard = null
  }
}

function onRequestStart({ request }: { request: Request }) {
  requestUrls.push(request.url)
}

beforeEach(() => {
  requestUrls = []
  server.events.on('request:start', onRequestStart)
})

afterEach(() => {
  server.events.removeListener('request:start', onRequestStart)
  restoreClipboard?.()
  localStorage.clear()
  sessionStorage.clear()
})

function newClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

function renderTab(client: QueryClient) {
  return render(
    <QueryClientProvider client={client}>
      <EnrollmentsTab />
    </QueryClientProvider>,
  )
}

function mutationStateContains(client: QueryClient, secret: string): boolean {
  return client
    .getMutationCache()
    .getAll()
    .some((m) => JSON.stringify(m.state).includes(secret))
}

function queryCacheContains(client: QueryClient, secret: string): boolean {
  return client
    .getQueryCache()
    .getAll()
    .some((q) => JSON.stringify({ key: q.queryKey, data: q.state.data }).includes(secret))
}

// ---- positive controls for the matchers themselves -------------------------

test('the console matcher catches a bare-string leak', () => {
  const spies = spyOnConsole()
  console.log(`prefix ${TOKEN} suffix`)
  expect(() => assertNoConsoleLeak(spies, TOKEN)).toThrow(/secret leaked/)
  spies.forEach((s) => s.mockRestore())
})

test('the console matcher catches a token carried INSIDE an Error, not just a string', () => {
  // The exact gap that bit this project: JSON.stringify([new Error(TOKEN)]) is
  // '[{}]', so a stringify-based matcher reports clean while the secret sits in
  // the recorded call.
  const spies = spyOnConsole()
  console.error(new Error(`enroll failed: ${TOKEN}`))
  expect(findConsoleLeak(spies, TOKEN)).not.toBeNull()
  expect(JSON.stringify([new Error(TOKEN)])).toBe('[{}]')
  spies.forEach((s) => s.mockRestore())
})

test('the console matcher catches a token on an Error cause', () => {
  const spies = spyOnConsole()
  console.warn(new Error('outer', { cause: new Error(TOKEN) }))
  expect(findConsoleLeak(spies, TOKEN)).not.toBeNull()
  spies.forEach((s) => s.mockRestore())
})

test('the storage matcher catches a manual write', () => {
  localStorage.setItem('probe', TOKEN)
  expect(storageContainsSecret(TOKEN)).toBe(true)
  localStorage.removeItem('probe')
  expect(storageContainsSecret(TOKEN)).toBe(false)
})

test('the DOM matcher sees a value that lives only in an input property', () => {
  // queryByText / textContent would both miss this. If this control ever fails,
  // every "token is gone" assertion below is vacuous.
  const { container } = render(<input readOnly value={TOKEN} aria-label="probe" />)
  expect(container.textContent).not.toContain(TOKEN)
  expect(domContainsSecret(TOKEN)).toBe(true)
})

// ---- the real flow ---------------------------------------------------------

test('the token is revealed once, then leaves the DOM, the caches, storage, URLs, and the console', async () => {
  const spies = spyOnConsole()
  const writeText = vi.fn().mockResolvedValue(undefined)
  installClipboard(writeText)
  server.use(
    http.get('/v1/agent-enrollments', () =>
      HttpResponse.json({ items: [ROW], next_cursor: '', total: 1 }),
    ),
    http.post('/v1/agent-enrollments', () =>
      HttpResponse.json({ id: 'e9', token: TOKEN, expires_at: ROW.expires_at }, { status: 201 }),
    ),
  )
  const client = newClient()
  renderTab(client)
  await screen.findByText('farm-west-13')

  await userEvent.click(screen.getByRole('button', { name: '+ Enroll agent' }))
  await userEvent.click(screen.getByRole('button', { name: 'Enroll' }))
  await screen.findByRole('dialog')

  // POSITIVE CONTROLS, while the dialog is open: the token really did flow
  // through the real components, and the two "it is gone" instruments below can
  // both see it when it is present.
  expect(domContainsSecret(TOKEN)).toBe(true)
  expect(mutationStateContains(client, TOKEN)).toBe(true)

  await userEvent.click(screen.getByRole('button', { name: 'Copy' }))
  expect(writeText).toHaveBeenCalledWith(TOKEN)

  await userEvent.click(screen.getByRole('button', { name: /I have copied it/ }))
  await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())

  // 1. Gone from the DOM, including every input value.
  expect(domContainsSecret(TOKEN)).toBe(false)
  // 2. create.reset() ran (detaching the observer) AND gcTime: 0 on the create
  //    mutation (useAgentEnrollmentActions.ts) let the now-observer-less Mutation
  //    fall out of the cache: reset() alone only unhooks the observer, it does not
  //    delete the underlying Mutation from queryClient.getMutationCache(), so
  //    without gcTime: 0 the token would still be readable there for the default
  //    5-minute gcTime. The removal is scheduled on the next tick, hence waitFor.
  await waitFor(() => expect(mutationStateContains(client, TOKEN)).toBe(false))
  // 3. It never entered the query cache - it is a mutation result, and no query
  //    fetches it.
  expect(queryCacheContains(client, TOKEN)).toBe(false)
  // 4. It never entered web storage. No query persister is configured
  //    (web/src/lib/queryClient.ts), so nothing reaches IndexedDB either.
  expect(storageContainsSecret(TOKEN)).toBe(false)
  // 5. It never entered a request URL - no path segment and no query param, so it
  //    cannot leak into history, a Referer header, or a proxy log.
  expect(requestUrls.length).toBeGreaterThan(0) // the instrument recorded something
  for (const url of requestUrls) expect(url).not.toContain(TOKEN)
  // 6. No console method ever received it, in any representation.
  assertNoConsoleLeak(spies, TOKEN)

  spies.forEach((s) => s.mockRestore())
})

test('the URL instrument would catch a token in a query param (positive control)', async () => {
  server.use(
    http.get('/v1/agent-enrollments', () =>
      HttpResponse.json({ items: [], next_cursor: '', total: 0 }),
    ),
  )
  // The same handler answers the probe, so MSW's fail-closed policy is satisfied.
  await fetch(`/v1/agent-enrollments?probe=${TOKEN}`)
  expect(requestUrls.some((u) => u.includes(TOKEN))).toBe(true)
})

test('the reveal is reachable only through the mutation - no route or link carries the token', async () => {
  server.use(
    http.get('/v1/agent-enrollments', () =>
      HttpResponse.json({ items: [ROW], next_cursor: '', total: 1 }),
    ),
  )
  renderTab(newClient())
  await screen.findByText('farm-west-13')
  // The list response carries no token field at all, so nothing on this page can
  // link to, bookmark, or re-display one.
  expect(domContainsSecret(TOKEN)).toBe(false)
  for (const a of Array.from(document.querySelectorAll('a'))) {
    expect(a.getAttribute('href') ?? '').not.toContain(TOKEN)
  }
})
