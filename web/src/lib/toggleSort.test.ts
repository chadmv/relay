import { expect, test } from 'vitest'
import { toggleSort } from './toggleSort'

// A union standing in for a surface's sort type, so S infers from `current` the way it
// does in production. A bare literal would infer S as that one literal, which makes the
// declared return type describe a one-member union and the field constraint vacuous.
type Sort = 'name' | '-name' | 'email' | '-email'
type PrefixSort = 'created' | '-created' | 'created_at' | '-created_at'

// A compile-time pin, not a runtime test. The constraint is the whole guard against a
// typo'd column reaching the server as a sort key, and nothing at runtime can observe
// it. Drop the constraint and this directive has no error to suppress, so tsc fails it
// as unused: the pin goes red exactly when the guard is lost.
// @ts-expect-error 'hostname_hint' is not a base name of Sort
void toggleSort('hostname_hint', 'name' as Sort)

test('clicking the active ascending column flips it to descending', () => {
  expect(toggleSort('name', 'name' as Sort)).toBe('-name')
})

test('clicking the active descending column flips it back to ascending', () => {
  expect(toggleSort('name', '-name' as Sort)).toBe('name')
})

test('clicking a different column selects it ascending from an ascending current', () => {
  expect(toggleSort('email', 'name' as Sort)).toBe('email')
})

// Discriminates an implementation that carries the leading minus sign across a column
// change: that returns '-email' here while still returning 'email' from the ascending
// current above, so only this input separates the two.
test('clicking a different column selects it ascending from a descending current', () => {
  expect(toggleSort('email', '-name' as Sort)).toBe('email')
})

// Discriminates equality from a startsWith comparison: 'created_at' starts with
// 'created', so a startsWith treats the column as already active and returns '-created'.
test('a field whose name is a prefix of the current field is not treated as active', () => {
  expect(toggleSort('created', 'created_at' as PrefixSort)).toBe('created')
})
