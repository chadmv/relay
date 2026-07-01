import { useMutation, useQueryClient } from '@tanstack/react-query'
import type { Worker, WorkerPatch } from './api'
import {
  disableWorker,
  enableWorker,
  evictWorkspace,
  revokeWorkerToken,
  updateWorker,
} from './api'

// Mutations for the admin worker-detail actions. Default strategy is
// invalidate-on-success (mirrors useScheduleActions). Key invariants:
//  - Invalidate the bare ['workers'] prefix so both the active list (['workers',
//    sort]) and the revoked list (['workers','revoked',cursor]) refresh.
//  - Revoke does NOT invalidate ['worker', id] (that query 404s post-revoke); the
//    caller navigates to /workers instead.
//  - Only the disable/enable toggle is optimistic; all others plain-invalidate.
export function useWorkerActions(id: string) {
  const qc = useQueryClient()

  const update = useMutation({
    mutationFn: (patch: WorkerPatch) => updateWorker(id, patch),
    onSuccess: (updated) => {
      qc.setQueryData(['worker', id], updated)
      qc.invalidateQueries({ queryKey: ['worker', id] })
      qc.invalidateQueries({ queryKey: ['workers'] })
    },
  })

  const disable = useMutation({
    mutationFn: (requeue: boolean) => disableWorker(id, requeue),
    // Optimistic: flip the cached status so the pill does not lag the ~3s poll.
    onMutate: async () => {
      await qc.cancelQueries({ queryKey: ['worker', id] })
      const previous = qc.getQueryData<Worker>(['worker', id])
      if (previous) {
        qc.setQueryData<Worker>(['worker', id], {
          ...previous,
          status: 'disabled',
          disabled_at: new Date().toISOString(),
        })
      }
      return { previous }
    },
    onError: (_err, _vars, ctx) => {
      if (ctx?.previous) qc.setQueryData(['worker', id], ctx.previous)
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['worker', id] })
      qc.invalidateQueries({ queryKey: ['workers'] })
    },
  })

  const enable = useMutation({
    mutationFn: () => enableWorker(id),
    onMutate: async () => {
      await qc.cancelQueries({ queryKey: ['worker', id] })
      const previous = qc.getQueryData<Worker>(['worker', id])
      if (previous) {
        qc.setQueryData<Worker>(['worker', id], {
          ...previous,
          status: 'online',
          disabled_at: undefined,
        })
      }
      return { previous }
    },
    onError: (_err, _vars, ctx) => {
      if (ctx?.previous) qc.setQueryData(['worker', id], ctx.previous)
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['worker', id] })
      qc.invalidateQueries({ queryKey: ['workers'] })
    },
  })

  const revoke = useMutation({
    mutationFn: () => revokeWorkerToken(id),
    // Revoke is terminal: the worker becomes `revoked` and GET /workers/{id}
    // 404s. Do NOT invalidate ['worker', id]; the caller navigates away.
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['workers'] })
    },
  })

  const evict = useMutation({
    mutationFn: (shortId: string) => evictWorkspace(id, shortId),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['worker', id, 'workspaces'] })
    },
  })

  return { update, disable, enable, revoke, evict }
}
