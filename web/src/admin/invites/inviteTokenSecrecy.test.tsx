import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import { server } from '../../test/setup-helpers'
import {
  assertNoConsoleLeak,
  domContainsSecret,
  spyOnConsole,
  storageContainsSecret,
} from '../../test/secretLeaks'
import { InvitesTab } from './InvitesTab'

// The matcher self-tests (a bare string, an Error, an Error cause, an Error nested
// in an object and in an array, a storage write, an input value property) are
// SHIPPED in web/src/admin/enrollments/enrollmentTokenSecrecy.test.tsx:84-155
// against the same web/src/test/secretLeaks.ts module. They are not duplicated
// here; the per-instrument positive controls below are taken inline instead.

// A distinctive 64-hex-char stand-in for the real credential.
const TOKEN = 'f00dcafe'.repeat(8)
// The SECOND asset: the invitee address, passed as the mutation VARIABLE and
// therefore also captured in the mutationFn closure. Deliberately absent from
// every list fixture below so it can be traced independently of the token.
const INVITEE = 'invitee-secret@studio.dev'

const ROW = {
  id: 'i1',
  created_at: '2026-08-01T09:00:00Z',
  expires_at: '2026-08-10T09:00:00Z',
  created_by: 'u1',
  created_by_email: 'admin@studio.dev',
  email: 'listed@studio.dev',
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
      <InvitesTab />
    </QueryClientProvider>,
  )
}

function queryCacheContains(client: QueryClient, secret: string): boolean {
  return client
    .getQueryCache()
    .getAll()
    .some((q) => JSON.stringify({ key: q.queryKey, data: q.state.data }).includes(secret))
}

async function createOne(client: QueryClient) {
  server.use(
    http.get('/v1/invites', () =>
      HttpResponse.json({ items: [ROW], next_cursor: '', total: 1 }),
    ),
    http.post('/v1/invites', () =>
      HttpResponse.json(
        { id: 'i9', token: TOKEN, expires_at: ROW.expires_at, email: INVITEE },
        { status: 201 },
      ),
    ),
  )
  renderTab(client)
  await screen.findByText('listed@studio.dev')
  await userEvent.click(screen.getByRole('button', { name: '+ Create invite' }))
  await userEvent.type(screen.getByLabelText('Email'), INVITEE)
  await userEvent.click(screen.getByRole('button', { name: 'Create invite' }))
  await screen.findByRole('dialog')
}

