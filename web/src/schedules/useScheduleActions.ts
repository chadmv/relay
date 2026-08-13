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
    onSuccess: () => qc.invalidateQueries({ queryKey: ['schedules'] }),
  })

  return { runNow, setEnabled, update, remove }
}
