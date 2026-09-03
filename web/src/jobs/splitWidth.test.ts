import { expect, test } from 'vitest'
import {
  clampSplit,
  parseStoredSplit,
  splitFromPointer,
  SPLIT_DEFAULT,
  SPLIT_MAX,
  SPLIT_MIN,
} from './splitWidth'

// Kills: dropping the clamp. Both ends, because a one-sided clamp passes half of
// any single-ended test.
test('clampSplit holds the range and returns integers', () => {
  expect(clampSplit(29)).toBe(SPLIT_MIN)
  expect(clampSplit(-500)).toBe(SPLIT_MIN)
  expect(clampSplit(71)).toBe(SPLIT_MAX)
  expect(clampSplit(5000)).toBe(SPLIT_MAX)
  expect(clampSplit(42.4)).toBe(42)
  expect(clampSplit(42.6)).toBe(43)
  expect(clampSplit(Number.NaN)).toBe(SPLIT_DEFAULT)
})

// Kills: accepting any stored number. Each input is a state a previous release,
// another tab or a hand-edited storage value can actually hold, and an
// out-of-range value restored into aria-valuenow would announce a range it is
// outside of.
test('a stored value that is not an in-range integer yields the default', () => {
  expect(parseStoredSplit(null)).toBe(SPLIT_DEFAULT)
  expect(parseStoredSplit('')).toBe(SPLIT_DEFAULT)
  expect(parseStoredSplit('abc')).toBe(SPLIT_DEFAULT)
  expect(parseStoredSplit('55.5')).toBe(SPLIT_DEFAULT)
  expect(parseStoredSplit('90')).toBe(SPLIT_DEFAULT)
  expect(parseStoredSplit('10')).toBe(SPLIT_DEFAULT)
  // The positive control on the same instrument: a value it must accept.
  expect(parseStoredSplit('40')).toBe(40)
})

// Kills: inverting the sign, and dropping the clamp on the pointer path only.
// The rect is injected because jsdom performs no layout: every
// getBoundingClientRect there is zero, so a function that measured internally
// could not be tested at all.
test('a pointer position maps to a clamped percentage of the container', () => {
  expect(splitFromPointer(400, { left: 0, width: 1000 })).toBe(40)
  expect(splitFromPointer(600, { left: 0, width: 1000 })).toBe(60)
  // Moving RIGHT grows the LEFT pane. An inverted sign reads 60 here.
  expect(splitFromPointer(400, { left: 0, width: 1000 })).toBeLessThan(
    splitFromPointer(600, { left: 0, width: 1000 }),
  )
  // The container's own offset, not the viewport origin.
  expect(splitFromPointer(500, { left: 100, width: 1000 })).toBe(40)
  expect(splitFromPointer(5000, { left: 0, width: 1000 })).toBe(SPLIT_MAX)
  expect(splitFromPointer(-5000, { left: 0, width: 1000 })).toBe(SPLIT_MIN)
  // A container with no width cannot produce a ratio.
  expect(splitFromPointer(400, { left: 0, width: 0 })).toBe(SPLIT_DEFAULT)
})
