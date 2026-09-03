import { act, renderHook } from '@testing-library/react'
import { afterEach, expect, test, vi } from 'vitest'
import { usePersistedChoice } from './usePersistedChoice'

const VIEWS = ['table', 'lanes', 'timeline'] as const

afterEach(() => {
  localStorage.clear()
  vi.restoreAllMocks()
})

test('an absent key yields the fallback', () => {
  const { result } = renderHook(() => usePersistedChoice('k', VIEWS, 'table'))
  expect(result.current[0]).toBe('table')
})

test('a stored value inside the allow-list is restored', () => {
  localStorage.setItem('k', 'timeline')
  const { result } = renderHook(() => usePersistedChoice('k', VIEWS, 'table'))
  expect(result.current[0]).toBe('timeline')
})

test('a stored value outside the allow-list yields the fallback', () => {
  // Not a typo of a real value: a later version that shipped a fourth view would
  // write exactly this shape, and accepting it puts a page into a state it has no
  // branch for.
  localStorage.setItem('k', 'gantt')
  const { result } = renderHook(() => usePersistedChoice('k', VIEWS, 'table'))
  expect(result.current[0]).toBe('table')
})

test('a read that throws yields the fallback', () => {
  vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
    throw new Error('storage disabled')
  })
  const { result } = renderHook(() => usePersistedChoice('k', VIEWS, 'lanes'))
  expect(result.current[0]).toBe('lanes')
})

test('choosing writes the value under the key', () => {
  const { result } = renderHook(() => usePersistedChoice('k', VIEWS, 'table'))
  act(() => {
    result.current[1]('lanes')
  })
  expect(result.current[0]).toBe('lanes')
  expect(localStorage.getItem('k')).toBe('lanes')
})

test('a write that throws still changes the choice', () => {
  // The preference is lost for the session; the click is not.
  vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
    throw new Error('quota exceeded')
  })
  const { result } = renderHook(() => usePersistedChoice('k', VIEWS, 'table'))
  act(() => {
    result.current[1]('lanes')
  })
  expect(result.current[0]).toBe('lanes')
})
