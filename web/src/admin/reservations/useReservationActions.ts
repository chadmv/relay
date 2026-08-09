import { useMutation, useQueryClient } from '@tanstack/react-query'
import { createReservation, deleteReservation, type CreateReservationBody } from './api'

// Mutations for the admin Reservations tab.
//
// NOTHING HERE IS SECRET. reservationResponse carries no token, hash or credential
// and the 201 echoes the same row the list returns, so - unlike
// useAgentEnrollmentActions - there is deliberately NO gcTime: 0, no reveal dialog
// and no secrecy harness. Do not copy that machinery in.
//
// No optimistic insert or removal: the table's STATUS column is derived from the
// row's own timestamps, and a locally synthesised row is one more place for the
// client and the scheduler to disagree. A refetch is cheap and always right.
export function useReservationActions() {
  const qc = useQueryClient()
  // BARE prefix, never a fully-qualified key, so every mounted
  // ['reservations', sort, cursor] combination refetches (see
  // web/src/jobs/queryKeyDecoupling.test.tsx).
  const invalidate = () => qc.invalidateQueries({ queryKey: ['reservations'] })

  const create = useMutation({
    mutationFn: (body: CreateReservationBody) => createReservation(body),
    onSuccess: invalidate,
  })

  const remove = useMutation({
    mutationFn: (id: string) => deleteReservation(id),
    // onSettled, NOT onSuccess. The interesting failure is a 404 from a row someone
    // else already deleted; refetching on that path is what makes the error message
    // informational (the stale row disappears) instead of a dead end the admin can
    // only escape by reloading. Refetching after a 500 is harmless.
    onSettled: invalidate,
  })

  return { create, remove }
}
