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
// rate; storage is written on pointerup, on pointercancel, and on each key
// press, since one press is one gesture.
//
// A MOVE WRITES THE DOM DIRECTLY AND COMMITS REACT STATE ONCE, ON RELEASE. A
// 30-move drag through JobDetailPage's tree measured 22 full-page re-renders
// when every move called setWidth: the split's percentage lived in React
// state, and JobDetailPage (everything from the tab panel to the task table)
// re-rendered on every commit. The move handler instead writes the container's
// --relay-split custom property and the separator's aria-valuenow attribute
// straight onto the DOM nodes it already has references to, and only calls
// setWidth once, from the SAME finish() that already persists - one commit per
// gesture, matching persistence's own cadence. aria-valuetext is NOT kept live
// this way; it lags to the release commit. That is deliberate, not an
// oversight: a screen reader user operates this control by keyboard, which
// still commits (and announces) on every press, so the live gap is a mouse-only
// gap on a control a mouse user is already watching resize.
//
// THE RECT IS CAPTURED ONCE, AT POINTERDOWN, AND REUSED FOR EVERY MOVE IN THE
// GESTURE - inherited from the hi-fi's own onDragStart, which reads
// containerRef.current.clientWidth once per drag rather than per move. A
// container that resizes mid-drag (a window resize while a mouse button is
// down) maps against a stale rect until the drag ends; accepted rather than
// re-measured on every move, which would add a layout read to the same hot
// path this change exists to lighten.
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
      // The separator itself: React's currentTarget follows native semantics
      // and is only guaranteed during the synchronous handler, so it is read
      // into a plain DOM reference here rather than off the event later.
      const sep = e.currentTarget as HTMLElement

      const move = (ev: PointerEvent) => {
        const v = splitFromPointer(ev.clientX, rect)
        widthRef.current = v
        el.style.setProperty('--relay-split', `${v}%`)
        sep.setAttribute('aria-valuenow', String(v))
      }

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
        // The ONE React commit for the whole gesture. applyWidth re-clamps
        // widthRef.current, which is already clamped by every move - a
        // harmless no-op re-clamp, not a second source of truth.
        applyWidth(widthRef.current)
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
