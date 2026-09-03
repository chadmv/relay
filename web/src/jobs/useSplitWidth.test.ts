import { fireEvent, render } from '@testing-library/react'
import { createElement, useRef } from 'react'
import { afterEach, expect, test, vi } from 'vitest'
import { SPLIT_DEFAULT, SPLIT_MAX, SPLIT_MIN, SPLIT_STORAGE_KEY } from './splitWidth'
import { useSplitWidth } from './useSplitWidth'

afterEach(() => {
  window.localStorage.clear()
  vi.restoreAllMocks()
})

// A harness rather than renderHook: the drag needs a real container element whose
// rect the hook reads, and jsdom returns a zero rect for every element, so the
// rect is stubbed on THIS element only.
function Harness() {
  const ref = useRef<HTMLDivElement>(null)
  const split = useSplitWidth(ref)
  return createElement(
    'div',
    {
      ref,
      'data-testid': 'container',
    },
    createElement('span', { 'data-testid': 'value' }, String(split.width)),
    createElement('button', { 'data-testid': 'grip', onPointerDown: split.onPointerDown }),
  )
}

function renderHarness() {
  const view = render(createElement(Harness))
  const container = view.getByTestId('container')
  Object.defineProperty(container, 'getBoundingClientRect', {
    value: () => ({ left: 0, width: 1000, top: 0, height: 100, right: 1000, bottom: 100, x: 0, y: 0 }),
  })
  return view
}

const value = (view: ReturnType<typeof renderHarness>) => view.getByTestId('value').textContent

// Kills: inverting the sign in the move handler; not wiring the move handler at
// all.
test('a pointer drag moves the split in the direction of travel', () => {
  const view = renderHarness()
  expect(value(view)).toBe(String(SPLIT_DEFAULT))
  fireEvent.pointerDown(view.getByTestId('grip'), { clientX: 550 })
  fireEvent.pointerMove(window, { clientX: 650 })
  expect(value(view)).toBe('65')
  fireEvent.pointerMove(window, { clientX: 350 })
  expect(value(view)).toBe('35')
})

// Kills: handling pointerup only. A cancelled drag - a context menu, a browser
// gesture - would otherwise leave the listeners armed, and the next pointer move
// keeps resizing with no button held.
test('a cancelled drag disarms', () => {
  const view = renderHarness()
  fireEvent.pointerDown(view.getByTestId('grip'), { clientX: 550 })
  fireEvent.pointerMove(window, { clientX: 400 })
  expect(value(view)).toBe('40')
  fireEvent.pointerCancel(window)
  fireEvent.pointerMove(window, { clientX: 650 })
  expect(value(view)).toBe('40')
})

// Kills: dropping the unmount cleanup. THE OBSERVATION IS THE STALE WRITE, not a
// React warning: React 18 emits none for a setState after unmount. With the
// cleanup dropped, the still-armed pointerup handler runs the dying component's
// persist() and writes the preference this gesture never finished.
test('unmounting mid-drag disarms the window listeners', () => {
  const view = renderHarness()
  fireEvent.pointerDown(view.getByTestId('grip'), { clientX: 550 })
  fireEvent.pointerMove(window, { clientX: 400 })
  expect(window.localStorage.getItem(SPLIT_STORAGE_KEY)).toBeNull()
  view.unmount()
  fireEvent.pointerMove(window, { clientX: 650 })
  fireEvent.pointerUp(window)
  expect(window.localStorage.getItem(SPLIT_STORAGE_KEY)).toBeNull()
})

// Kills: persisting inside the move handler. A pointer move fires at the display
// refresh rate, so a per-move write is dozens of synchronous storage writes a
// second; the count is the property, not the value.
test('persistence is once per gesture', () => {
  const setItem = vi.spyOn(Storage.prototype, 'setItem')
  const view = renderHarness()
  fireEvent.pointerDown(view.getByTestId('grip'), { clientX: 550 })
  for (const x of [560, 570, 580, 590, 600]) fireEvent.pointerMove(window, { clientX: x })
  expect(setItem).not.toHaveBeenCalled()
  fireEvent.pointerUp(window)
  expect(setItem).toHaveBeenCalledTimes(1)
  expect(setItem).toHaveBeenCalledWith(SPLIT_STORAGE_KEY, '60')
})

// Kills: reading storage on every render instead of once, and reading a value
// the range does not admit.
//
// Unmounts between renders: RTL's render() appends into document.body, and its
// returned queries search the whole body rather than being scoped to their own
// subtree, so leaving the first mount in place makes getByTestId ambiguous once
// a second one is mounted alongside it.
test('the initial value comes from storage and degrades to the default', () => {
  window.localStorage.setItem(SPLIT_STORAGE_KEY, '40')
  const first = renderHarness()
  expect(value(first)).toBe('40')
  first.unmount()

  window.localStorage.setItem(SPLIT_STORAGE_KEY, '99')
  const second = renderHarness()
  expect(value(second)).toBe(String(SPLIT_DEFAULT))
})

// Kills: removing the try. A storage read can throw outright (a browser with
// storage disabled), and losing the preference must not lose the page.
test('a throwing storage read does not throw out of the hook', () => {
  vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
    throw new Error('storage disabled')
  })
  expect(value(renderHarness())).toBe(String(SPLIT_DEFAULT))
})

// Kills: removing the try on the WRITE side, which is a different call site.
test('a throwing storage write does not lose the drag', () => {
  vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
    throw new Error('quota')
  })
  const view = renderHarness()
  fireEvent.pointerDown(view.getByTestId('grip'), { clientX: 550 })
  fireEvent.pointerMove(window, { clientX: 400 })
  fireEvent.pointerUp(window)
  expect(value(view)).toBe('40')
})

// Kills: dropping the clamp from the hook's own setter, which the key handler
// uses.
test('setWidth clamps at both ends', () => {
  const view = renderHarness()
  fireEvent.pointerDown(view.getByTestId('grip'), { clientX: 550 })
  fireEvent.pointerMove(window, { clientX: 5000 })
  expect(value(view)).toBe(String(SPLIT_MAX))
  fireEvent.pointerMove(window, { clientX: -5000 })
  expect(value(view)).toBe(String(SPLIT_MIN))
})
