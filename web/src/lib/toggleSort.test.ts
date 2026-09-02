import { expect, test } from 'vitest'
import { toggleSort } from './toggleSort'

test('clicking the active ascending column flips it to descending', () => {
  expect(toggleSort('name', 'name')).toBe('-name')
})

test('clicking the active descending column flips it back to ascending', () => {
  expect(toggleSort('name', '-name')).toBe('name')
})

test('clicking a different column selects it ascending from an ascending current', () => {
  expect(toggleSort('email', 'name')).toBe('email')
})

// Discriminates an implementation that carries the leading minus sign across a column
// change: that returns '-email' here while still returning 'email' from the ascending
// current above, so only this input separates the two.
test('clicking a different column selects it ascending from a descending current', () => {
  expect(toggleSort('email', '-name')).toBe('email')
})

// Discriminates equality on the stripped string from a startsWith comparison:
// 'created_at' starts with 'created', so a startsWith treats the column as already
// active and returns '-created'.
test('a field whose name is a prefix of the current field is not treated as active', () => {
  expect(toggleSort('created', 'created_at')).toBe('created')
})
