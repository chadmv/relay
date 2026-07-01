import { expect, test } from 'vitest'
import { taskStatusColor } from './taskStatus'

test('maps each of the six task statuses to a dot class', () => {
  expect(taskStatusColor('done').dot).toBe('bg-ok')
  expect(taskStatusColor('running').dot).toBe('bg-accent')
  expect(taskStatusColor('dispatched').dot).toBe('bg-accent')
  expect(taskStatusColor('pending').dot).toBe('bg-warn')
  expect(taskStatusColor('failed').dot).toBe('bg-err')
  expect(taskStatusColor('timed_out').dot).toBe('bg-err')
})

test('covers dispatched and timed_out (the statuses status.ts lacks)', () => {
  expect(taskStatusColor('dispatched').text).toBe('text-accent')
  expect(taskStatusColor('timed_out').text).toBe('text-err')
})
