import { useState } from 'react'

/**
 * One user preference held in localStorage behind an allow-list.
 *
 * The allow-list is the point: a value written by a different version, by hand, or
 * by another origin sharing the key would otherwise put a page into a state it has
 * no branch for. Anything not in `allowed` reads as `fallback`.
 *
 * Both sides are guarded. A read can throw (a blocked third-party context, a
 * private window), and a failed WRITE must cost the preference and never the
 * click - the choice still applies for this session, it just does not survive a
 * reload.
 */
export function usePersistedChoice<T extends string>(
  key: string,
  allowed: readonly T[],
  fallback: T,
): [T, (v: T) => void] {
  const [value, setValue] = useState<T>(() => {
    try {
      const stored = localStorage.getItem(key)
      return allowed.includes(stored as T) ? (stored as T) : fallback
    } catch {
      return fallback
    }
  })

  function choose(v: T) {
    setValue(v)
    try {
      localStorage.setItem(key, v)
    } catch {
      // Deliberately swallowed; see the note above about the click.
    }
  }

  return [value, choose]
}
