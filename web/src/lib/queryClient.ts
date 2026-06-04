import { QueryClient } from '@tanstack/react-query'

// Shared client for the app. Polling (refetchInterval) and keepPreviousData are
// set per-hook (see workers/useWorkers.ts), not globally, so non-polled queries
// added later are unaffected. The existing 401 interceptor in lib/api.ts handles
// auth redirects; the client only needs sane retry/staleness defaults.
export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 0,
      retry: 1,
    },
  },
})
