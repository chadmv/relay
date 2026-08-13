import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, renderHook, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import type { ReactNode } from 'react'
import { expect, test, vi } from 'vitest'
import { server } from '../../test/setup-helpers'
import { useInviteActions } from './useInviteActions'
import { useInvites } from './useInvites'

const TOKEN = 'f00dcafe'.repeat(8)
// Distinct from every email in the list fixtures on purpose: it is the SECOND
// asset this mutation carries, and it must be traceable independently of the token.
const EMAIL = 'invitee-secret@studio.dev'

const ROW = {
  id: 'i1',
  created_at: '2026-08-01T09:00:00Z',
  expires_at: '2026-08-04T09:00:00Z',
  created_by: 'u1',
  created_by_email: 'admin@studio.dev',
}

function makeWrapper(client: QueryClient) {
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  )
}

function newClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

function created() {
  return HttpResponse.json(
    { id: 'i9', token: TOKEN, expires_at: ROW.expires_at, email: EMAIL },
    { status: 201 },
  )
}

test('create POSTs the exact body and invalidates the BARE ["invites"] prefix', async () => {
  const client = newClient()
  const spy = vi.spyOn(client, 'invalidateQueries')
  let body: unknown
  server.use(
    http.post('/v1/invites', async ({ request }) => {
      body = await request.json()
      return created()
    }),
  )
  const { result } = renderHook(() => useInviteActions(), { wrapper: makeWrapper(client) })
  const out = await result.current.create.mutateAsync({ email: EMAIL, expires_in: '72h' })

  expect(body).toEqual({ email: EMAIL, expires_in: '72h' })
  expect(out.token).toBe(TOKEN)
  await waitFor(() => expect(spy).toHaveBeenCalledWith({ queryKey: ['invites'] }))

  // The decoupling lesson from web/src/jobs/queryKeyDecoupling.test.tsx: a
  // fully-qualified key only refetches the sort/page combination that happens to
  // be mounted. EVERY call must use the bare prefix.
  for (const call of spy.mock.calls) {
    expect((call[0] as { queryKey: unknown[] }).queryKey).toEqual(['invites'])
  }
})

test('creating refetches a MOUNTED invites list (active observer, not a cache seed)', async () => {
  let listCalls = 0
  server.use(
    http.get('/v1/invites', () => {
      listCalls++
      return HttpResponse.json({ items: [ROW], next_cursor: '', total: 1 })
    }),
    http.post('/v1/invites', () => created()),
  )
  const client = newClient()
  const wrapper = makeWrapper(client)

  // The list query MUST be mounted via renderHook so it has an ACTIVE OBSERVER.
  // A client.fetchQuery / setQueryData seed leaves no observer, invalidateQueries'
  // default refetchType:'active' never fires, and this assertion would pass
  // vacuously no matter what key the mutation invalidated.
  const { result: list } = renderHook(() => useInvites('-created_at', ''), { wrapper })
  await waitFor(() => expect(list.current.status).toBe('success'))
  expect(listCalls).toBe(1)

  const { result: actions } = renderHook(() => useInviteActions(), { wrapper })
  await actions.current.create.mutateAsync({ expires_in: '72h' })

  await waitFor(() => expect(listCalls).toBeGreaterThanOrEqual(2))
})

// RED #1 for the token-retention rule. Fails against a mutation without
// gcTime: 0, because reset() only DETACHES the observer - the underlying Mutation
// then sits in the cache for the default 5-minute gcTime with the token in
// state.data, the invitee email in state.variables, AND the same email captured in
// the mutationFn closure that no state stringify can reach.
test('after reset() the settled create mutation leaves the mutation cache entirely', async () => {
  const client = newClient()
  server.use(http.post('/v1/invites', () => created()))
  const { result } = renderHook(() => useInviteActions(), { wrapper: makeWrapper(client) })
  await result.current.create.mutateAsync({ email: EMAIL, expires_in: '72h' })

  // POSITIVE CONTROL, taken BEFORE reset() and therefore while the observer is
  // still attached. Mutation.optionalRemove only removes when observers.length
  // === 0 (query-core mutation.js:47-55), so a settled mutation with a live
  // observer stays put even with gcTime: 0 - which is exactly why the control is
  // valid here and would be vacuous after the reset below.
  const held = client.getMutationCache().getAll()
  expect(held).toHaveLength(1)
  expect(JSON.stringify(held[0].state)).toContain(TOKEN)
  expect(JSON.stringify(held[0].state)).toContain(EMAIL)

  act(() => {
    result.current.create.reset()
  })

  // EMPTY, not "no entry stringifies to contain the secret". The 2026-08-12
  // profile-pages slice found a plaintext secret surviving in the settled
  // mutation's mutationFn CLOSURE, which mutationObserver builds from
  // this.options and does not replace on post-success re-renders; a
  // JSON.stringify(m.state) assertion can never see a closure
  // (docs/retros/2026-08-12-profile-pages.md:171-184). This mutation passes
  // variables, so the closure really does hold the invitee email. Do NOT weaken
  // this back to a state check.
  await waitFor(() => expect(client.getMutationCache().getAll()).toHaveLength(0))
})

// Guard for the ordering trap, and it PASSES ON FIRST WRITE - its RED is produced
// by the mutate-and-revert in Step 4b, not by the missing implementation.
// Mutation.execute awaits the hook-level onSuccess at query-core mutation.js:123
// and only THEN dispatches {type:'success'} at :144, while
// MutationObserver.reset() detaches the observer (mutationObserver.js:50-55). So a
// reset() inside onSuccess silently kills the success state: isSuccess stays
// false, data stays undefined, and InvitesTab's reveal dialog - which renders iff
// create.data exists - never opens. reset() belongs at the three UI-driven sites
// in InvitesTab, never here.
test('a settled create still exposes data and isSuccess - reset() must NOT live in onSuccess', async () => {
  const client = newClient()
  server.use(http.post('/v1/invites', () => created()))
  const { result } = renderHook(() => useInviteActions(), { wrapper: makeWrapper(client) })
  await result.current.create.mutateAsync({ expires_in: '72h' })

  await waitFor(() => expect(result.current.create.isSuccess).toBe(true))
  expect(result.current.create.data?.token).toBe(TOKEN)
})

test('a create failure surfaces the ApiError and does not invalidate', async () => {
  const client = newClient()
  const spy = vi.spyOn(client, 'invalidateQueries')
  server.use(
    http.post('/v1/invites', () =>
      HttpResponse.json({ error: 'invalid email address' }, { status: 400 }),
    ),
  )
  const { result } = renderHook(() => useInviteActions(), { wrapper: makeWrapper(client) })
  await expect(
    result.current.create.mutateAsync({ email: 'nope', expires_in: '72h' }),
  ).rejects.toMatchObject({ status: 400 })
  expect(spy).not.toHaveBeenCalled()
})
