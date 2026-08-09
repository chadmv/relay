import { expect, test } from 'vitest'
import { isTerminalTask, taskStatusColor } from './taskStatus'

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

test('isTerminalTask covers exactly done, failed and timed_out', () => {
  expect(isTerminalTask('done')).toBe(true)
  expect(isTerminalTask('failed')).toBe(true)
  expect(isTerminalTask('timed_out')).toBe(true)
  expect(isTerminalTask('pending')).toBe(false)
  expect(isTerminalTask('dispatched')).toBe(false)
  expect(isTerminalTask('running')).toBe(false)
  expect(isTerminalTask(undefined)).toBe(false)
})
