import { expect, test } from 'vitest'
import { ENABLED_FILTERS, enabledParam, type EnabledFilterKey } from './scheduleFilters'

// THE DISABLED CASE COMES FIRST, DELIBERATELY. `enabledParam('disabled')`
// returning undefined is the silent failure of this whole lane - the Disabled
// chip then behaves exactly as All, the list looks plausible, and no other
// assertion in the suite can tell the two apart. A poisoned input placed last is
// missed by an early-exit mutation, so it goes first.
test('enabledParam maps each chip key to its own wire value', () => {
  expect(enabledParam('disabled')).toBe('false')
  expect(enabledParam('enabled')).toBe('true')
  expect(enabledParam('all')).toBeUndefined()
})

// A CENSUS OVER THE TUPLE, not three hand-written assertions. A fourth chip added
// to ENABLED_FILTERS with no case in enabledParam falls through to undefined and
// becomes a second All; this goes red the moment the vocabulary moves, so the
// mapping is revisited rather than silently desynchronized.
test('the chip vocabulary is exactly all, enabled, disabled, in that order', () => {
  expect(ENABLED_FILTERS.map((f) => f.key)).toEqual(['all', 'enabled', 'disabled'])
  expect(ENABLED_FILTERS.map((f) => f.label)).toEqual(['All', 'Enabled', 'Disabled'])
})

test('exactly one chip key maps to no wire parameter', () => {
  const keys: EnabledFilterKey[] = ENABLED_FILTERS.map((f) => f.key)
  const unmapped = keys.filter((k) => enabledParam(k) === undefined)
  expect(unmapped).toEqual(['all'])
})
