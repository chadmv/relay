import { useQuery } from '@tanstack/react-query'
import { getServerConfig } from './api'

// AllowSelfRegister is read from process env at startup (internal/api/config.go:9
// reads a Server field set at wiring time), so it cannot change without a server
// restart - and that restart also restarts the process serving this SPA. There is
// nothing to poll for, so this is fetched once per page load and never again.
// gcTime: Infinity backs that contract: the default gcTime (5 min) would evict the
// cache entry and let a remount past that window refetch, contradicting "never
// again". AuthProvider's queryClient.clear() on logout/401 still bounds any
// cross-user residue, so this doesn't leak config across a user switch.
// The raw query state is returned and nothing is fabricated here, because the
// consumers disagree about what a failure means: the sign-up screen must fail
// closed to the invite-required form, while the admin server tab must render the
// failure. A default invented in this layer would be wrong for one of them.
export function useServerConfig() {
  return useQuery({
    queryKey: ['server-config'],
    queryFn: getServerConfig,
    staleTime: Infinity,
    gcTime: Infinity,
  })
}
