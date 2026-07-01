import { expect, test } from 'vitest'
import * as holo from './index'

test('barrel re-exports the built primitives', () => {
  expect(typeof holo.GlassPanel).toBe('function')
  expect(typeof holo.Eyebrow).toBe('function')
  expect(typeof holo.ProgressBar).toBe('function')
  expect(typeof holo.Chip).toBe('function')
  expect(typeof holo.PillButton).toBe('function')
  expect(typeof holo.KpiStat).toBe('function')
  expect(typeof holo.Panel).toBe('function')
  expect(typeof holo.StatusDot).toBe('function')
})

test('does not export the deferred Spark primitive', () => {
  expect('Spark' in holo).toBe(false)
})
