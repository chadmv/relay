import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderHook, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import type { ReactNode } from 'react'
import { expect, test, vi } from 'vitest'
import { server } from '../test/setup-helpers'
import { useScheduleActions } from './useScheduleActions'

function makeWrapper(client: QueryClient) {
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  )
}

test('runNow POSTs run-now and invalidates the schedules query', async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const spy = vi.spyOn(client, 'invalidateQueries')
  server.use(
    http.post('/v1/scheduled-jobs/s1/run-now', () => HttpResponse.json({ id: 'job1' }, { status: 201 })),
  )

  const { result } = renderHook(() => useScheduleActions(), { wrapper: makeWrapper(client) })
  await result.current.runNow.mutateAsync('s1')

  await waitFor(() => expect(spy).toHaveBeenCalledWith({ queryKey: ['schedules'] }))
})

test('setEnabled PATCHes and invalidates the schedules query', async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const spy = vi.spyOn(client, 'invalidateQueries')
  server.use(
    http.patch('/v1/scheduled-jobs/s1', () => HttpResponse.json({ id: 's1', enabled: false })),
  )

  const { result } = renderHook(() => useScheduleActions(), { wrapper: makeWrapper(client) })
  await result.current.setEnabled.mutateAsync({ id: 's1', enabled: false })

  await waitFor(() => expect(spy).toHaveBeenCalledWith({ queryKey: ['schedules'] }))
})
