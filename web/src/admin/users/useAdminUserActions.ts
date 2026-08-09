import { useMutation, useQueryClient } from '@tanstack/react-query'
import {
  archiveUser,
  createUser,
  renameUser,
  resetUserPassword,
  unarchiveUser,
  type CreateUserBody,
} from './api'

// Mutations for the admin Users tab. Direct port of useWorkerActions' shape with
// two deliberate differences:
//  - Every mutation invalidates the BARE ['users'] prefix, never a fully-qualified
//    key, so any mounted ['users', sort, includeArchived, cursor, email]
//    combination refetches (see web/src/jobs/queryKeyDecoupling.test.tsx).
//  - No optimistic updates. useWorkerActions' optimistic disable/enable exists
//    because a 3s poll made the pill lag; useAdminUsers does not poll and every
//    call here returns the updated row (or 204), so invalidate-on-success is both
//    simplest and correct.
export function useAdminUserActions() {
  const qc = useQueryClient()
  const invalidate = () => qc.invalidateQueries({ queryKey: ['users'] })

  const create = useMutation({
    mutationFn: (body: CreateUserBody) => createUser(body),
    onSuccess: invalidate,
  })

  const rename = useMutation({
    mutationFn: ({ id, name }: { id: string; name: string }) => renameUser(id, name),
    onSuccess: invalidate,
  })

  const archive = useMutation({
    mutationFn: (id: string) => archiveUser(id),
    onSuccess: invalidate,
  })

  const unarchive = useMutation({
    mutationFn: (id: string) => unarchiveUser(id),
    onSuccess: invalidate,
  })

  const resetPassword = useMutation({
    mutationFn: ({ email, newPassword }: { email: string; newPassword: string }) =>
      resetUserPassword(email, newPassword),
    onSuccess: invalidate,
  })

  return { create, rename, archive, unarchive, resetPassword }
}
