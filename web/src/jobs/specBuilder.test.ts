import { expect, test } from 'vitest'
import { newBuilderState, newCommandRow, newTaskRow, toSpec } from './specBuilder'

// A minimal state built by hand rather than through newBuilderState, so these
// tests keep meaning if the starter template changes.
function oneTask() {
  const t = newTaskRow()
  t.name = 'hello'
  t.commands = [newCommandRow(['echo'])]
  return { name: 'my-job', priority: 'normal' as const, labels: [], tasks: [t] }
}

test('an untouched optional emits no key at the job level', () => {
  expect(Object.keys(toSpec(oneTask())).sort()).toEqual(['name', 'priority', 'tasks'])
})

test('an untouched optional emits no key at the task level', () => {
  expect(Object.keys(toSpec(oneTask()).tasks[0]).sort()).toEqual(['command', 'name'])
})

test('a blank argv token is dropped and does not become an empty string', () => {
  const s = oneTask()
  // The blank goes FIRST: an implementation that stops at the first empty token
  // rather than filtering every one of them cannot pass by never reaching it.
  s.tasks[0].commands = [newCommandRow(['', 'echo', '', 'hi'])]
  expect(toSpec(s).tasks[0].command).toEqual(['echo', 'hi'])
})

test('a command left with no tokens emits neither command nor commands', () => {
  const s = oneTask()
  s.tasks[0].commands = [newCommandRow([''])]
  const task = toSpec(s).tasks[0]
  expect(task.command).toBeUndefined()
  expect(task.commands).toBeUndefined()
  expect(Object.keys(task)).toEqual(['name'])
})

test('an argument containing a space is one argv element', () => {
  const s = oneTask()
  s.tasks[0].commands = [newCommandRow(['echo', 'hello world'])]
  expect(toSpec(s).tasks[0].command).toEqual(['echo', 'hello world'])
})

test('newBuilderState models the starter template', () => {
  expect(newBuilderState().tasks).toHaveLength(1)
  expect(newBuilderState().name).toBe('my-job')
})
