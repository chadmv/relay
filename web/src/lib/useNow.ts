import { useEffect, useState } from 'react'

// Returns a Date that re-renders the caller every intervalMs. Sibling of
// useDebouncedValue: this is a local CLOCK tick, not a data refresh - it issues
// no network request. The admin enrollments table uses it at 60s so a relative
// "in 21h" label and an EXPIRING/EXPIRED pill stay correct without polling
// GET /v1/agent-enrollments, which is not live data.
export function useNow(intervalMs: number): Date {
  const [now, setNow] = useState(() => new Date())
  useEffect(() => {
    const id = setInterval(() => setNow(new Date()), intervalMs)
    return () => clearInterval(id)
  }, [intervalMs])
  return now
}
