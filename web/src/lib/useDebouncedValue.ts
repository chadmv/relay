import { useEffect, useState } from 'react'

// Returns `value` delayed by delayMs, restarting the timer on every change, so a
// burst of keystrokes produces exactly one downstream update. Used by the admin
// Users tab's exact-email filter: the query key only changes on the debounced
// value, so typing does not fan out one request per keystroke.
export function useDebouncedValue<T>(value: T, delayMs: number): T {
  const [debounced, setDebounced] = useState(value)
  useEffect(() => {
    const t = setTimeout(() => setDebounced(value), delayMs)
    return () => clearTimeout(t)
  }, [value, delayMs])
  return debounced
}
