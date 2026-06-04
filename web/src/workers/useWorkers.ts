import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { listWorkers, type WorkerSort } from './api'

// Polls the first page of workers. keepPreviousData keeps the old rows visible
// while a new sort loads and between polls, so the page never flashes empty.
// intervalMs defaults to 3000; tests inject a small value.
export function useWorkers(sort: WorkerSort, intervalMs = 3000) {
  return useQuery({
    queryKey: ['workers', sort],
    queryFn: () => listWorkers(sort),
    refetchInterval: intervalMs,
    placeholderData: keepPreviousData,
  })
}
