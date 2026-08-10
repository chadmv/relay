import { afterEach, expect, test, vi } from 'vitest'
import {
  __resetForTest,
  getLayer,
  getTopmostPanel,
  isEmpty,
  isTopmost,
  registerDialog,
  unregisterDialog,
} from './dialogStack'

// dialogStack is module state shared across the tests in this file. React
// Testing Library's auto-cleanup empties it for component tests, but these drive
// the module directly with no React in the picture, so reset explicitly.
afterEach(() => {
  __resetForTest()
  document.body.style.overflow = ''
})

function panel(): HTMLElement {
  const el = document.createElement('div')
  el.tabIndex = -1
  return el
}

test('unregister removes its OWN id by identity, not the top of the stack', () => {
  registerDialog('a', panel())
  registerDialog('b', panel())
  expect(isTopmost('b')).toBe(true)

  unregisterDialog('a')

  // A stack.pop() teardown would have removed b here, leaving a's dead id
  // topmost: Escape would stop working and the scroll lock would release while
  // b is still on screen. This is the "identity-checked teardown" invariant, in
  // a React effect cleanup.
  expect(isTopmost('b')).toBe(true)
  expect(isTopmost('a')).toBe(false)
  expect(isEmpty()).toBe(false)
})

test('registering the same id twice is idempotent', () => {
  registerDialog('a', panel())
  registerDialog('b', panel())
  // StrictMode's dev double-invoke and hot reload both produce this.
  registerDialog('a', panel())

  expect(isTopmost('b')).toBe(true)
  unregisterDialog('a')
  unregisterDialog('b')
  expect(isEmpty()).toBe(true)
})

test('re-registering an existing id refreshes its panel node in place', () => {
  const first = panel()
  registerDialog('a', first)
  expect(getTopmostPanel()).toBe(first)

  // StrictMode's dev double-invoke, or a hot reload, can re-register an id
  // whose cleanup never ran - WITH a fresh node. Dropping the new panel (the
  // old `if (stack.some(...)) return` guard did exactly that) would leave
  // getTopmostPanel and Tab's wrap-around targeting a DETACHED element from a
  // previous render.
  const second = panel()
  registerDialog('a', second)

  expect(getTopmostPanel()).toBe(second)
  expect(getTopmostPanel()).not.toBe(first)
})

test('unregistering an id that is not on the stack is a no-op', () => {
  registerDialog('a', panel())
  unregisterDialog('ghost')
  expect(isTopmost('a')).toBe(true)

  unregisterDialog('a')
  unregisterDialog('a')
  expect(isEmpty()).toBe(true)
})

test('body overflow is saved exactly once across two registrations and restored exactly', () => {
  document.body.style.overflow = 'auto'

  registerDialog('a', panel())
  expect(document.body.style.overflow).toBe('hidden')
  registerDialog('b', panel())
  expect(document.body.style.overflow).toBe('hidden')

  unregisterDialog('b')
  // A dialog is still open, so the lock must hold.
  expect(document.body.style.overflow).toBe('hidden')

  unregisterDialog('a')
  // A per-dialog save/restore pair would have had b save the 'hidden' that a
  // wrote, so this would read 'hidden' forever - a page that can never scroll
  // again.
  expect(document.body.style.overflow).toBe('auto')
})

test('the background is marked and unmarked, and the layer comes and goes', () => {
  const background = document.createElement('div')
  document.body.appendChild(background)

  registerDialog('a', panel())
  expect(background).toHaveAttribute('inert')
  expect(background).toHaveAttribute('aria-hidden', 'true')
  expect(document.querySelector('[data-dialog-layer]')).not.toBeNull()
  // The layer itself must never be marked: it holds the dialog, and aria-hidden
  // on an ancestor of the focused element is an outright AT violation.
  expect(getLayer().hasAttribute('inert')).toBe(false)
  expect(getLayer().hasAttribute('aria-hidden')).toBe(false)

  unregisterDialog('a')
  expect(background).not.toHaveAttribute('inert')
  expect(background).not.toHaveAttribute('aria-hidden')
  expect(document.querySelector('[data-dialog-layer]')).toBeNull()

  background.remove()
})

// Finding 5 (code review, 2026-08-09). A silent no-op when not in DEV means a
// prod-mode call - or a reference that survives into a production bundle -
// fails quietly instead of loudly. Throwing also protects the OTHER tests in
// this file: this suite genuinely depends on __resetForTest doing real work
// in its own afterEach, so a silent no-op here would let every other test's
// state leak into the next one without any test ever telling you why.
test('__resetForTest throws outside DEV instead of silently doing nothing', () => {
  vi.stubEnv('DEV', false)
  try {
    expect(() => __resetForTest()).toThrow()
  } finally {
    vi.unstubAllEnvs()
  }
})
