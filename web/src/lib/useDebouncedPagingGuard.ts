import { useEffect, useRef } from 'react'

/**
 * Guards a cursor-paginated list against the debounce-window cursor race: a
 * per-keystroke pager reset lands on the CACHED page-1 key while the raw input
 * has outrun the debounced filter, so `next` can mint a cursor that belongs to
 * the OLD filters - and the debounced value then carries that cursor into a
 * request for the NEW filters once it lands. The server does not reject a
 * mismatched cursor, so this silently pages from a nonsensical position.
 *
 * Two-part fix, and the caller supplies both halves: disable next/prev while
 * the returned value is true (the vulnerable window - a click right now could
 * mint exactly that cursor), and this hook resets the pager whenever the
 * DEBOUNCED value itself changes, as a second line of defence for anything the
 * disabled window does not cover. It does not fire on the initial mount -
 * nothing has "changed" from before mount, and the pager already starts at
 * page 1.
 */
export function useDebouncedPagingGuard(
  raw: string,
  debounced: string,
  pager: { resetPaging: () => void },
): boolean {
  const prev = useRef(debounced)
  useEffect(() => {
    if (prev.current !== debounced) {
      prev.current = debounced
      pager.resetPaging()
    }
  }, [debounced, pager])

  return raw !== debounced
}
