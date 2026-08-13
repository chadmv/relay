import { useMutation, useQueryClient } from '@tanstack/react-query'
import {
  deleteSchedule,
  runScheduleNow,
  setScheduleEnabled,
  updateSchedule,
  type SchedulePatch,
} from './api'

// Mutations for the row actions and the detail page. All four invalidate the same
// BARE ['schedules'] prefix on success, which reaches the list key
// ['schedules', sort, cursor] (useSchedules.ts:10), the detail key
// ['schedules','detail',id] and the runs key ['schedules','runs',id] alike - so a
// Run now refreshes the runs panel and a save refreshes the header without a reload.
//
// None of them writes the response into the cache or into any form state. A settled
// mutation must never reanimate state a later action has already replaced; the fresh
// value arrives through the invalidated refetch, which is the server's own row.
export function useScheduleActions() {
  const qc = useQueryClient()

  const runNow = useMutation({
    mutationFn: (id: string) => runScheduleNow(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['schedules'] }),
  })

  const setEnabled = useMutation({
    mutationFn: ({ id, enabled }: { id: string; enabled: boolean }) => setScheduleEnabled(id, enabled),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['schedules'] }),
  })

  // Pass-through: the patch is sent exactly as given. It is the CALLER's job to have
  // built it from a diff - any extra key recomputes next_run_at server-side
  // (internal/api/scheduled_jobs.go:585).
  const update = useMutation({
    mutationFn: ({ id, patch }: { id: string; patch: SchedulePatch }) => updateSchedule(id, patch),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['schedules'] }),
  })

  const remove = useMutation({
    mutationFn: (id: string) => deleteSchedule(id),
    // Invariant 1 (end the generation before releasing the resource): the row is
    // already gone server-side, so its own detail and runs queries must never be
    // refetched - both are guaranteed 404s. Removing them from the cache FIRST means
    // the broad ['schedules'] invalidate below has nothing left to match for this id,
    // so it can only refresh the list query. Order matters: an invalidate that ran
    // first would refetch the still-active detail/runs queries (the page has not
    // necessarily unmounted yet - the mutate-level onSuccess that navigates away runs
    // strictly AFTER this hook-level onSuccess resolves) before this cleanup got a
    // chance to run.
    onSuccess: (_data, id) => {
      qc.removeQueries({ queryKey: ['schedules', 'detail', id] })
      qc.removeQueries({ queryKey: ['schedules', 'runs', id] })
      return qc.invalidateQueries({ queryKey: ['schedules'] })
    },
  })

  return { runNow, setEnabled, update, remove }
}
