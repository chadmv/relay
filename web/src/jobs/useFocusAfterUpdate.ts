import { useEffect, useState } from 'react'

// Focus a control that does not exist yet. An add or a remove changes the row
// list, and the target only exists after React has committed the new list, so
// the id is queued here and the effect focuses it on the next commit. Focus that
// falls to the document body after a row disappears is the silent regression
// this exists to prevent.
export function useFocusAfterUpdate(): (id: string) => void {
  const [pending, setPending] = useState<string | null>(null)
  useEffect(() => {
    if (pending === null) return
    document.getElementById(pending)?.focus()
    setPending(null)
  }, [pending])
  return setPending
}
