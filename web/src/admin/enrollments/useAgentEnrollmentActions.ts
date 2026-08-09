import { useMutation, useQueryClient } from '@tanstack/react-query'
import { createAgentEnrollment, type CreateEnrollmentBody } from './api'

// Mutations for the admin Agent-enrollments tab. Plural name for a single
// mutation on purpose, matching useAdminUserActions' convention, so the
// revocation follow-up
// (docs/backlog/feature-2026-06-26-agent-enrollment-revocation.md) is an addition
// here rather than a rename of the module.
//
// SECURITY - read before editing:
//  - create.data holds the RAW enrollment token, and TanStack retains a
//    mutation's data AND variables for the mutation's lifetime. So create.reset()
//    is load-bearing, not tidiness: EnrollmentsTab calls it when the reveal dialog
//    closes and before the create panel is reopened. reset() only detaches the
//    observer though (MutationObserver.reset() clears currentMutation, it does not
//    delete the underlying Mutation from the mutation cache) - the default gcTime
//    is 5 minutes, so without gcTime: 0 below the token would keep sitting in
//    queryClient.getMutationCache().getAll() for up to 5 minutes after Done.
//    gcTime: 0 makes the now-observer-less mutation eligible for cache removal on
//    the very next tick once reset() detaches it.
//  - No onSuccess logging, ever. The success payload is a credential.
//  - No optimistic append: the 201 echoes neither created_at nor hostname_hint
//    (internal/api/agent_enrollments.go:68-72), so a locally synthesised row would
//    be partly invented.
export function useAgentEnrollmentActions() {
  const qc = useQueryClient()

  const create = useMutation({
    mutationFn: (body: CreateEnrollmentBody) => createAgentEnrollment(body),
    gcTime: 0,
    // BARE prefix, never a fully-qualified key, so every mounted
    // ['agent-enrollments', sort, cursor] combination refetches (see
    // web/src/jobs/queryKeyDecoupling.test.tsx).
    onSuccess: () => qc.invalidateQueries({ queryKey: ['agent-enrollments'] }),
  })

  return { create }
}
