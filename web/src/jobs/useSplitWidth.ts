import { useCallback, useEffect, useRef, useState } from 'react'
import type { PointerEvent as ReactPointerEvent, RefObject } from 'react'
import { clampSplit, parseStoredSplit, splitFromPointer, SPLIT_STORAGE_KEY } from './splitWidth'

function readStored(): number {
  try {
    return parseStoredSplit(window.localStorage.getItem(SPLIT_STORAGE_KEY))
  } catch {
    // A browser with storage disabled throws on the READ. Losing the preference
    // must not lose the page.
    return parseStoredSplit(null)
  }
}

// The job detail split, as an integer percentage for the LEFT pane, plus the
// pointer drag that moves it.
//
// WINDOW LISTENERS, NOT POINTER CAPTURE. Capture is the tidier API; the installed
// jsdom implements none of it, so the capture route would need a second code path
// behind a capability check to be testable at all.
//
// THE GENERATION-ORDERING INVARIANT HAS A LIVE SUBJECT HERE. The listeners are the
// resource and cleanupRef is the generation. Three rules, and each is a test:
// the handle is nulled BEFORE the listeners come off, so a pointercancel arriving
// mid-teardown finds nothing to run twice; pointercancel ends the drag exactly as
// pointerup does, or a cancelled gesture leaves the listeners armed and the next
// pointer move keeps resizing with no button held; and the effect removes them on
// unmount, or a drag interrupted by a navigation persists a gesture the reader
// never finished. The handle is armed BEFORE the listeners are added, in the same
// breath as taking the state, so nothing between the two can leave a drag armed
// with no way to release it - removeEventListener on a listener that was never
// added is a no-op, so arming early is free.
//
// PERSISTENCE IS ONCE PER GESTURE. A pointer move fires at the display refresh
// rate. State updates on every move; storage is written on pointerup, on
// pointercancel, and on each key press, since one press is one gesture.
export function useSplitWidth(containerRef: RefObject<HTMLElement>) {
  const [width, setWidth] = useState(readStored)

  // The value the window listeners and persist() read. React state is invisible
  // to a listener closed over an earlier render, and persist must write what the
  // last move set, not what the render that armed the drag saw.
  const widthRef = useRef(width)

  const applyWidth = useCallback((next: number) => {
    const v = clampSplit(next)
    widthRef.current = v
    setWidth(v)
  }, [])

  const persist = useCallback(() => {
    try {
      window.localStorage.setItem(SPLIT_STORAGE_KEY, String(widthRef.current))
    } catch {
      // A storage failure loses the preference. It must not lose the drag.
    }
  }, [])

  const cleanupRef = useRef<(() => void) | null>(null)

  const onPointerDown = useCallback(
    (e: ReactPointerEvent) => {
      const el = containerRef.current
      if (el === null) return
      // Without this a drag selects the text of both panes.
      e.preventDefault()
      const rect = el.getBoundingClientRect()

      const move = (ev: PointerEvent) => applyWidth(splitFromPointer(ev.clientX, rect))

      const release = () => {
        window.removeEventListener('pointermove', move)
        window.removeEventListener('pointerup', finish)
        window.removeEventListener('pointercancel', finish)
        document.body.style.cursor = ''
        document.body.style.userSelect = ''
      }

      function finish() {
        const armed = cleanupRef.current
        if (armed === null) return
        cleanupRef.current = null
        armed()
        persist()
      }

      cleanupRef.current = release
      window.addEventListener('pointermove', move)
      window.addEventListener('pointerup', finish)
      window.addEventListener('pointercancel', finish)
      document.body.style.cursor = 'col-resize'
      document.body.style.userSelect = 'none'
    },
    [applyWidth, containerRef, persist],
  )

  // Unmount releases and does NOT persist: a gesture interrupted by a navigation
  // is one the reader did not finish.
  useEffect(
    () => () => {
      const armed = cleanupRef.current
      cleanupRef.current = null
      armed?.()
    },
    [],
  )

  return { width, setWidth: applyWidth, persist, onPointerDown }
}
