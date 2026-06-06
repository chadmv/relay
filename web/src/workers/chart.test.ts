import { describe, expect, test } from 'vitest'
import { chartPath } from './chart'

describe('chartPath', () => {
  test('empty series yields empty paths', () => {
    expect(chartPath([], 100, 50, 100)).toEqual({ line: '', area: '' })
  })

  test('single point draws a flat line across the full width', () => {
    const { line } = chartPath([50], 100, 50, 100)
    expect(line).toBe('M0,25 L100,25')
  })

  test('maps min to the baseline and max to the top', () => {
    const { line } = chartPath([0, 100], 100, 50, 100)
    expect(line).toBe('M0,50 L100,0')
  })

  test('clamps values above max to the top', () => {
    const { line } = chartPath([150], 100, 50, 100)
    expect(line).toBe('M0,0 L100,0')
  })

  test('non-positive max maps everything to the baseline (no NaN)', () => {
    const { line } = chartPath([5, 9], 100, 50, 0)
    expect(line).toBe('M0,50 L100,50')
  })

  test('area closes along the baseline', () => {
    const { area } = chartPath([0, 100], 100, 50, 100)
    expect(area).toBe('M0,50 L100,0 L100,50 L0,50 Z')
  })
})
