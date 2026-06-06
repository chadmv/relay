import { useMutation, useQueryClient } from '@tanstack/react-query'
import { runScheduleNow, setScheduleEnabled } from './api'

// Mutations for the row actions. Both invalidate the schedules list on success so
// the table reflects the new state on the next render without waiting for a poll.
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

  return { runNow, setEnabled }
}
