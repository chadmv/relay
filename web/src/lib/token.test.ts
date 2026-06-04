import { afterEach, expect, test } from 'vitest'
import { clearToken, getToken, setToken } from './token'

afterEach(() => clearToken())

test('round-trips a token', () => {
  expect(getToken()).toBeNull()
  setToken('abc123')
  expect(getToken()).toBe('abc123')
})

test('clears a token', () => {
  setToken('abc123')
  clearToken()
  expect(getToken()).toBeNull()
})