test('the token is revealed once, then leaves the DOM, the caches, storage, URLs and the console', async () => {
  const spies = spyOnConsole()
  const writeText = vi.fn().mockResolvedValue(undefined)
  installClipboard(writeText)
  const client = newClient()
  await createOne(client)

  // POSITIVE CONTROLS, taken WHILE THE DIALOG IS OPEN - i.e. while the mutation
  // observer is still attached. Mutation.optionalRemove only removes an entry when
  // observers.length === 0 (query-core mutation.js:47-55), so with gcTime: 0 the
  // entry survives exactly until create.reset() detaches the observer. Taken after
  // Done instead, this control would measure an already-evicted cache and report
  // clean forever.
  expect(domContainsSecret(TOKEN)).toBe(true)
  const held = client.getMutationCache().getAll()
  expect(held).toHaveLength(1)
  expect(JSON.stringify(held[0].state)).toContain(TOKEN)
  expect(JSON.stringify(held[0].state)).toContain(INVITEE)

  await userEvent.click(screen.getByRole('button', { name: 'Copy' }))
  expect(writeText).toHaveBeenCalledWith(TOKEN)

  await userEvent.click(screen.getByRole('button', { name: /I have copied it/ }))
  await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())

  // 1. Gone from the DOM, including every input value.
  expect(domContainsSecret(TOKEN)).toBe(false)
  // 2. THE MUTATION CACHE IS EMPTY. Not "no entry's state stringifies to contain
  //    the token" - that weaker form is what the 2026-08-12 profile slice shipped,
  //    and it was blind to the mutationFn CLOSURE, which mutationObserver builds
  //    from this.options and does not replace on post-success re-renders
  //    (docs/retros/2026-08-12-profile-pages.md:171-184). This mutation passes
  //    variables, so that closure really does hold INVITEE. Emptiness is the only
  //    assertion that covers both assets. DO NOT weaken it.
  //    It requires BOTH create.reset() on the dialog's onDone (which detaches the
  //    observer) and gcTime: 0 on the mutation (which lets the now-observer-less
  //    Mutation fall out on the next tick). Removing either turns this RED.
  await waitFor(() => expect(client.getMutationCache().getAll()).toHaveLength(0))
  // 3. Neither asset ever entered the query cache - they are mutation results, and
  //    no query fetches them.
  expect(queryCacheContains(client, TOKEN)).toBe(false)
  expect(queryCacheContains(client, INVITEE)).toBe(false)
  // 4. Neither entered web storage. No query persister is configured
  //    (web/src/lib/queryClient.ts), so nothing reaches IndexedDB either.
  expect(storageContainsSecret(TOKEN)).toBe(false)
  expect(storageContainsSecret(INVITEE)).toBe(false)
  // 5. Neither entered a request URL - no path segment and no query param, so
  //    neither can leak into history, a Referer header, or a proxy log. The
  //    invitee address travels in the POST BODY only.
  expect(requestUrls.length).toBeGreaterThan(0) // the instrument recorded something
  for (const url of requestUrls) {
    expect(url).not.toContain(TOKEN)
    expect(url).not.toContain(encodeURIComponent(INVITEE))
    expect(url).not.toContain(INVITEE)
  }
  // 6. No console method ever received either, in any representation.
  assertNoConsoleLeak(spies, TOKEN)
  assertNoConsoleLeak(spies, INVITEE)

  spies.forEach((s) => s.mockRestore())
})

test('the URL instrument would catch a secret in a query param (positive control)', async () => {
  server.use(
    http.get('/v1/invites', () => HttpResponse.json({ items: [], next_cursor: '', total: 0 })),
  )
  // The same handler answers the probe, so MSW's fail-closed policy is satisfied.
  await fetch(`/v1/invites?probe=${TOKEN}`)
  expect(requestUrls.some((u) => u.includes(TOKEN))).toBe(true)
})

test('the reveal is reachable only through the mutation - no route or link carries the token', async () => {
  server.use(
    http.get('/v1/invites', () =>
      HttpResponse.json({ items: [ROW], next_cursor: '', total: 1 }),
    ),
  )
  renderTab(newClient())
  await screen.findByText('listed@studio.dev')
  // The list response carries no token field at all (internal/store/query/invites.sql:22-25
  // omits token_hash from every projection), so nothing on this page can link to,
  // bookmark, or re-display one.
  expect(domContainsSecret(TOKEN)).toBe(false)
  for (const a of Array.from(document.querySelectorAll('a'))) {
    expect(a.getAttribute('href') ?? '').not.toContain(TOKEN)
  }
})

test('the dialog layer leaves the DOM with the credential, retaining no detached subtree', async () => {
  const spies = spyOnConsole()
  const client = newClient()
  await createOne(client)

  // The dialog is portaled into a single shared layer under <body>
  // (web/src/components/dialog/dialogStack.ts). Hold a reference to it so the
  // DETACHED node can be inspected after teardown - a container that is removed
  // from the document but still holds the credential in a subtree is exactly the
  // leak a portal could introduce and document.body-scoped sweeps could not see.
  const layer = document.querySelector('[data-dialog-layer]') as HTMLElement
  expect(layer).not.toBeNull()
  // Positive control on THIS instrument: it can see the token when it is present.
  expect(layer.innerHTML).toContain(TOKEN)

  await userEvent.click(screen.getByRole('button', { name: /I have copied it/ }))
  await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())

  expect(document.querySelector('[data-dialog-layer]')).toBeNull()
  expect(layer.innerHTML).not.toContain(TOKEN)
  expect(layer.parentNode).toBeNull()
  expect(domContainsSecret(TOKEN)).toBe(false)
  assertNoConsoleLeak(spies, TOKEN)

  spies.forEach((s) => s.mockRestore())
})
