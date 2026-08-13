import { useMutation, useQueryClient } from '@tanstack/react-query'
import { createInvite, type CreateInviteBody } from './api'

// Mutations for the admin Invites tab. Plural name for a single mutation, matching
// useAgentEnrollmentActions and useAdminUserActions, so a future second action is
// an addition here rather than a rename of the module.
//
// SECURITY - read before editing:
//  - create.data holds the RAW invite token, which grants account creation on this
//    server, and create.variables holds the INVITEE EMAIL. TanStack retains a
//    mutation's data AND variables for the mutation's lifetime, and the same email
//    is additionally captured in the mutationFn CLOSURE that mutationObserver
//    builds from this.options - a place no state inspection can reach. So the test
//    that guards this asserts the mutation cache is EMPTY, not that one field of
//    one entry is clean (docs/retros/2026-08-12-profile-pages.md:171-184).
//  - gcTime: 0 and create.reset() are BOTH required and neither is sufficient.
//    reset() only detaches the observer (MutationObserver.reset() clears
//    currentMutation; it does not delete the underlying Mutation), and
//    Mutation.optionalRemove refuses to remove while any observer is attached
//    (query-core mutation.js:47-55). With the default 5-minute gcTime the token
//    and the email would stay readable in queryClient.getMutationCache() long
//    after the admin clicked Done. gcTime: 0 makes the now-observer-less mutation
//    eligible for removal on the very next tick once reset() detaches it.
//    DO NOT DELETE gcTime: 0 as redundant - useInviteActions.test.tsx goes RED.
//  - reset() is NEVER called here. Mutation.execute awaits this onSuccess at
//    query-core mutation.js:123 and dispatches the success action only at :144, so
//    a reset() inside onSuccess would detach the observer before the notification:
//    isSuccess would never become true, data would never arrive, and InvitesTab's
//    reveal dialog would never open - silently. reset() lives at the three
//    UI-driven sites in InvitesTab (dialog onDone, panel cancel, panel reopen).
//  - No onSuccess logging, ever. The success payload is a credential.
//  - No optimistic append: the 201 echoes no created_at and no created_by_email
//    (internal/api/invites.go:79-87), so a locally synthesised row would be partly
//    invented.
export function useInviteActions() {
  const qc = useQueryClient()

  const create = useMutation({
    mutationFn: (body: CreateInviteBody) => createInvite(body),
    gcTime: 0,
    // BARE prefix, never a fully-qualified key, so every mounted
    // ['invites', sort, cursor] combination refetches (see
    // web/src/jobs/queryKeyDecoupling.test.tsx).
    onSuccess: () => qc.invalidateQueries({ queryKey: ['invites'] }),
  })

  return { create }
}
