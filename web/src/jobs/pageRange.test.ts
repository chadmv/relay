import { describe, expect, test } from 'vitest'
import { computePageRange } from './pageRange'

describe('computePageRange', () => {
  test('empty list shows 0 of 0', () => {
    expect(computePageRange(0, 0)).toEqual({ x: 0, y: 0 })
  })

  test('empty page against a non-zero total shows 0 result', () => {
    // Edge: a filter returns 0 rows but total reflects overall count.
    // The range computation itself returns {0,0}; caller formats with the total.
    expect(computePageRange(0, 0)).toEqual({ x: 0, y: 0 })
  })

  test('full first page: 1-50', () => {
    expect(computePageRange(0, 50)).toEqual({ x: 1, y: 50 })
  })

  test('second full page: 51-100', () => {
    // startOffset = 50 (accumulated after first page of 50 rows)
    expect(computePageRange(50, 50)).toEqual({ x: 51, y: 100 })
  })

  test('partial last page: 101-120', () => {
    // startOffset = 100 (two full pages of 50), pageSize = 20 (partial)
    expect(computePageRange(100, 20)).toEqual({ x: 101, y: 120 })
  })

  test('naive stack.length * pageSize baseline would be wrong on partial page', () => {
    // Prove the naive approach fails. The difference surfaces when the FIRST
    // page is partial (e.g. total=30 so page1 returns 30 rows, not 50).
    // After page 1: naive offset = stack.length(1) * pageSize(50) = 50 (WRONG)
    //               actual offset = 30 (accumulated real rows)
    const naiveOffset = 1 * 50 // stack.length * pageSize after page 1
    const actualOffset = 30 // accumulated actual rows from page 1
    expect(naiveOffset).not.toBe(actualOffset)

    // With actual offset the start position is correct (30+1=31 on page 2),
    // whereas naive would show 51.
    expect(computePageRange(actualOffset, 5)).toEqual({ x: 31, y: 35 })
    expect(computePageRange(naiveOffset, 5)).toEqual({ x: 51, y: 55 }) // naive is wrong
  })

  test('single item on first page: 1-1', () => {
    expect(computePageRange(0, 1)).toEqual({ x: 1, y: 1 })
  })
})
